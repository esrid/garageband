package agents

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/a-h/templ"
)

var uuidPattern = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

type handler struct {
	store     *Store
	principal PrincipalResolver
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	page, err := h.store.List(r.Context(), principal.TenantID, principal.UserID)
	if err != nil {
		h.fail(w, "load agents", err)
		return
	}
	if r.URL.Query().Get("saved") == "1" {
		page.Notice = Notice{Kind: NoticeSuccess, Message: "La configuration est enregistrée."}
	}
	h.render(w, r, List(page), http.StatusOK)
}

func (h *handler) form(w http.ResponseWriter, r *http.Request) {
	principal, agentID, ok := h.resolveAgent(w, r)
	if !ok {
		return
	}
	page, err := h.store.Form(r.Context(), principal.TenantID, principal.UserID, agentID)
	if err != nil {
		h.handleReadError(w, r, "load agent", err)
		return
	}
	h.render(w, r, Form(page), http.StatusOK)
}

func (h *handler) save(w http.ResponseWriter, r *http.Request) {
	principal, agentID, ok := h.resolveAgent(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulaire invalide.", http.StatusBadRequest)
		return
	}
	input := Input{
		Name:     strings.TrimSpace(r.FormValue(FieldName)),
		Greeting: strings.TrimSpace(r.FormValue(FieldGreeting)),
		Prompt:   strings.TrimSpace(r.FormValue(FieldPrompt)),
		Fallback: strings.TrimSpace(r.FormValue(FieldFallback)),
		Locale:   strings.TrimSpace(r.FormValue(FieldLocale)),
		LLM:      strings.TrimSpace(r.FormValue(FieldLLM)),
		STT:      strings.TrimSpace(r.FormValue(FieldSTT)),
		TTS:      strings.TrimSpace(r.FormValue(FieldTTS)),
	}
	fieldErrors := validateInput(input)
	if len(fieldErrors) != 0 {
		h.renderSubmitted(
			w, r, principal, agentID, input, fieldErrors,
			Notice{Kind: NoticeInvalid, Message: "Corrigez les champs signalés."},
			http.StatusUnprocessableEntity,
		)
		return
	}
	err := h.store.Save(r.Context(), principal.TenantID, principal.UserID, agentID, input)
	if err != nil {
		var fieldError *FieldError
		switch {
		case errors.As(err, &fieldError):
			h.renderSubmitted(
				w, r, principal, agentID, input,
				map[string]string{fieldError.Field: fieldError.Message},
				Notice{Kind: NoticeInvalid, Message: "Corrigez les champs signalés."},
				http.StatusUnprocessableEntity,
			)
		case errors.Is(err, ErrForbidden):
			http.Error(w, "Vous ne pouvez pas modifier cet agent.", http.StatusForbidden)
		case errors.Is(err, sql.ErrNoRows):
			http.NotFound(w, r)
		default:
			h.fail(w, "save agent", err)
		}
		return
	}
	http.Redirect(w, r, "/agents?saved=1", http.StatusSeeOther)
}

func (h *handler) activate(w http.ResponseWriter, r *http.Request) {
	h.lifecycle(w, r, true)
}

func (h *handler) pause(w http.ResponseWriter, r *http.Request) {
	h.lifecycle(w, r, false)
}

func (h *handler) lifecycle(w http.ResponseWriter, r *http.Request, activate bool) {
	principal, agentID, ok := h.resolveAgent(w, r)
	if !ok {
		return
	}
	var err error
	if activate {
		err = h.store.Activate(r.Context(), principal.TenantID, principal.UserID, agentID)
	} else {
		err = h.store.Pause(r.Context(), principal.TenantID, principal.UserID, agentID)
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrNotReady):
			page, loadErr := h.store.Form(
				r.Context(), principal.TenantID, principal.UserID, agentID,
			)
			if loadErr != nil {
				h.handleReadError(w, r, "reload agent", loadErr)
				return
			}
			page.Notice = Notice{
				Kind:    NoticeError,
				Message: "Choisissez une connexion active pour écouter, comprendre et parler avant la mise en ligne.",
			}
			h.render(w, r, Form(page), http.StatusConflict)
		case errors.Is(err, ErrForbidden):
			http.Error(w, "Vous ne pouvez pas changer l’état de cet agent.", http.StatusForbidden)
		case errors.Is(err, sql.ErrNoRows):
			http.NotFound(w, r)
		default:
			h.fail(w, "change agent lifecycle", err)
		}
		return
	}
	http.Redirect(w, r, "/agents/"+agentID, http.StatusSeeOther)
}

