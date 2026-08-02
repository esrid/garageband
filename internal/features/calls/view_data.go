// Package calls renders the telephone call inbox and one call's transcript.
//
// It owns no data and talks to no database: a handler builds these view models
// from the store. The contract is written down in docs/calls-ui-contract.md.
// The models are local on purpose — a feature never imports another feature.
//
// Every time handed to this package is already in the workshop's timezone;
// only the handler knows which location a call belongs to.
package calls

import (
	"strconv"
	"strings"
	"time"
)

// FieldStatus is the inbox filter, and therefore the query-string key.
const FieldStatus = "status"

// FilterNeedsAttention keeps only the calls a human still has to deal with.
const FilterNeedsAttention = "attention"

// Notice kinds. The view derives the heading from the kind, so French copy
// stays in the view layer instead of leaking into handlers.
const NoticeError = "error"

// Notice is a single server-side outcome shown at the top of a screen.
type Notice struct {
	Kind    string
	Message string
}

func (n Notice) Empty() bool { return strings.TrimSpace(n.Message) == "" }

// Speakers of a transcript line, as constrained by call_messages.speaker.
const (
	SpeakerCaller = "caller"
	SpeakerAgent  = "agent"
	SpeakerSystem = "system"
	SpeakerTool   = "tool"
)

// Call is one telephone call in the inbox.
type Call struct {
	ID           string
	StartedAt    time.Time
	EndedAt      time.Time
	Direction    string // inbound or outbound
	Status       string // calls.status
	CallerNumber string // already formatted for display
	CustomerID   string // empty when the caller was not recognised
	CustomerName string
	LocationName string
	Summary      string
	Outcome      string
	HasRecording bool
}

// Identified reports whether the agent matched the caller to a customer. An
// unmatched call is the one a human most often has to pick up.
func (c Call) Identified() bool { return strings.TrimSpace(c.CustomerID) != "" }

// Answered reports whether anyone actually spoke. A missed call has no
// transcript worth opening, and the inbox says so instead of showing an empty
// conversation.
func (c Call) Answered() bool {
	switch c.Status {
	case "completed", "in_progress":
		return true
	}
	return false
}

// NeedsAttention marks a call a human should look at: nobody picked up, or it
// ended without the agent recognising who called.
func (c Call) NeedsAttention() bool {
	return !c.Answered() || !c.Identified()
}

// Duration is how long the call lasted, empty while it is still running or
// when it never connected.
func (c Call) Duration() string {
	if c.StartedAt.IsZero() || c.EndedAt.IsZero() || !c.EndedAt.After(c.StartedAt) {
		return ""
	}
	seconds := int(c.EndedAt.Sub(c.StartedAt).Seconds())
	if seconds < 60 {
		return strconv.Itoa(seconds) + " s"
	}
	minutes := seconds / 60
	rest := seconds % 60
	if rest == 0 {
		return strconv.Itoa(minutes) + " min"
	}
	return strconv.Itoa(minutes) + " min " + strconv.Itoa(rest) + " s"
}

// Inbox backs the call list.
type Inbox struct {
	Organization string
	Calls        []Call
	Filter       string // empty for everything, FilterNeedsAttention otherwise
	Notice       Notice
}

func (i Inbox) FilteringAttention() bool { return i.Filter == FilterNeedsAttention }

// AttentionCount counts the calls that still want a human, which is what the
// filter is worth offering for.
func (i Inbox) AttentionCount() int {
	count := 0
	for _, call := range i.Calls {
		if call.NeedsAttention() {
			count++
		}
	}
	return count
}

// Message is one line of a transcript.
type Message struct {
	Speaker    string
	Content    string
	OccurredAt time.Time
}

// Transcript backs one call's page.
type Transcript struct {
	Organization string
	Call         Call
	Messages     []Message
	Notice       Notice
}

func callPath(call Call) string { return "/calls/" + call.ID }

func customerPath(call Call) string { return "/customers/" + call.CustomerID }

// directionLabel says which way the call went.
func directionLabel(direction string) string {
	if direction == "outbound" {
		return "Sortant"
	}
	return "Entrant"
}

// statusLabel translates calls.status.
func statusLabel(status string) string {
	switch status {
	case "ringing":
		return "Sonne"
	case "in_progress":
		return "En cours"
	case "completed":
		return "Terminé"
	case "failed":
		return "Échec"
	case "busy":
		return "Occupé"
	case "no_answer":
		return "Sans réponse"
	case "cancelled":
		return "Annulé"
	}
	return status
}

// speakerLabel names who is talking. "system" and "tool" are not people: they
// are what the agent did, and saying so keeps a transcript honest.
func speakerLabel(speaker string) string {
	switch speaker {
	case SpeakerCaller:
		return "Client"
	case SpeakerAgent:
		return "Agent"
	case SpeakerSystem:
		return "Système"
	case SpeakerTool:
		return "Action de l'agent"
	}
	return speaker
}

// callSummary counts the inbox in words, agreeing in number.
func callSummary(count int) string {
	switch count {
	case 0:
		return "Aucun appel"
	case 1:
		return "1 appel"
	}
	return strconv.Itoa(count) + " appels"
}

// attentionSummary describes the backlog, agreeing in number.
func attentionSummary(count int) string {
	if count == 1 {
		return "1 appel demande votre attention"
	}
	return strconv.Itoa(count) + " appels demandent votre attention"
}

// Label is what to call this caller on screen: the customer when the agent
// recognised them, the number otherwise, and a plain statement when the number
// was withheld.
func (c Call) Label() string {
	switch {
	case c.Identified() && strings.TrimSpace(c.CustomerName) != "":
		return c.CustomerName
	case strings.TrimSpace(c.CallerNumber) != "":
		return c.CallerNumber
	default:
		return "Numéro masqué"
	}
}

// currentFilter renders the aria-current value of a filter link.
func currentFilter(active bool) string {
	if active {
		return "page"
	}
	return "false"
}
