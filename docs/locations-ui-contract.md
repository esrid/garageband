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
| `POST` | `/locations/{id}/deactivate` | `Store.SetStatus(..., "inactive")` |
| `POST` | `/locations/{id}/reactivate` | `Store.SetStatus(..., "active")` |

All of them are registered by `Register` behind the `requireTenant` middleware
and run inside the tenant scope. Nothing in the app links to `/locations` yet:
the navigation shell is a separate slice, so the entry point is added there
rather than left as a dead link somewhere else.

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

Customer sharing, employee permissions beyond `CanManage`, and the navigation
entry point into `/locations` are out of scope for the UI slice.
