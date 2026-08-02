package agenda_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/esrid/garageband/internal/features/agenda"
	"github.com/esrid/garageband/internal/platform/assistanttools"
)

func TestCheckAvailabilityToolReusesTheBookingRules(t *testing.T) {
	fixture := newAgendaFixture(t)
	mustExec(t, fixture.fixtures, `
		INSERT INTO location_opening_hours (tenant_id, location_id, weekday, opens_at, closes_at)
		VALUES ($1, $2, 3, '08:00', '14:00')`, fixture.tenantID, fixture.locationID)
	mustExec(t, fixture.fixtures, `
		INSERT INTO service_resource_requirements (
		    tenant_id, location_id, service_id, resource_kind, quantity
		) VALUES ($1, $2, $3, 'bay', 1)`,
		fixture.tenantID, fixture.locationID, fixture.serviceID)
	scope := assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.userID, LocationID: fixture.locationID,
	}
	input := json.RawMessage(`{"service_id":"` + fixture.serviceID + `","date":"2026-08-12"}`)

	preview, err := fixture.store.Preview(t.Context(), scope, agenda.ToolCheckAvailability, input)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Summary == "" {
		t.Fatal("empty availability preview summary")
	}

	result, err := fixture.store.Execute(t.Context(), scope, agenda.ToolCheckAvailability, input)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Date          string `json:"date"`
		AutoAllocated bool   `json:"auto_allocated"`
		Slots         []struct {
			StartsAt string `json:"starts_at"`
		} `json:"slots"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.Date != "2026-08-12" || !output.AutoAllocated || len(output.Slots) == 0 {
		t.Fatalf("availability output = %#v", output)
	}

	if _, err := fixture.store.Preview(
		t.Context(), scope, agenda.ToolCheckAvailability,
		json.RawMessage(`{"service_id":"`+fixture.serviceID+`","date":"not-a-date"}`),
	); !isToolInputError(err) {
		t.Fatalf("invalid date = %v, want a ToolError", err)
	}
}

func TestCheckAvailabilityToolNamesItsScopeBoundaryForManualResources(t *testing.T) {
	fixture := newAgendaFixture(t)
	mustExec(t, fixture.fixtures, `
		INSERT INTO location_opening_hours (tenant_id, location_id, weekday, opens_at, closes_at)
		VALUES ($1, $2, 3, '08:00', '14:00')`, fixture.tenantID, fixture.locationID)
	scope := assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.userID, LocationID: fixture.locationID,
	}
	// The fixture's service has no service_resource_requirements row, so it
	// is not auto-allocated and needs a resource id this tool cannot supply.
	_, err := fixture.store.Execute(
		t.Context(), scope, agenda.ToolCheckAvailability,
		json.RawMessage(`{"service_id":"`+fixture.serviceID+`","date":"2026-08-12"}`),
	)
	var toolErr *assistanttools.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "unsupported" {
		t.Fatalf("manual-resource service = %v, want an unsupported ToolError", err)
	}
}

func TestFindAppointmentsToolListsWhatTheAgendaBooked(t *testing.T) {
	fixture := newAgendaFixture(t)
	if _, err := fixture.store.Save(
		t.Context(), fixture.tenantID, fixture.userID, "", fixture.input("2026-08-12", "09:00"),
	); err != nil {
		t.Fatal(err)
	}
	scope := assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.userID, LocationID: fixture.locationID,
	}
	input := json.RawMessage(`{"date":"2026-08-12"}`)

	result, err := fixture.store.Execute(t.Context(), scope, agenda.ToolFindAppointments, input)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Date         string `json:"date"`
		Appointments []struct {
			CustomerName string `json:"customer_name"`
			Status       string `json:"status"`
		} `json:"appointments"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Appointments) != 1 || output.Appointments[0].CustomerName != "Alice Martin" {
		t.Fatalf("found appointments = %#v", output.Appointments)
	}
	if len(result.AffectedRecords) != 1 || result.AffectedRecords[0].Kind != "appointment" {
		t.Fatalf("affected records = %#v", result.AffectedRecords)
	}

	empty := json.RawMessage(`{"date":"2030-01-01"}`)
	result, err = fixture.store.Execute(t.Context(), scope, agenda.ToolFindAppointments, empty)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Appointments) != 0 {
		t.Fatalf("empty day appointments = %#v", output.Appointments)
	}
}

