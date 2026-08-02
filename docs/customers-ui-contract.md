# Customers UI — what the handler owes the views

The `customers` feature ships the search screen (`views.templ`) and its view
models (`view_data.go`) without a handler or a store. The views never query
anything, never rank results, and never decide who may see whom.

## Routes the templates need

| Method | Path | Effect |
|---|---|---|
| `GET` | `/customers` | `Index(Page)`; reads the query from `?q=` |
| `GET` | `/customers/{id}` | `Show(Profile)` — the profile, linked from each result |
| `POST` | `/customers/{id}/shares` | Grant a share to another site; owner/admin only |
| `POST` | `/customers/{id}/shares/{grantID}/revoke` | Revoke one share; owner/admin only |
| `POST` | `/customers/{id}/offboard` | Soft-delete the customer and their active contacts; owner/admin only |

Both sit behind `auth.RequireTenant`. **Nothing links to `/customers` yet**: the
shell has no entry, because a link to a route that 404s is worse than no link.
`ui.SectionCustomers` already exists — add the entry in `ui.shell()` in the same
change that registers the route.

Each result links to `/customers/{id}`. Land both routes in the same change, or
the list ships with dead links.

## Filling Page

```go
customers.Page{
    Organization: workspace.Name,
    Query:        r.URL.Query().Get(customers.FieldQuery), // "q"
    Customers:    results,
    Notice:       customers.Notice{},
}
```

`Query` empty renders the "start searching" state; `Query` set with no results
renders the "nothing matched" state, which is a different message. Echo the
query back unchanged so a typo can be corrected instead of retyped.

`Customer.Phone` and `Customer.Email` are the **primary** contact of each kind
(`customer_contacts.is_primary`), already formatted for display. The screen
shows the phone first because that is how customers arrive.

`Customer.Vehicles` may hold the whole fleet; the card caps the list at three
and appends the overflow count itself, so there is no need to truncate in SQL.

## Search behaviour the backend owns

The doc accepts search by name, phone, email, plate and VIN. The screen only
sends the raw string; normalising it is the handler's job — in particular a
plate typed with or without hyphens must match, which the hint under the field
promises the user.

## Who may see whom

This is the part that must not be retrofitted. A customer belongs to its
`home_location_id`, and reaches other locations only through an active row in
`customer_location_grants`. The result set must therefore contain only:

- customers whose home location the user reaches, and
- customers explicitly granted to a location the user reaches.

Set `Customer.Shared` on the second kind: the screen marks those "Partagé avec
vous", so the person knows the record is not theirs to treat as private. Set
`HomeLocationName` on every result — with several sites in one organization,
"which workshop owns this customer" is part of reading the row.

Enforce this in the query and in RLS, not by trusting the view. The view has no
concept of permission at all.

## Filling Profile

```go
customers.Profile{
    Organization: workspace.Name,
    Customer:     customer,       // same type as a search result
    Vehicles:     vehicles,       // every vehicle of the dossier
    Timeline:     events,         // repairs and appointments, newest first
    Memories:     memories,       // what the agent retained
    CanEdit:      actorCanAccess(customer.HomeLocationID),
    CanManage:    actorManagesTenant, // gates sharing + offboard, not CanEdit's rule
    Grants:       grants,             // full share history, active and revoked
    ShareOptions: shareOptions,       // active sites not already sharing this dossier
    Notice:       customers.Notice{},
}
```

`CanManage` answers a different question than `CanEdit`: it is
`app_current_user_manages_tenant()` (owner/admin), not home-location
ownership. A staff member assigned to the home location can edit the
dossier but still cannot grant a share or offboard the customer; an
owner/admin at any site can. `Grants` and `ShareOptions` are only worth
fetching when `CanManage` is true — the store skips both queries otherwise.

The view does **not** sort: hand `Timeline` over already ordered, newest first,
merging `repair_orders` and `appointments` into one list. Use `opened_at` for a
repair and `starts_at` for an appointment.

`Event.AuthoredHere` means the actor can access the event's authoring location.
This supports employees assigned to several sites without inventing a session
location selector for a read-only profile. An entry from an inaccessible
workshop is labelled "Autre site, en lecture", and the banner above says the
dossier may be read and added to but not rewritten. Set `Event.Status` to the
raw database value — the view holds the French labels for both
`repair_orders.status` and `appointments.status`, and shows an unknown value
through rather than swallowing it.

Money arrives as integer cents plus a currency code, straight from
`repair_orders.total_cents` / `currency`. Zero renders as nothing rather than
"0,00 €", so leave it at zero when there is no amount instead of inventing one.
Dates arrive as `time.Time`; the view formats them in French, and the zero time
renders "Date inconnue" rather than year 1.

`Memory.Confidence` is the `NUMERIC(4,3)` score as a 0–1 float. Zero means the
provider gave none and prints nothing — do not substitute a default, or an
unscored memory will read as a worthless one.

## Notices

Two kinds: `NoticeError` (search failed, or a write was rejected) and
`NoticeSuccess` (a share was granted or revoked, or the customer was
offboarded). The handler writes the sentence and picks the kind from a
`?notice=` redirect code (`shared`, `revoked`, `offboarded`,
`grant_duplicate`, `grant_same_location`, `error`); the view supplies the
heading, icon, and styling for each kind.

## Sharing and offboarding

Both live on the profile screen behind `CanManage`, not as separate screens.
Granting writes `source_location_id` as the customer's *current*
`home_location_id` — a database foreign key enforces this, not a Go check —
so a stale caller-supplied source location can never be inserted. Revoking
never deletes the row: `revoked_at`/`revoked_by_user_id` are set once and the
row stays as history, same as everywhere else in this schema. Offboarding
soft-deletes the customer and their active contacts
(`customers.deleted_at`, `customer_contacts.deleted_at`); the active-contact
unique index is partial (`WHERE deleted_at IS NULL`), so a freed phone or
email becomes assignable to a new customer with no separate release step.
After offboarding, the handler redirects to `/customers`, not back to the
profile — the profile query filters `deleted_at IS NULL`, so the dossier
would 404 right after the action that just succeeded.

## Not covered here

Creating a customer, editing one, merging duplicates, and reviewing or
correcting the agent's memories are separate screens (the last two live in
the operations assistant's confirm-in-chat flow, not here). There is
deliberately no "new customer" action here: most records are created by the
telephone agent, and a button pointing at a form that does not exist would be
a dead link.
