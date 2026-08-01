package customers

import (
	"bytes"
	"context"
	"html"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

// render returns the page as plain text: templ escapes the apostrophes French
// copy is full of, so assertions would otherwise miss them.
func render(t *testing.T, page templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	if err := page.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return html.UnescapeString(buf.String())
}

func mustContain(t *testing.T, page string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}

func mustNotContain(t *testing.T, page string, unwanted ...string) {
	t.Helper()
	for _, bad := range unwanted {
		if strings.Contains(page, bad) {
			t.Errorf("page should not contain %q", bad)
		}
	}
}

// Nothing asked yet is not the same as nothing found, and the screen must not
// let the two look alike.
func TestEmptyStateBeforeAnySearch(t *testing.T) {
	page := render(t, Index(Page{Organization: "Garage Central"}))
	mustContain(t, page, "Clients", "Trouvez un client", `name="q"`, "Rechercher")
	mustNotContain(t, page, "Aucun résultat", "clients trouvés")
}

func TestNoResultsExplainsSharing(t *testing.T) {
	page := render(t, Index(Page{Organization: "Garage Central", Query: "ZZ-999-ZZ"}))
	mustContain(t, page,
		"Aucun résultat pour « ZZ-999-ZZ »",
		// A customer owned by another site is the likeliest reason for a miss.
		"n'apparaît que s'il vous a été partagé",
	)
	// The query stays in the field so the search can be corrected, not retyped.
	mustContain(t, page, `value="ZZ-999-ZZ"`)
}

func TestResultsShowWhatAGarageNeeds(t *testing.T) {
	page := render(t, Index(Page{
		Organization: "Garage Central", Query: "dupont",
		Customers: []Customer{{
			ID: "c1", FirstName: "Claire", LastName: "Dupont",
			Phone: "+33472000000", Email: "claire@example.fr",
			Vehicles:         []Vehicle{{Plate: "AB-123-CD", Model: "Renault Clio"}},
			HomeLocationName: "Atelier Gerland",
		}},
	}))
	mustContain(t, page,
		"1 client trouvé", "Claire Dupont",
		"+33472000000", "claire@example.fr",
		"AB-123-CD · Renault Clio",
		"Atelier Gerland",
		`href="/customers/c1"`,
	)
	mustNotContain(t, page, "Partagé avec vous")
}

func TestSharedCustomerIsMarked(t *testing.T) {
	page := render(t, Index(Page{
		Organization: "Garage Central", Query: "dupont",
		Customers: []Customer{{
			ID: "c1", LastName: "Dupont", HomeLocationName: "Atelier Vaise", Shared: true,
		}},
	}))
	mustContain(t, page, "Partagé avec vous", "Atelier Vaise")
}

func TestResultSummaryAgreesInNumber(t *testing.T) {
	if got := ResultSummary(1); got != "1 client trouvé" {
		t.Errorf("ResultSummary(1) = %q", got)
	}
	if got := ResultSummary(3); got != "3 clients trouvés" {
		t.Errorf("ResultSummary(3) = %q", got)
	}
	if got := ResultSummary(0); got != "0 clients trouvés" {
		t.Errorf("ResultSummary(0) = %q", got)
	}
}

func TestMissingContactsAndVehiclesAreStated(t *testing.T) {
	page := render(t, Index(Page{
		Organization: "Garage Central", Query: "x",
		Customers: []Customer{{ID: "c1", CompanyName: "Transports Martin"}},
	}))
	// Silence would read as a rendering bug rather than as missing data.
	mustContain(t, page, "Aucun contact enregistré", "Aucun véhicule enregistré")
}

func TestServerError(t *testing.T) {
	page := render(t, Index(Page{
		Organization: "Garage Central",
		Notice:       Notice{Kind: NoticeError, Message: "La recherche est indisponible."},
	}))
	mustContain(t, page, "Action impossible pour le moment", "La recherche est indisponible.", "alert-warning")
}

func TestCustomerLabelCoversEveryIdentityShape(t *testing.T) {
	person := Customer{FirstName: "Claire", LastName: "Dupont"}
	if person.Label() != "Claire Dupont" {
		t.Errorf("person = %q", person.Label())
	}
	company := Customer{CompanyName: "Transports Martin"}
	if company.Label() != "Transports Martin" {
		t.Errorf("company = %q", company.Label())
	}
	both := Customer{CompanyName: "Transports Martin", LastName: "Martin"}
	if both.Label() != "Transports Martin — Martin" {
		t.Errorf("both = %q", both.Label())
	}
	// The schema allows a first name alone; it must not render with a stray space.
	firstOnly := Customer{FirstName: "Claire"}
	if firstOnly.Label() != "Claire" {
		t.Errorf("first name only = %q", firstOnly.Label())
	}
}

func TestVehicleSummaryCapsLongFleets(t *testing.T) {
	fleet := []Vehicle{
		{Plate: "AA-111-AA"}, {Plate: "BB-222-BB"},
		{Plate: "CC-333-CC"}, {Plate: "DD-444-DD"}, {Plate: "EE-555-EE"},
	}
	got := vehicleSummary(fleet)
	if got != "AA-111-AA, BB-222-BB, CC-333-CC, +2" {
		t.Errorf("vehicleSummary() = %q", got)
	}
	if summary := vehicleSummary(fleet[:2]); summary != "AA-111-AA, BB-222-BB" {
		t.Errorf("short fleet = %q", summary)
	}
}

func TestVehicleLabelHandlesMissingParts(t *testing.T) {
	if got := (Vehicle{Plate: "AB-123-CD"}).Label(); got != "AB-123-CD" {
		t.Errorf("plate only = %q", got)
	}
	// A vehicle can exist before its plate is known, e.g. created from a call.
	if got := (Vehicle{Model: "Renault Clio"}).Label(); got != "Renault Clio" {
		t.Errorf("model only = %q", got)
	}
}
