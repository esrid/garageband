package team

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// nameLimit matches the form's maxlength. A name longer than this is a paste
// accident, not a person.
const nameLimit = 120

type handler struct {
	deps Deps
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	page, err := h.deps.LoadPage(r.Context(), principal)
	if err != nil {
		h.fail(w, err)
		return
	}
	switch r.URL.Query().Get("saved") {
	case "1":
		page.Notice = Notice{
			Kind: NoticeSuccess, Message: "Les accès aux sites ont été enregistrés.",
		}
	case "renamed":
		page.Notice = Notice{
			Kind: NoticeSuccess, Message: "Le nom a été corrigé.",
		}
	case "removed":
		page.Notice = Notice{
			Kind:    NoticeSuccess,
			Message: "La personne a été retirée de l'équipe. Son lien et son accès ne fonctionnent plus.",
		}
	}
	h.render(w, r, page, http.StatusOK)
}

// invite answers with the page itself rather than redirecting: the link it
// returns exists nowhere else, so a redirect would throw it away.
func (h *handler) invite(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	locationIDs, ok := h.formLocations(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.Form.Get(FieldName))
	if name == "" || len(name) > nameLimit {
		h.renderNotice(w, r, principal, Notice{
			Kind:    NoticeError,
			Message: "Indiquez le prénom et le nom de la personne.",
		}, http.StatusUnprocessableEntity)
		return
	}

	invitation, err := h.deps.InviteStaff(r.Context(), principal, name, locationIDs)
	if err != nil {
		switch {
		case errors.Is(err, ErrForbidden):
			http.Error(w, "Vous ne pouvez pas ajouter d'employé.", http.StatusForbidden)
		case errors.Is(err, ErrNameRequired):
			h.renderNotice(w, r, principal, Notice{
				Kind:    NoticeError,
				Message: "Indiquez le prénom et le nom de la personne.",
			}, http.StatusUnprocessableEntity)
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "Un site sélectionné est introuvable.", http.StatusUnprocessableEntity)
		default:
			h.fail(w, err)
		}
		return
	}

	h.renderInvitation(w, r, principal, invitation, name)
}

// reissue mints a new code for someone already on the team: a second screen to
// sign in on, or a code that was lost. Like invite, it answers with the page
// itself, because the code exists nowhere but this response.
func (h *handler) reissue(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	targetUserID, ok := h.target(w, r)
	if !ok {
		return
	}
	invitation, err := h.deps.ReissueInvite(r.Context(), principal, targetUserID)
	if err != nil {
		h.failWrite(w, r, err)
		return
	}
	h.renderInvitation(w, r, principal, invitation, "")
}

// renderInvitation shows a credential exactly once. name may be empty, in which
// case the member list supplies it: reissuing knows an id, not a person.
func (h *handler) renderInvitation(
	w http.ResponseWriter,
	r *http.Request,
	principal Principal,
	invitation Invitation,
	name string,
) {
	page, err := h.deps.LoadPage(r.Context(), principal)
	if err != nil {
		h.fail(w, err)
		return
	}
	if name == "" {
		targetUserID := r.PathValue("userID")
		for _, member := range page.Members {
			if member.UserID == targetUserID {
				name = member.Label()
				break
			}
		}
	}
	page.Invite = invitation
	page.InvitedName = name
	h.render(w, r, page, http.StatusOK)
}

// rename fixes a typo without costing the person their access or their sites.
func (h *handler) rename(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	targetUserID, ok := h.target(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulaire invalide.", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.Form.Get(FieldName))
	if name == "" || len(name) > nameLimit {
		h.renderNotice(w, r, principal, Notice{
			Kind:    NoticeError,
			Message: "Indiquez le prénom et le nom de la personne.",
		}, http.StatusUnprocessableEntity)
		return
	}
	if err := h.deps.RenameStaff(r.Context(), principal, targetUserID, name); err != nil {
		h.failWrite(w, r, err)
		return
	}
	http.Redirect(w, r, "/team?saved=renamed", http.StatusSeeOther)
}

func (h *handler) replace(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	targetUserID, ok := h.target(w, r)
	if !ok {
		return
	}
	locationIDs, ok := h.formLocations(w, r)
	if !ok {
		return
	}
	if err := h.deps.ReplaceAssignments(
		r.Context(), principal, targetUserID, locationIDs,
	); err != nil {
		h.failWrite(w, r, err)
		return
	}
	http.Redirect(w, r, "/team?saved=1", http.StatusSeeOther)
}

func (h *handler) revoke(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	targetUserID, ok := h.target(w, r)
	if !ok {
		return
	}
	if err := h.deps.RemoveStaff(r.Context(), principal, targetUserID); err != nil {
		h.failWrite(w, r, err)
		return
	}
	http.Redirect(w, r, "/team?saved=removed", http.StatusSeeOther)
}

func (h *handler) target(w http.ResponseWriter, r *http.Request) (string, bool) {
	targetUserID := r.PathValue("userID")
	if !uuidPattern.MatchString(targetUserID) {
		http.NotFound(w, r)
		return "", false
	}
	return targetUserID, true
}

// formLocations parses and validates the site checkboxes shared by the
// invitation and assignment forms.
func (h *handler) formLocations(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulaire invalide.", http.StatusBadRequest)
		return nil, false
	}
	locationIDs := r.Form[FieldLocations]
	for _, locationID := range locationIDs {
		if !uuidPattern.MatchString(locationID) {
			http.Error(w, "Un site sélectionné est invalide.", http.StatusUnprocessableEntity)
			return nil, false
		}
	}
	return locationIDs, true
}

func (h *handler) failWrite(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		http.Error(w, "Vous ne pouvez pas modifier ces accès.", http.StatusForbidden)
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	default:
		h.fail(w, err)
	}
}

// renderNotice re-renders the screen carrying a message, so a rejected form
// lands the owner back on the page instead of a bare error string.
func (h *handler) renderNotice(
	w http.ResponseWriter,
	r *http.Request,
	principal Principal,
	notice Notice,
	status int,
) {
	page, err := h.deps.LoadPage(r.Context(), principal)
	if err != nil {
		h.fail(w, err)
		return
	}
	page.Notice = notice
	h.render(w, r, page, status)
}

func (h *handler) resolve(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	principal, ok := h.deps.Principal(r.Context())
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
