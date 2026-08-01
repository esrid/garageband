package team

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type handler struct {
	principal          PrincipalResolver
	loadPage           PageLoader
	replaceAssignments AssignmentReplacer
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	page, err := h.loadPage(r.Context(), principal)
	if err != nil {
		h.fail(w, err)
		return
	}
	if r.URL.Query().Get("saved") == "1" {
		page.Notice = Notice{
			Kind: NoticeSuccess, Message: "Les accès aux sites ont été enregistrés.",
		}
	}
	h.render(w, r, page, http.StatusOK)
}

func (h *handler) replace(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	targetUserID := r.PathValue("userID")
	if !uuidPattern.MatchString(targetUserID) {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulaire invalide.", http.StatusBadRequest)
		return
	}
	locationIDs := r.Form[FieldLocations]
	for _, locationID := range locationIDs {
		if !uuidPattern.MatchString(locationID) {
			http.Error(w, "Un site sélectionné est invalide.", http.StatusUnprocessableEntity)
			return
		}
	}
	if err := h.replaceAssignments(
		r.Context(), principal, targetUserID, locationIDs,
	); err != nil {
		switch {
		case errors.Is(err, ErrForbidden):
			http.Error(w, "Vous ne pouvez pas modifier ces accès.", http.StatusForbidden)
		case errors.Is(err, sql.ErrNoRows):
			http.NotFound(w, r)
		default:
			h.fail(w, err)
		}
		return
	}
	http.Redirect(w, r, "/team?saved=1", http.StatusSeeOther)
}

func (h *handler) resolve(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	principal, ok := h.principal(r.Context())
	if !ok || principal.UserID == "" || principal.TenantID == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return Principal{}, false
	}
	return principal, true
}

func (h *handler) fail(w http.ResponseWriter, err error) {
	slog.Error("team access", "err", err)
	http.Error(w, "Impossible de charger ou modifier les accès.", http.StatusInternalServerError)
}

func (h *handler) render(w http.ResponseWriter, r *http.Request, page Page, status int) {
	w.WriteHeader(status)
	if err := Index(page).Render(r.Context(), w); err != nil {
		slog.Error("render team access", "err", err)
	}
}
