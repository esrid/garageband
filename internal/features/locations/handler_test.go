package locations_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
	if err := fixtures.QueryRow(`
		INSERT INTO service_offerings (
		    tenant_id, location_id, code, name, duration_minutes
		) VALUES ($1, $2, 'revision', 'Révision', 60)
		RETURNING id::text`, tenantID, location.ID).Scan(&serviceID); err != nil {
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
		!strings.Contains(response.Body.String(), "Alice") || !strings.Contains(response.Body.String(), "1 × Technicien") {
		t.Fatalf("configured schedule = %d %q", response.Code, response.Body.String())
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
}

func locationHandler(store *locations.Store, principal locations.Principal) http.Handler {
	mux := http.NewServeMux()
	locations.Register(
		mux,
		store,
		func(next http.Handler) http.Handler { return next },
		func(context.Context) (locations.Principal, bool) { return principal, true },
	)
	return mux
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
