package catalog

import "net/http"

func Register(
	mux *http.ServeMux,
	store *Store,
	requireTenant Middleware,
	principal PrincipalResolver,
) {
	h := &handler{store: store, principal: principal}
	mux.Handle("GET /catalog", requireTenant(http.HandlerFunc(h.index)))
	mux.Handle("GET /catalog/new", requireTenant(http.HandlerFunc(h.newItem)))
	mux.Handle("POST /catalog", requireTenant(http.HandlerFunc(h.create)))
	mux.Handle("GET /catalog/imports", requireTenant(http.HandlerFunc(h.imports)))
	mux.Handle("GET /catalog/imports/new", requireTenant(http.HandlerFunc(h.newImport)))
	mux.Handle("POST /catalog/imports", requireTenant(http.HandlerFunc(h.upload)))
	mux.Handle("GET /catalog/imports/{importID}", requireTenant(http.HandlerFunc(h.importPreview)))
	mux.Handle("POST /catalog/imports/{importID}/publish", requireTenant(http.HandlerFunc(h.publish)))
	mux.Handle("POST /catalog/imports/{importID}/cancel", requireTenant(http.HandlerFunc(h.cancelImport)))
	mux.Handle("GET /catalog/{itemID}", requireTenant(http.HandlerFunc(h.editItem)))
	mux.Handle("POST /catalog/{itemID}", requireTenant(http.HandlerFunc(h.update)))
	mux.Handle("POST /catalog/{itemID}/delete", requireTenant(http.HandlerFunc(h.archive)))
}
