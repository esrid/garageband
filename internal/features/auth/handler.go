package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/oauth"
)

const (
	// ponytail: rename to __Host-session once the app is always behind HTTPS.
	sessionCookie = "session"
	stateCookie   = "oauth_state"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type handler struct {
	store     *Store
	providers map[string]oauth.Provider
	secure    bool // Secure flag on cookies; false only for local http dev
}

func (h *handler) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := UserFrom(r.Context()); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	names := slices.Sorted(maps.Keys(h.providers))
	if err := LoginPage(names).Render(r.Context(), w); err != nil {
		slog.Error("render login page", "err", err)
	}
}

// start begins the authorization-code flow: random state (CSRF) + PKCE
// verifier, both kept in a short-lived HttpOnly cookie.
func (h *handler) start(w http.ResponseWriter, r *http.Request) {
	p, ok := h.providers[r.PathValue("provider")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	state := rand.Text()
	verifier := oauth.GenerateVerifier()
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Value:    state + "." + verifier,
		Path:     "/auth",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, p.AuthCodeURL(state, verifier), http.StatusFound)
}

func (h *handler) callback(w http.ResponseWriter, r *http.Request) {
	p, ok := h.providers[r.PathValue("provider")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	c, err := r.Cookie(stateCookie)
	clearCookie(w, stateCookie, "/auth", h.secure)
	if err != nil {
		http.Error(w, "login expired, please retry", http.StatusBadRequest)
		return
	}
	state, verifier, ok := strings.Cut(c.Value, ".")
	if !ok || subtle.ConstantTimeCompare([]byte(state), []byte(r.FormValue("state"))) != 1 {
		http.Error(w, "invalid oauth state", http.StatusBadRequest)
		return
	}
	info, err := p.Authenticate(r.Context(), r.FormValue("code"), verifier)
	if err != nil {
		slog.Error("oauth authenticate", "provider", p.Name(), "err", err)
		http.Error(w, "login failed, please retry", http.StatusBadGateway)
		return
	}
	user, err := h.store.UpsertUser(r.Context(), p.Name(), info)
	if err != nil {
		h.fail(w, err)
		return
	}
	// Fresh token on every login (OWASP: no session fixation).
	token, err := h.store.CreateSession(r.Context(), user.ID, "", sessionTTL)
	if err != nil {
		h.fail(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// invitationPage previews an invitation. Read-only on purpose: see
// InvitationPage for why looking must not consume the link.
func (h *handler) invitationPage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	invitation, err := h.store.InvitationByToken(r.Context(), token)
	if err != nil {
		h.renderInvitationDeadEnd(w, r, err)
		return
	}
	if err := InvitationPage(invitation, token).Render(r.Context(), w); err != nil {
		slog.Error("render invitation page", "err", err)
	}
}

// invitationCodePage offers the typed way in, for a screen nobody can hand a
// link to.
func (h *handler) invitationCodePage(w http.ResponseWriter, r *http.Request) {
	if err := InvitationCodePage(false).Render(r.Context(), w); err != nil {
		slog.Error("render invitation code page", "err", err)
	}
}

// acceptInvitationCode consumes a typed code directly. Unlike a pasted link
// there is no preview step: a person typing twelve characters is not a
// messenger's preview bot, and the extra click would only be in the way.
func (h *handler) acceptInvitationCode(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulaire invalide.", http.StatusBadRequest)
		return
	}
	token, err := h.store.AcceptInvitation(r.Context(), r.Form.Get("code"))
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) && !errors.Is(err, sql.ErrNoRows) {
			h.fail(w, err)
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		if err := InvitationCodePage(true).Render(r.Context(), w); err != nil {
			slog.Error("render invitation code page", "err", err)
		}
		return
	}
	h.startStaffSession(w, r, token)
}

func (h *handler) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	token, err := h.store.AcceptInvitation(r.Context(), r.PathValue("token"))
	if err != nil {
		h.renderInvitationDeadEnd(w, r, err)
		return
	}
	h.startStaffSession(w, r, token)
}

func (h *handler) startStaffSession(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(staffSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// renderInvitationDeadEnd tells consumed, expired and unknown tokens apart in
// the logs but never on screen, so the page cannot be used to probe which
// tokens exist.
func (h *handler) renderInvitationDeadEnd(w http.ResponseWriter, r *http.Request, err error) {
	if !errors.Is(err, pgx.ErrNoRows) && !errors.Is(err, sql.ErrNoRows) {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNotFound)
	if err := InvitationExpiredPage().Render(r.Context(), w); err != nil {
		slog.Error("render expired invitation page", "err", err)
	}
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := h.store.DeleteSession(r.Context(), c.Value); err != nil {
			slog.Error("delete session", "err", err)
		}
	}
	clearCookie(w, sessionCookie, "/", h.secure)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *handler) activateWorkspace(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenantID")
	if !uuidPattern.MatchString(tenantID) {
		http.NotFound(w, r)
		return
	}
	if err := h.store.ActivateTenant(r.Context(), tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.fail(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *handler) fail(w http.ResponseWriter, err error) {
	slog.Error("auth", "err", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func clearCookie(w http.ResponseWriter, name, path string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Path:     path,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
