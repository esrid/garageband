# Calls UI — what the handler owes the views

The `calls` feature ships the call inbox and the transcript page
(`views.templ`) with their view models (`view_data.go`), and no handler or
store. The views never query anything and never decide permissions.

## Routes the templates need

| Method | Path | Effect |
|---|---|---|
| `GET` | `/calls` | `Index(Inbox)`; `?status=attention` keeps only the calls wanting a human |
| `GET` | `/calls/{id}` | `Show(Transcript)` |

Both sit behind `auth.RequireTenant`. **Nothing links to `/calls` yet**: the
shell has no entry, because a link to a route that 404s is worse than no link.
`ui.SectionCalls` exists — add the entry in `ui.shell()` in the same change that
registers the routes.

The inbox links identified callers to `/customers/{id}`, so land the customer
routes too or those links are dead.

## Timezones are the handler's problem

Every `time.Time` is rendered exactly as given. Convert to the location's
timezone before filling these models; the views have no idea which workshop a
call belongs to.

## Filling Inbox

```go
calls.Inbox{
    Organization: workspace.Name,
    Calls:        rows,   // ordered by started_at, newest first
    Filter:       r.URL.Query().Get(calls.FieldStatus), // "" or "attention"
    Notice:       calls.Notice{},
}
```

The view does **not** sort and does **not** filter: when `Filter` is
`FilterNeedsAttention`, hand over only the matching rows. What matches is
`Call.NeedsAttention()` — nobody answered, or the agent never matched the caller
to a customer. Reuse that method rather than reimplementing the rule in SQL, or
the banner and the filter will disagree.

`Call.CustomerID` empty is what "unrecognised caller" means; the screen says so
in words rather than leaving a blank. `Call.CallerNumber` should already be
formatted for display, and may be empty for a withheld number.

`Call.HasRecording` is `recording_uri <> ''`. The screen only states that a
recording exists — playback, retention and the lawful-purpose workflow are a
separate slice.

## Filling Transcript

```go
calls.Transcript{
    Organization: workspace.Name,
    Call:         call,
    Messages:     messages, // ordered by call_messages.sequence, ascending
}
```

Order by `sequence`, not by `occurred_at`: tool lines are written with the
timestamp of the action, which can precede the sentence that announced it, and
sorting by time would scramble the conversation.

`Message.Speaker` comes straight from the database. `system` and `tool` are not
people and are drawn apart from speech — a tool line reads "Action de l'agent",
so a reader never mistakes a lookup for something the agent said aloud.

A call nobody answered has no transcript to open: the inbox hides the link, and
the page states the absence rather than showing an empty conversation.

## Not covered here

Recording playback and retention, attaching an unrecognised call to a customer,
outbound calling, and call quality review are separate slices.
