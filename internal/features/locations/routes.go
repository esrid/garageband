package locations

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

func Register(
	mux *http.ServeMux,
	store *Store,
	requireTenant Middleware,
	principal PrincipalResolver,
) {
	h := &handler{store: store, principal: principal}
	mux.Handle("GET /locations", requireTenant(http.HandlerFunc(h.index)))
	mux.Handle("GET /locations/new", requireTenant(http.HandlerFunc(h.showNew)))
	mux.Handle("POST /locations", requireTenant(http.HandlerFunc(h.create)))
	mux.Handle("GET /locations/{locationID}", requireTenant(http.HandlerFunc(h.showEdit)))
	mux.Handle("POST /locations/{locationID}", requireTenant(http.HandlerFunc(h.update)))
	mux.Handle(
		"POST /locations/{locationID}/deactivate",
		requireTenant(http.HandlerFunc(h.deactivate)),
	)
	mux.Handle(
		"POST /locations/{locationID}/reactivate",
		requireTenant(http.HandlerFunc(h.reactivate)),
	)
}
