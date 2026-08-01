# Customers UI — what the handler owes the views

The `customers` feature ships the search screen (`views.templ`) and its view
models (`view_data.go`) without a handler or a store. The views never query
anything, never rank results, and never decide who may see whom.

## Routes the templates need

| Method | Path | Effect |
|---|---|---|
| `GET` | `/customers` | `Index(Page)`; reads the query from `?q=` |
| `GET` | `/customers/{id}` | the customer profile — a later slice, already linked from each result |

Both sit behind `auth.RequireTenant`. **Nothing links to `/customers` yet**: the
shell has no entry, because a link to a route that 404s is worse than no link.
`ui.SectionCustomers` already exists — add the entry in `ui.shell()` in the same
change that registers the route.

Each result already links to `/customers/{id}`. Land that route in the same
change, or the list ships with dead links.

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

## Notices

`NoticeError` is the only kind this screen uses: the search failed. The handler
writes the sentence, the view supplies the heading and the styling.

## Not covered here

Creating a customer, merging duplicates, the profile with vehicles and repair
timeline, and granting or revoking a share are separate screens. There is
deliberately no "new customer" action here: most records are created by the
telephone agent, and a button pointing at a form that does not exist would be
a dead link.
