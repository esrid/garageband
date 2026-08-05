# Team UI — what the handler owes the views

The `team` feature ships the "Accès aux sites" screen (`views.templ`), its
HTTP handler, and its view models. The views never query anything and never
decide permissions. This document records the boundary the implementation
must preserve.

The store lives in a **different** feature: `accesscontrol.Store`. A feature
never imports another feature, so `team` declares function types and
`app/router.go` passes closures over `accesscontrol.Store` from the composition
root.

The screen implements the accepted rule from `PRODUCT_FEATURES.md`: owners and
admins reach every location by role, managers and members reach only the
locations explicitly assigned to them. It is backed by
`user_location_assignments` (migration 0013).

## Routes the templates need

| Method | Path | Effect |
|---|---|---|
| `GET` | `/team` | `Index(Page)` |
| `POST` | `/team/invite` | enrol an employee, then **render the page** carrying their code |
| `POST` | `/team/{userID}/locations` | atomically replace that member's assignments, then redirect to `/team?saved=1` |
| `POST` | `/team/{userID}/name` | correct a name, then redirect to `/team?saved=renamed` |
| `POST` | `/team/{userID}/code` | mint a fresh code, then **render the page** carrying it |
| `POST` | `/team/{userID}/revoke` | remove the person, then redirect to `/team?saved=removed` |

All sit behind `auth.RequireTenant`; the workspace shell links to `/team`.

The two that render instead of redirecting do so on purpose: the code they
return exists nowhere else. `staff_invites` keeps only its SHA-256 hash, so a
redirect would discard the one copy that can ever be shown.

## Filling Page

```go
team.Page{
    Organization: workspace.Name,
    Members:      members,   // every membership of the organization
    Locations:    sites,     // every location, active and inactive
    CanManage:    role == "owner" || role == "admin",
    Notice:       team.Notice{},
    Invite:       team.Invitation{}, // set on exactly one render, see below
    InvitedName:  "",
}
```

`Member.InviteState` is `""` for someone who has signed in at least once,
`"pending"` while their code is live, and `"expired"` once it is not. It comes
from a `LEFT JOIN` on the one `staff_invites` row with `accepted_at IS NULL` —
a partial unique index guarantees there is never more than one.

`Member.LocationIDs` holds the **active** assignments only — rows of
`user_location_assignments` where `revoked_at IS NULL`. Leave it empty for
owners and admins: the screen shows their implicit access instead of offering
checkboxes, and `Member.Unassigned()` deliberately never flags them.

`Member.Name` may be empty; the screen falls back to the email, so at least one
of the two must be set.

Inactive locations are still listed and still assignable, marked "Inactif". A
site can be reopened, and dropping its staff on deactivation would silently
lose the assignments.

## Handling the assignment POST

The form posts the checked sites as repeated `location_ids` values
(`team.FieldLocations`), so:

```go
selected := r.Form[team.FieldLocations]
```

An empty slice is a legitimate submission: it means "this person reaches no
site". It is not treated as a missing field.

The POST carries the complete desired state, not a delta, because a checkbox
set has no other honest reading. `accesscontrol.Store.ReplaceLocationAssignments`
validates every requested location and applies the complete desired state in
one transaction. If any location is invalid, none of the changes are committed.

`user_location_assignments` keeps revoked rows: revoking sets `revoked_at` and
`revoked_by_user_id` rather than deleting, and the partial unique index already
allows re-assigning the same pair later.

Reject a POST targeting an owner or an admin: their access comes from the role,
and the screen never offers those checkboxes.

## Handling the invitation POSTs

`POST /team/invite` takes a name (`team.FieldName`) and the same repeated
`location_ids` as the assignment form. Every employee is enrolled as `member`:
the screen offers no role picker, because `manager` and `member` are
indistinguishable to every policy in the schema, and `admin` would hand over
the whole organization.

`Page.Invite` carries the credential two ways — `Code` to type, `Link` to tap —
and `Page.InvitedName` says whose it is, so an owner enrolling three people in
a row cannot hand the wrong code to the wrong person. Set both only on the
response that minted it; a later `GET /team` must not show them again.

`POST /team/{userID}/code` retires the person's previous pending code before
minting one. It is the answer to a second device or a lost code, and the only
reason a member who has already signed in still needs this screen.

Renaming and removal are refused for owners and admins, and the screen hides
both controls for them (`Member.Removable()`). An owner's name is rewritten
from their identity provider at every login, so editing it would quietly
revert; removing one is not the routine staff change this screen offers.

## Notices

`Notice.Kind` picks the styling and the French heading; the handler writes only
the sentence.

- `NoticeSuccess` — after assignments were saved, a name corrected, or someone
  removed. The `saved` query parameter picks the sentence.
- `NoticeError` — the store failed, or the request was refused. A rejected
  invitation form re-renders the screen carrying this rather than replacing it
  with a bare error string.

## Permissions

`CanManage` is the only permission input: false drops every form and renders
the read-only presentation. The handler must still refuse the POST itself —
hiding a form is not a check.

## Not covered here

Changing membership roles and customer-to-location sharing grants are separate
concerns. Signing a staff device out without removing the person, and showing
whether and when someone signed in, are unbuilt.

Accepting an invitation belongs to `auth`, not here: `GET /rejoindre/{token}`
previews without consuming (a messenger's preview fetch must not burn an
employee's only way in), `POST /rejoindre/{token}` consumes it, and
`GET`/`POST /rejoindre` is the typed-code way in for a machine nobody can send
a link to.
