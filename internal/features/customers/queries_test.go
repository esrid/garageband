package customers_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/esrid/garageband/internal/features/customers"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
)

type customerFixture struct {
	fixtures          *db.DB
	store             *customers.Store
	tenantID          string
	ownerID           string
	homeStaffID       string
	receivingStaffID  string
	homeLocationID    string
	receivingLocation string
	customerID        string
	vehicleID         string
}

func TestCustomerSearchProfileAndShareLifecycle(t *testing.T) {
	fixture := newCustomerFixture(t)

	page, err := fixture.store.Search(
		t.Context(), fixture.tenantID, fixture.receivingStaffID, "Martin",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Customers) != 0 {
		t.Fatalf("receiving site found %d customers before grant", len(page.Customers))
	}

	for _, query := range []string{
		"martin", "0612345678", "alice@example.fr", "AA123AA",
		"VF1RJA00012345678",
	} {
		page, err = fixture.store.Search(
			t.Context(), fixture.tenantID, fixture.homeStaffID, query,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Customers) != 1 || page.Customers[0].ID != fixture.customerID {
			t.Fatalf("search %q returned %#v", query, page.Customers)
		}
	}
	result := page.Customers[0]
	if result.Shared || result.HomeLocationName != "Atelier Nord" ||
		result.Phone != "06 12 34 56 78" || len(result.Vehicles) != 1 {
		t.Fatalf("home search result = %#v", result)
	}

	grantID := fixture.grant(t)
	page, err = fixture.store.Search(
		t.Context(), fixture.tenantID, fixture.receivingStaffID, "AA-123-AA",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Customers) != 1 || !page.Customers[0].Shared ||
		page.Customers[0].HomeLocationName != "Atelier Nord" {
		t.Fatalf("shared search result = %#v", page.Customers)
	}

	profile, err := fixture.store.Profile(
		t.Context(), fixture.tenantID, fixture.receivingStaffID, fixture.customerID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if profile.CanEdit || !profile.Customer.Shared || len(profile.Vehicles) != 1 ||
		len(profile.Timeline) != 2 || len(profile.Memories) != 1 {
		t.Fatalf("shared profile = %#v", profile)
	}
	for _, event := range profile.Timeline {
		if event.AuthoredHere || event.LocationName != "Atelier Nord" {
			t.Fatalf("source event permissions = %#v", event)
		}
	}
	if profile.Memories[0].Value != "Préfère les rendez-vous le matin" {
		t.Fatalf("memory = %#v", profile.Memories[0])
	}

	receivingAppointmentID := fixture.insertReceivingAppointment(t)
	fixture.revoke(t, grantID)
	if _, err := fixture.store.Profile(
		t.Context(), fixture.tenantID, fixture.receivingStaffID, fixture.customerID,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("profile after revocation = %v, want sql.ErrNoRows", err)
	}
	page, err = fixture.store.Search(
		t.Context(), fixture.tenantID, fixture.receivingStaffID, "Martin",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Customers) != 0 {
		t.Fatalf("receiving search after revocation = %#v", page.Customers)
	}

	homeProfile, err := fixture.store.Profile(
		t.Context(), fixture.tenantID, fixture.homeStaffID, fixture.customerID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !homeProfile.CanEdit || homeProfile.Customer.Shared {
		t.Fatalf("home profile permissions = %#v", homeProfile)
	}
	foundReceivingEvent := false
	for _, event := range homeProfile.Timeline {
		if event.ID == receivingAppointmentID {
			foundReceivingEvent = true
			if event.AuthoredHere || event.LocationName != "Atelier Sud" {
				t.Fatalf("retained receiving event = %#v", event)
			}
		}
	}
	if !foundReceivingEvent {
		t.Fatal("home profile lost receiving-site event created during grant")
	}
}

func TestCustomerHTTPRoutesAndValidation(t *testing.T) {
	fixture := newCustomerFixture(t)
	handler := customerHandler(fixture.store, customers.Principal{
		UserID: fixture.homeStaffID, TenantID: fixture.tenantID,
	})

	response := request(handler, "/customers?q="+url.QueryEscape("AA123AA"))
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "Alice Martin") ||
		!strings.Contains(response.Body.String(), "/customers/"+fixture.customerID) {
		t.Fatalf("search = %d %q", response.Code, response.Body.String())
	}

	response = request(handler, "/customers/"+fixture.customerID)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "Historique") ||
		!strings.Contains(response.Body.String(), "Révision annuelle") {
		t.Fatalf("profile = %d %q", response.Code, response.Body.String())
	}

	response = request(handler, "/customers/not-a-uuid")
	if response.Code != http.StatusNotFound {
		t.Fatalf("invalid customer id = %d, want 404", response.Code)
	}

	response = request(handler, "/customers?q="+strings.Repeat("a", 101))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("long search = %d, want 422", response.Code)
	}
}

