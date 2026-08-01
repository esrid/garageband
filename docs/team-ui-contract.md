# Team UI — what the handler owes the views

The `team` feature ships the "Accès aux sites" screen (`views.templ`) and its
view models (`view_data.go`) without a handler or a store. This is what the
backend has to provide. The views never query anything and never decide
permissions.

The store already exists, in a **different** feature: `accesscontrol.Store`
(`AssignLocation`, `RevokeLocationAssignment`). A feature never imports another
feature, so `team` must not import it. Wire it the way `dashboard` is wired in
`app/router.go`: declare function types in `team` and pass closures over
`accesscontrol.Store` from the composition root.

The screen implements the accepted rule from `PRODUCT_FEATURES.md`: owners and
admins reach every location by role, managers and members reach only the
locations explicitly assigned to them. It is backed by
`user_location_assignments` (migration 0013).

## Routes the templates need

| Method | Path | Effect |
|---|---|---|
| `GET` | `/team` | `Index(Page)` |
| `POST` | `/team/{userID}/locations` | replace that member's assignments, then redirect to `/team` |

Both sit behind `auth.RequireTenant`. **Nothing links to `/team` yet**: the
shell deliberately has no navigation entry, because a link to a route that
404s is worse than no link. `ui.SectionTeam` already exists — add the entry in
`ui.shell()` in the same change that registers the routes.

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
site". Do not treat it as a missing field.

The POST carries the complete desired state, not a delta, because a checkbox
set has no other honest reading. The handler diffs it against the member's
current assignments and calls `accesscontrol.Store.AssignLocation` for each
added site and `RevokeLocationAssignment` for each removed one.

Note the asymmetry: revocation takes an **assignment id**, not a location id,
so whatever loads the page must keep the id of each active assignment around —
`Member.LocationIDs` carries location ids only, since that is all the screen
draws. Resolve the assignment id in the handler.

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
