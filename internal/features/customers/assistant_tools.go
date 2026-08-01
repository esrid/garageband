package customers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/esrid/garageband/internal/platform/assistanttools"
)

const ToolSearchCustomers = "search_customers"

var searchCustomersSchema = json.RawMessage(`{
  "type":"object",
  "properties":{"query":{"type":"string","minLength":1,"maxLength":160,"description":"Customer name, company, phone, email, plate, or VIN"}},
  "required":["query"],
  "additionalProperties":false
}`)

type searchCustomersInput struct {
	Query string `json:"query"`
}

func (s *Store) Definitions() []assistanttools.Definition {
	return []assistanttools.Definition{{
		Name:        ToolSearchCustomers,
		Description: "Find customer and vehicle records visible at the scoped location.",
		InputSchema: searchCustomersSchema, Consequence: assistanttools.ConsequenceRead,
	}}
}

func (s *Store) Preview(ctx context.Context, scope assistanttools.Scope, name string, input json.RawMessage) (assistanttools.Preview, error) {
	if name != ToolSearchCustomers {
		return assistanttools.Preview{}, assistanttools.ErrUnknownTool
	}
	parsed, canonical, err := parseCustomerToolInput(input)
	if err != nil {
		return assistanttools.Preview{}, err
	}
	if _, err := s.searchAtLocation(ctx, scope, parsed.Query); err != nil {
		return assistanttools.Preview{}, err
	}
	return assistanttools.Preview{Summary: "Rechercher un client ou véhicule : « " + parsed.Query + " ».", Input: canonical}, nil
}

func (s *Store) Execute(ctx context.Context, scope assistanttools.Scope, name string, input json.RawMessage) (assistanttools.Result, error) {
	if name != ToolSearchCustomers {
		return assistanttools.Result{}, assistanttools.ErrUnknownTool
	}
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
	err = s.db.WithinTenantUser(ctx, scope.TenantID, scope.UserID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
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
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return errors.Join(err, rows.Close())
			}
			allowed[id] = true
		}
		return errors.Join(rows.Err(), rows.Close())
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
