package locations

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

func TestIndexWithOneActiveLocation(t *testing.T) {
	page := render(t, Index(IndexPage{
		Organization: "Garage Central",
		CanManage:    true,
		Locations: []Location{{
			ID:           "loc-1",
			Name:         "Atelier Gerland",
			AddressLine1: "12 rue des Ateliers", PostalCode: "69007", City: "Lyon",
			CountryCode: "FR", SIRET: "12345678900012", PhoneE164: "+33472000000",
			Email: "contact@garage-central.fr", Timezone: "Europe/Paris",
			Status: StatusActive,
		}},
	}))
	mustContain(t, page,
		"Sites du garage", "Garage Central", "Atelier Gerland",
		"12 rue des Ateliers", "69007 Lyon", "12345678900012",
		"+33472000000", "Europe/Paris", "Actif", "Configuration complète",
		"Ajouter un site", "Configurer", "Désactiver",
		"/locations/loc-1/deactivate",
	)
	// A site is never destroyed, so no screen may offer it.
	mustNotContain(t, page, "Supprimer", "/delete")
}

func TestIndexWithSeveralActiveLocations(t *testing.T) {
	page := render(t, Index(IndexPage{
		Organization: "Garage Central", CanManage: true,
		Locations: []Location{
			{ID: "a", Name: "Atelier Gerland", Timezone: "Europe/Paris", Status: StatusActive},
			{ID: "b", Name: "Atelier Villeurbanne", Timezone: "Europe/Paris", Status: StatusActive},
		},
	}))
	mustContain(t, page, "Atelier Gerland", "Atelier Villeurbanne", "/locations/a", "/locations/b")
	// Nothing hints that a second site is expected of everyone.
	mustContain(t, page, "Un seul site suffit")
}

func TestIndexMixesActiveAndInactiveLocations(t *testing.T) {
	page := render(t, Index(IndexPage{
		Organization: "Garage Central", CanManage: true,
		Locations: []Location{
			{ID: "a", Name: "Atelier Gerland", Timezone: "Europe/Paris", Status: StatusActive},
			{ID: "b", Name: "Atelier Vaise", Timezone: "Europe/Paris", Status: "inactive"},
		},
	}))
	mustContain(t, page,
		"Actif", "Inactif",
		"Réactiver", "/locations/b/reactivate",
		// The status is spelled out, never signalled by the badge colour alone.
		"Configuration 1 / 5", "Il manque :",
		"historique reste consultable",
	)
}

func TestIndexReadOnlyForMemberWithoutRights(t *testing.T) {
	page := render(t, Index(IndexPage{
		Organization: "Garage Central", CanManage: false,
		Locations: []Location{
			{ID: "a", Name: "Atelier Gerland", Timezone: "Europe/Paris", Status: StatusActive},
		},
	}))
	mustContain(t, page, "Atelier Gerland", "lecture seule")
	mustNotContain(t, page, "Ajouter un site", "Configurer", "Désactiver", "/deactivate")
}

func TestIndexServerError(t *testing.T) {
	page := render(t, Index(IndexPage{
		Organization: "Garage Central", CanManage: true,
		Notice: Notice{Kind: NoticeError, Message: "Les sites n'ont pas pu être chargés."},
	}))
	mustContain(t, page,
		"Action impossible pour le moment",
		"Les sites n'ont pas pu être chargés.",
		"alert-warning",
		"Aucun site pour l'instant",
	)
}

func TestFormPostsEveryFieldTheBackendParses(t *testing.T) {
	page := render(t, Form(FormPage{CanManage: true}))
	for _, field := range []string{
		FieldName, FieldSIRET, FieldAddressLine1, FieldAddressLine2,
		FieldPostalCode, FieldCity, FieldCountry, FieldTimezone,
		FieldEmail, FieldPhone, FieldWebsite,
	} {
		if !strings.Contains(page, `name="`+field+`"`) {
			t.Errorf("form is missing the %q control", field)
		}
	}
	mustContain(t, page,
		`action="/locations"`, "Ajouter un site", "Requis", "Optionnel",
		"data-pending-label", // the pending-submit affordance
	)
	// Deactivation makes no sense for a site that does not exist yet.
	mustNotContain(t, page, "Désactiver ce site")
}

