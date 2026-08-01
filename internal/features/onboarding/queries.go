package onboarding

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/esrid/garageband/internal/platform/businesslookup"
	"github.com/esrid/garageband/internal/platform/db"
)

const draftTTL = 30 * time.Minute

var (
	ErrDraftExpired  = errors.New("onboarding draft expired")
	ErrInvalidGarage = errors.New("invalid garage details")
	ErrSIRETMismatch = errors.New("confirmed SIRET differs from onboarding draft")
)

type Store struct{ db *db.DB }

type Draft struct {
	ID        string
	Profile   businesslookup.Profile
	ExpiresAt time.Time
}

type GarageInput struct {
	Name         string
	LegalName    string
	Slug         string
	WebsiteURL   string
	LocationName string
	SIRET        string
	AddressLine1 string
	AddressLine2 string
	PostalCode   string
	City         string
	CountryCode  string
}

func NewStore(database *db.DB) *Store { return &Store{db: database} }

func (s *Store) CreateDraft(
	ctx context.Context,
	userID string,
	provider businesslookup.Provider,
	profile businesslookup.Profile,
) (Draft, error) {
	encoded, err := json.Marshal(profile)
	if err != nil {
		return Draft{}, err
	}
	draft := Draft{Profile: profile, ExpiresAt: time.Now().UTC().Add(draftTTL)}
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO onboarding_drafts
			(user_id, source_kind, source_value, provider, profile, expires_at)
		VALUES ($1, 'siret', $2, $3, $4, $5)
		RETURNING id`,
		userID, profile.SIRET, provider.Name(), encoded, draft.ExpiresAt,
	).Scan(&draft.ID)
	return draft, err
}

// FinalizeDraft locks the user-owned draft and creates the tenant, owner
// membership, first location, and enrichment audit in one RLS-scoped
// transaction. A completed draft returns its existing tenant for safe retries.
func (s *Store) FinalizeDraft(
	ctx context.Context,
	userID string,
	draftID string,
	input GarageInput,
) (tenantID string, err error) {
	if !validSIRET(input.SIRET) || len(input.CountryCode) != 2 {
		return "", ErrInvalidGarage
	}
	err = s.db.WithinNewTenant(ctx, func(tx *sql.Tx, newTenantID string) error {
		var sourceKind, sourceValue, provider, status string
		var profile []byte
		var existingTenantID sql.NullString
		var expiresAt time.Time
		err := tx.QueryRowContext(ctx, `
			SELECT source_kind, source_value, provider, status, profile,
			       tenant_id, expires_at
			FROM onboarding_drafts
			WHERE id = $1 AND user_id = $2
			FOR UPDATE`, draftID, userID,
		).Scan(
			&sourceKind, &sourceValue, &provider, &status, &profile,
			&existingTenantID, &expiresAt,
		)
		if err != nil {
			return err
		}
		if status == "completed" && existingTenantID.Valid {
			tenantID = existingTenantID.String
			return nil
		}
		if status != "ready" || !expiresAt.After(time.Now().UTC()) {
			return ErrDraftExpired
		}
		if input.SIRET != sourceValue {
			return ErrSIRETMismatch
		}

		tenantID = newTenantID
		var website any
		if input.WebsiteURL != "" {
			website = input.WebsiteURL
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tenants (id, slug, name, legal_name, siren, website_url)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6)`,
			tenantID, input.Slug, input.Name, input.LegalName, input.SIRET[:9], website,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tenant_memberships (tenant_id, user_id, role)
			VALUES ($1, $2, 'owner')`, tenantID, userID,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO locations (
				tenant_id, slug, name, siret, address_line1, address_line2,
				postal_code, city, country_code
			) VALUES ($1, 'principal', $2, $3, NULLIF($4, ''), NULLIF($5, ''),
			          NULLIF($6, ''), NULLIF($7, ''), $8)`,
			tenantID, input.LocationName, input.SIRET, input.AddressLine1,
			input.AddressLine2, input.PostalCode, input.City, input.CountryCode,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO business_enrichment_runs (
				tenant_id, source_kind, source_value, provider, status, result,
				started_at, completed_at
			) VALUES ($1, $2, $3, $4, 'succeeded', $5, now(), now())`,
			tenantID, sourceKind, sourceValue, provider, profile,
		); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE onboarding_drafts
			SET status = 'completed', tenant_id = $1, profile = '{}'::jsonb,
			    updated_at = now()
			WHERE id = $2 AND user_id = $3 AND status = 'ready'`,
			tenantID, draftID, userID,
		)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			return sql.ErrNoRows
		}
		return nil
	})
	return tenantID, err
}
