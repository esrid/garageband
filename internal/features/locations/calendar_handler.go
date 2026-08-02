package locations

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"golang.org/x/oauth2"

	"github.com/esrid/garageband/internal/platform/secrets"
)

// CalendarConfig bundles what the Google Calendar connect flow needs. Zero
// value (Enabled: false) turns the routes into 404s: no ENCRYPTION_KEY or no
// Google OAuth client configured means the feature stays off, the same way
// login providers are only registered when their env vars are set.
type CalendarConfig struct {
	OAuth   oauth2.Config
	Secrets secrets.Store
	Enabled bool
	// Secure sets the Secure flag on the short-lived state cookie; false only
	// for local http development, same meaning as auth's own cookie flag.
	Secure bool
}

const calendarStateCookie = "calendar_oauth_state"

// connectCalendar begins the Calendar-scoped OAuth flow for one location.
// Same shape as auth's login flow (random state + PKCE verifier in a
// short-lived HttpOnly cookie) plus the location id, since Google only ever
// redirects to the fixed, pre-registered callback path - the location has to
// travel some other way, and the cookie is exactly as tamper-proof as state
// itself.
func (h *handler) connectCalendar(w http.ResponseWriter, r *http.Request) {
	if !h.calendar.Enabled {
		http.NotFound(w, r)
		return
	}
	_, locationID, ok := h.locationRequest(w, r)
	if !ok {
		return
	}
	state := rand.Text()
	verifier := oauth2.GenerateVerifier()
	http.SetCookie(w, &http.Cookie{
		Name:     calendarStateCookie,
		Value:    state + "." + verifier + "." + locationID,
		Path:     "/oauth/google-calendar",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   h.calendar.Secure,
		SameSite: http.SameSiteLaxMode,
	})
	authURL := h.calendar.OAuth.AuthCodeURL(
		state, oauth2.S256ChallengeOption(verifier),
		// Offline + force the consent screen: without both, Google may not
		// return a refresh_token (only issued the first time a user
		// consents, per the OAuth2 web-server flow docs), and a calendar
		// connection with no refresh token is useless the moment the short
		//-lived access token expires.
		oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (h *handler) calendarCallback(w http.ResponseWriter, r *http.Request) {
	if !h.calendar.Enabled {
		http.NotFound(w, r)
		return
	}
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	cookie, err := r.Cookie(calendarStateCookie)
	clearCalendarStateCookie(w, h.calendar.Secure)
	if err != nil {
		http.Error(w, "La connexion a expiré, réessayez.", http.StatusBadRequest)
		return
	}
	state, rest, ok := strings.Cut(cookie.Value, ".")
	verifier, locationID, ok2 := strings.Cut(rest, ".")
	if !ok || !ok2 || subtle.ConstantTimeCompare([]byte(state), []byte(r.FormValue("state"))) != 1 {
		http.Error(w, "État OAuth invalide.", http.StatusBadRequest)
		return
	}
	token, err := h.calendar.OAuth.Exchange(r.Context(), r.FormValue("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		slog.Error("google calendar oauth exchange", "err", err)
		http.Error(w, "La connexion au calendrier a échoué, réessayez.", http.StatusBadGateway)
		return
	}
	if token.RefreshToken == "" {
		http.Error(w, "Google n’a pas renvoyé d’accès durable ; réessayez la connexion.", http.StatusBadGateway)
		return
	}
	email, err := googleAccountEmail(r.Context(), h.calendar.OAuth.Client(r.Context(), token))
	if err != nil {
		slog.Error("google calendar account lookup", "err", err)
	}
	if err := h.store.ConnectCalendar(
		r.Context(), principal.TenantID, principal.UserID, locationID,
		h.calendar.Secrets, token.RefreshToken, email,
	); err != nil {
		h.storeError(w, r, err)
		return
	}
	http.Redirect(w, r, "/locations/"+locationID+"?calendar=connected", http.StatusSeeOther)
}

func (h *handler) disconnectCalendar(w http.ResponseWriter, r *http.Request) {
	principal, locationID, ok := h.locationRequest(w, r)
	if !ok {
		return
	}
	if err := h.store.DisconnectCalendar(
		r.Context(), principal.TenantID, principal.UserID, locationID, h.calendar.Secrets,
	); err != nil {
		h.storeError(w, r, err)
		return
	}
	http.Redirect(w, r, "/locations/"+locationID+"?calendar=disconnected", http.StatusSeeOther)
}

func clearCalendarStateCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: calendarStateCookie, Path: "/oauth/google-calendar",
		MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

// googleAccountEmail is display-only (which Google account a location is
// connected to); a failure here must not fail the connection itself.
// Verified against https://developers.google.com/identity/openid-connect/openid-connect
// 2026-08-03, same userinfo endpoint the login provider uses.
func googleAccountEmail(ctx context.Context, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://openidconnect.googleapis.com/v1/userinfo", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&claims); err != nil {
		return "", err
	}
	return claims.Email, nil
}
