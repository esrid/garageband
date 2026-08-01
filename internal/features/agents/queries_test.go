package agents_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/esrid/garageband/internal/features/agents"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
	"github.com/jackc/pgx/v5/pgconn"
)

type agentsFixture struct {
	fixtures   *db.DB
	store      *agents.Store
	tenantID   string
	ownerID    string
	memberID   string
	locationID string
	agentID    string
}

func TestLocationCreatesOneAgentAndLifecycleRequiresSelectedProviders(t *testing.T) {
	fixture := newAgentsFixture(t)
	page, err := fixture.store.List(
		t.Context(), fixture.tenantID, fixture.ownerID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Agents) != 1 || len(page.Agents[0].Missing) != 3 || !page.CanManage {
		t.Fatalf("initial agents = %#v", page)
	}

	unready := agents.Input{
		Name: "Léa", Greeting: "Bonjour, Garage Central.",
		Fallback: "Je transmets votre demande à l’atelier.", Locale: "fr-FR",
	}
	if err := fixture.store.Save(
		t.Context(), fixture.tenantID, fixture.ownerID, fixture.agentID, unready,
	); err != nil {
		t.Fatalf("save while providers are absent: %v", err)
	}
	if err := fixture.store.Activate(
		t.Context(), fixture.tenantID, fixture.ownerID, fixture.agentID,
	); !errors.Is(err, agents.ErrNotReady) {
		t.Fatalf("activate unready = %v", err)
	}

	llm := fixture.connection(t, "llm", "openai", "llm-account")
	stt := fixture.connection(t, "speech_to_text", "speech", "stt-account")
	tts := fixture.connection(t, "text_to_speech", "speech", "tts-account")
	configured := unready
	configured.LLM, configured.STT, configured.TTS = llm, stt, tts
	if err := fixture.store.Save(
		t.Context(), fixture.tenantID, fixture.ownerID, fixture.agentID, configured,
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Activate(
		t.Context(), fixture.tenantID, fixture.ownerID, fixture.agentID,
	); err != nil {
		t.Fatal(err)
	}

	page, err = fixture.store.List(t.Context(), fixture.tenantID, fixture.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Agents[0].Missing) != 0 || !page.Agents[0].Answering() ||
		page.Agents[0].Reachable() {
		t.Fatalf("configured without number = %#v", page.Agents[0])
	}
	fixture.addNumber(t)
	page, err = fixture.store.List(t.Context(), fixture.tenantID, fixture.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if !page.Agents[0].Reachable() || page.Agents[0].Numbers[0] != "09 00 00 00 00" {
		t.Fatalf("reachable agent = %#v", page.Agents[0])
	}
	if err := fixture.store.Pause(
		t.Context(), fixture.tenantID, fixture.ownerID, fixture.agentID,
	); err != nil {
		t.Fatal(err)
	}
}

func TestAgentWritesRequireAdminAndDatabaseRejectsWrongProviderKind(t *testing.T) {
	fixture := newAgentsFixture(t)
	memberPage, err := fixture.store.Form(
		t.Context(), fixture.tenantID, fixture.memberID, fixture.agentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if memberPage.CanManage {
		t.Fatal("member form unexpectedly allows management")
	}
	if err := fixture.store.Save(
		t.Context(), fixture.tenantID, fixture.memberID, fixture.agentID,
		agents.Input{Name: "Intrus", Greeting: "Bonjour", Fallback: "Non", Locale: "fr-FR"},
	); !errors.Is(err, agents.ErrForbidden) {
		t.Fatalf("member save = %v", err)
	}

	stt := fixture.connection(t, "speech_to_text", "speech", "wrong-kind")
	_, err = fixture.fixtures.Exec(`
		UPDATE agents SET llm_connection_id = $1
		WHERE tenant_id = $2 AND id = $3`, stt, fixture.tenantID, fixture.agentID)
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "23503" {
		t.Fatalf("wrong provider kind update = %v", err)
	}

	_, err = fixture.fixtures.Exec(`
		INSERT INTO agents (tenant_id, location_id, name)
		VALUES ($1, $2, 'Duplicate')`, fixture.tenantID, fixture.locationID)
	if !errors.As(err, &pgError) || pgError.Code != "23505" {
		t.Fatalf("duplicate location agent = %v", err)
	}
}

func TestAgentActivationHTTPExplainsMissingProviders(t *testing.T) {
	fixture := newAgentsFixture(t)
	mux := http.NewServeMux()
	agents.Register(
		mux, fixture.store,
		func(next http.Handler) http.Handler { return next },
		func(_ context.Context) (agents.Principal, bool) {
			return agents.Principal{UserID: fixture.ownerID, TenantID: fixture.tenantID}, true
		},
	)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(
		http.MethodPost, "/agents/"+fixture.agentID+"/activate", nil,
	))
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "Choisissez une connexion active") {
		t.Fatalf("activation = %d %q", response.Code, response.Body.String())
	}
}

func newAgentsFixture(t *testing.T) agentsFixture {
	t.Helper()
	fixtures, runtime := dbtest.OpenRuntime(t)
	fixture := agentsFixture{fixtures: fixtures, store: agents.NewStore(runtime)}
	fixture.ownerID = insertReturningID(t, fixtures, `
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', 'agents-owner', 'agents-owner@example.com', 'Agents Owner')
		RETURNING id::text`)
	fixture.memberID = insertReturningID(t, fixtures, `
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', 'agents-member', 'agents-member@example.com', 'Agents Member')
		RETURNING id::text`)
	fixture.tenantID = insertReturningID(t, fixtures, `
		INSERT INTO tenants (slug, name)
		VALUES ('agents-garage', 'Garage Agents') RETURNING id::text`)
	mustExec(t, fixtures, `
		INSERT INTO tenant_memberships (tenant_id, user_id, role)
		VALUES ($1, $2, 'owner'), ($1, $3, 'member')`,
		fixture.tenantID, fixture.ownerID, fixture.memberID)
	fixture.locationID = insertReturningID(t, fixtures, `
		INSERT INTO locations (tenant_id, slug, name, timezone)
		VALUES ($1, 'central', 'Atelier Central', 'Europe/Paris')
		RETURNING id::text`, fixture.tenantID)
	if err := fixtures.QueryRow(`
		SELECT id::text FROM agents
		WHERE tenant_id = $1 AND location_id = $2`,
		fixture.tenantID, fixture.locationID,
	).Scan(&fixture.agentID); err != nil {
		t.Fatal(err)
	}
	mustExec(t, fixtures, `
		INSERT INTO user_location_assignments (
		    tenant_id, user_id, location_id, assigned_by_user_id
		) VALUES ($1, $2, $3, $4)`,
		fixture.tenantID, fixture.memberID, fixture.locationID, fixture.ownerID)
	return fixture
}

func (fixture agentsFixture) connection(
	t *testing.T,
	kind string,
	provider string,
	externalID string,
) string {
	t.Helper()
	return insertReturningID(t, fixture.fixtures, `
		INSERT INTO provider_connections (
		    tenant_id, location_id, kind, provider,
		    external_account_id, secret_ref
		) VALUES ($1, $2, $3, $4, $5, 'secret/test')
		RETURNING id::text`,
		fixture.tenantID, fixture.locationID, kind, provider, externalID)
}

func (fixture agentsFixture) addNumber(t *testing.T) {
	t.Helper()
	telephony := fixture.connection(t, "telephony", "twilio", "voice-account")
	mustExec(t, fixture.fixtures, `
		INSERT INTO phone_numbers (
		    tenant_id, location_id, agent_id, telephony_connection_id,
		    phone_e164, external_number_id
		) VALUES ($1, $2, $3, $4, '+33900000000', 'number-1')`,
		fixture.tenantID, fixture.locationID, fixture.agentID, telephony)
}

func insertReturningID(t *testing.T, database *db.DB, query string, args ...any) string {
	t.Helper()
	var id string
	if err := database.QueryRow(query, args...).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustExec(t *testing.T, database *db.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
