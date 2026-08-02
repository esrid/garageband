package customers_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/esrid/garageband/internal/features/customers"
	"github.com/esrid/garageband/internal/platform/assistanttools"
)

func TestCorrectCustomerToolAppliesChangesOnceAndIsIdempotent(t *testing.T) {
	fixture := newCustomerFixture(t)
	scope := assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.homeStaffID, LocationID: fixture.homeLocationID,
		IdempotencyKey: "correct-customer-test-1",
	}
	input := json.RawMessage(`{
		"customer_id":"` + fixture.customerID + `",
		"first_name":"Alicia","email":"alicia@example.fr"
	}`)

	preview, err := fixture.store.Preview(t.Context(), scope, customers.ToolCorrectCustomer, input)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Summary == "" {
		t.Fatal("empty correction preview summary")
	}

	result, err := fixture.store.Execute(t.Context(), scope, customers.ToolCorrectCustomer, preview.Input)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		FirstName string `json:"first_name"`
		Email     string `json:"email"`
		Phone     string `json:"phone_e164"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	// Phone wasn't part of the patch, so it must survive untouched.
	if output.FirstName != "Alicia" || output.Email != "alicia@example.fr" || output.Phone != "06 12 34 56 78" {
		t.Fatalf("corrected customer = %#v", output)
	}

	// Retrying with the same idempotency key must not touch the contact
	// history again.
	if _, err := fixture.store.Execute(t.Context(), scope, customers.ToolCorrectCustomer, preview.Input); err != nil {
		t.Fatal(err)
	}
	var emailContacts int
	if err := fixture.fixtures.QueryRow(
		t.Context(),
		`SELECT count(*) FROM customer_contacts
		 WHERE tenant_id = $1 AND customer_id = $2 AND kind = 'email'`,
		fixture.tenantID, fixture.customerID,
	).Scan(&emailContacts); err != nil {
		t.Fatal(err)
	}
	if emailContacts != 2 { // original alice@example.fr (superseded) + alicia@example.fr
		t.Fatalf("email contacts after retry = %d, want 2", emailContacts)
	}
}

func TestCorrectCustomerToolRejectsSharedAccess(t *testing.T) {
	fixture := newCustomerFixture(t)
	fixture.grant(t)
	scope := assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.receivingStaffID, LocationID: fixture.receivingLocation,
	}
	_, err := fixture.store.Preview(t.Context(), scope, customers.ToolCorrectCustomer, json.RawMessage(`{
		"customer_id":"`+fixture.customerID+`","first_name":"Forgé"
	}`))
	var toolErr *assistanttools.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "forbidden" {
		t.Fatalf("shared-access correction = %v, want forbidden", err)
	}
}

func TestCorrectCustomerToolRejectsAnInvalidPhone(t *testing.T) {
	fixture := newCustomerFixture(t)
	scope := assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.homeStaffID, LocationID: fixture.homeLocationID,
	}
	preview, err := fixture.store.Preview(t.Context(), scope, customers.ToolCorrectCustomer, json.RawMessage(`{
		"customer_id":"`+fixture.customerID+`","phone_e164":"abc"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.store.Execute(t.Context(), scope, customers.ToolCorrectCustomer, preview.Input)
	var toolErr *assistanttools.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "invalid_arguments" {
		t.Fatalf("invalid phone = %v, want invalid_arguments", err)
	}
}

func TestCorrectVehicleToolAppliesChangesOnceAndIsIdempotent(t *testing.T) {
	fixture := newCustomerFixture(t)
	scope := assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.homeStaffID, LocationID: fixture.homeLocationID,
		IdempotencyKey: "correct-vehicle-test-1",
	}
	input := json.RawMessage(`{"vehicle_id":"` + fixture.vehicleID + `","registration_plate":"bb-456-bb"}`)

	preview, err := fixture.store.Preview(t.Context(), scope, customers.ToolCorrectVehicle, input)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Summary == "" {
		t.Fatal("empty vehicle correction preview summary")
	}
	result, err := fixture.store.Execute(t.Context(), scope, customers.ToolCorrectVehicle, preview.Input)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Plate string `json:"registration_plate"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.Plate != "BB-456-BB" {
		t.Fatalf("corrected plate = %q, want BB-456-BB", output.Plate)
	}

	retried, err := fixture.store.Execute(t.Context(), scope, customers.ToolCorrectVehicle, preview.Input)
	if err != nil {
		t.Fatal(err)
	}
	var retriedOutput struct {
		Plate string `json:"registration_plate"`
	}
	if err := json.Unmarshal(retried.Output, &retriedOutput); err != nil {
		t.Fatal(err)
	}
	if retriedOutput.Plate != output.Plate {
		t.Fatalf("retry changed plate: first %q, retry %q", output.Plate, retriedOutput.Plate)
	}
}

func TestCorrectVehicleToolDetectsAStaleProposal(t *testing.T) {
	fixture := newCustomerFixture(t)
	scope := assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.homeStaffID, LocationID: fixture.homeLocationID,
		IdempotencyKey: "correct-vehicle-stale-test",
	}
	preview, err := fixture.store.Preview(t.Context(), scope, customers.ToolCorrectVehicle, json.RawMessage(`{
		"vehicle_id":"`+fixture.vehicleID+`","model":"Clio V"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	// A direct write between preview and confirm changes updated_at, so the
	// prepared proposal's expected_updated_at no longer matches — the same
	// UPDATE ... WHERE updated_at = $expected guard applyVehicleCorrection
	// itself uses, so this must bump updated_at explicitly too.
	if _, err := fixture.fixtures.Exec(
		t.Context(), `UPDATE vehicles SET model = 'Clio IV', updated_at = now() WHERE id = $1`,
		fixture.vehicleID,
	); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.store.Execute(t.Context(), scope, customers.ToolCorrectVehicle, preview.Input)
	var toolErr *assistanttools.ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "conflict" {
		t.Fatalf("stale vehicle correction = %v, want conflict", err)
	}
}

func TestCustomerToolsRejectUnknownNames(t *testing.T) {
	fixture := newCustomerFixture(t)
	scope := assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.homeStaffID, LocationID: fixture.homeLocationID,
	}
	if _, err := fixture.store.Preview(t.Context(), scope, "not_a_tool", json.RawMessage(`{}`)); !errors.Is(err, assistanttools.ErrUnknownTool) {
		t.Fatalf("unknown tool preview = %v, want ErrUnknownTool", err)
	}
	if _, err := fixture.store.Execute(t.Context(), scope, "not_a_tool", json.RawMessage(`{}`)); !errors.Is(err, assistanttools.ErrUnknownTool) {
		t.Fatalf("unknown tool execute = %v, want ErrUnknownTool", err)
	}
}
