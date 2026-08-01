package agenda

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

func at(hour, minute int) time.Time {
	return time.Date(2026, 3, 12, hour, minute, 0, 0, time.UTC)
}

func dayFixture() Day {
	return Day{
		Organization: "Garage Central", LocationName: "Atelier Gerland",
		Date: at(0, 0), CanManage: true,
		Appointments: []Appointment{
			{
				ID: "a1", StartsAt: at(9, 0), EndsAt: at(10, 30),
				CustomerID: "c1", CustomerName: "Claire Dupont",
				VehicleLabel: "AB-123-CD", ServiceName: "Révision annuelle",
				ResourceName: "Pont 1", Status: "confirmed", Source: "agent",
				Note: "Bruit au freinage",
			},
			{
				ID: "a2", StartsAt: at(11, 0), EndsAt: at(11, 45),
				CustomerID: "c2", CustomerName: "Transports Martin",
				VehicleLabel: "AA-111-AA", ServiceName: "Vidange",
				ResourceName: "Pont 2", Status: "pending", Source: "dashboard",
			},
		},
	}
}

func TestDayShowsTheSchedule(t *testing.T) {
	page := render(t, Show(dayFixture()))
	mustContain(t, page,
		"jeudi 12 mars 2026", "Atelier Gerland", "2 rendez-vous",
		"09:00 – 10:30", "Claire Dupont", "Révision annuelle", "AB-123-CD",
		"Pont 1", "Confirmé", "Bruit au freinage",
		// Knowing the agent booked it, rather than a colleague, matters at the desk.
		"Pris par l'agent",
		"11:00 – 11:45", "À confirmer",
		`href="/customers/c1"`, `href="/agenda/a1"`,
		"Nouveau rendez-vous",
	)
	// A booking typed in the dashboard needs no provenance label.
	if strings.Count(page, "Pris par l'agent") != 1 {
		t.Error("only the agent-booked appointment should carry a source label")
	}
}

func TestDayNavigationCrossesMonthsCorrectly(t *testing.T) {
	day := dayFixture()
	day.Date = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if got := day.PreviousDate(); got != "2026-02-28" {
		t.Errorf("PreviousDate() = %q, want the end of February", got)
	}
	day.Date = time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	if got := day.NextDate(); got != "2027-01-01" {
		t.Errorf("NextDate() = %q, want the new year", got)
	}
	page := render(t, Show(dayFixture()))
	mustContain(t, page, `href="/agenda?date=2026-03-11"`, `href="/agenda?date=2026-03-13"`)
}

// A cancelled slot stays on the day for the record but must not be counted as
// occupying the workshop, or the desk reads the day as fuller than it is.
func TestCancelledAppointmentsDoNotFillTheDay(t *testing.T) {
	day := dayFixture()
	day.Appointments[1].Status = "cancelled"
	if got := day.Booked(); got != 1 {
		t.Errorf("Booked() = %d, want 1", got)
	}
	page := render(t, Show(day))
	mustContain(t, page, "1 rendez-vous", "Annulé", "Transports Martin")
}

func TestEmptyDay(t *testing.T) {
	day := dayFixture()
	day.Appointments = nil
	page := render(t, Show(day))
	mustContain(t, page, "Journée libre", "Aucun rendez-vous", "apparaissent ici automatiquement")
}

func TestDayReadOnlyHidesBookingAndEditing(t *testing.T) {
	day := dayFixture()
	day.CanManage = false
	page := render(t, Show(day))
	mustContain(t, page, "Claire Dupont", "09:00 – 10:30")
	mustNotContain(t, page, "Nouveau rendez-vous", `href="/agenda/a1"`)
}

func formFixture() FormPage {
	return FormPage{
		Organization: "Garage Central", LocationName: "Atelier Gerland",
		CanManage: true,
		Customer:  CustomerRef{ID: "c1", Label: "Claire Dupont"},
		Vehicles:  []Option{{Value: "v1", Label: "AB-123-CD · Renault Clio"}},
		Services:  []Option{{Value: "s1", Label: "Révision annuelle · 1 h 30"}},
		Resources: []Option{{Value: "r1", Label: "Pont 1"}},
		Values:    FormValues{Date: "2026-03-12", StartTime: "09:00"},
	}
}

func TestFormPostsEveryFieldTheBackendParses(t *testing.T) {
	page := render(t, Form(formFixture()))
	for _, field := range []string{FieldDate, FieldStartTime, FieldVehicle, FieldService, FieldResource, FieldNote} {
		if !strings.Contains(page, `name="`+field+`"`) {
			t.Errorf("form is missing the %q control", field)
		}
	}
	mustContain(t, page,
		`action="/agenda"`, "Prendre le rendez-vous", "Claire Dupont",
		`value="2026-03-12"`, `value="09:00"`,
		"Révision annuelle · 1 h 30",
	)
	// Nothing to call off before the appointment exists.
	mustNotContain(t, page, "Annuler le rendez-vous")
}

