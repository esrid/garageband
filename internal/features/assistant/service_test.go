package assistant_test

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/esrid/garageband/internal/features/assistant"
	"github.com/esrid/garageband/internal/features/catalog"
	"github.com/esrid/garageband/internal/features/customers"
	"github.com/esrid/garageband/internal/features/locations"
	"github.com/esrid/garageband/internal/platform/assistanttools"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
	"github.com/esrid/garageband/internal/platform/llm"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestAssistantPreviewsConfirmsAndAuditsLocationContactChange(t *testing.T) {
	fixture := newAssistantFixture(t)
	conversationID, err := fixture.service.Send(
		t.Context(), fixture.tenantID, fixture.ownerID, "", fixture.locationA,
		"Mets l'e-mail du site à ACCUEIL@GARAGE.FR",
	)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.store.Workspace(t.Context(), fixture.tenantID, fixture.ownerID, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Messages) != 2 || workspace.Messages[0].Role != "user" ||
		len(workspace.Executions) != 1 || workspace.Executions[0].Status != "proposed" {
		t.Fatalf("proposed workspace = %#v", workspace)
	}
	if !strings.Contains(workspace.Executions[0].PreviewSummary, "accueil@garage.fr") {
		t.Fatalf("preview = %q", workspace.Executions[0].PreviewSummary)
	}
	assertLocationEmail(t, fixture.fixtures, fixture.locationA, "old@garage.fr")

	executionID := workspace.Executions[0].ID
	if err := fixture.service.Confirm(
		t.Context(), fixture.tenantID, fixture.ownerID, conversationID, executionID,
	); err != nil {
		t.Fatal(err)
	}
	// Confirmation is retry-safe: the second request observes a terminal audit
	// and does not execute or append another result.
	if err := fixture.service.Confirm(
		t.Context(), fixture.tenantID, fixture.ownerID, conversationID, executionID,
	); err != nil {
		t.Fatal(err)
	}
	assertLocationEmail(t, fixture.fixtures, fixture.locationA, "accueil@garage.fr")
	workspace, err = fixture.store.Workspace(t.Context(), fixture.tenantID, fixture.ownerID, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Executions[0].Status != "succeeded" || len(workspace.Messages) != 3 ||
		!strings.Contains(workspace.Messages[2].Content, "mises à jour") {
		t.Fatalf("confirmed workspace = %#v", workspace)
	}

	// Even the fixture/migration role cannot rewrite the immutable evidence.
	_, err = fixture.fixtures.Exec(t.Context(), `
		UPDATE assistant_tool_executions SET input = '{"email":"forged@example.com"}'
		WHERE id = $1`, executionID)
	assertConstraint(t, err, "assistant_tool_audit_immutable")
	_, err = fixture.fixtures.Exec(t.Context(), `
		UPDATE assistant_messages SET content = 'forged' WHERE conversation_id = $1`,
		conversationID)
	assertConstraint(t, err, "assistant_message_history_immutable")
	_, err = fixture.fixtures.Exec(t.Context(), `
		UPDATE application_tool_receipts SET output = '{"forged":true}'
		WHERE tenant_id = $1 AND idempotency_key = $2`, fixture.tenantID, executionID)
	assertConstraint(t, err, "application_tool_receipt_immutable")
}

func TestAssistantEnforcesConversationLocationAndToolRole(t *testing.T) {
	fixture := newAssistantFixture(t)
	conversationID, err := fixture.service.Send(
		t.Context(), fixture.tenantID, fixture.ownerID, "", fixture.locationA,
		"Mets l'e-mail du site à owner@garage.fr",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Workspace(
		t.Context(), fixture.tenantID, fixture.memberID, conversationID,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("other employee conversation = %v, want sql.ErrNoRows", err)
	}

	memberConversation, err := fixture.service.Send(
		t.Context(), fixture.tenantID, fixture.memberID, "", fixture.locationA,
		"Mets l'e-mail du site à member@garage.fr",
	)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.store.Workspace(t.Context(), fixture.tenantID, fixture.memberID, memberConversation)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Executions) != 0 || len(workspace.Messages) != 2 ||
		!strings.Contains(workspace.Messages[1].Content, "rôle") {
		t.Fatalf("member assistant response = %#v", workspace)
	}
	assertLocationEmail(t, fixture.fixtures, fixture.locationA, "old@garage.fr")

	if _, err := fixture.service.Send(
		t.Context(), fixture.tenantID, fixture.memberID, "", fixture.locationB,
		"Bonjour",
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unassigned location conversation = %v, want sql.ErrNoRows", err)
	}
}

func TestAssistantCanRejectProposalWithoutChangingData(t *testing.T) {
	fixture := newAssistantFixture(t)
	conversationID, err := fixture.service.Send(
		t.Context(), fixture.tenantID, fixture.ownerID, "", fixture.locationA,
		"Mets le téléphone du site à +596596123456",
	)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.store.Workspace(t.Context(), fixture.tenantID, fixture.ownerID, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	executionID := workspace.Executions[0].ID
	if err := fixture.service.Reject(
		t.Context(), fixture.tenantID, fixture.ownerID, conversationID, executionID,
	); err != nil {
		t.Fatal(err)
	}
	workspace, err = fixture.store.Workspace(t.Context(), fixture.tenantID, fixture.ownerID, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Executions[0].Status != "rejected" ||
		!strings.Contains(workspace.Messages[len(workspace.Messages)-1].Content, "Aucune donnée") {
		t.Fatalf("rejected workspace = %#v", workspace)
	}
	var phone sql.NullString
	if err := fixture.fixtures.QueryRow(t.Context(), `SELECT phone_e164 FROM locations WHERE id = $1`, fixture.locationA).Scan(&phone); err != nil {
		t.Fatal(err)
	}
	if phone.Valid {
		t.Fatalf("rejected proposal changed phone to %q", phone.String)
	}
}

func TestAssistantRejectsStalePreviewAndResumesAppliedExecution(t *testing.T) {
	fixture := newAssistantFixture(t)
	conversationID, err := fixture.service.Send(
		t.Context(), fixture.tenantID, fixture.ownerID, "", fixture.locationA,
		"Mets l'e-mail du site à first@garage.fr",
	)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.store.Workspace(t.Context(), fixture.tenantID, fixture.ownerID, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	executionID := workspace.Executions[0].ID
	if _, err := fixture.fixtures.Exec(t.Context(), `
		UPDATE locations SET email = 'concurrent@garage.fr',
		       updated_at = updated_at + interval '1 microsecond'
		WHERE id = $1`, fixture.locationA); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.Confirm(
		t.Context(), fixture.tenantID, fixture.ownerID, conversationID, executionID,
	); err != nil {
		t.Fatal(err)
	}
	assertLocationEmail(t, fixture.fixtures, fixture.locationA, "concurrent@garage.fr")
	workspace, err = fixture.store.Workspace(t.Context(), fixture.tenantID, fixture.ownerID, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Executions[0].Status != "failed" ||
		!strings.Contains(workspace.Executions[0].ErrorMessage, "changé depuis l’aperçu") {
		t.Fatalf("stale execution = %#v", workspace.Executions[0])
	}

	conversationID, err = fixture.service.Send(
		t.Context(), fixture.tenantID, fixture.ownerID, "", fixture.locationA,
		"Mets l'e-mail du site à resumed@garage.fr",
	)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = fixture.store.Workspace(t.Context(), fixture.tenantID, fixture.ownerID, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	executionID = workspace.Executions[0].ID
	execution, shouldExecute, err := fixture.store.BeginExecution(
		t.Context(), fixture.tenantID, fixture.ownerID, conversationID, executionID,
	)
	if err != nil || !shouldExecute {
		t.Fatalf("begin execution = %#v, %v, %v", execution, shouldExecute, err)
	}
	if _, err := fixture.tools.Execute(t.Context(), assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.ownerID,
		LocationID: fixture.locationA, IdempotencyKey: executionID,
	}, execution.ToolName, execution.Input); err != nil {
		t.Fatal(err)
	}
	// Simulate a process exit before FinishExecution. Confirm resumes the
	// running audit and the domain tool returns its durable receipt.
	if err := fixture.service.Confirm(
		t.Context(), fixture.tenantID, fixture.ownerID, conversationID, executionID,
	); err != nil {
		t.Fatal(err)
	}
	assertLocationEmail(t, fixture.fixtures, fixture.locationA, "resumed@garage.fr")
	workspace, err = fixture.store.Workspace(t.Context(), fixture.tenantID, fixture.ownerID, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Executions[0].Status != "succeeded" {
		t.Fatalf("resumed execution = %#v", workspace.Executions[0])
	}
}

func TestAssistantReadsOnlyCurrentlyQuotableCatalogPrices(t *testing.T) {
	fixture := newAssistantFixture(t)
	amount := int64(7900)
	duration := 45
	if _, err := fixture.catalogStore.Create(
		t.Context(), fixture.tenantID, fixture.ownerID, catalog.ItemInput{
			Kind: catalog.KindService, Reference: "VID-01", Name: "Vidange",
			PriceKind: catalog.PriceFrom, AmountCents: &amount,
			TaxBasis: catalog.TaxInclusive, VATBasisPoints: 2000,
			DurationMinutes: &duration, LocationScope: catalog.ScopeAll,
		},
	); err != nil {
		t.Fatal(err)
	}
	conversationID, err := fixture.service.Send(
		t.Context(), fixture.tenantID, fixture.ownerID, "", fixture.locationA,
		"Quel est le prix de la vidange ?",
	)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.store.Workspace(t.Context(), fixture.tenantID, fixture.ownerID, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Executions) != 1 || workspace.Executions[0].ToolName != catalog.ToolSearchCatalog ||
		workspace.Executions[0].Status != "succeeded" || workspace.Executions[0].ConfirmedAt.Valid {
		t.Fatalf("catalog read execution = %#v", workspace.Executions)
	}
	answer := workspace.Messages[len(workspace.Messages)-1].Content
	if !strings.Contains(answer, "Vidange — à partir de 79,00 € TTC") ||
		!strings.Contains(answer, "45 min") || !strings.Contains(answer, "VID-01") {
		t.Fatalf("catalog answer = %q", answer)
	}
}

func TestAssistantSearchesCustomersOnlyInConversationLocation(t *testing.T) {
	fixture := newAssistantFixture(t)
	var customerID string
	if err := fixture.fixtures.QueryRow(t.Context(), `
		INSERT INTO customers (tenant_id, home_location_id, first_name, last_name)
		VALUES ($1, $2, 'Alice', 'Martin') RETURNING id::text`,
		fixture.tenantID, fixture.locationA,
	).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.fixtures.Exec(t.Context(), `
		INSERT INTO vehicles (tenant_id, location_id, customer_id, registration_plate, make, model)
		VALUES ($1, $2, $3, 'AA-123-AA', 'Renault', 'Clio')`,
		fixture.tenantID, fixture.locationA, customerID); err != nil {
		t.Fatal(err)
	}
	var otherCustomerID string
	if err := fixture.fixtures.QueryRow(t.Context(), `
		INSERT INTO customers (tenant_id, home_location_id, first_name, last_name)
		VALUES ($1, $2, 'Alice', 'Martin') RETURNING id::text`,
		fixture.tenantID, fixture.locationB,
	).Scan(&otherCustomerID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.fixtures.Exec(t.Context(), `
		INSERT INTO vehicles (tenant_id, location_id, customer_id, registration_plate, make, model)
		VALUES ($1, $2, $3, 'BB-456-BB', 'Peugeot', '208')`,
		fixture.tenantID, fixture.locationB, otherCustomerID); err != nil {
		t.Fatal(err)
	}
	conversationID, err := fixture.service.Send(
		t.Context(), fixture.tenantID, fixture.ownerID, "", fixture.locationA,
		"Trouve le client Alice Martin",
	)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.store.Workspace(t.Context(), fixture.tenantID, fixture.ownerID, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	answer := workspace.Messages[len(workspace.Messages)-1].Content
	if !strings.Contains(answer, "Alice Martin") || !strings.Contains(answer, "AA-123-AA · Renault Clio") {
		t.Fatalf("customer search answer = %q", answer)
	}
	if strings.Contains(answer, "BB-456-BB") {
		t.Fatalf("customer search leaked another location: %q", answer)
	}
	if len(workspace.Executions) != 1 || workspace.Executions[0].ToolName != customers.ToolSearchCustomers {
		t.Fatalf("customer search audit = %#v", workspace.Executions)
	}
}

type assistantFixture struct {
	fixtures      *db.DB
	store         *assistant.Store
	service       *assistant.Service
	tools         *assistanttools.Registry
	catalogStore  *catalog.Store
	customerStore *customers.Store
	tenantID      string
	ownerID       string
	memberID      string
	locationA     string
	locationB     string
}

func newAssistantFixture(t *testing.T) assistantFixture {
	t.Helper()
	fixtures, runtime := dbtest.OpenRuntime(t)
	ownerID := createAssistantUser(t, fixtures, "assistant-owner@example.com")
	memberID := createAssistantUser(t, fixtures, "assistant-member@example.com")
	tenantID := createAssistantTenant(t, fixtures, ownerID)
	if _, err := fixtures.Exec(t.Context(), `
		INSERT INTO tenant_memberships (tenant_id, user_id, role)
		VALUES ($1, $2, 'member')`, tenantID, memberID); err != nil {
		t.Fatal(err)
	}
	locationStore := locations.NewStore(runtime)
	locationA, err := locationStore.Create(t.Context(), tenantID, ownerID, locations.Input{
		Name: "Atelier A", CountryCode: "FR", Timezone: "Europe/Paris", Email: "old@garage.fr",
	})
	if err != nil {
		t.Fatal(err)
	}
	locationB, err := locationStore.Create(t.Context(), tenantID, ownerID, locations.Input{
		Name: "Atelier B", CountryCode: "FR", Timezone: "Europe/Paris",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtures.Exec(t.Context(), `
		INSERT INTO user_location_assignments (
		    tenant_id, user_id, location_id, assigned_by_user_id
		) VALUES ($1, $2, $3, $4)`, tenantID, memberID, locationA.ID, ownerID); err != nil {
		t.Fatal(err)
	}
	store := assistant.NewStore(runtime)
	catalogStore := catalog.NewStore(runtime)
	customerStore := customers.NewStore(runtime)
	tools := assistanttools.NewRegistry(locationStore, catalogStore, customerStore)
	return assistantFixture{
		fixtures: fixtures, store: store, tools: tools,
		catalogStore: catalogStore, customerStore: customerStore,
		service: assistant.NewService(
			store, llm.NewDemonstrationProvider(),
			tools,
		),
		tenantID: tenantID, ownerID: ownerID, memberID: memberID,
		locationA: locationA.ID, locationB: locationB.ID,
	}
}

func createAssistantUser(t *testing.T, database *db.DB, email string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(t.Context(), `
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', $1, $1, 'Test User') RETURNING id::text`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func createAssistantTenant(t *testing.T, database *db.DB, ownerID string) string {
	t.Helper()
	var tenantID string
	err := database.WithinNewTenantUser(t.Context(), ownerID, func(tx pgx.Tx, id string) error {
		tenantID = id
		if _, err := tx.Exec(t.Context(), `
			INSERT INTO tenants (id, slug, name)
			VALUES ($1::uuid, 'assistant-' || left(replace($1::text, '-', ''), 12), 'Garage Assistant')`, id); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), `
			INSERT INTO tenant_memberships (tenant_id, user_id, role)
			VALUES ($1, $2, 'owner')`, id, ownerID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tenantID
}

func assertLocationEmail(t *testing.T, database *db.DB, locationID string, want string) {
	t.Helper()
	var got string
	if err := database.QueryRow(t.Context(), `SELECT email FROM locations WHERE id = $1`, locationID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("location email = %q, want %q", got, want)
	}
}

func assertConstraint(t *testing.T, err error, name string) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.ConstraintName != name {
		t.Fatalf("error = %v, want constraint %s", err, name)
	}
}
