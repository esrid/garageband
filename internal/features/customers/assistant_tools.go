package customers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/assistanttools"
	"github.com/esrid/garageband/internal/platform/db"
)

const (
	ToolSearchCustomers = "search_customers"
	ToolCorrectCustomer = "correct_customer"
	ToolCorrectVehicle  = "correct_vehicle"
)

// Correction field names, matching the JSON keys the tools accept.
const (
	FieldCustomerID  = "customer_id"
	FieldFirstName   = "first_name"
	FieldLastName    = "last_name"
	FieldCompanyName = "company_name"
	FieldEmail       = "email"
	FieldPhone       = "phone_e164"
	FieldVehicleID   = "vehicle_id"
	FieldPlate       = "registration_plate"
	FieldMake        = "make"
	FieldModel       = "model"
	FieldVIN         = "vin"
)

var searchCustomersSchema = json.RawMessage(`{
  "type":"object",
  "properties":{"query":{"type":"string","minLength":1,"maxLength":160,"description":"Customer name, company, phone, email, plate, or VIN"}},
  "required":["query"],
  "additionalProperties":false
}`)

var correctCustomerSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "customer_id": {"type": "string", "description": "Customer id to correct"},
    "first_name": {"type": "string", "description": "New first name; empty clears it"},
    "last_name": {"type": "string", "description": "New last name; empty clears it"},
    "company_name": {"type": "string", "description": "New company name; empty clears it"},
    "email": {"type": "string", "description": "New contact email; empty clears it"},
    "phone_e164": {"type": "string", "description": "New phone; may be a French local or international number, empty clears it"}
  },
  "required": ["customer_id"],
  "additionalProperties": false,
  "minProperties": 2
}`)

var correctVehicleSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "vehicle_id": {"type": "string", "description": "Vehicle id to correct"},
    "registration_plate": {"type": "string", "description": "New plate; empty clears it"},
    "make": {"type": "string", "description": "New make; empty clears it"},
    "model": {"type": "string", "description": "New model; empty clears it"},
    "vin": {"type": "string", "description": "New 17-character VIN; empty clears it"}
  },
  "required": ["vehicle_id"],
  "additionalProperties": false,
  "minProperties": 2
}`)

type searchCustomersInput struct {
	Query string `json:"query"`
}

type customerCorrectionPatch struct {
	FirstName   *string `json:"first_name,omitempty"`
	LastName    *string `json:"last_name,omitempty"`
	CompanyName *string `json:"company_name,omitempty"`
	Email       *string `json:"email,omitempty"`
	PhoneE164   *string `json:"phone_e164,omitempty"`
}

type correctCustomerInput struct {
	CustomerID string `json:"customer_id"`
	customerCorrectionPatch
}

type preparedCustomerCorrection struct {
	CustomerID string `json:"customer_id"`
	customerCorrectionPatch
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
}

type customerCorrectionState struct {
	ID          string    `json:"id"`
	FirstName   string    `json:"first_name"`
	LastName    string    `json:"last_name"`
	CompanyName string    `json:"company_name"`
	Email       string    `json:"email"`
	PhoneE164   string    `json:"phone_e164"`
	CanCorrect  bool      `json:"-"`
	UpdatedAt   time.Time `json:"-"`
}

func (s customerCorrectionState) label() string {
	name := strings.TrimSpace(strings.TrimSpace(s.FirstName) + " " + strings.TrimSpace(s.LastName))
	switch {
	case name != "":
		return name
	case s.CompanyName != "":
		return s.CompanyName
	default:
		return "ce client"
	}
}

type vehicleCorrectionPatch struct {
	Plate *string `json:"registration_plate,omitempty"`
	Make  *string `json:"make,omitempty"`
	Model *string `json:"model,omitempty"`
	VIN   *string `json:"vin,omitempty"`
}

type correctVehicleInput struct {
	VehicleID string `json:"vehicle_id"`
	vehicleCorrectionPatch
}

type preparedVehicleCorrection struct {
	VehicleID string `json:"vehicle_id"`
	vehicleCorrectionPatch
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
}

