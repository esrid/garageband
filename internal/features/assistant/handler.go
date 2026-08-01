package assistant

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type handler struct {
	store     *Store
	service   *Service
	principal PrincipalResolver
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversation"))
	if conversationID != "" && !uuidPattern.MatchString(conversationID) {
		http.NotFound(w, r)
		return
	}
	workspace, err := h.store.Workspace(r.Context(), principal.TenantID, principal.UserID, conversationID)
	if err != nil {
		h.readError(w, r, err)
		return
	}
	page := Page{Workspace: workspace, FieldErrors: map[string]string{}}
	page.SelectedLocationID = selectedLocation(workspace, r.URL.Query().Get("location"))
	switch r.URL.Query().Get("saved") {
	case "proposed":
		page.Notice = Notice{Kind: NoticeSuccess, Message: "La réponse a été ajoutée. Toute action proposée attend votre confirmation."}
	case "confirmed":
		page.Notice = Notice{Kind: NoticeSuccess, Message: "L’action confirmée a été traitée et son résultat a été audité."}
	case "rejected":
		page.Notice = Notice{Kind: NoticeSuccess, Message: "L’action a été abandonnée sans modifier les données."}
	case "model-error":
		page.Notice = Notice{Kind: NoticeError, Message: "Votre message est conservé, mais le modèle n’a pas pu répondre. Réessayez plus tard."}
	}
	h.render(w, r, page, http.StatusOK)
}

func (h *handler) send(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulaire invalide.", http.StatusBadRequest)
		return
	}
	conversationID := strings.TrimSpace(r.FormValue(FieldConversation))
	locationID := strings.TrimSpace(r.FormValue(FieldLocation))
	message := strings.TrimSpace(r.FormValue(FieldMessage))
	fieldErrors := make(map[string]string)
	if conversationID != "" && !uuidPattern.MatchString(conversationID) {
		http.NotFound(w, r)
		return
	}
	if conversationID == "" && !uuidPattern.MatchString(locationID) {
		fieldErrors[FieldLocation] = "Choisissez l’établissement concerné."
	}
	if message == "" {
		fieldErrors[FieldMessage] = "Écrivez une demande."
	} else if utf8.RuneCountInString(message) > 4000 {
		fieldErrors[FieldMessage] = "La demande ne peut pas dépasser 4 000 caractères."
	}
	if len(fieldErrors) != 0 {
		h.renderSendRetry(w, r, principal, conversationID, locationID, message, fieldErrors)
		return
	}
	createdID, err := h.service.Send(
		r.Context(), principal.TenantID, principal.UserID,
		conversationID, locationID, message,
	)
	if err != nil {
		if createdID != "" {
			slog.Error("assistant model response", "err", err)
			h.redirect(w, r, createdID, "model-error")
			return
		}
		h.writeError(w, r, err)
		return
	}
	h.redirect(w, r, createdID, "proposed")
}

func (h *handler) confirm(w http.ResponseWriter, r *http.Request) {
	h.resolveToolAction(w, r, true)
}

func (h *handler) reject(w http.ResponseWriter, r *http.Request) {
	h.resolveToolAction(w, r, false)
}

func (h *handler) resolveToolAction(w http.ResponseWriter, r *http.Request, confirm bool) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	conversationID := r.PathValue("conversationID")
	executionID := r.PathValue("executionID")
	if !uuidPattern.MatchString(conversationID) || !uuidPattern.MatchString(executionID) {
		http.NotFound(w, r)
		return
	}
	var err error
	result := "rejected"
	if confirm {
		err = h.service.Confirm(r.Context(), principal.TenantID, principal.UserID, conversationID, executionID)
		result = "confirmed"
	} else {
		err = h.service.Reject(r.Context(), principal.TenantID, principal.UserID, conversationID, executionID)
	}
	if err != nil {
		if errors.Is(err, ErrExecutionClosed) {
			h.redirect(w, r, conversationID, result)
			return
		}
		h.writeError(w, r, err)
		return
	}
	h.redirect(w, r, conversationID, result)
}

func (h *handler) renderSendRetry(
	w http.ResponseWriter,
	r *http.Request,
	principal Principal,
	conversationID string,
	locationID string,
	message string,
	fieldErrors map[string]string,
) {
	workspace, err := h.store.Workspace(r.Context(), principal.TenantID, principal.UserID, conversationID)
	if err != nil {
		h.readError(w, r, err)
		return
	}
	h.render(w, r, Page{
		Workspace: workspace, SelectedLocationID: selectedLocation(workspace, locationID),
		MessageValue: message, FieldErrors: fieldErrors,
		Notice: Notice{Kind: NoticeInvalid, Message: "Corrigez les champs indiqués avant d’envoyer."},
	}, http.StatusUnprocessableEntity)
}

func selectedLocation(workspace Workspace, requested string) string {
	if workspace.Current.LocationID != "" {
		return workspace.Current.LocationID
	}
	for _, location := range workspace.Locations {
		if location.ID == requested {
			return requested
		}
	}
	if len(workspace.Locations) != 0 {
		return workspace.Locations[0].ID
	}
	return ""
}

func (h *handler) redirect(w http.ResponseWriter, r *http.Request, conversationID string, result string) {
	query := url.Values{"conversation": {conversationID}, "saved": {result}}
	http.Redirect(w, r, "/assistant?"+query.Encode(), http.StatusSeeOther)
}

func (h *handler) resolve(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	principal, ok := h.principal(r.Context())
	if !ok || principal.UserID == "" || principal.TenantID == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return Principal{}, false
	}
	return principal, true
}

func (h *handler) readError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		http.Error(w, "Accès interdit.", http.StatusForbidden)
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	default:
		slog.Error("read assistant", "err", err)
		http.Error(w, "Impossible de charger l’assistant.", http.StatusInternalServerError)
	}
}

func (h *handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		http.Error(w, "Accès interdit.", http.StatusForbidden)
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	default:
		slog.Error("write assistant", "err", err)
		http.Error(w, "Impossible de traiter cette demande.", http.StatusInternalServerError)
	}
}

func (h *handler) render(w http.ResponseWriter, r *http.Request, page Page, status int) {
	w.WriteHeader(status)
	if err := View(page).Render(r.Context(), w); err != nil {
		slog.Error("render assistant", "err", err)
	}
}
