package customers

import (
	"bytes"
	"context"
	"html"
	"strings"
	"testing"
	"time"

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

func profileFixture() Profile {
	return Profile{
		Organization: "Garage Central",
		Customer: Customer{
			ID: "c1", FirstName: "Claire", LastName: "Dupont",
			Phone: "+33472000000", Email: "claire@example.fr",
			HomeLocationName: "Atelier Gerland",
		},
		CanEdit: true,
		Vehicles: []ProfileVehicle{{
			ID: "v1", Plate: "AB-123-CD", Make: "Renault", Model: "Clio",
			Year: 2019, VIN: "VF1RJA00012345678",
		}},
		Timeline: []Event{{
			ID: "r1", Kind: EventRepair, At: time.Date(2026, 3, 12, 9, 0, 0, 0, time.UTC),
			Title: "Remplacement plaquettes avant", VehicleLabel: "AB-123-CD",
			Status: "completed", LocationName: "Atelier Gerland", AuthoredHere: true,
			AmountCents: 148050, Currency: "EUR",
		}},
	}
}

func TestProfileShowsIdentityVehiclesAndHistory(t *testing.T) {
	page := render(t, Show(profileFixture()))
	mustContain(t, page,
		"Claire Dupont", "+33472000000", "Atelier Gerland",
		"Véhicules", "AB-123-CD", "Renault Clio · 2019", "VF1RJA00012345678",
		"Historique", "Remplacement plaquettes avant",
		"Réparation", "Terminée", "12 mars 2026",
		`href="/customers"`,
	)
	// The dossier is ours, so no shared-read banner.
	mustNotContain(t, page, "Dossier partagé, en lecture", "Partagé avec vous")
}

// The sharing rule only means something if the screen says what you may touch.
func TestProfileOfASharedDossierExplainsTheLimits(t *testing.T) {
	profile := profileFixture()
	profile.CanEdit = false
	profile.Customer.Shared = true
	profile.Customer.HomeLocationName = "Atelier Vaise"
	profile.Timeline[0].AuthoredHere = false
	profile.Timeline[0].LocationName = "Atelier Vaise"
	page := render(t, Show(profile))
	mustContain(t, page,
		"Partagé avec vous", "Dossier partagé, en lecture",
		"vous pouvez y ajouter votre propre travail",
		"Atelier Vaise", "Autre site, en lecture",
	)
}

func TestProfileEmptySections(t *testing.T) {
	page := render(t, Show(Profile{
		Organization: "Garage Central", CanEdit: true,
		Customer: Customer{ID: "c1", LastName: "Dupont"},
	}))
	mustContain(t, page,
		"Aucun véhicule enregistré pour ce client.",
		"Aucun rendez-vous ni réparation pour l'instant.",
	)
	// The memories section disappears entirely rather than showing a stub.
	mustNotContain(t, page, "Ce que l'agent a retenu")
}

func TestProfileMemoriesAreShownWithTheirStanding(t *testing.T) {
	profile := profileFixture()
	profile.Memories = []Memory{
		{Key: "Véhicule de courtoisie", Value: "En demande systématiquement", Status: "active", Confidence: 0.92},
		{Key: "Horaires", Value: "Préfère le matin", Status: "superseded", Confidence: 0.4},
		{Key: "Adresse", Value: "Ancienne adresse", Status: "rejected"},
	}
	page := render(t, Show(profile))
	mustContain(t, page,
		"Ce que l'agent a retenu", "À vérifier avant de s'en servir",
		"Véhicule de courtoisie", "Retenu", "Confiance élevée",
		"Remplacé", "Confiance faible", "Écarté",
	)
	// A missing score must not read as "no confidence".
	if strings.Count(page, "Confiance") != 2 {
		t.Errorf("confidence labels = %d, want 2", strings.Count(page, "Confiance"))
	}
}

func TestProfileHidesSharingAndOffboardFromNonManagers(t *testing.T) {
	page := render(t, Show(profileFixture()))
	mustNotContain(t, page, "Partage entre sites", "Zone sensible", "Ce client est parti")
}

func TestProfileShowsSharingAndOffboardToManagers(t *testing.T) {
	profile := profileFixture()
	profile.CanManage = true
	profile.ShareOptions = []LocationOption{{ID: "l2", Name: "Atelier Vaise"}}
	granted := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	revoked := time.Date(2026, 2, 15, 10, 0, 0, 0, time.UTC)
	profile.Grants = []Grant{
		{ID: "g1", ReceivingLocationName: "Atelier Vaise", GrantedByName: "Sam Owner", GrantedAt: granted},
		{ID: "g2", ReceivingLocationName: "Atelier Croix-Rousse", GrantedByName: "Sam Owner", GrantedAt: granted, RevokedByName: "Sam Owner", RevokedAt: &revoked},
	}
	page := render(t, Show(profile))
	mustContain(t, page,
		"Partage entre sites", "Atelier Vaise", "Actif", "Révoqué",
		"/customers/c1/shares/g1/revoke", "/customers/c1/shares",
		"Zone sensible", "Ce client est parti", "/customers/c1/offboard",
	)
	// A revoked grant has no revoke button; an active one does.
	if strings.Contains(page, "/customers/c1/shares/g2/revoke") {
		t.Error("revoke button shown for an already-revoked grant")
	}
}

func TestFormatAmountFollowsFrenchTypography(t *testing.T) {
	// The separators are non-breaking spaces on purpose: French typography puts
	// one before the currency symbol and between thousands, and neither may wrap.
	cases := map[int]string{
		148050:  "1\u00a0480,50\u00a0€",
		999:     "9,99\u00a0€",
		100:     "1,00\u00a0€",
		5:       "0,05\u00a0€",
		1234567: "12\u00a0345,67\u00a0€",
		0:       "", // nothing to show rather than a misleading 0,00 €
	}
	for cents, want := range cases {
		if got := formatAmount(cents, "EUR"); got != want {
			t.Errorf("formatAmount(%d) = %q, want %q", cents, got, want)
		}
	}
	if got := formatAmount(1000, "CHF"); got != "10,00\u00a0CHF" {
		t.Errorf("non-euro = %q", got)
	}
}

func TestEventLabelsCoverBothKinds(t *testing.T) {
	repair := Event{Kind: EventRepair, Status: "awaiting_approval"}
	if got := eventStatusLabel(repair); got != "En attente d'accord" {
		t.Errorf("repair status = %q", got)
	}
	appointment := Event{Kind: EventAppointment, Status: "no_show"}
	if got := eventStatusLabel(appointment); got != "Non venu" {
		t.Errorf("appointment status = %q", got)
	}
	// "in_progress" exists for both and must not be mistranslated by kind.
	if eventStatusLabel(Event{Kind: EventRepair, Status: "in_progress"}) != "En cours" ||
		eventStatusLabel(Event{Kind: EventAppointment, Status: "in_progress"}) != "En cours" {
		t.Error("in_progress should read the same for both kinds")
	}
	// An unknown status shows through rather than vanishing.
	if got := eventStatusLabel(Event{Kind: EventRepair, Status: "surprise"}); got != "surprise" {
		t.Errorf("unknown status = %q", got)
	}
	// A record with no description still renders a line.
	if got := eventTitle(Event{Kind: EventRepair}); got != "Réparation" {
		t.Errorf("empty title = %q", got)
	}
}

func TestProfileVehicleLabelHandlesSparseData(t *testing.T) {
	if got := (ProfileVehicle{Make: "Renault", Model: "Clio", Year: 2019}).Label(); got != "Renault Clio · 2019" {
		t.Errorf("full = %q", got)
	}
	if got := (ProfileVehicle{Make: "Renault"}).Label(); got != "Renault" {
		t.Errorf("make only = %q", got)
	}
	// A vehicle created from a plate lookup can have nothing else yet.
	if got := (ProfileVehicle{Plate: "AB-123-CD"}).Label(); got != "Véhicule sans description" {
		t.Errorf("plate only = %q", got)
	}
}
