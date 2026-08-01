package assistant

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
	service *Service,
	requireTenant Middleware,
	principal PrincipalResolver,
) {
	h := &handler{store: store, service: service, principal: principal}
	mux.Handle("GET /assistant", requireTenant(http.HandlerFunc(h.index)))
	mux.Handle("POST /assistant/messages", requireTenant(http.HandlerFunc(h.send)))
	mux.Handle("POST /assistant/{conversationID}/tools/{executionID}/confirm", requireTenant(http.HandlerFunc(h.confirm)))
	mux.Handle("POST /assistant/{conversationID}/tools/{executionID}/reject", requireTenant(http.HandlerFunc(h.reject)))
}
