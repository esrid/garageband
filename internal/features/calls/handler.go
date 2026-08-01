package calls

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"regexp"

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
	filter := r.URL.Query().Get(FieldStatus)
	if filter != FilterNeedsAttention {
		filter = ""
	}
	page, err := h.store.Inbox(
		r.Context(), principal.TenantID, principal.UserID, filter,
	)
	if err != nil {
		h.fail(w, "load calls", err)
		return
	}
	h.render(w, r, Index(page), http.StatusOK)
}

func (h *handler) show(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	callID := r.PathValue("callID")
	if !uuidPattern.MatchString(callID) {
		http.NotFound(w, r)
		return
	}
	page, err := h.store.Transcript(
		r.Context(), principal.TenantID, principal.UserID, callID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.fail(w, "load call transcript", err)
		return
	}
	h.render(w, r, Show(page), http.StatusOK)
}

func (h *handler) resolve(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	principal, ok := h.principal(r.Context())
	if !ok || principal.UserID == "" || principal.TenantID == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return Principal{}, false
	}
	return principal, true
}

func (h *handler) fail(w http.ResponseWriter, operation string, err error) {
	slog.Error(operation, "err", err)
	http.Error(w, "Impossible de charger les appels.", http.StatusInternalServerError)
}

func (h *handler) render(
	w http.ResponseWriter,
	r *http.Request,
	component templ.Component,
	status int,
) {
	w.WriteHeader(status)
	if err := component.Render(r.Context(), w); err != nil {
		slog.Error("render calls", "err", err)
	}
}

type Middleware func(http.Handler) http.Handler

type Principal struct {
	UserID   string
	TenantID string
}

type PrincipalResolver func(context.Context) (Principal, bool)
