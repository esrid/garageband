package locations_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"

	"github.com/esrid/garageband/internal/features/locations"
	"github.com/esrid/garageband/internal/platform/dbtest"
)

func TestLocationHTTPFlowAndPermissions(t *testing.T) {
	database := dbtest.Open(t)
	ownerID := createUser(t, database, "handler-owner@example.com")
	memberID := createUser(t, database, "handler-member@example.com")
	tenantID := createTenant(t, database, ownerID)
	addMembership(t, database, tenantID, memberID, "member")
	store := locations.NewStore(database)

	ownerHandler := locationHandler(store, locations.Principal{
		UserID: ownerID, TenantID: tenantID,
	})
	response := getLocationPage(ownerHandler, "/locations")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Garage") {
		t.Fatalf("index = %d %q", response.Code, response.Body.String())
	}

	valid := url.Values{
		locations.FieldName:         {"Atelier Sud"},
		locations.FieldSIRET:        {"12345678901234"},
		locations.FieldAddressLine1: {"1 rue du Sud"},
		locations.FieldPostalCode:   {"97200"},
		locations.FieldCity:         {"Fort-de-France"},
		locations.FieldCountry:      {"FR"},
		locations.FieldTimezone:     {"America/Martinique"},
		locations.FieldEmail:        {"sud@example.com"},
		locations.FieldPhone:        {"+596596123456"},
		locations.FieldWebsite:      {"https://garage.example.com/sud"},
	}
	response = postLocationForm(ownerHandler, "/locations", valid)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/locations?saved=created" {
		t.Fatalf("create = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	overview, err := store.Overview(t.Context(), tenantID, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Locations) != 1 {
		t.Fatalf("created locations = %d, want 1", len(overview.Locations))
	}
	locationID := overview.Locations[0].ID

	response = postLocationForm(
		ownerHandler, "/locations/"+locationID+"/deactivate", nil,
	)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("deactivate = %d body %q", response.Code, response.Body.String())
	}
	location, _, err := store.Get(t.Context(), tenantID, ownerID, locationID)
	if err != nil {
		t.Fatal(err)
	}
	if location.Status != "inactive" {
		t.Fatalf("deactivated status = %q", location.Status)
	}

	memberHandler := locationHandler(store, locations.Principal{
		UserID: memberID, TenantID: tenantID,
	})
	response = postLocationForm(memberHandler, "/locations", valid)
	if response.Code != http.StatusForbidden {
		t.Fatalf("member create = %d, want 403", response.Code)
	}
	response = getLocationPage(memberHandler, "/locations")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "lecture seule") {
		t.Fatalf("member index = %d %q", response.Code, response.Body.String())
	}
}

func TestLocationHTTPValidationDoesNotWrite(t *testing.T) {
	database := dbtest.Open(t)
	ownerID := createUser(t, database, "invalid-handler-owner@example.com")
	tenantID := createTenant(t, database, ownerID)
	store := locations.NewStore(database)
	handler := locationHandler(store, locations.Principal{
		UserID: ownerID, TenantID: tenantID,
	})

	response := postLocationForm(handler, "/locations", url.Values{
		locations.FieldName:     {""},
		locations.FieldCountry:  {"France"},
		locations.FieldTimezone: {"Mars/Olympus_Mons"},
		locations.FieldEmail:    {"not-an-email"},
		locations.FieldPhone:    {"0596123456"},
		locations.FieldWebsite:  {"javascript:alert(1)"},
	})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid create = %d body %q", response.Code, response.Body.String())
	}
	overview, err := store.Overview(t.Context(), tenantID, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Locations) != 0 {
		t.Fatalf("invalid submission created %d locations", len(overview.Locations))
	}
}