type vehicleCorrectionState struct {
	ID         string    `json:"id"`
	Plate      string    `json:"registration_plate"`
	Make       string    `json:"make"`
	Model      string    `json:"model"`
	VIN        string    `json:"vin"`
	CanCorrect bool      `json:"-"`
	UpdatedAt  time.Time `json:"-"`
}

func (s vehicleCorrectionState) label() string {
	switch {
	case s.Plate != "":
		return s.Plate
	case s.VIN != "":
		return s.VIN
	default:
		return "ce véhicule"
	}
}

func (s *Store) Definitions() []assistanttools.Definition {
	return []assistanttools.Definition{
		{
			Name:        ToolSearchCustomers,
			Description: "Find customer and vehicle records visible at the scoped location.",
			InputSchema: searchCustomersSchema, Consequence: assistanttools.ConsequenceRead,
		},
		{
			Name:                 ToolCorrectCustomer,
			Description:          "Propose correcting a customer's name, company name, email, or phone.",
			InputSchema:          correctCustomerSchema,
			Consequence:          assistanttools.ConsequenceWrite,
			ConfirmationRequired: true,
		},
		{
			Name:                 ToolCorrectVehicle,
			Description:          "Propose correcting a vehicle's plate, make, model, or VIN.",
			InputSchema:          correctVehicleSchema,
			Consequence:          assistanttools.ConsequenceWrite,
			ConfirmationRequired: true,
		},
	}
}

func (s *Store) Preview(
	ctx context.Context,
	scope assistanttools.Scope,
	name string,
	input json.RawMessage,
) (assistanttools.Preview, error) {
	switch name {
	case ToolSearchCustomers:
		parsed, canonical, err := parseCustomerToolInput(input)
		if err != nil {
			return assistanttools.Preview{}, err
		}
		if _, err := s.searchAtLocation(ctx, scope, parsed.Query); err != nil {
			return assistanttools.Preview{}, err
		}
		return assistanttools.Preview{Summary: "Rechercher un client ou véhicule : « " + parsed.Query + " ».", Input: canonical}, nil
	case ToolCorrectCustomer:
		parsed, err := parseCorrectCustomerInput(input)
		if err != nil {
			return assistanttools.Preview{}, err
		}
		current, err := s.loadCustomerCorrectionState(ctx, scope, parsed.CustomerID)
		if err != nil {
			return assistanttools.Preview{}, mapCustomerWriteError(err)
		}
		if !current.CanCorrect {
			return assistanttools.Preview{}, &assistanttools.ToolError{
				Code: "forbidden", Field: FieldCustomerID,
				Message: "Ce client appartient à un autre site ; seul le site d’origine peut corriger ses informations.",
			}
		}
		canonical, err := json.Marshal(preparedCustomerCorrection{
			CustomerID: parsed.CustomerID, customerCorrectionPatch: parsed.customerCorrectionPatch,
			ExpectedUpdatedAt: current.UpdatedAt,
		})
		if err != nil {
			return assistanttools.Preview{}, err
		}
		return assistanttools.Preview{
			Summary: correctCustomerSummary(current, parsed.customerCorrectionPatch), Input: canonical,
			AffectedRecords: []assistanttools.AffectedRecord{{Kind: "customer", ID: parsed.CustomerID}},
		}, nil
	case ToolCorrectVehicle:
		parsed, err := parseCorrectVehicleInput(input)
		if err != nil {
			return assistanttools.Preview{}, err
		}
		current, err := s.loadVehicleCorrectionState(ctx, scope, parsed.VehicleID)
		if err != nil {
			return assistanttools.Preview{}, mapCustomerWriteError(err)
		}
		if !current.CanCorrect {
			return assistanttools.Preview{}, &assistanttools.ToolError{
				Code: "forbidden", Field: FieldVehicleID,
				Message: "Ce véhicule appartient à un client d’un autre site ; seul le site d’origine peut le corriger.",
			}
		}
		canonical, err := json.Marshal(preparedVehicleCorrection{
			VehicleID: parsed.VehicleID, vehicleCorrectionPatch: parsed.vehicleCorrectionPatch,
			ExpectedUpdatedAt: current.UpdatedAt,
		})
		if err != nil {
			return assistanttools.Preview{}, err
		}
		return assistanttools.Preview{
			Summary: correctVehicleSummary(current, parsed.vehicleCorrectionPatch), Input: canonical,
			AffectedRecords: []assistanttools.AffectedRecord{{Kind: "vehicle", ID: parsed.VehicleID}},
		}, nil
	default:
		return assistanttools.Preview{}, assistanttools.ErrUnknownTool
	}
}

