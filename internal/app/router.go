package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/esrid/garageband/internal/features/accesscontrol"
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
	agendaStore := agenda.NewStore(database)
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
		agendaCalendarConfig,
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
	accessStore := accesscontrol.NewStore(database)
	team.Register(mux, auth.RequireTenant, team.Deps{
		Principal: func(ctx context.Context) (team.Principal, bool) {
			user, userOK := auth.UserFrom(ctx)
			tenantID, tenantOK := auth.TenantFrom(ctx)
			return team.Principal{
				UserID: user.ID, TenantID: tenantID,
			}, userOK && tenantOK
		},
		LoadPage: func(ctx context.Context, principal team.Principal) (team.Page, error) {
			overview, err := accessStore.TeamOverview(
				ctx, principal.TenantID, principal.UserID,
			)
			if err != nil {
				return team.Page{}, err
			}
			page := team.Page{
				Organization: overview.Organization,
				CanManage:    overview.CanManage,
				Locations:    make([]team.LocationRef, 0, len(overview.Locations)),
				Members:      make([]team.Member, 0, len(overview.Members)),
			}
			for _, location := range overview.Locations {
				page.Locations = append(page.Locations, team.LocationRef{
					ID: location.ID, Name: location.Name, Active: location.Active,
				})
			}
			for _, member := range overview.Members {
				page.Members = append(page.Members, team.Member{
					UserID: member.UserID, Name: member.Name, Email: member.Email,
					Role: member.Role, LocationIDs: member.LocationIDs,
					InviteState: member.InviteState,
				})
			}
			return page, nil
		},
		ReplaceAssignments: func(
			ctx context.Context,
			principal team.Principal,
			targetUserID string,
			locationIDs []string,
		) error {
			return teamError(accessStore.ReplaceLocationAssignments(
				ctx, principal.TenantID, principal.UserID,
				targetUserID, locationIDs,
			))
		},
		InviteStaff: func(
			ctx context.Context,
			principal team.Principal,
			name string,
			locationIDs []string,
		) (team.Invitation, error) {
			invite, err := accessStore.InviteStaff(
				ctx, principal.TenantID, principal.UserID, name, locationIDs,
			)
			if err != nil {
				return team.Invitation{}, teamError(err)
			}
			return staffInvitation(cfg.BaseURL, invite.Token), nil
		},
		ReissueInvite: func(
			ctx context.Context,
			principal team.Principal,
			targetUserID string,
		) (team.Invitation, error) {
			invite, err := accessStore.ReissueInvite(
				ctx, principal.TenantID, principal.UserID, targetUserID,
			)
			if err != nil {
				return team.Invitation{}, teamError(err)
			}
			return staffInvitation(cfg.BaseURL, invite.Token), nil
		},
		RenameStaff: func(
			ctx context.Context,
			principal team.Principal,
			targetUserID string,
			name string,
		) error {
			return teamError(accessStore.RenameStaff(
				ctx, principal.TenantID, principal.UserID, targetUserID, name,
			))
		},
		RemoveStaff: func(
			ctx context.Context,
			principal team.Principal,
			targetUserID string,
		) error {
			return teamError(accessStore.RevokeStaff(
				ctx, principal.TenantID, principal.UserID, targetUserID,
			))
		},
	})

	// The two endpoints a call needs. Neither is behind RequireTenant: a
	// caller is anonymous. Both prove their own origin instead — Twilio's
	// signature on the webhook, and a token we minted in the TwiML on the
	// socket. Until a model is wired to the permitted tools, the agent picks
	// up and says so rather than leaving the line ringing into nothing.
	voice.Register(
		mux,
		voice.Config{
			PublicBaseURL: cfg.BaseURL,
			AuthToken:     cfg.TwilioAuthToken,
			Greeting:      cfg.VoiceGreeting,
		},
		voice.StaticResponder{Sentence: cfg.VoiceGreeting},
		slog.Default(),
	)

	// Stdlib CSRF protection (Go 1.25+): blocks cross-origin non-safe methods
	// via Sec-Fetch-Site/Origin; requests without those headers pass.
	csrf := http.NewCrossOriginProtection()

	return withRecover(withLogging(csrf.Handler(authStore.WithUser(mux))))
}

// teamError maps store refusals onto the team screen's own errors, so the
// feature never has to know which package rejected it.
func teamError(err error) error {
	switch {
	case errors.Is(err, accesscontrol.ErrForbidden):
		return team.ErrForbidden
	case errors.Is(err, accesscontrol.ErrNameRequired):
		return team.ErrNameRequired
	default:
		return err
	}
}

// staffInvitation presents one credential two ways: a code to type on a shop
// computer, and the same secret as a link to tap on a phone.
func staffInvitation(baseURL, token string) team.Invitation {
	return team.Invitation{Link: baseURL + "/rejoindre/" + token, Code: token}
}