func TestAgendaToolsRejectUnknownNames(t *testing.T) {
	fixture := newAgendaFixture(t)
	scope := assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.userID, LocationID: fixture.locationID,
	}
	if _, err := fixture.store.Preview(t.Context(), scope, "not_a_tool", json.RawMessage(`{}`)); !errors.Is(err, assistanttools.ErrUnknownTool) {
		t.Fatalf("unknown tool preview = %v, want ErrUnknownTool", err)
	}
	if _, err := fixture.store.Execute(t.Context(), scope, "not_a_tool", json.RawMessage(`{}`)); !errors.Is(err, assistanttools.ErrUnknownTool) {
		t.Fatalf("unknown tool execute = %v, want ErrUnknownTool", err)
	}
}

func TestBookAppointmentToolCreatesOnceAndIsIdempotent(t *testing.T) {
	fixture := newAgendaFixture(t)
	autoAllocate(t, fixture)
	scope := assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.userID, LocationID: fixture.locationID,
		IdempotencyKey: "book-test-1",
	}
	input := json.RawMessage(`{
		"customer_id":"` + fixture.customerID + `","vehicle_id":"` + fixture.vehicleID + `",
		"service_id":"` + fixture.serviceID + `","date":"2026-08-12","start_time":"09:00"
	}`)

	preview, err := fixture.store.Preview(t.Context(), scope, agenda.ToolBookAppointment, input)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Summary == "" {
		t.Fatal("empty book preview summary")
	}

	result, err := fixture.store.Execute(t.Context(), scope, agenda.ToolBookAppointment, input)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		ID   string `json:"id"`
		Date string `json:"date"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.ID == "" || output.Date != "2026-08-12" {
		t.Fatalf("book output = %#v", output)
	}

	// A retried Execute with the same idempotency key must not book twice.
	retried, err := fixture.store.Execute(t.Context(), scope, agenda.ToolBookAppointment, input)
	if err != nil {
		t.Fatal(err)
	}
	var retriedOutput struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(retried.Output, &retriedOutput); err != nil {
		t.Fatal(err)
	}
	if retriedOutput.ID != output.ID {
		t.Fatalf("retry booked again: first %q, retry %q", output.ID, retriedOutput.ID)
	}
	var count int
	if err := fixture.fixtures.QueryRow(
		t.Context(), `SELECT count(*) FROM appointments WHERE tenant_id = $1`, fixture.tenantID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("appointments after retry = %d, want 1", count)
	}
}

func TestBookAppointmentToolRejectsAnUnknownVehicle(t *testing.T) {
	fixture := newAgendaFixture(t)
	autoAllocate(t, fixture)
	otherCustomerID := insertReturningID(t, fixture.fixtures, `
		INSERT INTO customers (tenant_id, home_location_id, first_name, last_name)
		VALUES ($1, $2, 'Bob', 'Autre') RETURNING id::text`,
		fixture.tenantID, fixture.locationID)
	scope := assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.userID, LocationID: fixture.locationID,
	}
	_, err := fixture.store.Preview(t.Context(), scope, agenda.ToolBookAppointment, json.RawMessage(`{
		"customer_id":"`+otherCustomerID+`","vehicle_id":"`+fixture.vehicleID+`",
		"service_id":"`+fixture.serviceID+`","date":"2026-08-12","start_time":"09:00"
	}`))
	if !isToolInputError(err) {
		t.Fatalf("vehicle belonging to a different customer = %v, want invalid_arguments", err)
	}
}

func TestRescheduleAppointmentToolMovesTheSameBooking(t *testing.T) {
	fixture := newAgendaFixture(t)
	autoAllocate(t, fixture)
	date, err := fixture.store.Save(
		t.Context(), fixture.tenantID, fixture.userID, "", fixture.input("2026-08-12", "09:00"),
	)
	if err != nil {
		t.Fatal(err)
	}
	appointmentID := onlyAppointmentID(t, fixture, date)
	scope := assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.userID, LocationID: fixture.locationID,
		IdempotencyKey: "reschedule-test-1",
	}
	input := json.RawMessage(`{"appointment_id":"` + appointmentID + `","date":"2026-08-12","start_time":"11:00"}`)

	preview, err := fixture.store.Preview(t.Context(), scope, agenda.ToolRescheduleAppointment, input)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Summary == "" {
		t.Fatal("empty reschedule preview summary")
	}
	result, err := fixture.store.Execute(t.Context(), scope, agenda.ToolRescheduleAppointment, input)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.ID != appointmentID {
		t.Fatalf("rescheduled id = %q, want %q", output.ID, appointmentID)
	}
	var startsAt string
	if err := fixture.fixtures.QueryRow(
		t.Context(),
		`SELECT to_char(starts_at AT TIME ZONE 'America/Martinique', 'HH24:MI')
		 FROM appointments WHERE id = $1`, appointmentID,
	).Scan(&startsAt); err != nil {
		t.Fatal(err)
	}
	if startsAt != "11:00" {
		t.Fatalf("stored local start time = %q, want 11:00", startsAt)
	}
}

func TestCancelAppointmentToolIsIdempotent(t *testing.T) {
	fixture := newAgendaFixture(t)
	autoAllocate(t, fixture)
	date, err := fixture.store.Save(
		t.Context(), fixture.tenantID, fixture.userID, "", fixture.input("2026-08-12", "09:00"),
	)
	if err != nil {
		t.Fatal(err)
	}
	appointmentID := onlyAppointmentID(t, fixture, date)
	scope := assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.userID, LocationID: fixture.locationID,
		IdempotencyKey: "cancel-test-1",
	}
	input := json.RawMessage(`{"appointment_id":"` + appointmentID + `"}`)

	preview, err := fixture.store.Preview(t.Context(), scope, agenda.ToolCancelAppointment, input)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Summary == "" {
		t.Fatal("empty cancel preview summary")
	}
	if _, err := fixture.store.Execute(t.Context(), scope, agenda.ToolCancelAppointment, input); err != nil {
		t.Fatal(err)
	}
	// Retrying with the same key must not error even though the appointment
	// is already cancelled.
	if _, err := fixture.store.Execute(t.Context(), scope, agenda.ToolCancelAppointment, input); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := fixture.fixtures.QueryRow(
		t.Context(), `SELECT status FROM appointments WHERE id = $1`, appointmentID,
	).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", status)
	}
}

// autoAllocate gives the fixture's service a resource requirement and
// opening hours, so booking/rescheduling doesn't hit the manual-resource
// scope boundary this test isn't exercising.
func autoAllocate(t *testing.T, fixture agendaFixture) {
	t.Helper()
	mustExec(t, fixture.fixtures, `
		INSERT INTO location_opening_hours (tenant_id, location_id, weekday, opens_at, closes_at)
		VALUES ($1, $2, 3, '08:00', '14:00')`, fixture.tenantID, fixture.locationID)
	mustExec(t, fixture.fixtures, `
		INSERT INTO service_resource_requirements (
		    tenant_id, location_id, service_id, resource_kind, quantity
		) VALUES ($1, $2, $3, 'bay', 1)`,
		fixture.tenantID, fixture.locationID, fixture.serviceID)
}

func onlyAppointmentID(t *testing.T, fixture agendaFixture, date string) string {
	t.Helper()
	var id string
	if err := fixture.fixtures.QueryRow(
		t.Context(), `SELECT id::text FROM appointments WHERE tenant_id = $1 AND starts_at::date = $2::date`,
		fixture.tenantID, date,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func isToolInputError(err error) bool {
	var toolErr *assistanttools.ToolError
	return errors.As(err, &toolErr) && toolErr.Code == "invalid_arguments"
}