func (s *Store) Execute(
	ctx context.Context,
	scope assistanttools.Scope,
	name string,
	input json.RawMessage,
) (assistanttools.Result, error) {
	switch name {
	case ToolSearchCustomers:
		parsed, _, err := parseCustomerToolInput(input)
		if err != nil {
			return assistanttools.Result{}, err
		}
		customers, err := s.searchAtLocation(ctx, scope, parsed.Query)
		if err != nil {
			return assistanttools.Result{}, err
		}
		output, err := json.Marshal(map[string]any{"query": parsed.Query, "customers": customers})
		if err != nil {
			return assistanttools.Result{}, err
		}
		affected := make([]assistanttools.AffectedRecord, 0, len(customers))
		for _, customer := range customers {
			affected = append(affected, assistanttools.AffectedRecord{Kind: "customer", ID: customer.ID})
		}
		return assistanttools.Result{Summary: customerToolAnswer(parsed.Query, customers), Output: output, AffectedRecords: affected}, nil
	case ToolCorrectCustomer:
		var prepared preparedCustomerCorrection
		if err := strictUnmarshal(input, &prepared); err != nil {
			return assistanttools.Result{}, correctionInputError(FieldCustomerID, err)
		}
		output, affected, err := s.withReceipt(ctx, scope, ToolCorrectCustomer, func(tx pgx.Tx) (json.RawMessage, []assistanttools.AffectedRecord, error) {
			return s.applyCustomerCorrection(ctx, tx, scope.TenantID, prepared)
		})
		if err != nil {
			return assistanttools.Result{}, err
		}
		return assistanttools.Result{
			Summary: "Fiche client mise à jour.", Output: output, AffectedRecords: affected,
		}, nil
	case ToolCorrectVehicle:
		var prepared preparedVehicleCorrection
		if err := strictUnmarshal(input, &prepared); err != nil {
			return assistanttools.Result{}, correctionInputError(FieldVehicleID, err)
		}
		output, affected, err := s.withReceipt(ctx, scope, ToolCorrectVehicle, func(tx pgx.Tx) (json.RawMessage, []assistanttools.AffectedRecord, error) {
			return s.applyVehicleCorrection(ctx, tx, scope.TenantID, prepared)
		})
		if err != nil {
			return assistanttools.Result{}, err
		}
		return assistanttools.Result{
			Summary: "Fiche véhicule mise à jour.", Output: output, AffectedRecords: affected,
		}, nil
	default:
		return assistanttools.Result{}, assistanttools.ErrUnknownTool
	}
}

