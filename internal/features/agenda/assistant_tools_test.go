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

func isToolInputError(err error) bool {
	var toolErr *assistanttools.ToolError
	return errors.As(err, &toolErr) && toolErr.Code == "invalid_arguments"
}
