package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/esrid/template/internal/platform/oauth"
)

const (
	// ponytail: rename to __Host-session once the app is always behind HTTPS.
	sessionCookie = "session"
	stateCookie   = "oauth_state"
)

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
	token, err := h.store.CreateSession(r.Context(), user.ID)
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

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := h.store.DeleteSession(r.Context(), c.Value); err != nil {
			slog.Error("delete session", "err", err)
		}
	}
	clearCookie(w, sessionCookie, "/", h.secure)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
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