func TestFormEditSeparatesDeactivationFromSaving(t *testing.T) {
	page := render(t, Form(FormPage{
		CanManage: true,
		ID:        "loc-1", Active: true,
		Values: Input{Name: "Atelier Gerland", CountryCode: "FR", Timezone: "Europe/Paris"},
	}))
	mustContain(t, page,
		`action="/locations/loc-1"`, "Enregistrer les modifications",
		"Fermer ce site", "Désactiver ce site", "/locations/loc-1/deactivate",
		"btn-error", "Rien n'est supprimé",
	)
	// The destructive control lives outside the edit form, so it can never be
	// triggered by submitting the fields.
	saveForm := page[strings.Index(page, `action="/locations/loc-1"`):]
	if end := strings.Index(saveForm, "</form>"); end >= 0 {
		if strings.Contains(saveForm[:end], "deactivate") {
			t.Error("deactivation must not sit inside the save form")
		}
	}
}

func TestFormInactiveLocationOffersReopening(t *testing.T) {
	page := render(t, Form(FormPage{
		CanManage: true,
		ID:        "loc-1",
		Values:    Input{Name: "Atelier Vaise", CountryCode: "FR", Timezone: "Europe/Paris"},
	}))
	mustContain(t, page, "Rouvrir ce site", "Réactiver ce site", "/locations/loc-1/reactivate")
	mustNotContain(t, page, "Désactiver ce site")
}

func TestFormValidationErrorsAreTiedToTheirFields(t *testing.T) {
	page := render(t, Form(FormPage{
		CanManage: true,
		Notice:    Notice{Kind: NoticeInvalid, Message: "Deux champs demandent une correction."},
		FieldErrors: map[string]string{
			FieldName:       "Donnez un nom à ce site.",
			FieldPostalCode: "Le code postal doit contenir 5 chiffres.",
		},
		ID:     "loc-1",
		Values: Input{CountryCode: "FR", Timezone: "Europe/Paris"},
	}))
	mustContain(t, page,
		"Vérifiez les informations ci-dessous",
		"Donnez un nom à ce site.",
		"Le code postal doit contenir 5 chiffres.",
		`id="name-error"`, `aria-describedby="name-hint name-error"`,
		`id="postal_code-error"`, `aria-describedby="postal_code-error"`,
		`aria-invalid="true"`,
		"input-error",
	)
	// A field without an error must not claim to be invalid.
	if strings.Count(page, `aria-invalid="true"`) != 2 {
		t.Errorf("aria-invalid=true count = %d, want 2", strings.Count(page, `aria-invalid="true"`))
	}
}

func TestFormServerError(t *testing.T) {
	page := render(t, Form(FormPage{
		CanManage: true,
		Notice:    Notice{Kind: NoticeError, Message: "Le site n'a pas pu être enregistré. Réessayez."},
		Values:    Input{CountryCode: "FR", Timezone: "Europe/Paris"},
	}))
	mustContain(t, page, "Action impossible pour le moment", "Réessayez.", "alert-warning")
}

func TestFormReadOnlyForMemberWithoutRights(t *testing.T) {
	page := render(t, Form(FormPage{
		CanManage: false,
		ID:        "loc-1",
		Values: Input{
			Name: "Atelier Gerland", SIRET: "12345678900012",
			AddressLine1: "12 rue des Ateliers", PostalCode: "69007", City: "Lyon",
			CountryCode: "FR", Timezone: "Europe/Paris",
		},
	}))
	mustContain(t, page, "lecture seule", "Atelier Gerland", "12345678900012", "Non renseigné")
	// No editable control and no lifecycle action at all.
	mustNotContain(t, page,
		`name="name"`, "Enregistrer les modifications",
		"Désactiver ce site", "Réactiver ce site",
	)
}

