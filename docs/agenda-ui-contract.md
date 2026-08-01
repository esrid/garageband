# Agenda UI — what the handler owes the views

The `agenda` feature ships the day agenda and the booking form (`views.templ`)
with their view models (`view_data.go`), and no handler or store. The views
never query anything, never compute availability, and never decide permissions.

## Routes the templates need

| Method | Path | Effect |
|---|---|---|
| `GET` | `/agenda?date=YYYY-MM-DD` | `Show(Day)`; today when the parameter is absent or unparsable |
| `GET` | `/agenda/new?customer=…` | `Form(FormPage)` with an empty `ID` |
| `POST` | `/agenda` | create, then redirect to `/agenda?date=…` of the booked day |
| `GET` | `/agenda/{id}` | `Form(FormPage)` for an existing appointment |
| `POST` | `/agenda/{id}` | update, then redirect to the agenda |
| `POST` | `/agenda/{id}/cancel` | set the status to `cancelled`, then redirect |

All sit behind `auth.RequireTenant`. **Nothing links to `/agenda` yet**: the
shell has no entry, because a link to a route that 404s is worse than no link.
`ui.SectionAgenda` exists — add the entry in `ui.shell()` in the same change
that registers the routes.

The agenda links each appointment to `/customers/{id}`, so land the customer
routes too or the day ships with dead links.

## Timezones are the handler's problem

Every `time.Time` in these models is rendered exactly as given. Convert to the
location's timezone (`locations.timezone`) before filling them, and parse the
`date` and `start_time` fields back **in that same zone** when saving. The views
have no idea which workshop they are showing, so they cannot do it, and a UTC
slip here books cars at the wrong hour.

## Filling Day

```go
agenda.Day{
    Organization: workspace.Name,
    LocationName: location.Name,
    Date:         day,          // midnight in the location's zone
    Appointments: appointments, // ordered by starts_at, ascending
    CanManage:    canManage,
    Notice:       agenda.Notice{},
}
```

The view does **not** sort. Include cancelled and no-show appointments: they
stay visible for the record, and `Day.Booked()` excludes them from the count so
the day does not read as fuller than it is. That method mirrors the statuses
the database counts in its double-booking exclusion constraint
(`pending`, `confirmed`, `in_progress`) — if the constraint ever changes, change
both.

`Appointment.Source` comes straight from `appointments.source`. Only `agent`,
`calendar` and `import` render a label; a `dashboard` booking is the norm and
says nothing.

## Filling FormPage

```go
agenda.FormPage{
    ID:        appointment.ID,   // empty ⇒ "Nouveau rendez-vous", posts to /agenda
    Customer:  agenda.CustomerRef{ID: customer.ID, Label: name},
    Vehicles:  vehicleOptions,   // that customer's vehicles only
    Services:  serviceOptions,   // the location's service_offerings, duration in the label
    Resources: resourceOptions,  // the location's active bookable_resources
    Values:    agenda.FormValues{Date: "2026-03-12", StartTime: "09:00", …},
    Cancellable: appointment.Status != "cancelled" && appointment.Status != "completed",
}
```

`Customer` must be resolved before rendering. With none, the screen shows a
prompt to go and find one instead of a form that cannot be submitted — this is
deliberate: there is no customer picker yet, and the realistic paths into
booking are from a customer profile or from a call.

The end of the appointment is derived from the chosen service's
`duration_minutes` plus its buffers; the form asks only for a start. Put the
duration in the service option label (`ui.FormatDuration`) so the choice is
informed.

## The conflict state is not a validation error

`appointments` carries an exclusion constraint that refuses to book a resource
twice over the same range. Catch that violation and report it as
`NoticeConflict` with a sentence naming the resource and the clashing hours —
not as a generic failure, and not as `NoticeInvalid`, because nothing the user
typed is malformed. `NoticeInvalid` is for `FieldErrors`, which render inline
against their control.

## Permissions

`CanManage` is the only permission input: false hides booking and editing but
still shows the day. The handler must refuse the write routes itself — hiding a
button is not a check.

## Not covered here

Availability search, opening-hours and buffer validation, rescheduling by drag,
week and month views, reminders, and Google Calendar synchronisation are
separate slices.
