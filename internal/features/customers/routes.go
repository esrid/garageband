package customers

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
	mux.Handle("GET /customers", requireTenant(http.HandlerFunc(h.index)))
	mux.Handle("GET /customers/{customerID}", requireTenant(http.HandlerFunc(h.show)))
	mux.Handle("POST /customers/{customerID}/shares", requireTenant(http.HandlerFunc(h.grant)))
	mux.Handle("POST /customers/{customerID}/shares/{grantID}/revoke", requireTenant(http.HandlerFunc(h.revoke)))
	mux.Handle("POST /customers/{customerID}/offboard", requireTenant(http.HandlerFunc(h.offboard)))
}
