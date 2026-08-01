# Locations UI — what the handler owes the views

This records the boundary between the `locations` screens (`views.templ`, with
their view models in `view_data.go`) and the handler that feeds them
(`handler.go`, `routes.go`). The views never query anything and never decide
permissions — everything below is what the handler owes them.

## Routes the templates already link to

| Method | Path | Screen / effect |
|---|---|---|
| `GET` | `/locations` | `Index(IndexPage)` — the list |
| `GET` | `/locations/new` | `Form(FormPage)` with an empty `ID` |
| `POST` | `/locations` | create, then redirect to `/locations` |
| `GET` | `/locations/{id}` | `Form(FormPage)` for an existing site |
| `POST` | `/locations/{id}` | update, then redirect to `/locations` |
| `GET` | `/locations/{id}/schedule` | weekly opening hours and upcoming exceptional closures |
| `POST` | `/locations/{id}/schedule/hours` | add a non-overlapping local-time opening window |
| `POST` | `/locations/{id}/schedule/hours/delete` | remove the exact window named by hidden fields |
| `POST` | `/locations/{id}/schedule/closures` | add an exceptional closure parsed in the location timezone |
| `POST` | `/locations/{id}/schedule/closures/{closureID}/delete` | remove an exceptional closure |
| `POST` | `/locations/{id}/schedule/resources` | add a technician, bay, equipment, or calendar resource |
| `POST` | `/locations/{id}/schedule/resources/{resourceID}/active` | activate/deactivate capacity without deleting history |
| `POST` | `/locations/{id}/schedule/requirements` | upsert one resource kind/quantity required by a service |
| `POST` | `/locations/{id}/schedule/requirements/delete` | return that service kind to manual selection |
| `POST` | `/locations/{id}/deactivate` | `Store.SetStatus(..., "inactive")` |
| `POST` | `/locations/{id}/reactivate` | `Store.SetStatus(..., "active")` |

All of them are registered by `Register` behind the `requireTenant` middleware
and run inside the tenant/user scope. The shared navigation links to
`/locations`; the edit screen is the entry point into a site's schedule.

## Weekly schedule and closures

`ScheduleView(SchedulePage)` renders every weekday, including closed days, and
supports multiple windows on one day for lunch breaks. The first inserted
window durably enables schedule enforcement. Removing the final window does
not silently return the site to unrestricted legacy booking: the enabled site
is closed until another window is added.

Closure date/time values are parsed in `Location.Timezone`, stored as
`TIMESTAMPTZ`, and converted back to that timezone before rendering. Only
upcoming closures are shown. PostgreSQL rejects overlapping windows,
overlapping closures, closures over active appointments, and removal of an
opening window that would strand a future active appointment. These conflicts
are user-correctable outcomes, not server errors.

The location timezone becomes immutable once the site has an appointment or a
closure. Changing it later would reinterpret weekly hours and shift historical
local-time displays, so PostgreSQL rejects the update and the location form
ties the explanation to its timezone field.

The same screen owns workshop capacity. Owners/admins name bookable resources
and declare each active scheduling service's required kinds and quantities.
Members can inspect both but cannot write. A resource with a future active
reservation cannot be deactivated, and a new/upscaled requirement cannot
invalidate future appointments. Services without requirements retain explicit
manual resource selection; services with at least one requirement are allocated
automatically by the agenda store.

## Filling IndexPage

```go
locations.IndexPage{
    Organization: workspace.Name,   // shown so the scope is unambiguous
    Locations:    overview.Locations, // Store.Overview rows, already ordered
    CanManage:    overview.CanManage, // false ⇒ read-only rendering
    Notice:       locations.Notice{}, // see below
}
```

`Location.Status` drives the Actif/Inactif badge; only `"active"`
(`locations.StatusActive`) counts as open. Configuration completeness is derived
by the view from the row itself (`setupOf`), so there is nothing to compute in
the handler.

## Filling FormPage

```go
locations.FormPage{
    ID:          location.ID,  // empty ⇒ "Ajouter un site", posts to /locations
    Active:      location.Status == locations.StatusActive,
    Values:      locations.Input{...}, // exactly what Store.Create/Update take
    FieldErrors: map[string]string{locations.FieldPostalCode: "…"},
    Notice:      locations.Notice{Kind: locations.NoticeInvalid, Message: "…"},
    CanManage:   overview.CanManage,
}
```

The form posts the `Field*` constants as its input names, so parsing is
`r.FormValue(locations.FieldPostalCode)` and so on — one source of truth for
both sides. `FieldPhone` maps to `Input.PhoneE164` and `FieldWebsite` to
`Input.WebsiteURL`.

## Notices

`Notice.Kind` picks the styling and the French heading; the handler only writes
the sentence.

- `NoticeError` — the store or an upstream service failed.
- `NoticeInvalid` — the submission needs corrections; pair it with `FieldErrors`.
- `NoticeSuccess` — after a successful create, update or status change.

Field-level messages go in `FieldErrors` and are rendered next to the control,
tied to it by `aria-describedby` and `aria-invalid`. A message that belongs to
no single field goes in the `Notice`.

## Permissions

`CanManage` is the only permission input. When it is false the index drops the
add button and every per-site action, and the form renders a read-only summary
instead of controls. The handler must still refuse the write routes itself —
`Store` already returns `ErrForbidden`, and hiding a button is not a check.

## Not covered here

Customer sharing and employee permissions beyond `CanManage` are out of scope
for this UI slice.
