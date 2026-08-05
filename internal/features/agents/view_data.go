// Package agents renders the configuration of a location's telephone agent.
//
// The views never query anything and never decide permissions: a handler
// builds these view models from the store, and views_test.go is the contract.
// The models are local on purpose — a feature never imports another feature.
package agents

import (
	"strconv"
	"strings"
)

// Statuses, as constrained by agents.status.
const (
	StatusDraft    = "draft"
	StatusActive   = "active"
	StatusPaused   = "paused"
	StatusArchived = "archived"
)

// Provider connection kinds an agent needs before it can answer a call.
const (
	KindLLM = "llm"
	KindSTT = "speech_to_text"
	KindTTS = "text_to_speech"
)

// Form field names, which are also what the handler parses.
const (
	FieldName     = "name"
	FieldGreeting = "greeting"
	FieldPrompt   = "system_prompt"
	FieldFallback = "fallback_message"
	FieldLocale   = "locale"
	FieldLLM      = "llm_connection_id"
	FieldSTT      = "speech_to_text_connection_id"
	FieldTTS      = "text_to_speech_connection_id"
)

// Notice kinds. The view derives the heading from the kind, so French copy
// stays in the view layer instead of leaking into handlers.
const (
	NoticeError   = "error"
	NoticeInvalid = "invalid"
	NoticeSuccess = "success"
)

// Notice is a single server-side outcome shown at the top of a screen.
type Notice struct {
	Kind    string
	Message string
}

func (n Notice) Empty() bool { return strings.TrimSpace(n.Message) == "" }

// Agent is one location's telephone agent as the list shows it.
type Agent struct {
	ID           string
	Name         string
	LocationName string
	Status       string
	// Missing names the provider kinds still unconnected. An agent cannot
	// answer a call without all three, whatever its status says.
	Missing []string
	// Numbers are the telephone numbers routed to this agent, already
	// formatted for display.
	Numbers []string
}

// Ready reports whether every provider the agent needs is connected.
func (a Agent) Ready() bool { return len(a.Missing) == 0 }

// Answering reports whether this agent is supposed to be picking up right now.
func (a Agent) Answering() bool { return a.Status == StatusActive }

// Reachable reports whether a caller can actually get through: an active agent
// with its providers and at least one number pointing at it.
func (a Agent) Reachable() bool { return a.Answering() && a.Ready() && len(a.Numbers) > 0 }

// Index backs the agent list.
type Index struct {
	Organization string
	Agents       []Agent
	CanManage    bool
	Notice       Notice
}

// Option is one entry of a select control.
type Option struct {
	Value string
	Label string
}

// FormValues holds the editable fields, keyed like the POST body.
type FormValues struct {
	Name     string
	Greeting string
	Prompt   string
	Fallback string
	Locale   string
	LLM      string
	STT      string
	TTS      string
}

// FormPage backs the configuration screen of one agent.
type FormPage struct {
	ID           string
	Organization string
	LocationName string
	Status       string
	Numbers      []string
	Values       FormValues
	// Connections lists what is available per provider kind. An empty list
	// means nothing of that kind is connected yet, which the screen explains
	// rather than showing an empty select.
	LLMConnections []Option
	STTConnections []Option
	TTSConnections []Option
	Locales        []Option
	FieldErrors    map[string]string
	Notice         Notice
	CanManage      bool
}

func (p FormPage) Error(field string) string { return p.FieldErrors[field] }

func (p FormPage) HasError(field string) bool { return p.FieldErrors[field] != "" }

// Missing names the provider kinds with nothing to choose from.
func (p FormPage) Missing() []string {
	var missing []string
	if !optionSelected(p.LLMConnections, p.Values.LLM) {
		missing = append(missing, KindLLM)
	}
	if !optionSelected(p.STTConnections, p.Values.STT) {
		missing = append(missing, KindSTT)
	}
	if !optionSelected(p.TTSConnections, p.Values.TTS) {
		missing = append(missing, KindTTS)
	}
	return missing
}

// Ready reports whether the screen can offer to put this agent on the line.
func (p FormPage) Ready() bool { return len(p.Missing()) == 0 }

func (p FormPage) Answering() bool { return p.Status == StatusActive }

func optionSelected(options []Option, selected string) bool {
	for _, option := range options {
		if option.Value == selected {
			return true
		}
	}
	return false
}

// statusLabel translates agents.status.
func statusLabel(status string) string {
	switch status {
	case StatusDraft:
		return "Brouillon"
	case StatusActive:
		return "En ligne"
	case StatusPaused:
		return "En pause"
	case StatusArchived:
		return "Archivé"
	}
	return status
}

// kindLabel is the French name of a provider kind, as a garage owner would
// understand it rather than as the schema spells it.
func kindLabel(kind string) string {
	switch kind {
	case KindLLM:
		return "le modèle de langage"
	case KindSTT:
		return "la transcription de la voix"
	case KindTTS:
		return "la synthèse vocale"
	}
	return kind
}

// missingLabels turns provider kinds into a readable French list.
func missingLabels(missing []string) string {
	labels := make([]string, 0, len(missing))
	for _, kind := range missing {
		labels = append(labels, kindLabel(kind))
	}
	return strings.Join(labels, ", ")
}

// numbersSummary says which lines reach this agent, or that none do.
func numbersSummary(numbers []string) string {
	if len(numbers) == 0 {
		return "Aucun numéro ne pointe vers cet agent"
	}
	return strings.Join(numbers, ", ")
}

// agentSummary counts the agents in words, agreeing in number.
func agentSummary(count int) string {
	switch count {
	case 0:
		return "Aucun agent"
	case 1:
		return "1 agent"
	}
	return strconv.Itoa(count) + " agents"
}

func agentPath(agent Agent) string { return "/agents/" + agent.ID }

func formActionPath(p FormPage) string { return "/agents/" + p.ID }

func activatePath(p FormPage) string { return "/agents/" + p.ID + "/activate" }

func pausePath(p FormPage) string { return "/agents/" + p.ID + "/pause" }

func noticeTitle(kind string) string {
	switch kind {
	case NoticeSuccess:
		return "C'est enregistré"
	case NoticeInvalid:
		return "Vérifiez les informations ci-dessous"
	default:
		return "Action impossible pour le moment"
	}
}

func noticeColor(kind string) string {
	switch kind {
	case NoticeSuccess:
		return "alert-success"
	case NoticeInvalid:
		return "alert-error"
	default:
		return "alert-warning"
	}
}

// ariaInvalid renders the attribute value; "false" is the ARIA-defined way to
// say a control is currently valid.
func ariaInvalid(hasError bool) string {
	if hasError {
		return "true"
	}
	return "false"
}

// describedBy links a control to its hint and, when present, its error.
func describedBy(field string, hasHint bool, hasError bool) string {
	ids := make([]string, 0, 2)
	if hasHint {
		ids = append(ids, field+"-hint")
	}
	if hasError {
		ids = append(ids, field+"-error")
	}
	return strings.Join(ids, " ")
}
