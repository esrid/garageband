package app

import (
	"context"
	"net/http"
	"time"

	"github.com/esrid/garageband/internal/features/auth"
	"github.com/esrid/garageband/internal/features/dashboard"
	"github.com/esrid/garageband/internal/features/onboarding"
	"github.com/esrid/garageband/internal/platform/businesslookup"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/web"
)

// NewRouter composes feature routers and global middleware. Each feature
// registers its own routes via its Register function; no business logic here.
func NewRouter(cfg Config, database *db.DB) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(web.Static)))

	authStore := auth.NewStore(database)
	auth.Register(mux, authStore, cfg.OAuthProviders(), cfg.CookieSecure)
	businessProvider := businesslookup.NewRechercheEntreprises(
		&http.Client{Timeout: 10 * time.Second},
		cfg.BusinessLookupURL,
		"garageband/1.0 (business onboarding)",
	)
	onboarding.Register(
		mux,
		onboarding.NewStore(database),
		businessProvider,
		auth.RequireUser,
		func(ctx context.Context) (string, bool) {
			user, ok := auth.UserFrom(ctx)
			return user.ID, ok
		},
		authStore.ActivateTenant,
	)
	dashboard.Register(
		mux,
		auth.RequireUser,
		func(ctx context.Context) (dashboard.User, bool) {
			user, ok := auth.UserFrom(ctx)
			return dashboard.User{
				ID: user.ID, ActiveTenantID: user.ActiveTenantID,
			}, ok
		},
		func(ctx context.Context, userID string) ([]dashboard.Workspace, error) {
			workspaces, err := authStore.Workspaces(ctx, userID)
			if err != nil {
				return nil, err
			}
			result := make([]dashboard.Workspace, 0, len(workspaces))
			for _, workspace := range workspaces {
				result = append(result, dashboard.Workspace{
					ID: workspace.ID, Name: workspace.Name, Role: workspace.Role,
				})
			}
			return result, nil
		},
	)

	// Stdlib CSRF protection (Go 1.25+): blocks cross-origin non-safe methods
	// via Sec-Fetch-Site/Origin; requests without those headers pass.
	csrf := http.NewCrossOriginProtection()

	return withRecover(withLogging(csrf.Handler(authStore.WithUser(mux))))
}
