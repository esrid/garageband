package locations

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/assistanttools"
)

const ToolUpdateLocationContact = "update_location_contact"

var updateLocationContactSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "email": {"type": "string", "description": "New contact email; empty clears it"},
    "phone_e164": {"type": "string", "description": "New E.164 phone; empty clears it"},
    "website_url": {"type": "string", "description": "New http(s) website; empty clears it"}
  },
  "additionalProperties": false,
  "minProperties": 1
}`)

type locationContactPatch struct {
	Email      *string `json:"email,omitempty"`
	PhoneE164  *string `json:"phone_e164,omitempty"`
	WebsiteURL *string `json:"website_url,omitempty"`
}

type locationContactState struct {
	Name       string    `json:"name"`
	Email      string    `json:"email"`
	PhoneE164  string    `json:"phone_e164"`
	WebsiteURL string    `json:"website_url"`
	UpdatedAt  time.Time `json:"-"`
}

type preparedLocationContactPatch struct {
	locationContactPatch
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
}

func (s *Store) Definitions() []assistanttools.Definition {
	return []assistanttools.Definition{{
		Name:                 ToolUpdateLocationContact,
		Description:          "Propose changing the email, E.164 phone, or website of the scoped garage location.",
		InputSchema:          updateLocationContactSchema,
		Consequence:          assistanttools.ConsequenceWrite,
		ConfirmationRequired: true,
	}}
}

func (s *Store) Preview(
	ctx context.Context,
	scope assistanttools.Scope,
	name string,
	input json.RawMessage,
) (assistanttools.Preview, error) {
	if name != ToolUpdateLocationContact {
		return assistanttools.Preview{}, assistanttools.ErrUnknownTool
	}
	patch, err := parseLocationContactPatch(input)
	if err != nil {
		return assistanttools.Preview{}, err
	}
	var current locationContactState
	err = s.db.WithinTenantUser(ctx, scope.TenantID, scope.UserID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, scope.TenantID, scope.UserID); err != nil {
			return mapAssistantToolAccess(err)
		}
		return tx.QueryRow(ctx, `
			SELECT name, COALESCE(email, ''), COALESCE(phone_e164, ''),
			       COALESCE(website_url, ''), updated_at
			FROM locations
			WHERE tenant_id = $1 AND id = $2`, scope.TenantID, scope.LocationID,
		).Scan(&current.Name, &current.Email, &current.PhoneE164, &current.WebsiteURL, &current.UpdatedAt)
	})
	if err != nil {
		return assistanttools.Preview{}, err
	}
	canonical, err := json.Marshal(preparedLocationContactPatch{
		locationContactPatch: patch, ExpectedUpdatedAt: current.UpdatedAt,
	})
	if err != nil {
		return assistanttools.Preview{}, err
	}
	return assistanttools.Preview{
		Summary: formatLocationContactPreview(current, patch),
		Input:   canonical,
		AffectedRecords: []assistanttools.AffectedRecord{{
			Kind: "location", ID: scope.LocationID,
		}},
	}, nil
}

func (s *Store) Execute(
	ctx context.Context,
	scope assistanttools.Scope,
	name string,
	input json.RawMessage,
) (assistanttools.Result, error) {
	if name != ToolUpdateLocationContact {
		return assistanttools.Result{}, assistanttools.ErrUnknownTool
	}
	prepared, err := parsePreparedLocationContactPatch(input)
	if err != nil {
		return assistanttools.Result{}, err
	}
	patch := prepared.locationContactPatch
	var updated locationContactState
	err = s.db.WithinTenantUser(ctx, scope.TenantID, scope.UserID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, scope.TenantID, scope.UserID); err != nil {
			return mapAssistantToolAccess(err)
		}
		var receiptOutput []byte
		err := tx.QueryRow(ctx, `
			SELECT output
			FROM application_tool_receipts
			WHERE tenant_id = $1 AND location_id = $2 AND idempotency_key = $3
			  AND tool_name = $4`, scope.TenantID, scope.LocationID,
			scope.IdempotencyKey, name,
		).Scan(&receiptOutput)
		if err == nil {
			if err := json.Unmarshal(receiptOutput, &updated); err != nil {
				return err
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		err = tx.QueryRow(ctx, `
			UPDATE locations SET
			    email = CASE WHEN $3 THEN NULLIF($4, '') ELSE email END,
			    phone_e164 = CASE WHEN $5 THEN NULLIF($6, '') ELSE phone_e164 END,
			    website_url = CASE WHEN $7 THEN NULLIF($8, '') ELSE website_url END,
			    updated_at = now()
			WHERE tenant_id = $1 AND id = $2 AND updated_at = $9
			RETURNING name, COALESCE(email, ''), COALESCE(phone_e164, ''),
			          COALESCE(website_url, ''), updated_at`,
			scope.TenantID, scope.LocationID,
			patch.Email != nil, stringValue(patch.Email),
			patch.PhoneE164 != nil, stringValue(patch.PhoneE164),
			patch.WebsiteURL != nil, stringValue(patch.WebsiteURL),
			prepared.ExpectedUpdatedAt,
		).Scan(&updated.Name, &updated.Email, &updated.PhoneE164, &updated.WebsiteURL, &updated.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return &assistanttools.ToolError{
				Code: "conflict", Message: "Les coordonnées du site ont changé depuis l’aperçu. Préparez une nouvelle demande.",
			}
		}
		if err != nil {
			return err
		}
		output, err := json.Marshal(updated)
		if err != nil {
			return err
		}
		affected, err := json.Marshal([]assistanttools.AffectedRecord{{Kind: "location", ID: scope.LocationID}})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO application_tool_receipts (
			    tenant_id, location_id, idempotency_key, tool_name, output, affected_records
			) VALUES ($1, $2, $3, $4, $5, $6)`,
			scope.TenantID, scope.LocationID, scope.IdempotencyKey, name, output, affected)
		return err
	})
	if err != nil {
		return assistanttools.Result{}, err
	}
	output, err := json.Marshal(updated)
	if err != nil {
		return assistanttools.Result{}, err
	}
	return assistanttools.Result{
		Summary: "Les coordonnées de « " + updated.Name + " » ont été mises à jour.",
		Output:  output,
		AffectedRecords: []assistanttools.AffectedRecord{{
			Kind: "location", ID: scope.LocationID,
		}},
	}, nil
}