func TestScheduleViewSeparatesWeeklyHoursAndClosures(t *testing.T) {
	page := render(t, ScheduleView(SchedulePage{
		Organization: "Garage Central",
		Location:     Location{ID: "loc-1", Name: "Atelier Gerland", Timezone: "Europe/Paris"},
		Enabled:      true, CanManage: true,
		OpeningHours:  []OpeningHour{{Weekday: 1, OpensAt: "08:00", ClosesAt: "12:00"}, {Weekday: 1, OpensAt: "14:00", ClosesAt: "18:00"}},
		Closures:      []Closure{{ID: "closure-1", StartsAt: time.Date(2030, 8, 12, 10, 0, 0, 0, time.UTC), EndsAt: time.Date(2030, 8, 12, 12, 0, 0, 0, time.UTC), Reason: "Réunion"}},
		HourValues:    OpeningHourInput{Weekday: 1, OpensAt: "08:00", ClosesAt: "18:00"},
		ClosureValues: ClosureInput{StartsDate: "2030-08-12", StartsTime: "10:00", EndsDate: "2030-08-12", EndsTime: "12:00"},
	}))
	mustContain(t, page,
		"Horaires hebdomadaires", "Lundi", "08:00–12:00", "14:00–18:00",
		"Fermetures exceptionnelles", "Réunion", "Ajouter la plage", "Ajouter la fermeture",
		"/locations/loc-1/schedule/hours", "/locations/loc-1/schedule/closures",
	)
}

func TestAddressLinesSkipWhatIsMissing(t *testing.T) {
	full := Location{
		AddressLine1: "12 rue des Ateliers", AddressLine2: "Bâtiment C",
		PostalCode: "69007", City: "Lyon",
	}
	if got := addressLines(full); len(got) != 3 || got[1] != "Bâtiment C" || got[2] != "69007 Lyon" {
		t.Errorf("addressLines() = %#v", got)
	}
	if got := addressLines(Location{}); len(got) != 0 {
		t.Errorf("empty address = %#v, want none", got)
	}
	if got := addressLines(Location{City: "Lyon"}); len(got) != 1 || got[0] != "Lyon" {
		t.Errorf("city-only = %#v", got)
	}
}

func TestSetupOfCountsWhatMakesASiteUsable(t *testing.T) {
	ready := Location{
		AddressLine1: "12 rue des Ateliers", SIRET: "12345678900012",
		PhoneE164: "+33472000000", Email: "contact@example.fr", Timezone: "Europe/Paris",
	}
	if got := setupOf(ready); !got.Complete() {
		t.Errorf("a fully filled site should be complete, missing %v", got.Missing)
	}
	bare := setupOf(Location{Timezone: "Europe/Paris"})
	if bare.Done() != 1 || len(bare.Missing) != 4 {
		t.Errorf("bare site: done=%d missing=%v", bare.Done(), bare.Missing)
	}
}

func TestSetupCounts(t *testing.T) {
	complete := Setup{Total: 5}
	if !complete.Complete() || complete.Done() != 5 || setupCount(complete) != "Complète" {
		t.Errorf("complete setup: done=%d label=%q", complete.Done(), setupCount(complete))
	}
	partial := Setup{Total: 5, Missing: []string{FieldPhone, FieldEmail}}
	if partial.Complete() || partial.Done() != 3 || setupCount(partial) != "3 / 5" {
		t.Errorf("partial setup: done=%d label=%q", partial.Done(), setupCount(partial))
	}
	// A progress element with max="0" renders undefined, so it must never be 0.
	if setupMax(Setup{}) != "1" {
		t.Errorf("setupMax(zero) = %q, want 1", setupMax(Setup{}))
	}
}
