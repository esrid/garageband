package onboarding_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/features/onboarding"
	"github.com/esrid/garageband/internal/platform/businesslookup"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
)

type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake-registry" }

func (fakeProvider) Enrich(_ context.Context, request businesslookup.Request) (businesslookup.Profile, error) {
	return businesslookup.Profile{
		SIREN:       request.SIRET[:9],
		SIRET:       request.SIRET,
		LegalName:   "GARAGE EXEMPLE SAS",
		TradingName: "Garage Exemple",
		Address: businesslookup.Address{
			Line1:       "12 rue des Mécaniciens",
			PostalCode:  "97200",
			City:        "Fort-de-France",
			CountryCode: "FR",
		},
		Raw: []byte(`{"source":"test"}`),
	}, nil
}

func TestOnboardingCreatesGarageAtomicallyAndIsIdempotent(t *testing.T) {
	database := dbtest.Open(t)
	userID := createUser(t, database, "owner@example.com")
	handler := setup(database, userID)

	lookup := postForm(handler, "/onboarding/lookup", url.Values{
		"siret": {"12345678901234"},
	})
	if lookup.Code != http.StatusOK {
		t.Fatalf("lookup status = %d, body = %q", lookup.Code, lookup.Body.String())
	}
	draftID := hiddenDraftID(t, lookup.Body.String())

	confirmation := url.Values{
		"draft_id":      {draftID},
		"name":          {"Garage Exemple"},
		"legal_name":    {"GARAGE EXEMPLE SAS"},
		"slug":          {"garage-exemple"},
		"siret":         {"12345678901234"},
		"location_name": {"Atelier principal"},
		"address_line1": {"12 rue des Mécaniciens"},
		"postal_code":   {"97200"},
		"city":          {"Fort-de-France"},
		"country_code":  {"FR"},
		"website_url":   {"https://garage.example"},
	}
	confirmed := postForm(handler, "/onboarding/confirm", confirmation)
	if confirmed.Code != http.StatusSeeOther || confirmed.Header().Get("Location") != "/?onboarded=1" {
		t.Fatalf("confirm status = %d, location = %q, body = %q", confirmed.Code, confirmed.Header().Get("Location"), confirmed.Body.String())
	}

	var tenantID string
	if err := database.QueryRow(t.Context(), `
		SELECT tenant_id
		FROM onboarding_drafts
		WHERE id = $1 AND user_id = $2 AND status = 'completed'`,
		draftID, userID,
	).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	assertGarageRows(t, database, tenantID, userID)

	// Browser retries and double submits return the original tenant rather than
	// creating a second workspace.
	retried := postForm(handler, "/onboarding/confirm", confirmation)
	if retried.Code != http.StatusSeeOther {
		t.Fatalf("retry status = %d, body = %q", retried.Code, retried.Body.String())
	}
	assertGarageRows(t, database, tenantID, userID)
}

func TestOnboardingDraftCannotBeConfirmedByAnotherUser(t *testing.T) {
	database := dbtest.Open(t)
	ownerID := createUser(t, database, "owner@example.com")
	otherID := createUser(t, database, "other@example.com")

	lookup := postForm(setup(database, ownerID), "/onboarding/lookup", url.Values{
		"siret": {"12345678901234"},
	})
	draftID := hiddenDraftID(t, lookup.Body.String())
	confirmation := url.Values{
		"draft_id": {draftID}, "name": {"Garage Exemple"},
		"legal_name": {"GARAGE EXEMPLE SAS"}, "slug": {"garage-exemple"},
		"siret": {"12345678901234"}, "location_name": {"Atelier principal"},
		"country_code": {"FR"},
	}

	response := postForm(setup(database, otherID), "/onboarding/confirm", confirmation)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %q", response.Code, response.Body.String())
	}
	var status string
	if err := database.QueryRow(t.Context(), `SELECT status FROM onboarding_drafts WHERE id = $1`, draftID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "ready" {
		t.Fatalf("draft status = %q, want ready", status)
	}
}

func TestOnboardingRejectsSIRETChangedAfterLookup(t *testing.T) {
	database := dbtest.Open(t)
	userID := createUser(t, database, "owner@example.com")
	handler := setup(database, userID)
	lookup := postForm(handler, "/onboarding/lookup", url.Values{
		"siret": {"12345678901234"},
	})
	response := postForm(handler, "/onboarding/confirm", url.Values{
		"draft_id": {hiddenDraftID(t, lookup.Body.String())},
		"name":     {"Garage Exemple"}, "legal_name": {"GARAGE EXEMPLE SAS"},
		"slug": {"garage-exemple"}, "siret": {"98765432109876"},
		"location_name": {"Atelier principal"}, "country_code": {"FR"},
	})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "lancez une nouvelle recherche") {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestOnboardingValidatesSIRETBeforeProviderCall(t *testing.T) {
	database := dbtest.Open(t)
	userID := createUser(t, database, "owner@example.com")
	response := postForm(setup(database, userID), "/onboarding/lookup", url.Values{
		"siret": {"123"},
	})
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "14 chiffres exactement") {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

func setup(database *db.DB, userID string) http.Handler {
	mux := http.NewServeMux()
	onboarding.Register(
		mux,
		onboarding.NewStore(database),
		fakeProvider{},
		func(next http.Handler) http.Handler { return next },
		func(context.Context) (string, bool) { return userID, true },
		func(context.Context, string) error { return nil },
	)
	return mux
}

func postForm(handler http.Handler, target string, values url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func hiddenDraftID(t *testing.T, body string) string {
	t.Helper()
	match := regexp.MustCompile(`name="draft_id" value="([^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("draft id not found in %q", body)
	}
	return match[1]
}

func createUser(t *testing.T, database *db.DB, email string) string {
	t.Helper()
	var userID string
	if err := database.QueryRow(t.Context(), `
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', $1, $1, 'Test User')
		RETURNING id`, email,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return userID
}

func assertGarageRows(t *testing.T, database *db.DB, tenantID, userID string) {
	t.Helper()
	err := database.WithinTenant(context.Background(), tenantID, func(tx pgx.Tx) error {
		checks := []struct {
			query string
			args  []any
		}{
			{`SELECT count(*) FROM tenants WHERE id = $1`, []any{tenantID}},
			{`SELECT count(*) FROM tenant_memberships WHERE tenant_id = $1 AND user_id = $2 AND role = 'owner'`, []any{tenantID, userID}},
			{`SELECT count(*) FROM locations WHERE tenant_id = $1 AND siret = '12345678901234'`, []any{tenantID}},
			{`SELECT count(*) FROM business_enrichment_runs WHERE tenant_id = $1 AND status = 'succeeded'`, []any{tenantID}},
		}
		for _, check := range checks {
			var count int
			if err := tx.QueryRow(context.Background(), check.query, check.args...).Scan(&count); err != nil {
				return err
			}
			if count != 1 {
				return fmt.Errorf("unexpected row count %d for %s", count, check.query)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
