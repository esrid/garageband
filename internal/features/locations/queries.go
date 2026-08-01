package locations

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/esrid/garageband/internal/platform/db"
)

var ErrForbidden = errors.New("location management is forbidden")

// Location is the persistence model for a physical garage site.
type Location struct {
	ID           string
	TenantID     string
	Name         string
	SIRET        string
	PhoneE164    string
	Email        string
	WebsiteURL   string
	AddressLine1 string
	AddressLine2 string
	PostalCode   string
	City         string
	CountryCode  string
	Timezone     string
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Input struct {
	Name         string
	SIRET        string
	PhoneE164    string
	Email        string
	WebsiteURL   string
	AddressLine1 string
	AddressLine2 string
	PostalCode   string
	City         string
	CountryCode  string
	Timezone     string
}

type Overview struct {
	Organization   string
	Locations      []Location
	MembershipRole string
	CanManage      bool
}

type Store struct{ db *db.DB }

func NewStore(database *db.DB) *Store { return &Store{db: database} }

func (s *Store) Overview(
	ctx context.Context,
	tenantID string,
	userID string,
) (overview Overview, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) (returnErr error) {
		organization, role, err := membershipContext(ctx, tx, tenantID, userID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrForbidden
			}
			return err
		}
		overview.Organization = organization
		overview.MembershipRole = role
		overview.CanManage = role == "owner" || role == "admin"

		rows, err := tx.QueryContext(ctx, `
			SELECT id, tenant_id, name,
			       COALESCE(siret, ''), COALESCE(phone_e164, ''),
			       COALESCE(email, ''), COALESCE(website_url, ''),
			       COALESCE(address_line1, ''), COALESCE(address_line2, ''),
			       COALESCE(postal_code, ''), COALESCE(city, ''),
			       country_code, timezone, status, created_at, updated_at
			FROM locations
			WHERE tenant_id = $1
			ORDER BY (status = 'active') DESC, name, id`, tenantID)
		if err != nil {
			return err
		}
		defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
		for rows.Next() {
			location, err := scanLocation(rows)
			if err != nil {
				return err
			}
			overview.Locations = append(overview.Locations, location)
		}
		return rows.Err()
	})
	return overview, err
}

func (s *Store) Get(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
) (location Location, canManage bool, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) error {
		_, role, err := membershipContext(ctx, tx, tenantID, userID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrForbidden
			}
			return err
		}
		canManage = role == "owner" || role == "admin"
		row := tx.QueryRowContext(ctx, `
			SELECT id, tenant_id, name,
			       COALESCE(siret, ''), COALESCE(phone_e164, ''),
			       COALESCE(email, ''), COALESCE(website_url, ''),
			       COALESCE(address_line1, ''), COALESCE(address_line2, ''),
			       COALESCE(postal_code, ''), COALESCE(city, ''),
			       country_code, timezone, status, created_at, updated_at
			FROM locations
			WHERE tenant_id = $1 AND id = $2`, tenantID, locationID)
		location, err = scanLocation(row)
		return err
	})
	return location, canManage, err
}

func (s *Store) Create(
	ctx context.Context,
	tenantID string,
	userID string,
	input Input,
) (location Location, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		row := tx.QueryRowContext(ctx, `
			WITH generated AS (SELECT uuidv7() AS id)
			INSERT INTO locations (
				id, tenant_id, slug, name, siret, phone_e164, email,
				website_url, address_line1, address_line2, postal_code,
				city, country_code, timezone
			)
			SELECT id, $1,
			       'site-' || left(replace(id::text, '-', ''), 12),
			       btrim($2), NULLIF(btrim($3), ''), NULLIF(btrim($4), ''),
			       NULLIF(lower(btrim($5)), ''), NULLIF(btrim($6), ''),
			       NULLIF(btrim($7), ''), NULLIF(btrim($8), ''),
			       NULLIF(btrim($9), ''), NULLIF(btrim($10), ''),
			       upper(btrim($11)), btrim($12)
			FROM generated
			RETURNING id, tenant_id, name,
			          COALESCE(siret, ''), COALESCE(phone_e164, ''),
			          COALESCE(email, ''), COALESCE(website_url, ''),
			          COALESCE(address_line1, ''), COALESCE(address_line2, ''),
			          COALESCE(postal_code, ''), COALESCE(city, ''),
			          country_code, timezone, status, created_at, updated_at`,
			tenantID, input.Name, input.SIRET, input.PhoneE164, input.Email,
			input.WebsiteURL, input.AddressLine1, input.AddressLine2,
			input.PostalCode, input.City, input.CountryCode, input.Timezone,
		)
		var err error
		location, err = scanLocation(row)
		return err
	})
	return location, err
}

