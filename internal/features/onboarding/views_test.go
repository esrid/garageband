package onboarding

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func renderConfirmation(t *testing.T, data formData) string {
	t.Helper()
	var buf bytes.Buffer
	if err := ConfirmationPage(data).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render confirmation page: %v", err)
	}
	return html.UnescapeString(buf.String())
}

// The confirmation form is the contract with POST /onboarding/confirm: losing a
// field here silently drops data instead of failing loudly.
func TestConfirmationPagePostsEveryContractField(t *testing.T) {
	html := renderConfirmation(t, formData{
		DraftID: "0198c0de-0000-7000-8000-000000000001", Name: "Garage Central",
		LegalName: "CENTRAL SAS", Slug: "garage-central", SIRET: "12345678900012",
		LocationName: "Main workshop", AddressLine1: "12 rue des Ateliers",
		AddressLine2: "Zone artisanale", PostalCode: "69007", City: "Lyon",
		CountryCode: "FR", WebsiteURL: "https://garage-central.fr",
	})
	for _, name := range []string{
		"draft_id", "name", "legal_name", "slug", "siret", "location_name",
		"address_line1", "address_line2", "postal_code", "city", "country_code",
		"website_url",
	} {
		if !strings.Contains(html, fmt.Sprintf(`name=%q`, name)) {
			t.Errorf("confirmation form is missing the %q field", name)
		}
	}
	for _, value := range []string{"Garage Central", "CENTRAL SAS", "Lyon", "69007"} {
		if !strings.Contains(html, value) {
			t.Errorf("imported value %q is not rendered for review", value)
		}
	}
	// The SIREN is shown for reference only; the store derives it again.
	if strings.Contains(html, `name="siren"`) {
		t.Error("the derived SIREN must not be submitted")
	}
	if !strings.Contains(html, "123456789") {
		t.Error("the derived SIREN is not displayed")
	}
}

func TestConfirmationPageShowsRecoverableStates(t *testing.T) {
	for _, testCase := range []struct {
		kind      string
		wantTitle string
		wantColor string
	}{
		{noticeExpired, "Cette recherche a expiré", "alert-warning"},
		{noticeDuplicate, "Ce garage existe déjà", "alert-error"},
		{noticeInvalid, "Vérifiez les informations ci-dessous", "alert-error"},
		{noticeMismatch, "Ce SIRET ne correspond pas à votre recherche", "alert-warning"},
	} {
		html := renderConfirmation(t, formData{Error: "boom", ErrorKind: testCase.kind})
		if !strings.Contains(html, testCase.wantTitle) {
			t.Errorf("%s: heading %q missing, so the state reads as colour only", testCase.kind, testCase.wantTitle)
		}
		if !strings.Contains(html, testCase.wantColor) {
			t.Errorf("%s: want %s", testCase.kind, testCase.wantColor)
		}
	}
	// These two are dead ends without a way back to the lookup.
	for _, kind := range []string{noticeExpired, noticeMismatch} {
		if !strings.Contains(renderConfirmation(t, formData{Error: "boom", ErrorKind: kind}), "Recommencer") {
			t.Errorf("%s must offer a way to start again", kind)
		}
	}
}

func TestLookupPageSeparatesRegistryOutageFromBadSIRET(t *testing.T) {
	for _, testCase := range []struct {
		status    int
		wantTitle string
	}{
		{http.StatusBadGateway, "Le registre ne répond pas"},
		{http.StatusUnprocessableEntity, "Nous n'avons pas pu utiliser ce SIRET"},
	} {
		var buf bytes.Buffer
		kind := noticeKindForLookupStatus(testCase.status)
		if err := LookupPage("123", "boom", kind).Render(context.Background(), &buf); err != nil {
			t.Fatalf("render lookup page: %v", err)
		}
		if !strings.Contains(html.UnescapeString(buf.String()), testCase.wantTitle) {
			t.Errorf("status %d: want heading %q", testCase.status, testCase.wantTitle)
		}
	}
}

func TestSIREN(t *testing.T) {
	if got := (formData{SIRET: "12345678900012"}).SIREN(); got != "123456789" {
		t.Errorf("SIREN = %q, want the first 9 digits", got)
	}
	if got := (formData{SIRET: "123"}).SIREN(); got != "" {
		t.Errorf("SIREN = %q, want empty for a short SIRET", got)
	}
}

func TestDuplicateMessage(t *testing.T) {
	slugTaken := &pgconn.PgError{Code: "23505", ConstraintName: "tenants_slug_key"}
	if message, ok := duplicateMessage(fmt.Errorf("insert: %w", slugTaken)); !ok ||
		!strings.Contains(message, "identifiant d'espace") {
		t.Errorf("slug conflict: got %q, ok=%v", message, ok)
	}
	otherUnique := &pgconn.PgError{Code: "23505", ConstraintName: "tenants_siren_key"}
	if message, ok := duplicateMessage(otherUnique); !ok || strings.Contains(message, "identifiant d'espace") {
		t.Errorf("business conflict: got %q, ok=%v", message, ok)
	}
	if _, ok := duplicateMessage(errors.New("connection reset")); ok {
		t.Error("a non-unique-violation must stay a logged failure")
	}
}
