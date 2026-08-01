package agents

import "net/http"

func Register(
	mux *http.ServeMux,
	store *Store,
	requireTenant Middleware,
	principal PrincipalResolver,
) {
	h := &handler{store: store, principal: principal}
	mux.Handle("GET /agents", requireTenant(http.HandlerFunc(h.index)))
	mux.Handle("GET /agents/{agentID}", requireTenant(http.HandlerFunc(h.form)))
	mux.Handle("POST /agents/{agentID}", requireTenant(http.HandlerFunc(h.save)))
	mux.Handle("POST /agents/{agentID}/activate", requireTenant(http.HandlerFunc(h.activate)))
	mux.Handle("POST /agents/{agentID}/pause", requireTenant(http.HandlerFunc(h.pause)))
}