func (s *Store) Update(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	input Input,
) (location Location, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		row := tx.QueryRowContext(ctx, `
			UPDATE locations
			SET name = btrim($3),
			    siret = NULLIF(btrim($4), ''),
			    phone_e164 = NULLIF(btrim($5), ''),
			    email = NULLIF(lower(btrim($6)), ''),
			    website_url = NULLIF(btrim($7), ''),
			    address_line1 = NULLIF(btrim($8), ''),
			    address_line2 = NULLIF(btrim($9), ''),
			    postal_code = NULLIF(btrim($10), ''),
			    city = NULLIF(btrim($11), ''),
			    country_code = upper(btrim($12)),
			    timezone = btrim($13)
			WHERE tenant_id = $1 AND id = $2
			RETURNING id, tenant_id, name,
			          COALESCE(siret, ''), COALESCE(phone_e164, ''),
			          COALESCE(email, ''), COALESCE(website_url, ''),
			          COALESCE(address_line1, ''), COALESCE(address_line2, ''),
			          COALESCE(postal_code, ''), COALESCE(city, ''),
			          country_code, timezone, status, created_at, updated_at`,
			tenantID, locationID, input.Name, input.SIRET, input.PhoneE164,
			input.Email, input.WebsiteURL, input.AddressLine1,
			input.AddressLine2, input.PostalCode, input.City,
			input.CountryCode, input.Timezone,
		)
		var err error
		location, err = scanLocation(row)
		return err
	})
	return location, err
}

func (s *Store) SetStatus(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	status string,
) (location Location, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		row := tx.QueryRowContext(ctx, `
			UPDATE locations
			SET status = $3
			WHERE tenant_id = $1 AND id = $2
			RETURNING id, tenant_id, name,
			          COALESCE(siret, ''), COALESCE(phone_e164, ''),
			          COALESCE(email, ''), COALESCE(website_url, ''),
			          COALESCE(address_line1, ''), COALESCE(address_line2, ''),
			          COALESCE(postal_code, ''), COALESCE(city, ''),
			          country_code, timezone, status, created_at, updated_at`,
			tenantID, locationID, status,
		)
		var err error
		location, err = scanLocation(row)
		return err
	})
	return location, err
}

func membershipContext(
	ctx context.Context,
	tx *sql.Tx,
	tenantID string,
	userID string,
) (organization string, role string, err error) {
	err = tx.QueryRowContext(ctx, `
		SELECT tenant.name, membership.role
		FROM tenant_memberships membership
		JOIN tenants tenant ON tenant.id = membership.tenant_id
		WHERE membership.tenant_id = $1 AND membership.user_id = $2`,
		tenantID, userID,
	).Scan(&organization, &role)
	return organization, role, err
}

func requireManager(ctx context.Context, tx *sql.Tx, tenantID, userID string) error {
	_, role, err := membershipContext(ctx, tx, tenantID, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		return err
	}
	if role != "owner" && role != "admin" {
		return ErrForbidden
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanLocation(row scanner) (Location, error) {
	var location Location
	err := row.Scan(
		&location.ID, &location.TenantID, &location.Name,
		&location.SIRET, &location.PhoneE164, &location.Email,
		&location.WebsiteURL, &location.AddressLine1, &location.AddressLine2,
		&location.PostalCode, &location.City, &location.CountryCode,
		&location.Timezone, &location.Status, &location.CreatedAt,
		&location.UpdatedAt,
	)
	return location, err
}
