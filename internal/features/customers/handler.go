package customers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"unicode/utf8"

	"github.com/a-h/templ"
)

const maxSearchRunes = 100

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
	query := r.URL.Query().Get(FieldQuery)
	if utf8.RuneCountInString(query) > maxSearchRunes {
		http.Error(w, "La recherche est trop longue.", http.StatusUnprocessableEntity)
		return
	}
	page, err := h.store.Search(
		r.Context(), principal.TenantID, principal.UserID, query,
	)
	if err != nil {
		h.fail(w, "search customers", err)
		return
	}
	h.render(w, r, Index(page), http.StatusOK)
}

func (h *handler) show(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	customerID := r.PathValue("customerID")
	if !uuidPattern.MatchString(customerID) {
		http.NotFound(w, r)
		return
	}
	profile, err := h.store.Profile(
		r.Context(), principal.TenantID, principal.UserID, customerID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.fail(w, "load customer profile", err)
		return
	}
	h.render(w, r, Show(profile), http.StatusOK)
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
	http.Error(w, "Impossible de charger les clients.", http.StatusInternalServerError)
}

func (h *handler) render(
	w http.ResponseWriter,
	r *http.Request,
	component templ.Component,
	status int,
) {
	w.WriteHeader(status)
	if err := component.Render(r.Context(), w); err != nil {
		slog.Error("render customers", "err", err)
	}
}
