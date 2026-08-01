package calls

import "net/http"

func Register(
	mux *http.ServeMux,
	store *Store,
	requireTenant Middleware,
	principal PrincipalResolver,
) {
	h := &handler{store: store, principal: principal}
	mux.Handle("GET /calls", requireTenant(http.HandlerFunc(h.index)))
	mux.Handle("GET /calls/{callID}", requireTenant(http.HandlerFunc(h.show)))
}
