package onboarding

import (
	"context"
	"net/http"

	"github.com/esrid/garageband/internal/platform/businesslookup"
)

type Middleware func(http.Handler) http.Handler
type UserIDResolver func(context.Context) (string, bool)
type TenantActivator func(context.Context, string) error

func Register(
	mux *http.ServeMux,
	store *Store,
	provider businesslookup.Provider,
	requireUser Middleware,
	userID UserIDResolver,
	activateTenant TenantActivator,
) {
	h := &handler{
		store: store, provider: provider, userID: userID,
		activateTenant: activateTenant,
	}
	mux.Handle("GET /onboarding", requireUser(http.HandlerFunc(h.show)))
	mux.Handle("POST /onboarding/lookup", requireUser(http.HandlerFunc(h.lookup)))
	mux.Handle("POST /onboarding/confirm", requireUser(http.HandlerFunc(h.confirm)))
}
