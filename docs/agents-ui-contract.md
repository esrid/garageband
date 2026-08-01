# Agents UI — what the handler owes the views

The `agents` feature ships the agent list and the configuration screen
(`views.templ`) with their view models (`view_data.go`), and no handler or
store. The views never query anything and never decide permissions.

## Routes the templates need

| Method | Path | Effect |
|---|---|---|
| `GET` | `/agents` | `List(Index)` |
| `GET` | `/agents/{id}` | `Form(FormPage)` |
| `POST` | `/agents/{id}` | save the configuration, then redirect |
| `POST` | `/agents/{id}/activate` | set the status to `active`, then redirect |
| `POST` | `/agents/{id}/pause` | set the status to `paused`, then redirect |

All sit behind `auth.RequireTenant`. **Nothing links to `/agents` yet**:
`ui.SectionAgents` exists — add the entry in `ui.shell()` in the change that
registers the routes.

There is no create route. An agent belongs to a location, so create it with the
location rather than as a separate act; the empty list points at `/locations`.

## Readiness is the point of this screen

The product ships provider *ports*, not provider integrations. An agent with no
`llm`, `speech_to_text` and `text_to_speech` connection cannot answer a call
whatever its status column says, and the screen refuses to pretend otherwise.

- `Agent.Missing` holds the provider kinds with no active `provider_connections`
  row for the agent's location. Empty means ready.
- `Agent.Reachable()` is the honest question — active, ready, and with at least
  one telephone number pointing at it. The list distinguishes the three ways it
  can fail, because "not configured", "no line" and "switched off" call for
  different actions.
- On the form, `FormPage.Missing()` requires a selected, active connection from
  each location-specific list. When a list is empty the screen explains it
  instead of rendering an empty select; when choices exist but none is selected,
  the form remains editable but the activation control is not offered.

Configuration stays editable while unready, on purpose: writing the greeting is
useful before a provider exists, and the screen says so.

## Filling the models

```go
agents.Index{
    Organization: workspace.Name,
    Agents:       rows,       // one per location, ordered by location name
    CanManage:    role == "owner" || role == "admin",
}

agents.FormPage{
    ID: agent.ID, Status: agent.Status,
    Numbers:  numbers,        // phone_numbers routed here, formatted for display
    Values:   agents.FormValues{…},
    LLMConnections: llm, STTConnections: stt, TTSConnections: tts,
    Locales:  locales,
    CanManage: canManage,
}
```

`Values.Prompt` maps to `agents.system_prompt`, `Values.Fallback` to
`fallback_message`. The field names the form posts are the `Field*` constants,
so parsing is `r.FormValue(agents.FieldGreeting)` and so on.

## Lifecycle

Activation and pause are separate routes, not a status field in the form: they
change what customers hear, and the screen keeps them outside the save form so
saving a greeting can never put an agent on the line. Refuse an activation for
an agent whose providers are missing — the screen hides the control, and hiding
a control is not a check.

## Not covered here

Connecting providers, buying or routing telephone numbers, per-agent opening
hours, escalation rules, call quality review and prompt versioning are separate
screens.
