package customers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/a-h/templ"

	"github.com/esrid/garageband/internal/platform/db"
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
	page.Notice = noticeFromQuery(r.URL.Query().Get("notice"))
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
	profile.Notice = noticeFromQuery(r.URL.Query().Get("notice"))
	h.render(w, r, Show(profile), http.StatusOK)
}

// grant shares a customer's dossier with another site. Only owners/admins
// may (Store.Grant checks with requireCustomerManager); everyone else gets a
// plain 403, since this is an internal tool action, not a public form.
func (h *handler) grant(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	customerID := r.PathValue("customerID")
	if !uuidPattern.MatchString(customerID) {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulaire invalide.", http.StatusBadRequest)
		return
	}
	receivingLocationID := strings.TrimSpace(r.PostForm.Get(FieldReceivingLocation))
	if !uuidPattern.MatchString(receivingLocationID) {
		http.Redirect(w, r, "/customers/"+customerID+"?notice=error", http.StatusSeeOther)
		return
	}
	_, err := h.store.Grant(r.Context(), principal.TenantID, principal.UserID, customerID, receivingLocationID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	case errors.Is(err, ErrForbidden):
		http.Error(w, "Action interdite.", http.StatusForbidden)
	case err != nil:
		http.Redirect(w, r, "/customers/"+customerID+"?notice="+grantErrorNotice(err), http.StatusSeeOther)
	default:
		http.Redirect(w, r, "/customers/"+customerID+"?notice=shared", http.StatusSeeOther)
	}
}

func (h *handler) revoke(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	customerID := r.PathValue("customerID")
	grantID := r.PathValue("grantID")
	if !uuidPattern.MatchString(customerID) || !uuidPattern.MatchString(grantID) {
		http.NotFound(w, r)
		return
	}
	err := h.store.Revoke(r.Context(), principal.TenantID, principal.UserID, grantID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	case errors.Is(err, ErrForbidden):
		http.Error(w, "Action interdite.", http.StatusForbidden)
	case err != nil:
		h.fail(w, "revoke customer share", err)
	default:
		http.Redirect(w, r, "/customers/"+customerID+"?notice=revoked", http.StatusSeeOther)
	}
}

// offboard soft-deletes a customer who has left. It redirects to the search
// screen, not back to the profile: the profile query filters deleted_at IS
// NULL, so the dossier would 404 right after the action that just succeeded.
func (h *handler) offboard(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	customerID := r.PathValue("customerID")
	if !uuidPattern.MatchString(customerID) {
		http.NotFound(w, r)
		return
	}
	err := h.store.Offboard(r.Context(), principal.TenantID, principal.UserID, customerID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	case errors.Is(err, ErrForbidden):
		http.Error(w, "Action interdite.", http.StatusForbidden)
	case err != nil:
		h.fail(w, "offboard customer", err)
	default:
		http.Redirect(w, r, "/customers?notice=offboarded", http.StatusSeeOther)
	}
}

// grantErrorNotice turns a constraint violation into a notice code the view
// resolves to French copy — the constraints (one active grant per site, never
// the home site) are the actual validation; this only decodes what they
// already rejected.
func grantErrorNotice(err error) string {
	if pgErr, ok := db.PgError(err); ok {
		switch pgErr.Code {
		case "23505":
			return "grant_duplicate"
		case "23514":
			return "grant_same_location"
		}
	}
	return "error"
}

func noticeFromQuery(code string) Notice {
	switch code {
	case "shared":
		return Notice{Kind: NoticeSuccess, Message: "Partage ajouté."}
	case "revoked":
		return Notice{Kind: NoticeSuccess, Message: "Partage révoqué."}
	case "offboarded":
		return Notice{Kind: NoticeSuccess, Message: "Client archivé. Son téléphone et son e-mail sont libres pour un nouveau client."}
	case "grant_duplicate":
		return Notice{Kind: NoticeError, Message: "Ce site a déjà accès à ce dossier."}
	case "grant_same_location":
		return Notice{Kind: NoticeError, Message: "Impossible de partager avec le site d'origine."}
	case "error":
		return Notice{Kind: NoticeError, Message: "L'action n'a pas pu être effectuée. Réessayez."}
	}
	return Notice{}
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
