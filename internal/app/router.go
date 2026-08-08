package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/esrid/garageband/internal/features/agenda"
	"github.com/esrid/garageband/internal/features/agents"
	"github.com/esrid/garageband/internal/features/assistant"
	"github.com/esrid/garageband/internal/features/auth"
	"github.com/esrid/garageband/internal/features/calls"
	"github.com/esrid/garageband/internal/features/catalog"
	"github.com/esrid/garageband/internal/features/customers"
	"github.com/esrid/garageband/internal/features/dashboard"
	"github.com/esrid/garageband/internal/features/locations"
	"github.com/esrid/garageband/internal/features/onboarding"
	"github.com/esrid/garageband/internal/features/team"
	"github.com/esrid/garageband/internal/features/voice"
	"github.com/esrid/garageband/internal/platform/assistanttools"
	"github.com/esrid/garageband/internal/platform/businesslookup"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/llm"
	"github.com/esrid/garageband/internal/platform/secrets"
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
	locationStore := locations.NewStore(database)
	calendarEnabled := cfg.CalendarEnabled()
	calendarConfig := locations.CalendarConfig{Enabled: calendarEnabled}
	agendaCalendarConfig := agenda.CalendarConfig{Enabled: calendarEnabled}
	if calendarEnabled {
		oauthConfig := cfg.GoogleCalendarOAuthConfig()
		secretStore, err := secrets.NewAESStore(cfg.EncryptionKey)
		if err != nil {
			// Unreachable in practice: Config already validated the key
			// length at boot. Fail loudly rather than silently disabling
			// calendar connections if that ever stops being true.
			panic(err)
		}
		calendarConfig.OAuth = oauthConfig
		calendarConfig.Secure = cfg.CookieSecure
		calendarConfig.Secrets = secretStore
		agendaCalendarConfig.OAuth = oauthConfig
		agendaCalendarConfig.Secrets = secretStore
	}
	locations.Register(
		mux,
		locationStore,
		auth.RequireTenant,
		func(ctx context.Context) (locations.Principal, bool) {
			user, userOK := auth.UserFrom(ctx)
			tenantID, tenantOK := auth.TenantFrom(ctx)
			return locations.Principal{
				UserID: user.ID, TenantID: tenantID, ActiveLocationID: user.ActiveLocationID,
			}, userOK && tenantOK
		},
		calendarConfig,
		authStore.ActivateLocation,
	)
	assistantStore := assistant.NewStore(database)
	catalogStore := catalog.NewStore(database)
	customerStore := customers.NewStore(database)
	agendaStore := agenda.NewStore(database, agendaCalendarConfig)
	assistant.Register(
		mux,
		assistantStore,
		assistant.NewService(
			assistantStore,
			llm.NewDemonstrationProvider(),
			assistanttools.NewRegistry(locationStore, catalogStore, customerStore, agendaStore),
		),
		auth.RequireTenant,
		func(ctx context.Context) (assistant.Principal, bool) {
			user, userOK := auth.UserFrom(ctx)
			tenantID, tenantOK := auth.TenantFrom(ctx)
			return assistant.Principal{
				UserID: user.ID, TenantID: tenantID,
			}, userOK && tenantOK
		},
	)
	customers.Register(
		mux,
		customerStore,
		auth.RequireTenant,
		func(ctx context.Context) (customers.Principal, bool) {
			user, userOK := auth.UserFrom(ctx)
			tenantID, tenantOK := auth.TenantFrom(ctx)
			return customers.Principal{
				UserID: user.ID, TenantID: tenantID, ActiveLocationID: user.ActiveLocationID,
			}, userOK && tenantOK
		},
	)
	agenda.Register(
		mux,
		agendaStore,
		auth.RequireTenant,
		func(ctx context.Context) (agenda.Principal, bool) {
			user, userOK := auth.UserFrom(ctx)
			tenantID, tenantOK := auth.TenantFrom(ctx)
			return agenda.Principal{
				UserID: user.ID, TenantID: tenantID, ActiveLocationID: user.ActiveLocationID,
			}, userOK && tenantOK
		},
	)
	calls.Register(
		mux,
		calls.NewStore(database),
		auth.RequireTenant,
		func(ctx context.Context) (calls.Principal, bool) {
			user, userOK := auth.UserFrom(ctx)
			tenantID, tenantOK := auth.TenantFrom(ctx)
			return calls.Principal{
				UserID: user.ID, TenantID: tenantID,
			}, userOK && tenantOK
		},
	)
	agents.Register(
		mux,
		agents.NewStore(database),
		auth.RequireTenant,
		func(ctx context.Context) (agents.Principal, bool) {
			user, userOK := auth.UserFrom(ctx)
			tenantID, tenantOK := auth.TenantFrom(ctx)
			return agents.Principal{
				UserID: user.ID, TenantID: tenantID,
			}, userOK && tenantOK
		},
	)
	catalog.Register(
		mux,
		catalogStore,
		auth.RequireTenant,
		func(ctx context.Context) (catalog.Principal, bool) {
			user, userOK := auth.UserFrom(ctx)
			tenantID, tenantOK := auth.TenantFrom(ctx)
			return catalog.Principal{
				UserID: user.ID, TenantID: tenantID,
			}, userOK && tenantOK
		},
	)
	team.Register(
		mux,
		team.NewStore(database),
		auth.RequireTenant,
		func(ctx context.Context) (team.Principal, bool) {
			user, userOK := auth.UserFrom(ctx)
			tenantID, tenantOK := auth.TenantFrom(ctx)
			return team.Principal{
				UserID: user.ID, TenantID: tenantID,
			}, userOK && tenantOK
		},
		cfg.BaseURL,
	)

	// The two endpoints a call needs. Neither is behind RequireTenant: a
	// caller is anonymous. Both prove their own origin instead — Twilio's
	// signature on the webhook, and a token we minted in the TwiML on the
	// socket. Until a model is wired to the permitted tools, the agent picks
	// up and says so rather than leaving the line ringing into nothing.
	voice.Register(
		mux,
		voice.NewStore(database),
		voice.Config{
			PublicBaseURL: cfg.BaseURL,
			AuthToken:     cfg.TwilioAuthToken,
			Greeting:      cfg.VoiceGreeting,
		},
		voice.StaticResponder{Sentence: cfg.VoiceFallback},
		slog.Default(),
	)

	// Stdlib CSRF protection (Go 1.25+): blocks cross-origin non-safe methods
	// via Sec-Fetch-Site/Origin; requests without those headers pass.
	csrf := http.NewCrossOriginProtection()

	return withRecover(withLogging(csrf.Handler(authStore.WithUser(mux))))
}