func newCustomerFixture(t *testing.T) customerFixture {
	t.Helper()
	fixtures, runtime := dbtest.OpenRuntime(t)
	fixture := customerFixture{fixtures: fixtures}
	fixture.store = customers.NewStore(runtime)
	fixture.ownerID = insertUser(t, fixtures, "customer-owner@example.com")
	fixture.homeStaffID = insertUser(t, fixtures, "customer-home@example.com")
	fixture.receivingStaffID = insertUser(t, fixtures, "customer-receiving@example.com")
	fixture.tenantID = insertTenant(t, fixtures, "customer-search", "Garage Central")
	insertMembership(t, fixtures, fixture.tenantID, fixture.ownerID, "owner")
	insertMembership(t, fixtures, fixture.tenantID, fixture.homeStaffID, "member")
	insertMembership(t, fixtures, fixture.tenantID, fixture.receivingStaffID, "member")
	fixture.homeLocationID = insertLocation(t, fixtures, fixture.tenantID, "north", "Atelier Nord")
	fixture.receivingLocation = insertLocation(t, fixtures, fixture.tenantID, "south", "Atelier Sud")
	insertAssignment(t, fixtures, fixture.tenantID, fixture.homeStaffID, fixture.homeLocationID, fixture.ownerID)
	insertAssignment(t, fixtures, fixture.tenantID, fixture.receivingStaffID, fixture.receivingLocation, fixture.ownerID)

	if err := fixtures.QueryRow(t.Context(), `
		INSERT INTO customers (
			tenant_id, home_location_id, first_name, last_name
		) VALUES ($1, $2, 'Alice', 'Martin') RETURNING id`,
		fixture.tenantID, fixture.homeLocationID,
	).Scan(&fixture.customerID); err != nil {
		t.Fatal(err)
	}
	for _, contact := range []struct{ kind, value, normalized string }{
		{kind: "phone", value: "06 12 34 56 78", normalized: "+33612345678"},
		{kind: "email", value: "alice@example.fr", normalized: "alice@example.fr"},
	} {
		if _, err := fixtures.Exec(t.Context(), `
			INSERT INTO customer_contacts (
				tenant_id, customer_id, kind, value, normalized_value, is_primary
			) VALUES ($1, $2, $3, $4, $5, TRUE)`,
			fixture.tenantID, fixture.customerID, contact.kind,
			contact.value, contact.normalized,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixtures.QueryRow(t.Context(), `
		INSERT INTO vehicles (
			tenant_id, location_id, customer_id, registration_plate, vin,
			make, model, first_registration_on
		) VALUES (
			$1, $2, $3, 'AA-123-AA', 'VF1RJA00012345678',
			'Renault', 'Clio', DATE '2019-04-12'
		) RETURNING id`, fixture.tenantID, fixture.homeLocationID, fixture.customerID,
	).Scan(&fixture.vehicleID); err != nil {
		t.Fatal(err)
	}
	var serviceID string
	if err := fixtures.QueryRow(t.Context(), `
		INSERT INTO service_offerings (
			tenant_id, location_id, code, name, duration_minutes
		) VALUES ($1, $2, 'annual-service', 'Révision annuelle', 60)
		RETURNING id`, fixture.tenantID, fixture.homeLocationID,
	).Scan(&serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtures.Exec(t.Context(), `
		INSERT INTO appointments (
			tenant_id, location_id, customer_id, vehicle_id, service_id,
			status, starts_at, ends_at
		) VALUES (
			$1, $2, $3, $4, $5, 'draft',
			TIMESTAMPTZ '2026-06-10 08:00:00Z',
			TIMESTAMPTZ '2026-06-10 09:00:00Z'
		)`, fixture.tenantID, fixture.homeLocationID,
		fixture.customerID, fixture.vehicleID, serviceID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtures.Exec(t.Context(), `
		INSERT INTO repair_orders (
			tenant_id, location_id, customer_id, vehicle_id, status,
			work_performed, subtotal_cents, tax_cents, total_cents,
			approved_at, completed_at, opened_at
		) VALUES (
			$1, $2, $3, $4, 'completed', 'Plaquettes remplacées',
			10000, 2000, 12000, now(), now(),
			TIMESTAMPTZ '2026-07-12 08:00:00Z'
		)`, fixture.tenantID, fixture.homeLocationID,
		fixture.customerID, fixture.vehicleID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtures.Exec(t.Context(), `
		INSERT INTO customer_memories (
			tenant_id, location_id, customer_id, key, value, confidence
		) VALUES (
			$1, $2, $3, 'availability.preference',
			'"Préfère les rendez-vous le matin"'::JSONB, 0.900
		)`, fixture.tenantID, fixture.homeLocationID, fixture.customerID,
	); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture customerFixture) grant(t *testing.T) string {
	t.Helper()
	var grantID string
	if err := fixture.fixtures.QueryRow(t.Context(), `
		INSERT INTO customer_location_grants (
			tenant_id, customer_id, source_location_id,
			receiving_location_id, granted_by_user_id
		) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		fixture.tenantID, fixture.customerID, fixture.homeLocationID,
		fixture.receivingLocation, fixture.ownerID,
	).Scan(&grantID); err != nil {
		t.Fatal(err)
	}
	return grantID
}

func (fixture customerFixture) revoke(t *testing.T, grantID string) {
	t.Helper()
	if _, err := fixture.fixtures.Exec(t.Context(), `
		UPDATE customer_location_grants
		SET revoked_by_user_id = $3, revoked_at = now()
		WHERE tenant_id = $1 AND id = $2`,
		fixture.tenantID, grantID, fixture.ownerID,
	); err != nil {
		t.Fatal(err)
	}
}

func (fixture customerFixture) insertReceivingAppointment(t *testing.T) string {
	t.Helper()
	var appointmentID string
	if err := fixture.fixtures.QueryRow(t.Context(), `
		INSERT INTO appointments (
			tenant_id, location_id, customer_id, vehicle_id,
			status, starts_at, ends_at, created_at
		) VALUES (
			$1, $2, $3, $4, 'draft', now() + interval '1 day',
			now() + interval '1 day 1 hour', now()
		) RETURNING id`, fixture.tenantID, fixture.receivingLocation,
		fixture.customerID, fixture.vehicleID,
	).Scan(&appointmentID); err != nil {
		t.Fatal(err)
	}
	return appointmentID
}

func customerHandler(store *customers.Store, principal customers.Principal) http.Handler {
	mux := http.NewServeMux()
	customers.Register(
		mux, store,
		func(next http.Handler) http.Handler { return next },
		func(context.Context) (customers.Principal, bool) { return principal, true },
	)
	return mux
}

func request(handler http.Handler, target string) *httptest.ResponseRecorder {
	record := httptest.NewRecorder()
	handler.ServeHTTP(record, httptest.NewRequest(http.MethodGet, target, nil))
	return record
}

func insertUser(t *testing.T, database *db.DB, email string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(t.Context(), `
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', $1, $1, 'Test User') RETURNING id`, email,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertTenant(t *testing.T, database *db.DB, slug, name string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(t.Context(), `
		INSERT INTO tenants (slug, name) VALUES ($1, $2) RETURNING id`, slug, name,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertMembership(t *testing.T, database *db.DB, tenantID, userID, role string) {
	t.Helper()
	if _, err := database.Exec(t.Context(), `
		INSERT INTO tenant_memberships (tenant_id, user_id, role)
		VALUES ($1, $2, $3)`, tenantID, userID, role,
	); err != nil {
		t.Fatal(err)
	}
}

func insertLocation(t *testing.T, database *db.DB, tenantID, slug, name string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(t.Context(), `
		INSERT INTO locations (tenant_id, slug, name)
		VALUES ($1, $2, $3) RETURNING id`, tenantID, slug, name,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertAssignment(
	t *testing.T,
	database *db.DB,
	tenantID, userID, locationID, actorID string,
) {
	t.Helper()
	if _, err := database.Exec(t.Context(), `
		INSERT INTO user_location_assignments (
			tenant_id, user_id, location_id, assigned_by_user_id
		) VALUES ($1, $2, $3, $4)`, tenantID, userID, locationID, actorID,
	); err != nil {
		t.Fatal(err)
	}
}