func (h *handler) renderSubmitted(
	w http.ResponseWriter,
	r *http.Request,
	principal Principal,
	agentID string,
	input Input,
	fieldErrors map[string]string,
	notice Notice,
	status int,
) {
	page, err := h.store.Form(r.Context(), principal.TenantID, principal.UserID, agentID)
	if err != nil {
		h.handleReadError(w, r, "reload agent", err)
		return
	}
	page.Values = FormValues{
		Name: input.Name, Greeting: input.Greeting, Prompt: input.Prompt,
		Fallback: input.Fallback, Locale: input.Locale,
		LLM: input.LLM, STT: input.STT, TTS: input.TTS,
	}
	page.FieldErrors = fieldErrors
	page.Notice = notice
	h.render(w, r, Form(page), status)
}

func validateInput(input Input) map[string]string {
	errorsByField := make(map[string]string)
	if length := utf8.RuneCountInString(input.Name); length == 0 || length > 120 {
		errorsByField[FieldName] = "Saisissez un nom de 120 caractères maximum."
	}
	if length := utf8.RuneCountInString(input.Greeting); length == 0 || length > 1000 {
		errorsByField[FieldGreeting] = "Saisissez une phrase d’accueil de 1 000 caractères maximum."
	}
	if utf8.RuneCountInString(input.Prompt) > 20000 {
		errorsByField[FieldPrompt] = "Les consignes dépassent 20 000 caractères."
	}
	if length := utf8.RuneCountInString(input.Fallback); length == 0 || length > 2000 {
		errorsByField[FieldFallback] = "Saisissez une réponse de 2 000 caractères maximum."
	}
	if !validLocale(input.Locale) {
		errorsByField[FieldLocale] = "Choisissez une langue proposée."
	}
	for field, value := range map[string]string{
		FieldLLM: input.LLM, FieldSTT: input.STT, FieldTTS: input.TTS,
	} {
		if value != "" && !uuidPattern.MatchString(value) {
			errorsByField[field] = "Choisissez une connexion valide."
		}
	}
	return errorsByField
}

func validLocale(value string) bool {
	for _, option := range localeOptions() {
		if option.Value == value {
			return true
		}
	}
	return false
}

func (h *handler) resolveAgent(
	w http.ResponseWriter,
	r *http.Request,
) (Principal, string, bool) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return Principal{}, "", false
	}
	agentID := r.PathValue("agentID")
	if !uuidPattern.MatchString(agentID) {
		http.NotFound(w, r)
		return Principal{}, "", false
	}
	return principal, agentID, true
}

func (h *handler) resolve(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	principal, ok := h.principal(r.Context())
	if !ok || principal.UserID == "" || principal.TenantID == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return Principal{}, false
	}
	return principal, true
}

func (h *handler) handleReadError(
	w http.ResponseWriter,
	r *http.Request,
	operation string,
	err error,
) {
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	h.fail(w, operation, err)
}

func (h *handler) fail(w http.ResponseWriter, operation string, err error) {
	slog.Error(operation, "err", err)
	http.Error(w, "Impossible de charger les agents.", http.StatusInternalServerError)
}

func (h *handler) render(
	w http.ResponseWriter,
	r *http.Request,
	component templ.Component,
	status int,
) {
	w.WriteHeader(status)
	if err := component.Render(r.Context(), w); err != nil {
		slog.Error("render agents", "err", err)
	}
}

type Middleware func(http.Handler) http.Handler

type Principal struct {
	UserID   string
	TenantID string
}

type PrincipalResolver func(context.Context) (Principal, bool)
