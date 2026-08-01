package auth

import (
	"net/http"

	"github.com/esrid/garageband/internal/platform/oauth"
)

// Register mounts the login/logout routes. providers may be empty: the login
// page then explains which env vars to set.
func Register(mux *http.ServeMux, store *Store, providers []oauth.Provider, secureCookies bool) {
	h := &handler{store: store, providers: make(map[string]oauth.Provider, len(providers)), secure: secureCookies}
	for _, p := range providers {
		h.providers[p.Name()] = p
	}
	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("GET /auth/{provider}", h.start)
	mux.HandleFunc("GET /auth/{provider}/callback", h.callback)
	mux.HandleFunc("POST /logout", h.logout)
	mux.Handle("POST /workspaces/{tenantID}/activate", RequireUser(http.HandlerFunc(h.activateWorkspace)))
}