func TestLocationScheduleHTTPFlow(t *testing.T) {
	fixtures, runtime := dbtest.OpenRuntime(t)
	ownerID := createUser(t, fixtures, "schedule-handler-owner@example.com")
	memberID := createUser(t, fixtures, "schedule-handler-member@example.com")
	tenantID := createTenant(t, fixtures, ownerID)
	addMembership(t, fixtures, tenantID, memberID, "member")
	store := locations.NewStore(runtime)
	location, err := store.Create(t.Context(), tenantID, ownerID, locations.Input{
		Name: "Atelier Planning", CountryCode: "FR", Timezone: "America/Martinique",
	})
	if err != nil {
		t.Fatal(err)
	}
	var serviceID string
	if err := fixtures.QueryRow(t.Context(), `
		INSERT INTO service_offerings (
		    tenant_id, location_id, code, name, duration_minutes
		) VALUES ($1, $2, 'revision', 'Révision', 60)
		RETURNING id::text`, tenantID, location.ID).Scan(&serviceID); err != nil {
		t.Fatal(err)
	}
	var catalogItemID string
	if err := fixtures.QueryRow(t.Context(), `
		INSERT INTO catalog_items (
		    tenant_id, kind, reference, name, price_kind, amount_cents,
		    tax_basis, vat_basis_points, duration_minutes, location_scope,
		    created_by_user_id, updated_by_user_id
		) VALUES ($1, 'service', 'FREINS-01', 'Contrôle des freins', 'fixed', 4900,
		          'incl', 2000, 30, 'all', $2, $2)
		RETURNING id::text`, tenantID, ownerID).Scan(&catalogItemID); err != nil {
		t.Fatal(err)
	}
	ownerHandler := locationHandler(store, locations.Principal{UserID: ownerID, TenantID: tenantID})
	base := "/locations/" + location.ID + "/schedule"

	response := getLocationPage(ownerHandler, base)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Horaires hebdomadaires") {
		t.Fatalf("schedule page = %d %q", response.Code, response.Body.String())
	}
	response = postLocationForm(ownerHandler, base+"/hours", url.Values{
		locations.FieldWeekday: {"1"}, locations.FieldOpensAt: {"08:00"}, locations.FieldClosesAt: {"12:00"},
	})
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "hours-added") {
		t.Fatalf("add hour = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	response = postLocationForm(ownerHandler, base+"/resources", url.Values{
		locations.FieldResourceKind: {"technician"}, locations.FieldResourceName: {"Alice"},
	})
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "resource-added") {
		t.Fatalf("add resource = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	response = postLocationForm(ownerHandler, base+"/requirements", url.Values{
		locations.FieldRequirementService: {serviceID}, locations.FieldRequirementKind: {"technician"},
		locations.FieldRequirementQuantity: {"1"},
	})
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "requirement-saved") {
		t.Fatalf("add requirement = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	response = postLocationForm(ownerHandler, base+"/services", url.Values{
		locations.FieldCatalogItem: {catalogItemID},
	})
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "catalog-service-linked") {
		t.Fatalf("link catalog service = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	response = postLocationForm(ownerHandler, base+"/closures", url.Values{
		locations.FieldClosureStartDate: {"2030-08-12"}, locations.FieldClosureStartTime: {"10:00"},
		locations.FieldClosureEndDate: {"2030-08-12"}, locations.FieldClosureEndTime: {"12:00"},
		locations.FieldClosureReason: {"Réunion d'équipe"},
	})
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "closure-added") {
		t.Fatalf("add closure = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	response = getLocationPage(ownerHandler, base)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "08:00–12:00") ||
		!strings.Contains(response.Body.String(), "Réunion d&#39;équipe") ||
		!strings.Contains(response.Body.String(), "Alice") || !strings.Contains(response.Body.String(), "1 × Technicien") ||
		!strings.Contains(response.Body.String(), "Contrôle des freins") || !strings.Contains(response.Body.String(), "49,00 € TTC") {
		t.Fatalf("configured schedule = %d %q", response.Code, response.Body.String())
	}
	var linkedServiceID string
	if err := fixtures.QueryRow(t.Context(), `
		SELECT id::text FROM service_offerings
		WHERE tenant_id = $1 AND location_id = $2 AND catalog_item_id = $3`,
		tenantID, location.ID, catalogItemID,
	).Scan(&linkedServiceID); err != nil {
		t.Fatal(err)
	}
	response = postLocationForm(ownerHandler, base+"/services/"+linkedServiceID+"/active", url.Values{
		locations.FieldCatalogServiceActive: {"false"},
	})
	if response.Code != http.StatusSeeOther || !strings.Contains(response.Header().Get("Location"), "catalog-service-updated") {
		t.Fatalf("disable catalog service = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	response = postLocationForm(ownerHandler, base+"/hours", url.Values{
		locations.FieldWeekday: {"1"}, locations.FieldOpensAt: {"18:00"}, locations.FieldClosesAt: {"08:00"},
	})
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "après l’ouverture") {
		t.Fatalf("invalid hour = %d %q", response.Code, response.Body.String())
	}
	memberHandler := locationHandler(store, locations.Principal{UserID: memberID, TenantID: tenantID})
	response = postLocationForm(memberHandler, base+"/hours", url.Values{
		locations.FieldWeekday: {"2"}, locations.FieldOpensAt: {"08:00"}, locations.FieldClosesAt: {"12:00"},
	})
	if response.Code != http.StatusForbidden {
		t.Fatalf("member add hour = %d body %q", response.Code, response.Body.String())
	}
	response = postLocationForm(memberHandler, base+"/resources", url.Values{
		locations.FieldResourceKind: {"bay"}, locations.FieldResourceName: {"Pont interdit"},
	})
	if response.Code != http.StatusForbidden {
		t.Fatalf("member add resource HTTP = %d body %q", response.Code, response.Body.String())
	}
	response = postLocationForm(memberHandler, base+"/services", url.Values{
		locations.FieldCatalogItem: {catalogItemID},
	})
	if response.Code != http.StatusForbidden {
		t.Fatalf("member link catalog service HTTP = %d body %q", response.Code, response.Body.String())
	}
}

