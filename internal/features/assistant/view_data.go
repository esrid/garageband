package assistant

import "strings"

const (
	FieldConversation = "conversation_id"
	FieldLocation     = "location_id"
	FieldMessage      = "message"
)

const (
	NoticeError   = "error"
	NoticeInvalid = "invalid"
	NoticeSuccess = "success"
)

type Notice struct {
	Kind    string
	Message string
}

func (n Notice) Empty() bool { return strings.TrimSpace(n.Message) == "" }

type Page struct {
	Workspace          Workspace
	SelectedLocationID string
	MessageValue       string
	FieldErrors        map[string]string
	Notice             Notice
}

func (p Page) Error(field string) string { return p.FieldErrors[field] }

func (p Page) HasError(field string) bool { return p.FieldErrors[field] != "" }

func (p Page) CurrentLocationID() string {
	if p.Workspace.Current.LocationID != "" {
		return p.Workspace.Current.LocationID
	}
	return p.SelectedLocationID
}

func (p Page) HasLocations() bool { return len(p.Workspace.Locations) != 0 }

func roleLabel(role string) string {
	switch role {
	case "user":
		return "Vous"
	case "assistant":
		return "Assistant"
	case "tool":
		return "Outil"
	default:
		return "Système"
	}
}

func executionStatusLabel(status string) string {
	switch status {
	case "proposed":
		return "Confirmation requise"
	case "running":
		return "En cours"
	case "succeeded":
		return "Effectuée"
	case "failed":
		return "Échec"
	case "rejected":
		return "Abandonnée"
	default:
		return status
	}
}

func executionBadge(status string) string {
	switch status {
	case "succeeded":
		return "badge-success"
	case "failed":
		return "badge-error"
	case "proposed":
		return "badge-warning"
	default:
		return "badge-ghost"
	}
}

func noticeTitle(kind string) string {
	switch kind {
	case NoticeSuccess:
		return "C’est enregistré"
	case NoticeInvalid:
		return "Vérifiez votre demande"
	default:
		return "Assistant indisponible"
	}
}

func noticeClass(kind string) string {
	switch kind {
	case NoticeSuccess:
		return "alert-success"
	case NoticeInvalid:
		return "alert-warning"
	default:
		return "alert-error"
	}
}
