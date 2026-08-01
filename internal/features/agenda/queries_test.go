package agenda_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/esrid/garageband/internal/features/agenda"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
)

type agendaFixture struct {
	fixtures         *db.DB
	store            *agenda.Store
	tenantID         string
	userID           string
	locationID       string
	customerID       string
	vehicleID        string
	serviceID        string
	resourceID       string
	secondResourceID string
}

func TestAgendaUsesLocationTimezoneAndReportsResourceConflict(t *testing.T) {
	fixture := newAgendaFixture(t)
	input := fixture.input("2026-08-12", "09:00")
	date, err := fixture.store.Save(
		t.Context(), fixture.tenantID, fixture.userID, "", input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if date != "2026-08-12" {
		t.Fatalf("saved date = %q", date)
	}

	page, err := fixture.store.Day(
		t.Context(), fixture.tenantID, fixture.userID,
		fixture.locationID, "2026-08-12",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Appointments) != 1 {
		t.Fatalf("appointments = %d", len(page.Appointments))
	}
	appointment := page.Appointments[0]
	if appointment.StartsAt.Format("15:04 -07:00") != "09:00 -04:00" ||
		appointment.EndsAt.Format("15:04") != "10:15" {
		t.Fatalf("local interval = %s to %s", appointment.StartsAt, appointment.EndsAt)
	}

	_, err = fixture.store.Save(
		t.Context(), fixture.tenantID, fixture.userID, "",
		fixture.input("2026-08-12", "09:30"),
	)
	var conflict *agenda.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("overlapping save = %v, want ConflictError", err)
	}
	if conflict.Resource != "Pont principal" {
		t.Fatalf("conflicting resource = %q", conflict.Resource)
	}

	date, locationID, err := fixture.store.Cancel(
		t.Context(), fixture.tenantID, fixture.userID, appointment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if date != "2026-08-12" || locationID != fixture.locationID {
		t.Fatalf("cancel redirect = %q %q", date, locationID)
	}
	page, err = fixture.store.Day(
		t.Context(), fixture.tenantID, fixture.userID,
		fixture.locationID, "2026-08-12",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Appointments) != 1 || page.Appointments[0].Status != "cancelled" ||
		page.Booked() != 0 {
		t.Fatalf("cancelled day = %#v", page)
	}
}

func TestAppointmentReservesEverySelectedResource(t *testing.T) {
	fixture := newAgendaFixture(t)
	input := fixture.input("2026-08-12", "09:00")
	input.ResourceIDs = []string{fixture.resourceID, fixture.secondResourceID}
	input.ResourceID = fixture.resourceID
	if _, err := fixture.store.Save(t.Context(), fixture.tenantID, fixture.userID, "", input); err != nil {
		t.Fatal(err)
	}
	var reservationCount int
	if err := fixture.fixtures.QueryRow(`
		SELECT count(*) FROM appointment_resource_reservations
		WHERE tenant_id = $1`, fixture.tenantID).Scan(&reservationCount); err != nil {
		t.Fatal(err)
	}
	if reservationCount != 2 {
		t.Fatalf("reservations = %d, want 2", reservationCount)
	}
	if _, err := fixture.fixtures.Exec(`
		UPDATE appointment_resource_reservations
		SET ends_at = ends_at + interval '15 minutes'
		WHERE tenant_id = $1 AND resource_id = $2`,
		fixture.tenantID, fixture.secondResourceID); err == nil {
		t.Fatal("reservation diverging from its appointment unexpectedly accepted")
	}
	day, err := fixture.store.Day(t.Context(), fixture.tenantID, fixture.userID, fixture.locationID, "2026-08-12")
	if err != nil {
		t.Fatal(err)
	}
	if len(day.Appointments) != 1 || !strings.Contains(day.Appointments[0].ResourceName, "Pont principal") ||
		!strings.Contains(day.Appointments[0].ResourceName, "Valise diagnostic") {
		t.Fatalf("multi-resource day = %#v", day.Appointments)
	}
	form, err := fixture.store.Form(t.Context(), fixture.tenantID, fixture.userID, day.Appointments[0].ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(form.Values.ResourceIDs) != 2 {
		t.Fatalf("selected resources = %#v", form.Values.ResourceIDs)
	}
	mustExec(t, fixture.fixtures, `
		INSERT INTO location_opening_hours (tenant_id, location_id, weekday, opens_at, closes_at)
		VALUES ($1, $2, 3, '08:00', '14:00')`, fixture.tenantID, fixture.locationID)
	availability, err := fixture.store.Availability(
		t.Context(), fixture.tenantID, fixture.userID, fixture.locationID,
		fixture.serviceID, []string{fixture.secondResourceID}, "2026-08-12",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(availability.Slots) == 0 || availability.Slots[0].StartsAt.Format("15:04") != "10:15" {
		t.Fatalf("secondary-resource availability = %#v", availability.Slots)
	}
	conflicting := fixture.input("2026-08-12", "09:30")
	conflicting.ResourceID = fixture.secondResourceID
	conflicting.ResourceIDs = []string{fixture.secondResourceID}
	_, err = fixture.store.Save(t.Context(), fixture.tenantID, fixture.userID, "", conflicting)
	var conflict *agenda.ConflictError
	if !errors.As(err, &conflict) || !strings.Contains(conflict.Resource, "Valise diagnostic") {
		t.Fatalf("secondary-resource conflict = %#v, err %v", conflict, err)
	}
	if _, _, err := fixture.store.Cancel(t.Context(), fixture.tenantID, fixture.userID, day.Appointments[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Save(t.Context(), fixture.tenantID, fixture.userID, "", conflicting); err != nil {
		t.Fatalf("resource stayed occupied after cancellation: %v", err)
	}
}

func TestAgendaHTTPConflictIsNotAValidationError(t *testing.T) {
	fixture := newAgendaFixture(t)
	if _, err := fixture.store.Save(
		t.Context(), fixture.tenantID, fixture.userID, "",
		fixture.input("2026-08-12", "09:00"),
	); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	agenda.Register(
		mux, fixture.store,
		func(next http.Handler) http.Handler { return next },
		func(_ context.Context) (agenda.Principal, bool) {
			return agenda.Principal{UserID: fixture.userID, TenantID: fixture.tenantID}, true
		},
	)
	form := url.Values{
		agenda.FieldLocation:  {fixture.locationID},
		agenda.FieldCustomer:  {fixture.customerID},
		agenda.FieldVehicle:   {fixture.vehicleID},
		agenda.FieldService:   {fixture.serviceID},
		agenda.FieldResource:  {fixture.resourceID},
		agenda.FieldDate:      {"2026-08-12"},
		agenda.FieldStartTime: {"09:30"},
	}
	request := httptest.NewRequest(http.MethodPost, "/agenda", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Ce créneau est déjà pris") ||
		strings.Contains(response.Body.String(), "Vérifiez les informations") {
		t.Fatalf("conflict body = %q", response.Body.String())
	}
}

func TestAvailabilityUsesOpeningHoursClosuresAndExistingBookings(t *testing.T) {
	fixture := newAgendaFixture(t)
	mustExec(t, fixture.fixtures, `
		INSERT INTO location_opening_hours (tenant_id, location_id, weekday, opens_at, closes_at)
		VALUES ($1, $2, 3, '08:00', '14:00')`, fixture.tenantID, fixture.locationID)
	mustExec(t, fixture.fixtures, `
		INSERT INTO location_closures (
		    tenant_id, location_id, starts_at, ends_at, reason, created_by_user_id
		) VALUES ($1, $2, '2026-08-12 14:00:00+00', '2026-08-12 15:00:00+00', 'Réunion', $3)`,
		fixture.tenantID, fixture.locationID, fixture.userID)
	if _, err := fixture.store.Save(
		t.Context(), fixture.tenantID, fixture.userID, "",
		fixture.input("2026-08-12", "08:00"),
	); err != nil {
		t.Fatal(err)
	}
	availability, err := fixture.store.Availability(
		t.Context(), fixture.tenantID, fixture.userID, fixture.locationID,
		fixture.serviceID, []string{fixture.resourceID}, "2026-08-12",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !availability.ScheduleConfigured || !availability.OpenThisDay || len(availability.Slots) == 0 {
		t.Fatalf("availability = %#v", availability)
	}
	for _, slot := range availability.Slots {
		if slot.StartsAt.Format("15:04") < "11:00" {
			t.Fatalf("busy or closed slot leaked: %#v", slot)
		}
	}
	_, err = fixture.store.Save(
		t.Context(), fixture.tenantID, fixture.userID, "",
		fixture.input("2026-08-12", "07:00"),
	)
	var fieldError *agenda.FieldError
	if !errors.As(err, &fieldError) || fieldError.Field != agenda.FieldStartTime ||
		!strings.Contains(fieldError.Message, "horaires") {
		t.Fatalf("outside hours = %#v err %v", fieldError, err)
	}
	_, err = fixture.store.Save(
		t.Context(), fixture.tenantID, fixture.userID, "",
		fixture.input("2026-08-12", "10:30"),
	)
	if !errors.As(err, &fieldError) || !strings.Contains(fieldError.Message, "fermé") {
		t.Fatalf("closure booking = %#v err %v", fieldError, err)
	}
	_, err = fixture.store.Save(
		t.Context(), fixture.tenantID, fixture.userID, "",
		fixture.input("2026-08-13", "08:00"),
	)
	if !errors.As(err, &fieldError) || !strings.Contains(fieldError.Message, "horaires") {
		t.Fatalf("closed weekday = %#v err %v", fieldError, err)
	}
	if _, err := fixture.fixtures.Exec(`
		INSERT INTO location_closures (
		    tenant_id, location_id, starts_at, ends_at, reason, created_by_user_id
		) VALUES ($1, $2, '2026-08-12 12:30:00+00', '2026-08-12 13:00:00+00', 'Chevauchement', $3)`,
		fixture.tenantID, fixture.locationID, fixture.userID); err == nil {
		t.Fatal("closure overlapping an active appointment unexpectedly accepted")
	}
	if _, err := fixture.fixtures.Exec(`
		DELETE FROM location_opening_hours
		WHERE tenant_id = $1 AND location_id = $2 AND weekday = 3 AND opens_at = '08:00'`,
		fixture.tenantID, fixture.locationID); err == nil {
		t.Fatal("opening window containing an active appointment unexpectedly deleted")
	}
	if _, err := fixture.fixtures.Exec(`
		UPDATE locations SET timezone = 'Europe/Paris'
		WHERE tenant_id = $1 AND id = $2`, fixture.tenantID, fixture.locationID); err == nil {
		t.Fatal("timezone with scheduling history unexpectedly changed")
	}
	if _, err := fixture.fixtures.Exec(`
		INSERT INTO location_opening_hours (tenant_id, location_id, weekday, opens_at, closes_at)
		VALUES ($1, $2, 3, '13:00', '15:00')`, fixture.tenantID, fixture.locationID); err == nil {
		t.Fatal("overlapping opening hours unexpectedly accepted")
	}
}

func TestAvailabilityHTTPDoesNotBookWhileSearching(t *testing.T) {
	fixture := newAgendaFixture(t)
	mustExec(t, fixture.fixtures, `
		INSERT INTO location_opening_hours (tenant_id, location_id, weekday, opens_at, closes_at)
		VALUES ($1, $2, 3, '08:00', '14:00')`, fixture.tenantID, fixture.locationID)
	mux := http.NewServeMux()
	agenda.Register(
		mux, fixture.store,
		func(next http.Handler) http.Handler { return next },
		func(_ context.Context) (agenda.Principal, bool) {
			return agenda.Principal{UserID: fixture.userID, TenantID: fixture.tenantID}, true
		},
	)
	form := url.Values{
		agenda.FieldLocation: {fixture.locationID}, agenda.FieldCustomer: {fixture.customerID},
		agenda.FieldVehicle: {fixture.vehicleID}, agenda.FieldService: {fixture.serviceID},
		agenda.FieldResource: {fixture.resourceID, fixture.secondResourceID}, agenda.FieldDate: {"2026-08-12"},
	}
	request := httptest.NewRequest(http.MethodPost, "/agenda/availability", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Créneaux disponibles") ||
		!strings.Contains(response.Body.String(), "08:00–09:15") {
		t.Fatalf("availability HTTP = %d %q", response.Code, response.Body.String())
	}
	var appointments int
	if err := fixture.fixtures.QueryRow(`SELECT count(*) FROM appointments WHERE tenant_id = $1`, fixture.tenantID).Scan(&appointments); err != nil {
		t.Fatal(err)
	}
	if appointments != 0 {
		t.Fatalf("availability search created %d appointments", appointments)
	}
}

func (fixture agendaFixture) input(date string, start string) agenda.SaveInput {
	return agenda.SaveInput{
		LocationID: fixture.locationID,
		CustomerID: fixture.customerID,
		VehicleID:  fixture.vehicleID,
		ServiceID:  fixture.serviceID,
		ResourceID: fixture.resourceID, ResourceIDs: []string{fixture.resourceID},
		Date:      date,
		StartTime: start,
		Note:      "Client attendra sur place",
	}
}

func newAgendaFixture(t *testing.T) agendaFixture {
	t.Helper()
	fixtures, runtime := dbtest.OpenRuntime(t)
	fixture := agendaFixture{fixtures: fixtures, store: agenda.NewStore(runtime)}
	fixture.userID = insertReturningID(t, fixtures, `
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', 'agenda-owner', 'agenda@example.com', 'Agenda Owner')
		RETURNING id::text`)
	fixture.tenantID = insertReturningID(t, fixtures, `
		INSERT INTO tenants (slug, name)
		VALUES ('agenda-garage', 'Garage Agenda') RETURNING id::text`)
	mustExec(t, fixtures, `
		INSERT INTO tenant_memberships (tenant_id, user_id, role)
		VALUES ($1, $2, 'owner')`, fixture.tenantID, fixture.userID)
	fixture.locationID = insertReturningID(t, fixtures, `
		INSERT INTO locations (tenant_id, slug, name, timezone)
		VALUES ($1, 'martinique', 'Atelier Martinique', 'America/Martinique')
		RETURNING id::text`, fixture.tenantID)
	fixture.customerID = insertReturningID(t, fixtures, `
		INSERT INTO customers (tenant_id, home_location_id, first_name, last_name)
		VALUES ($1, $2, 'Alice', 'Martin') RETURNING id::text`,
		fixture.tenantID, fixture.locationID)
	fixture.vehicleID = insertReturningID(t, fixtures, `
		INSERT INTO vehicles (
		    tenant_id, customer_id, location_id, registration_plate, make, model
		)
		VALUES ($1, $2, $3, 'AA-123-AA', 'Renault', 'Clio') RETURNING id::text`,
		fixture.tenantID, fixture.customerID, fixture.locationID)
	fixture.serviceID = insertReturningID(t, fixtures, `
		INSERT INTO service_offerings (
		    tenant_id, location_id, code, name, duration_minutes,
		    buffer_before_minutes, buffer_after_minutes
		) VALUES ($1, $2, 'revision', 'Révision', 60, 5, 10)
		RETURNING id::text`, fixture.tenantID, fixture.locationID)
	fixture.resourceID = insertReturningID(t, fixtures, `
		INSERT INTO bookable_resources (tenant_id, location_id, kind, name)
		VALUES ($1, $2, 'bay', 'Pont principal') RETURNING id::text`,
		fixture.tenantID, fixture.locationID)
	fixture.secondResourceID = insertReturningID(t, fixtures, `
		INSERT INTO bookable_resources (tenant_id, location_id, kind, name)
		VALUES ($1, $2, 'equipment', 'Valise diagnostic') RETURNING id::text`,
		fixture.tenantID, fixture.locationID)
	return fixture
}

func insertReturningID(t *testing.T, database *db.DB, query string, args ...any) string {
	t.Helper()
	var id string
	if err := database.QueryRow(query, args...).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustExec(t *testing.T, database *db.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