func locationHandler(store *locations.Store, principal locations.Principal) http.Handler {
	return locationHandlerWithCalendar(store, principal, locations.CalendarConfig{})
}

func locationHandlerWithCalendar(
	store *locations.Store, principal locations.Principal, calendar locations.CalendarConfig,
) http.Handler {
	mux := http.NewServeMux()
	locations.Register(
		mux,
		store,
		func(next http.Handler) http.Handler { return next },
		func(context.Context) (locations.Principal, bool) { return principal, true },
		calendar,
	)
	return mux
}

func TestCalendarRoutesAndUI(t *testing.T) {
	database := dbtest.Open(t)
	ownerID := createUser(t, database, "calendar-handler-owner@example.com")
	tenantID := createTenant(t, database, ownerID)
	store := locations.NewStore(database)
	created, err := store.Create(t.Context(), tenantID, ownerID, locations.Input{
		Name: "Atelier Calendrier", CountryCode: "FR", Timezone: "America/Martinique",
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := locations.Principal{UserID: ownerID, TenantID: tenantID}

	// Feature disabled: no calendar section, routes 404.
	disabledHandler := locationHandler(store, principal)
	response := getLocationPage(disabledHandler, "/locations/"+created.ID)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "Google Calendar") {
		t.Fatalf("edit page with calendar disabled = %d %q", response.Code, response.Body.String())
	}
	if response = getLocationPage(disabledHandler, "/locations/"+created.ID+"/calendar/connect"); response.Code != http.StatusNotFound {
		t.Fatalf("connect disabled = %d", response.Code)
	}
	if response = getLocationPage(disabledHandler, "/oauth/google-calendar/callback"); response.Code != http.StatusNotFound {
		t.Fatalf("callback disabled = %d", response.Code)
	}

	// Feature enabled: not-connected copy shows, connect redirects to Google.
	secretStore := newTestSecretStore(t)
	enabledHandler := locationHandlerWithCalendar(store, principal, locations.CalendarConfig{
		Enabled: true,
		OAuth:   oauth2.Config{ClientID: "test-client", Endpoint: endpoints.Google},
		Secrets: secretStore,
	})
	response = getLocationPage(enabledHandler, "/locations/"+created.ID)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Connecter Google Calendar") {
		t.Fatalf("edit page with calendar enabled = %d %q", response.Code, response.Body.String())
	}
	response = getLocationPage(enabledHandler, "/locations/"+created.ID+"/calendar/connect")
	if response.Code != http.StatusFound || !strings.Contains(response.Header().Get("Location"), "accounts.google.com") {
		t.Fatalf("connect = %d location %q", response.Code, response.Header().Get("Location"))
	}

	// Seed a connection directly through the store (the OAuth exchange itself
	// needs a live Google account, out of reach here) and check the edit page
	// reflects it, then disconnect end to end through the handler.
	if err := store.ConnectCalendar(
		t.Context(), tenantID, ownerID, created.ID, secretStore, "refresh-token", "owner@gmail.com",
	); err != nil {
		t.Fatal(err)
	}
	response = getLocationPage(enabledHandler, "/locations/"+created.ID)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "owner@gmail.com") {
		t.Fatalf("edit page connected = %d %q", response.Code, response.Body.String())
	}
	response = postLocationForm(enabledHandler, "/locations/"+created.ID+"/calendar/disconnect", nil)
	wantRedirect := "/locations/" + created.ID + "?calendar=disconnected"
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != wantRedirect {
		t.Fatalf("disconnect = %d location %q", response.Code, response.Header().Get("Location"))
	}
	response = getLocationPage(enabledHandler, wantRedirect)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "déconnecté") {
		t.Fatalf("edit page after disconnect notice = %d %q", response.Code, response.Body.String())
	}
}

func getLocationPage(handler http.Handler, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func postLocationForm(
	handler http.Handler,
	target string,
	values url.Values,
) *httptest.ResponseRecorder {
	encoded := ""
	if values != nil {
		encoded = values.Encode()
	}
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(encoded))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