// Booking needs somebody to book for; without one the screen sends the user to
// find a customer instead of showing a form that cannot be submitted.
func TestFormWithoutACustomerAsksForOne(t *testing.T) {
	page := render(t, Form(FormPage{Organization: "Garage Central", CanManage: true}))
	mustContain(t, page, "Choisissez d'abord un client", `href="/customers"`)
	mustNotContain(t, page, `name="service_id"`, "Prendre le rendez-vous")
}

func TestFormValidationErrorsAreTiedToTheirFields(t *testing.T) {
	form := formFixture()
	form.Notice = Notice{Kind: NoticeInvalid, Message: "Deux champs demandent une correction."}
	form.FieldErrors = map[string]string{
		FieldStartTime: "Indiquez une heure de début.",
		FieldResource:  "Choisissez la ressource occupée.",
	}
	page := render(t, Form(form))
	mustContain(t, page,
		"Vérifiez les informations ci-dessous",
		"Indiquez une heure de début.", "Choisissez la ressource occupée.",
		`id="start_time-error"`, `id="resource_id-error"`,
		`aria-describedby="start_time-error"`,
		"select-error", "input-error",
	)
	if got := strings.Count(page, `aria-invalid="true"`); got != 2 {
		t.Errorf("aria-invalid=true count = %d, want 2", got)
	}
}

// The database refuses a double booking with an exclusion constraint; the
// screen has to say which slot clashed rather than a generic failure.
func TestFormReportsASlotConflictDistinctly(t *testing.T) {
	form := formFixture()
	form.Notice = Notice{
		Kind:    NoticeConflict,
		Message: "Le Pont 1 est déjà occupé de 09:00 à 10:30. Choisissez une autre ressource ou un autre horaire.",
	}
	page := render(t, Form(form))
	mustContain(t, page, "Ce créneau est déjà pris", "Pont 1 est déjà occupé", "alert-warning")
	mustNotContain(t, page, "Vérifiez les informations ci-dessous")
}

func TestEditFormSeparatesCancellationFromSaving(t *testing.T) {
	form := formFixture()
	form.ID = "a1"
	form.Cancellable = true
	page := render(t, Form(form))
	mustContain(t, page,
		`action="/agenda/a1"`, "Enregistrer les modifications",
		"Annuler ce rendez-vous", `action="/agenda/a1/cancel"`,
		"btn-error", "Rien n'est supprimé",
	)
	// The cancel control sits outside the edit form, so saving can never fire it.
	save := page[strings.Index(page, `action="/agenda/a1"`):]
	if end := strings.Index(save, "</form>"); end >= 0 && strings.Contains(save[:end], "/cancel") {
		t.Error("cancellation must not sit inside the save form")
	}
}

func TestFinishedAppointmentOffersNoCancellation(t *testing.T) {
	form := formFixture()
	form.ID = "a1"
	form.Cancellable = false
	page := render(t, Form(form))
	mustContain(t, page, "Enregistrer les modifications")
	mustNotContain(t, page, "Annuler ce rendez-vous")
}

func TestServerError(t *testing.T) {
	day := dayFixture()
	day.Notice = Notice{Kind: NoticeError, Message: "L'agenda n'a pas pu être chargé."}
	page := render(t, Show(day))
	mustContain(t, page, "Action impossible pour le moment", "L'agenda n'a pas pu être chargé.", "alert-warning")
}

func TestBookedSummaryAgreesInNumber(t *testing.T) {
	cases := map[int]string{0: "Aucun rendez-vous", 1: "1 rendez-vous", 4: "4 rendez-vous"}
	for count, want := range cases {
		if got := bookedSummary(count); got != want {
			t.Errorf("bookedSummary(%d) = %q, want %q", count, got, want)
		}
	}
}

func TestStatusAndSourceLabels(t *testing.T) {
	if got := statusLabel("no_show"); got != "Non venu" {
		t.Errorf("no_show = %q", got)
	}
	// An unknown status shows through rather than vanishing.
	if got := statusLabel("surprise"); got != "surprise" {
		t.Errorf("unknown status = %q", got)
	}
	// A dashboard booking is the norm and needs no label at all.
	if got := sourceLabel("dashboard"); got != "" {
		t.Errorf("dashboard source = %q, want empty", got)
	}
	if got := sourceLabel("agent"); got != "Pris par l'agent" {
		t.Errorf("agent source = %q", got)
	}
}
