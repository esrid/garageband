package agenda

import "net/http"

func Register(
	mux *http.ServeMux,
	store *Store,
	requireTenant Middleware,
	principal PrincipalResolver,
) {
	h := &handler{store: store, principal: principal}
	mux.Handle("GET /agenda", requireTenant(http.HandlerFunc(h.index)))
	mux.Handle("GET /agenda/new", requireTenant(http.HandlerFunc(h.newAppointment)))
	mux.Handle("POST /agenda", requireTenant(http.HandlerFunc(h.create)))
	mux.Handle("GET /agenda/{appointmentID}", requireTenant(http.HandlerFunc(h.editAppointment)))
	mux.Handle("POST /agenda/{appointmentID}", requireTenant(http.HandlerFunc(h.update)))
	mux.Handle("POST /agenda/{appointmentID}/cancel", requireTenant(http.HandlerFunc(h.cancel)))
}
