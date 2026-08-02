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
	calendar CalendarConfig,
) {
	h := &handler{store: store, principal: principal, calendar: calendar}
	mux.Handle("GET /locations", requireTenant(http.HandlerFunc(h.index)))
	mux.Handle("GET /locations/new", requireTenant(http.HandlerFunc(h.showNew)))
	mux.Handle("POST /locations", requireTenant(http.HandlerFunc(h.create)))
	mux.Handle("GET /locations/{locationID}", requireTenant(http.HandlerFunc(h.showEdit)))
	mux.Handle("POST /locations/{locationID}", requireTenant(http.HandlerFunc(h.update)))
	mux.Handle("GET /locations/{locationID}/schedule", requireTenant(http.HandlerFunc(h.showSchedule)))
	mux.Handle("POST /locations/{locationID}/schedule/hours", requireTenant(http.HandlerFunc(h.addOpeningHour)))
	mux.Handle("POST /locations/{locationID}/schedule/hours/delete", requireTenant(http.HandlerFunc(h.deleteOpeningHour)))
	mux.Handle("POST /locations/{locationID}/schedule/closures", requireTenant(http.HandlerFunc(h.addClosure)))
	mux.Handle("POST /locations/{locationID}/schedule/closures/{closureID}/delete", requireTenant(http.HandlerFunc(h.deleteClosure)))
	mux.Handle("POST /locations/{locationID}/schedule/resources", requireTenant(http.HandlerFunc(h.addResource)))
	mux.Handle("POST /locations/{locationID}/schedule/resources/{resourceID}/active", requireTenant(http.HandlerFunc(h.setResourceActive)))
	mux.Handle("POST /locations/{locationID}/schedule/requirements", requireTenant(http.HandlerFunc(h.upsertRequirement)))
	mux.Handle("POST /locations/{locationID}/schedule/requirements/delete", requireTenant(http.HandlerFunc(h.deleteRequirement)))
	mux.Handle("POST /locations/{locationID}/schedule/services", requireTenant(http.HandlerFunc(h.linkCatalogService)))
	mux.Handle("POST /locations/{locationID}/schedule/services/{serviceID}/active", requireTenant(http.HandlerFunc(h.setCatalogServiceActive)))
	mux.Handle(
		"POST /locations/{locationID}/deactivate",
		requireTenant(http.HandlerFunc(h.deactivate)),
	)
	mux.Handle(
		"POST /locations/{locationID}/reactivate",
		requireTenant(http.HandlerFunc(h.reactivate)),
	)
	mux.Handle("GET /locations/{locationID}/calendar/connect", requireTenant(http.HandlerFunc(h.connectCalendar)))
	mux.Handle("GET /oauth/google-calendar/callback", requireTenant(http.HandlerFunc(h.calendarCallback)))
	mux.Handle("POST /locations/{locationID}/calendar/disconnect", requireTenant(http.HandlerFunc(h.disconnectCalendar)))
}