// withReceipt runs perform inside the same transaction as the idempotency
// receipt check-and-record, so a retried Execute call (network blip, model
// retry) never applies the same correction twice.
func (s *Store) withReceipt(
	ctx context.Context,
	scope assistanttools.Scope,
	name string,
	perform func(tx pgx.Tx) (json.RawMessage, []assistanttools.AffectedRecord, error),
) (output json.RawMessage, affected []assistanttools.AffectedRecord, err error) {
	err = s.db.WithinTenantUser(ctx, scope.TenantID, scope.UserID, func(tx pgx.Tx) error {
		var receiptOutput, receiptAffected []byte
		err := tx.QueryRow(ctx, `
			SELECT output, affected_records
			FROM application_tool_receipts
			WHERE tenant_id = $1 AND location_id = $2 AND idempotency_key = $3
			  AND tool_name = $4`, scope.TenantID, scope.LocationID,
			scope.IdempotencyKey, name,
		).Scan(&receiptOutput, &receiptAffected)
		if err == nil {
			output = receiptOutput
			return json.Unmarshal(receiptAffected, &affected)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		output, affected, err = perform(tx)
		if err != nil {
			return err
		}
		affectedJSON, err := json.Marshal(affected)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO application_tool_receipts (
			    tenant_id, location_id, idempotency_key, tool_name, output, affected_records
			) VALUES ($1, $2, $3, $4, $5, $6)`,
			scope.TenantID, scope.LocationID, scope.IdempotencyKey, name, output, affectedJSON)
		return err
	})
	return output, affected, err
}

const customerCorrectionSelect = `
	SELECT customer.id, COALESCE(customer.first_name, ''), COALESCE(customer.last_name, ''),
	       COALESCE(customer.company_name, ''),
	       COALESCE((
	           SELECT contact.value FROM customer_contacts contact
	           WHERE contact.tenant_id = customer.tenant_id AND contact.customer_id = customer.id
	             AND contact.kind = 'email' AND contact.is_primary AND contact.deleted_at IS NULL
	           ORDER BY contact.created_at, contact.id LIMIT 1
	       ), ''),
	       COALESCE((
	           SELECT contact.value FROM customer_contacts contact
	           WHERE contact.tenant_id = customer.tenant_id AND contact.customer_id = customer.id
	             AND contact.kind = 'phone' AND contact.is_primary AND contact.deleted_at IS NULL
	           ORDER BY contact.created_at, contact.id LIMIT 1
	       ), ''),
	       app_current_user_can_access_location(customer.home_location_id),
	       customer.updated_at
	FROM customers customer
	WHERE customer.tenant_id = $1 AND customer.id = $2 AND customer.deleted_at IS NULL`

func scanCustomerCorrectionState(row interface{ Scan(...any) error }, state *customerCorrectionState) error {
	return row.Scan(
		&state.ID, &state.FirstName, &state.LastName, &state.CompanyName,
		&state.Email, &state.PhoneE164, &state.CanCorrect, &state.UpdatedAt,
	)
}

func (s *Store) loadCustomerCorrectionState(
	ctx context.Context, scope assistanttools.Scope, customerID string,
) (state customerCorrectionState, err error) {
	err = s.db.WithinTenantUser(ctx, scope.TenantID, scope.UserID, func(tx pgx.Tx) error {
		return scanCustomerCorrectionState(
			tx.QueryRow(ctx, customerCorrectionSelect, scope.TenantID, customerID), &state,
		)
	})
	return state, err
}

func (s *Store) applyCustomerCorrection(
	ctx context.Context, tx pgx.Tx, tenantID string, prepared preparedCustomerCorrection,
) (json.RawMessage, []assistanttools.AffectedRecord, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE customers SET
		    first_name = CASE WHEN $3 THEN NULLIF(btrim($4), '') ELSE first_name END,
		    last_name = CASE WHEN $5 THEN NULLIF(btrim($6), '') ELSE last_name END,
		    company_name = CASE WHEN $7 THEN NULLIF(btrim($8), '') ELSE company_name END,
		    updated_at = now()
		WHERE tenant_id = $1 AND id = $2 AND updated_at = $9 AND deleted_at IS NULL`,
		tenantID, prepared.CustomerID,
		prepared.FirstName != nil, stringValue(prepared.FirstName),
		prepared.LastName != nil, stringValue(prepared.LastName),
		prepared.CompanyName != nil, stringValue(prepared.CompanyName),
		prepared.ExpectedUpdatedAt,
	)
	if err != nil {
		return nil, nil, mapCustomerWriteError(err)
	}
	if tag.RowsAffected() != 1 {
		return nil, nil, mapCustomerWriteError(sql.ErrNoRows)
	}
	if prepared.Email != nil {
		email := strings.TrimSpace(*prepared.Email)
		if err := replacePrimaryContact(
			ctx, tx, tenantID, prepared.CustomerID, "email", email, strings.ToLower(email),
		); err != nil {
			return nil, nil, mapCustomerWriteError(err)
		}
	}
	if prepared.PhoneE164 != nil {
		phone := strings.TrimSpace(*prepared.PhoneE164)
		if phone != "" {
			normalized := normalizePhoneSearch(phone)
			if normalized == "" {
				return nil, nil, &assistanttools.ToolError{
					Code: "invalid_arguments", Field: FieldPhone, Message: "Précisez un numéro de téléphone valide.",
				}
			}
			phone = normalized
		}
		if err := replacePrimaryContact(
			ctx, tx, tenantID, prepared.CustomerID, "phone", phone, phone,
		); err != nil {
			return nil, nil, mapCustomerWriteError(err)
		}
	}
	var final customerCorrectionState
	if err := scanCustomerCorrectionState(
		tx.QueryRow(ctx, customerCorrectionSelect, tenantID, prepared.CustomerID), &final,
	); err != nil {
		return nil, nil, mapCustomerWriteError(err)
	}
	output, err := json.Marshal(final)
	if err != nil {
		return nil, nil, err
	}
	return output, []assistanttools.AffectedRecord{{Kind: "customer", ID: final.ID}}, nil
}

// replacePrimaryContact soft-deletes the current primary contact of this
// kind and inserts the new one, so the customer's contact history stays
// intact instead of being overwritten in place. An empty value only clears.
func replacePrimaryContact(
	ctx context.Context, tx pgx.Tx, tenantID, customerID, kind, value, normalizedValue string,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE customer_contacts SET deleted_at = now()
		WHERE tenant_id = $1 AND customer_id = $2 AND kind = $3
		  AND is_primary AND deleted_at IS NULL`,
		tenantID, customerID, kind); err != nil {
		return err
	}
	if value == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO customer_contacts (tenant_id, customer_id, kind, value, normalized_value, is_primary)
		VALUES ($1, $2, $3, $4, $5, true)`,
		tenantID, customerID, kind, value, normalizedValue)
	return err
}

const vehicleCorrectionSelect = `
	SELECT vehicle.id, COALESCE(vehicle.registration_plate, ''), COALESCE(vehicle.make, ''),
	       COALESCE(vehicle.model, ''), COALESCE(vehicle.vin, ''),
	       app_current_user_can_access_location(vehicle.location_id), vehicle.updated_at
	FROM vehicles vehicle
	WHERE vehicle.tenant_id = $1 AND vehicle.id = $2 AND vehicle.deleted_at IS NULL`

func scanVehicleCorrectionState(row interface{ Scan(...any) error }, state *vehicleCorrectionState) error {
	return row.Scan(
		&state.ID, &state.Plate, &state.Make, &state.Model, &state.VIN,
		&state.CanCorrect, &state.UpdatedAt,
	)
}

func (s *Store) loadVehicleCorrectionState(
	ctx context.Context, scope assistanttools.Scope, vehicleID string,
) (state vehicleCorrectionState, err error) {
	err = s.db.WithinTenantUser(ctx, scope.TenantID, scope.UserID, func(tx pgx.Tx) error {
		return scanVehicleCorrectionState(
			tx.QueryRow(ctx, vehicleCorrectionSelect, scope.TenantID, vehicleID), &state,
		)
	})
	return state, err
}

func (s *Store) applyVehicleCorrection(
	ctx context.Context, tx pgx.Tx, tenantID string, prepared preparedVehicleCorrection,
) (json.RawMessage, []assistanttools.AffectedRecord, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE vehicles SET
		    registration_plate = CASE WHEN $3 THEN NULLIF(btrim(upper($4)), '') ELSE registration_plate END,
		    make = CASE WHEN $5 THEN NULLIF(btrim($6), '') ELSE make END,
		    model = CASE WHEN $7 THEN NULLIF(btrim($8), '') ELSE model END,
		    vin = CASE WHEN $9 THEN NULLIF(btrim(upper($10)), '') ELSE vin END,
		    updated_at = now()
		WHERE tenant_id = $1 AND id = $2 AND updated_at = $11 AND deleted_at IS NULL`,
		tenantID, prepared.VehicleID,
		prepared.Plate != nil, stringValue(prepared.Plate),
		prepared.Make != nil, stringValue(prepared.Make),
		prepared.Model != nil, stringValue(prepared.Model),
		prepared.VIN != nil, stringValue(prepared.VIN),
		prepared.ExpectedUpdatedAt,
	)
	if err != nil {
		return nil, nil, mapCustomerWriteError(err)
	}
	if tag.RowsAffected() != 1 {
		return nil, nil, mapCustomerWriteError(sql.ErrNoRows)
	}
	var final vehicleCorrectionState
	if err := scanVehicleCorrectionState(
		tx.QueryRow(ctx, vehicleCorrectionSelect, tenantID, prepared.VehicleID), &final,
	); err != nil {
		return nil, nil, mapCustomerWriteError(err)
	}
	output, err := json.Marshal(final)
	if err != nil {
		return nil, nil, err
	}
	return output, []assistanttools.AffectedRecord{{Kind: "vehicle", ID: final.ID}}, nil
}

func correctCustomerSummary(current customerCorrectionState, patch customerCorrectionPatch) string {
	changes := make([]string, 0, 5)
	if patch.FirstName != nil {
		changes = append(changes, "prénom : "+displayCorrectionValue(current.FirstName)+" → "+displayCorrectionValue(*patch.FirstName))
	}
	if patch.LastName != nil {
		changes = append(changes, "nom : "+displayCorrectionValue(current.LastName)+" → "+displayCorrectionValue(*patch.LastName))
	}
	if patch.CompanyName != nil {
		changes = append(changes, "société : "+displayCorrectionValue(current.CompanyName)+" → "+displayCorrectionValue(*patch.CompanyName))
	}
	if patch.Email != nil {
		changes = append(changes, "e-mail : "+displayCorrectionValue(current.Email)+" → "+displayCorrectionValue(*patch.Email))
	}
	if patch.PhoneE164 != nil {
		changes = append(changes, "téléphone : "+displayCorrectionValue(current.PhoneE164)+" → "+displayCorrectionValue(*patch.PhoneE164))
	}
	return "Corriger « " + current.label() + " » — " + strings.Join(changes, " ; ") + "."
}

func correctVehicleSummary(current vehicleCorrectionState, patch vehicleCorrectionPatch) string {
	changes := make([]string, 0, 4)
	if patch.Plate != nil {
		changes = append(changes, "plaque : "+displayCorrectionValue(current.Plate)+" → "+displayCorrectionValue(*patch.Plate))
	}
	if patch.Make != nil {
		changes = append(changes, "marque : "+displayCorrectionValue(current.Make)+" → "+displayCorrectionValue(*patch.Make))
	}
	if patch.Model != nil {
		changes = append(changes, "modèle : "+displayCorrectionValue(current.Model)+" → "+displayCorrectionValue(*patch.Model))
	}
	if patch.VIN != nil {
		changes = append(changes, "VIN : "+displayCorrectionValue(current.VIN)+" → "+displayCorrectionValue(*patch.VIN))
	}
	return "Corriger « " + current.label() + " » — " + strings.Join(changes, " ; ") + "."
}

func displayCorrectionValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "non renseigné"
	}
	return value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Store) searchAtLocation(ctx context.Context, scope assistanttools.Scope, query string) ([]Customer, error) {
	page, err := s.Search(ctx, scope.TenantID, scope.UserID, query)
	if err != nil || len(page.Customers) == 0 {
		return page.Customers, err
	}
	ids := make([]string, 0, len(page.Customers))
	for _, customer := range page.Customers {
		ids = append(ids, customer.ID)
	}
	allowed := make(map[string]bool)
	err = s.db.WithinTenantUser(ctx, scope.TenantID, scope.UserID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT customer.id::text
			FROM customers customer
			WHERE customer.tenant_id = $1 AND customer.id = ANY($2::uuid[])
			  AND (customer.home_location_id = $3 OR EXISTS (
			      SELECT 1 FROM customer_location_grants access_grant
			      WHERE access_grant.tenant_id = customer.tenant_id
			        AND access_grant.customer_id = customer.id
			        AND access_grant.receiving_location_id = $3
			        AND access_grant.revoked_at IS NULL
			  ))`, scope.TenantID, ids, scope.LocationID)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, id := range ids {
			allowed[id] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	filtered := make([]Customer, 0, len(allowed))
	for _, customer := range page.Customers {
		if allowed[customer.ID] {
			filtered = append(filtered, customer)
		}
	}
	return filtered, nil
}

func parseCustomerToolInput(input json.RawMessage) (searchCustomersInput, json.RawMessage, error) {
	var parsed searchCustomersInput
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return parsed, nil, customerToolInputError(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return parsed, nil, customerToolInputError(err)
	}
	parsed.Query = strings.TrimSpace(parsed.Query)
	if parsed.Query == "" || utf8.RuneCountInString(parsed.Query) > 160 {
		return parsed, nil, customerToolInputError(nil)
	}
	canonical, err := json.Marshal(parsed)
	return parsed, canonical, err
}

func customerToolInputError(cause error) error {
	toolErr := &assistanttools.ToolError{Code: "invalid_arguments", Field: "query", Message: "Précisez un nom, téléphone, e-mail, plaque ou VIN."}
	if cause == nil {
		return toolErr
	}
	return errors.Join(toolErr, cause)
}

func customerToolAnswer(query string, customers []Customer) string {
	if len(customers) == 0 {
		return "Je n’ai trouvé aucun client ou véhicule correspondant à « " + query + " » dans cet établissement."
	}
	lines := []string{"Dossiers trouvés :"}
	for _, customer := range customers {
		facts := append([]string{}, customer.Contacts()...)
		for _, vehicle := range customer.Vehicles {
			if label := vehicle.Label(); label != "" {
				facts = append(facts, label)
			}
		}
		line := "• " + customer.Label()
		if len(facts) != 0 {
			line += " — " + strings.Join(facts, " · ")
		}
		if customer.Shared {
			line += " · dossier partagé depuis " + customer.HomeLocationName
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func parseCorrectCustomerInput(input json.RawMessage) (correctCustomerInput, error) {
	var parsed correctCustomerInput
	if err := strictUnmarshal(input, &parsed); err != nil {
		return parsed, correctionInputError(FieldCustomerID, err)
	}
	parsed.CustomerID = strings.TrimSpace(parsed.CustomerID)
	if parsed.CustomerID == "" {
		return parsed, correctionInputError(FieldCustomerID, nil)
	}
	if parsed.FirstName == nil && parsed.LastName == nil && parsed.CompanyName == nil &&
		parsed.Email == nil && parsed.PhoneE164 == nil {
		return parsed, &assistanttools.ToolError{
			Code: "invalid_arguments", Message: "Précisez au moins une correction.",
		}
	}
	return parsed, nil
}

func parseCorrectVehicleInput(input json.RawMessage) (correctVehicleInput, error) {
	var parsed correctVehicleInput
	if err := strictUnmarshal(input, &parsed); err != nil {
		return parsed, correctionInputError(FieldVehicleID, err)
	}
	parsed.VehicleID = strings.TrimSpace(parsed.VehicleID)
	if parsed.VehicleID == "" {
		return parsed, correctionInputError(FieldVehicleID, nil)
	}
	if parsed.Plate == nil && parsed.Make == nil && parsed.Model == nil && parsed.VIN == nil {
		return parsed, &assistanttools.ToolError{
			Code: "invalid_arguments", Message: "Précisez au moins une correction.",
		}
	}
	return parsed, nil
}

// strictUnmarshal rejects unknown fields and trailing data, the same way
// every tool input in this codebase is parsed.
func strictUnmarshal(input json.RawMessage, dest any) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func correctionInputError(field string, cause error) error {
	toolErr := &assistanttools.ToolError{
		Code: "invalid_arguments", Field: field, Message: "Les arguments proposés sont invalides.",
	}
	if cause == nil {
		return toolErr
	}
	return errors.Join(toolErr, cause)
}

// mapCustomerWriteError translates database constraint violations and a
// stale/inaccessible target into the assistant's error contract — the
// constraints (unique contact values, unique plates, format checks) are the
// actual validation; this only decodes what they already rejected.
func mapCustomerWriteError(err error) error {
	if pgErr, ok := db.PgError(err); ok {
		switch pgErr.Code {
		case "23505":
			switch pgErr.ConstraintName {
			case "customer_contacts_active_value_unique":
				return &assistanttools.ToolError{Code: "conflict", Message: "Ce contact est déjà utilisé par un autre client."}
			case "vehicles_active_plate_unique":
				return &assistanttools.ToolError{Code: "conflict", Message: "Cette plaque est déjà utilisée par un autre véhicule."}
			}
			return &assistanttools.ToolError{Code: "conflict", Message: "Cette valeur est déjà utilisée."}
		case "23514":
			return &assistanttools.ToolError{Code: "invalid_arguments", Message: "Les informations proposées ne respectent pas le format attendu."}
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return &assistanttools.ToolError{
			Code:    "conflict",
			Message: "Cette fiche a changé depuis l’aperçu, ou n’est plus accessible depuis ce site. Préparez une nouvelle demande.",
		}
	}
	return err
}
