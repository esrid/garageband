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
| `POST` | `/team/{userID}/locations` | atomically replace that member's assignments, then redirect to `/team?saved=1` |

Both sit behind `auth.RequireTenant`; the workspace shell links to `/team`.

## Filling Page

```go
team.Page{
    Organization: workspace.Name,
    Members:      members,   // every membership of the organization
    Locations:    sites,     // every location, active and inactive
    CanManage:    role == "owner" || role == "admin",
    Notice:       team.Notice{},
}
```

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

## Notices

`Notice.Kind` picks the styling and the French heading; the handler writes only
the sentence.

- `NoticeSuccess` — after assignments were saved.
- `NoticeError` — the store failed, or the request was refused.

## Permissions

`CanManage` is the only permission input: false drops every form and renders
the read-only presentation. The handler must still refuse the POST itself —
hiding a form is not a check.

## Not covered here

Inviting users, changing membership roles, and customer-to-location sharing
grants are separate screens.
