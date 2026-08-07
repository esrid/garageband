package team

import (
	"context"
	"net/http"
)

type Middleware func(http.Handler) http.Handler

type Principal struct {
	UserID   string
	TenantID string
}

type PrincipalResolver func(context.Context) (Principal, bool)

// Register wires the team screen. baseURL is what an invitation link is built
// from: the code itself is minted by the store, the address it lives at is a
// deployment fact this feature is told rather than reads.
func Register(
	mux *http.ServeMux,
	store *Store,
	requireTenant Middleware,
	principal PrincipalResolver,
	baseURL string,
) {
	h := &handler{store: store, principal: principal, baseURL: baseURL}
	mux.Handle("GET /team", requireTenant(http.HandlerFunc(h.index)))
	mux.Handle("POST /team/invite", requireTenant(http.HandlerFunc(h.invite)))
	mux.Handle(
		"POST /team/{userID}/locations",
		requireTenant(http.HandlerFunc(h.replace)),
	)
	mux.Handle(
		"POST /team/{userID}/name",
		requireTenant(http.HandlerFunc(h.rename)),
	)
	mux.Handle(
		"POST /team/{userID}/code",
		requireTenant(http.HandlerFunc(h.reissue)),
	)
	mux.Handle(
		"POST /team/{userID}/revoke",
		requireTenant(http.HandlerFunc(h.revoke)),
	)
}