func parseLocationContactPatch(input json.RawMessage) (locationContactPatch, error) {
	var patch locationContactPatch
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&patch); err != nil {
		return patch, invalidToolArguments("arguments", "Les coordonnées proposées sont invalides.", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return patch, invalidToolArguments("arguments", "Les coordonnées proposées sont invalides.", err)
	}
	if patch.Email == nil && patch.PhoneE164 == nil && patch.WebsiteURL == nil {
		return patch, invalidToolArguments("arguments", "Aucune coordonnée à modifier.", nil)
	}
	if patch.Email != nil {
		normalized := strings.ToLower(strings.TrimSpace(*patch.Email))
		patch.Email = &normalized
		if normalized != "" && !emailPattern.MatchString(normalized) {
			return patch, invalidToolArguments("email", "L’adresse e-mail proposée est invalide.", nil)
		}
	}
	if patch.PhoneE164 != nil {
		normalized := strings.TrimSpace(*patch.PhoneE164)
		patch.PhoneE164 = &normalized
		if normalized != "" && !phonePattern.MatchString(normalized) {
			return patch, invalidToolArguments("phone_e164", "Le téléphone doit être au format international E.164.", nil)
		}
	}
	if patch.WebsiteURL != nil {
		normalized := strings.TrimSpace(*patch.WebsiteURL)
		patch.WebsiteURL = &normalized
		if normalized != "" && !validWebsite(normalized) {
			return patch, invalidToolArguments("website_url", "L’adresse du site web proposée est invalide.", nil)
		}
	}
	return patch, nil
}

func parsePreparedLocationContactPatch(input json.RawMessage) (preparedLocationContactPatch, error) {
	var prepared preparedLocationContactPatch
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&prepared); err != nil {
		return prepared, invalidToolArguments("arguments", "L’action préparée est invalide.", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return prepared, invalidToolArguments("arguments", "L’action préparée est invalide.", err)
	}
	if prepared.ExpectedUpdatedAt.IsZero() {
		return prepared, invalidToolArguments("arguments", "La version attendue du site est absente.", nil)
	}
	if prepared.Email == nil && prepared.PhoneE164 == nil && prepared.WebsiteURL == nil {
		return prepared, invalidToolArguments("arguments", "Aucune coordonnée à modifier.", nil)
	}
	return prepared, nil
}

func invalidToolArguments(field string, message string, cause error) error {
	toolErr := &assistanttools.ToolError{Code: "invalid_arguments", Field: field, Message: message}
	if cause == nil {
		return toolErr
	}
	return errors.Join(toolErr, cause)
}

func mapAssistantToolAccess(err error) error {
	if errors.Is(err, ErrForbidden) {
		return &assistanttools.ToolError{
			Code: "forbidden", Message: "Votre rôle ne permet pas de modifier les coordonnées de ce site.",
		}
	}
	return err
}

func formatLocationContactPreview(current locationContactState, patch locationContactPatch) string {
	changes := make([]string, 0, 3)
	if patch.Email != nil {
		changes = append(changes, fmt.Sprintf("e-mail : %s → %s", displayContact(current.Email), displayContact(*patch.Email)))
	}
	if patch.PhoneE164 != nil {
		changes = append(changes, fmt.Sprintf("téléphone : %s → %s", displayContact(current.PhoneE164), displayContact(*patch.PhoneE164)))
	}
	if patch.WebsiteURL != nil {
		changes = append(changes, fmt.Sprintf("site web : %s → %s", displayContact(current.WebsiteURL), displayContact(*patch.WebsiteURL)))
	}
	return "Modifier « " + current.Name + " » — " + strings.Join(changes, " ; ") + "."
}

func displayContact(value string) string {
	if value == "" {
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
