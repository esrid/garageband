package calls_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/esrid/garageband/internal/features/calls"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
)

type callsFixture struct {
	fixtures *db.DB
	store    *calls.Store
	tenantID string
	userID   string
	callID   string
}

func TestCallInboxFiltersInGoAndTranscriptUsesSequence(t *testing.T) {
	fixture := newCallsFixture(t)
	inbox, err := fixture.store.Inbox(
		t.Context(), fixture.tenantID, fixture.userID, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.Calls) != 2 || inbox.Calls[0].Status != "no_answer" ||
		inbox.Calls[1].ID != fixture.callID {
		t.Fatalf("inbox = %#v", inbox.Calls)
	}
	identified := inbox.Calls[1]
	if identified.CallerNumber != "06 12 34 56 78" ||
		identified.StartedAt.Format("15:04 -07:00") != "08:00 -04:00" ||
		identified.NeedsAttention() {
		t.Fatalf("identified call = %#v", identified)
	}

	attention, err := fixture.store.Inbox(
		t.Context(), fixture.tenantID, fixture.userID, calls.FilterNeedsAttention,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(attention.Calls) != 1 || !attention.Calls[0].NeedsAttention() {
		t.Fatalf("attention inbox = %#v", attention.Calls)
	}

	transcript, err := fixture.store.Transcript(
		t.Context(), fixture.tenantID, fixture.userID, fixture.callID,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Bonjour", "Recherche du client", "Bonjour Alice"}
	if len(transcript.Messages) != len(want) {
		t.Fatalf("messages = %#v", transcript.Messages)
	}
	for index, content := range want {
		if transcript.Messages[index].Content != content {
			t.Fatalf("message %d = %q, want %q", index, transcript.Messages[index].Content, content)
		}
	}
}

func TestCallHTTPRoutes(t *testing.T) {
	fixture := newCallsFixture(t)
	mux := http.NewServeMux()
	calls.Register(
		mux, fixture.store,
		func(next http.Handler) http.Handler { return next },
		func(_ context.Context) (calls.Principal, bool) {
			return calls.Principal{UserID: fixture.userID, TenantID: fixture.tenantID}, true
		},
	)

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/calls?status=attention", nil))
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "Correspondant non reconnu") ||
		strings.Contains(response.Body.String(), "Alice Martin") {
		t.Fatalf("filtered inbox = %d %q", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/calls/"+fixture.callID, nil))
	body := response.Body.String()
	if response.Code != http.StatusOK ||
		strings.Index(body, "Bonjour") > strings.Index(body, "Recherche du client") ||
		!strings.Contains(body, "Action de l&#39;agent") {
		t.Fatalf("transcript = %d %q", response.Code, body)
	}
}

func newCallsFixture(t *testing.T) callsFixture {
	t.Helper()
	fixtures, runtime := dbtest.OpenRuntime(t)
	fixture := callsFixture{fixtures: fixtures, store: calls.NewStore(runtime)}
	fixture.userID = insertReturningID(t, fixtures, `
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', 'calls-owner', 'calls@example.com', 'Calls Owner')
		RETURNING id::text`)
	fixture.tenantID = insertReturningID(t, fixtures, `
		INSERT INTO tenants (slug, name)
		VALUES ('calls-garage', 'Garage Calls') RETURNING id::text`)
	mustExec(t, fixtures, `
		INSERT INTO tenant_memberships (tenant_id, user_id, role)
		VALUES ($1, $2, 'owner')`, fixture.tenantID, fixture.userID)
	locationID := insertReturningID(t, fixtures, `
		INSERT INTO locations (tenant_id, slug, name, timezone)
		VALUES ($1, 'martinique', 'Atelier Martinique', 'America/Martinique')
		RETURNING id::text`, fixture.tenantID)
	customerID := insertReturningID(t, fixtures, `
		INSERT INTO customers (tenant_id, home_location_id, first_name, last_name)
		VALUES ($1, $2, 'Alice', 'Martin') RETURNING id::text`,
		fixture.tenantID, locationID)
	var agentID string
	if err := fixtures.QueryRow(`
		SELECT id::text FROM agents
		WHERE tenant_id = $1 AND location_id = $2`,
		fixture.tenantID, locationID,
	).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	fixture.callID = insertReturningID(t, fixtures, `
		INSERT INTO calls (
		    tenant_id, location_id, agent_id, customer_id, provider_call_id,
		    direction, status, from_e164, to_e164, started_at, answered_at,
		    ended_at, summary, outcome
		) VALUES ($1, $2, $3, $4, 'call-answered', 'inbound', 'completed',
		          '+33612345678', '+33900000000', $5, $5, $6,
		          'Demande de rendez-vous', 'booked')
		RETURNING id::text`,
		fixture.tenantID, locationID, agentID, customerID,
		started, started.Add(3*time.Minute))
	insertReturningID(t, fixtures, `
		INSERT INTO calls (
		    tenant_id, location_id, agent_id, provider_call_id, direction,
		    status, from_e164, to_e164, started_at, ended_at
		) VALUES ($1, $2, $3, 'call-missed', 'inbound', 'no_answer',
		          '+33699999999', '+33900000000', $4, $4)
		RETURNING id::text`, fixture.tenantID, locationID, agentID, started.Add(time.Hour))
	occurred := started.Add(time.Minute)
	for _, message := range []struct {
		sequence int
		speaker  string
		content  string
	}{
		{2, "agent", "Bonjour Alice"},
		{0, "caller", "Bonjour"},
		{1, "tool", "Recherche du client"},
	} {
		mustExec(t, fixtures, `
			INSERT INTO call_messages (
			    tenant_id, call_id, sequence, speaker, content, occurred_at
			) VALUES ($1, $2, $3, $4, $5, $6)`,
			fixture.tenantID, fixture.callID, message.sequence,
			message.speaker, message.content, occurred,
		)
	}
	return fixture
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
