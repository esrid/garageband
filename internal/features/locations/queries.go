package locations

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/esrid/garageband/internal/platform/db"
)

var ErrForbidden = errors.New("location management is forbidden")

var ErrCatalogServiceUnavailable = errors.New("catalog service is not schedulable at this location")

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

type OpeningHour struct {
	Weekday  int
	OpensAt  string
	ClosesAt string
}

type Closure struct {
	ID       string
	StartsAt time.Time
	EndsAt   time.Time
	Reason   string
}

type Schedule struct {
	Organization string
	Location     Location
	Enabled      bool
	OpeningHours []OpeningHour
	Closures     []Closure
	Resources    []WorkshopResource
	Services     []SchedulingService
	CatalogItems []CatalogSchedulingItem
	CanManage    bool
}

type WorkshopResource struct {
	ID     string
	Kind   string
	Name   string
	Active bool
}

type ServiceRequirement struct {
	Kind     string
	Quantity int
}

type SchedulingService struct {
	ID                 string
	Name               string
	Duration           int
	Active             bool
	CatalogItemID      string
	CatalogReference   string
	CatalogLinkEnabled bool
	CatalogAvailable   bool
	Price              CatalogPrice
	Requirements       []ServiceRequirement
}

type CatalogSchedulingItem struct {
	ID        string
	Name      string
	Reference string
	Duration  int
	Price     CatalogPrice
}

type CatalogPrice struct {
	Kind           string
	AmountCents    int64
	MaxAmountCents int64
	TaxBasis       string
	VATBasisPoints int
}

type ResourceInput struct {
	Kind string
	Name string
}

type RequirementInput struct {
	ServiceID string
	Kind      string
	Quantity  int
}

type OpeningHourInput struct {
	Weekday  int
	OpensAt  string
	ClosesAt string
}

type ClosureInput struct {
	StartsDate string
	StartsTime string
	EndsDate   string
	EndsTime   string
	Reason     string
}

type ScheduleFieldError struct {
	Field   string
	Message string
}

func (e *ScheduleFieldError) Error() string { return e.Message }

type Store struct{ db *db.DB }

func NewStore(database *db.DB) *Store { return &Store{db: database} }

func (s *Store) Schedule(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
) (schedule Schedule, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		organization, role, err := membershipContext(ctx, tx, tenantID, userID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrForbidden
			}
			return err
		}
		schedule.Organization = organization
		schedule.CanManage = role == "owner" || role == "admin"
		row := tx.QueryRow(ctx, `
			SELECT id, tenant_id, name,
			       COALESCE(siret, ''), COALESCE(phone_e164, ''),
			       COALESCE(email, ''), COALESCE(website_url, ''),
			       COALESCE(address_line1, ''), COALESCE(address_line2, ''),
			       COALESCE(postal_code, ''), COALESCE(city, ''),
			       country_code, timezone, status, created_at, updated_at
			FROM locations
			WHERE tenant_id = $1 AND id = $2`, tenantID, locationID)
		if schedule.Location, err = scanLocation(row); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT availability_schedule_enabled
			FROM locations WHERE tenant_id = $1 AND id = $2`,
			tenantID, locationID,
		).Scan(&schedule.Enabled); err != nil {
			return err
		}

		hourRows, err := tx.Query(ctx, `
			SELECT weekday, to_char(opens_at, 'HH24:MI'), to_char(closes_at, 'HH24:MI')
			FROM location_opening_hours
			WHERE tenant_id = $1 AND location_id = $2
			ORDER BY weekday, opens_at`, tenantID, locationID)
		if err != nil {
			return err
		}
		schedule.OpeningHours, err = pgx.CollectRows(hourRows, pgx.RowToStructByPos[OpeningHour])
		if err != nil {
			return err
		}

		zone, err := time.LoadLocation(schedule.Location.Timezone)
		if err != nil {
			return err
		}
		closureRows, err := tx.Query(ctx, `
			SELECT id::text, starts_at, ends_at, COALESCE(reason, '')
			FROM location_closures
			WHERE tenant_id = $1 AND location_id = $2 AND ends_at >= now()
			ORDER BY starts_at, id
			LIMIT 100`, tenantID, locationID)
		if err != nil {
			return err
		}
		for closureRows.Next() {
			var closure Closure
			if err := closureRows.Scan(&closure.ID, &closure.StartsAt, &closure.EndsAt, &closure.Reason); err != nil {
				closureRows.Close()
				return err
			}
			closure.StartsAt = closure.StartsAt.In(zone)
			closure.EndsAt = closure.EndsAt.In(zone)
			schedule.Closures = append(schedule.Closures, closure)
		}
		closureRows.Close()
		if err := closureRows.Err(); err != nil {
			return err
		}

		resourceRows, err := tx.Query(ctx, `
			SELECT id::text, kind, name, active
			FROM bookable_resources
			WHERE tenant_id = $1 AND location_id = $2
			ORDER BY active DESC, kind, name, id`, tenantID, locationID)
		if err != nil {
			return err
		}
		schedule.Resources, err = pgx.CollectRows(resourceRows, pgx.RowToStructByPos[WorkshopResource])
		if err != nil {
			return err
		}

		serviceRows, err := tx.Query(ctx, `
			SELECT service.id::text, service.name, service.duration_minutes,
			       service.active, COALESCE(service.catalog_item_id::text, ''),
			       service.catalog_link_enabled, COALESCE(item.reference, ''),
			       COALESCE(item.price_kind, ''), COALESCE(item.amount_cents, 0),
			       COALESCE(item.max_amount_cents, 0), COALESCE(item.tax_basis, ''),
			       COALESCE(item.vat_basis_points, 0),
			       COALESCE(
			           item.archived_at IS NULL
			           AND item.kind IN ('service', 'package')
			           AND item.duration_minutes IS NOT NULL
			           AND (
			               item.location_scope = 'all'
			               OR EXISTS (
			                   SELECT 1 FROM catalog_item_locations item_location
			                   WHERE item_location.tenant_id = item.tenant_id
			                     AND item_location.catalog_item_id = item.id
			                     AND item_location.location_id = service.location_id
			               )
			           ), FALSE
			       ), requirement.resource_kind, requirement.quantity
			FROM service_offerings service
			LEFT JOIN catalog_items item
			  ON item.tenant_id = service.tenant_id
			 AND item.id = service.catalog_item_id
			LEFT JOIN service_resource_requirements requirement
			  ON requirement.tenant_id = service.tenant_id
			 AND requirement.service_id = service.id
			WHERE service.tenant_id = $1 AND service.location_id = $2
			  AND (service.active OR service.catalog_item_id IS NOT NULL)
			ORDER BY service.active DESC, service.name, service.id, requirement.resource_kind`, tenantID, locationID)
		if err != nil {
			return err
		}
		serviceIndexes := make(map[string]int)
		for serviceRows.Next() {
			var service SchedulingService
			var kind sql.NullString
			var quantity sql.NullInt64
			if err := serviceRows.Scan(
				&service.ID, &service.Name, &service.Duration, &service.Active,
				&service.CatalogItemID, &service.CatalogLinkEnabled,
				&service.CatalogReference, &service.Price.Kind,
				&service.Price.AmountCents, &service.Price.MaxAmountCents,
				&service.Price.TaxBasis, &service.Price.VATBasisPoints,
				&service.CatalogAvailable, &kind, &quantity,
			); err != nil {
				serviceRows.Close()
				return err
			}
			index, exists := serviceIndexes[service.ID]
			if !exists {
				index = len(schedule.Services)
				serviceIndexes[service.ID] = index
				schedule.Services = append(schedule.Services, service)
			}
			if kind.Valid {
				schedule.Services[index].Requirements = append(schedule.Services[index].Requirements, ServiceRequirement{Kind: kind.String, Quantity: int(quantity.Int64)})
			}
		}
		serviceRows.Close()
		if err := serviceRows.Err(); err != nil {
			return err
		}

		if !schedule.CanManage {
			return nil
		}
		catalogRows, err := tx.Query(ctx, `
			SELECT item.id::text, item.name, COALESCE(item.reference, ''),
			       item.duration_minutes, item.price_kind,
			       COALESCE(item.amount_cents, 0), COALESCE(item.max_amount_cents, 0),
			       item.tax_basis, item.vat_basis_points
			FROM catalog_items item
			WHERE item.tenant_id = $1
			  AND item.archived_at IS NULL
			  AND item.kind IN ('service', 'package')
			  AND item.duration_minutes IS NOT NULL
			  AND (
			      item.location_scope = 'all'
			      OR EXISTS (
			          SELECT 1 FROM catalog_item_locations item_location
			          WHERE item_location.tenant_id = item.tenant_id
			            AND item_location.catalog_item_id = item.id
			            AND item_location.location_id = $2
			      )
			  )
			  AND NOT EXISTS (
			      SELECT 1 FROM service_offerings service
			      WHERE service.tenant_id = item.tenant_id
			        AND service.location_id = $2
			        AND service.catalog_item_id = item.id
			        AND service.catalog_link_enabled
			  )
			ORDER BY item.name, item.id`, tenantID, locationID)
		if err != nil {
			return err
		}
		for catalogRows.Next() {
			var item CatalogSchedulingItem
			if err := catalogRows.Scan(
				&item.ID, &item.Name, &item.Reference, &item.Duration,
				&item.Price.Kind, &item.Price.AmountCents, &item.Price.MaxAmountCents,
				&item.Price.TaxBasis, &item.Price.VATBasisPoints,
			); err != nil {
				catalogRows.Close()
				return err
			}
			schedule.CatalogItems = append(schedule.CatalogItems, item)
		}
		catalogRows.Close()
		return catalogRows.Err()
	})
	return schedule, err
}

func (s *Store) LinkCatalogService(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	catalogItemID string,
) (id string, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO service_offerings (
			    tenant_id, location_id, code, name, description,
			    duration_minutes, price_cents, currency, active,
			    catalog_item_id, catalog_link_enabled
			)
			SELECT item.tenant_id, location.id,
			       'catalog-' || replace(item.id::text, '-', ''),
			       item.name, item.description, item.duration_minutes,
			       CASE WHEN item.price_kind = 'fixed' THEN item.amount_cents END,
			       item.currency, TRUE, item.id, TRUE
			FROM catalog_items item
			JOIN locations location
			  ON location.tenant_id = item.tenant_id AND location.id = $2
			WHERE item.tenant_id = $1 AND item.id = $3
			  AND item.archived_at IS NULL
			  AND item.kind IN ('service', 'package')
			  AND item.duration_minutes IS NOT NULL
			  AND (
			      item.location_scope = 'all'
			      OR EXISTS (
			          SELECT 1 FROM catalog_item_locations item_location
			          WHERE item_location.tenant_id = item.tenant_id
			            AND item_location.catalog_item_id = item.id
			            AND item_location.location_id = location.id
			      )
			  )
			ON CONFLICT (tenant_id, location_id, catalog_item_id)
			    WHERE catalog_item_id IS NOT NULL
			DO UPDATE SET catalog_link_enabled = TRUE
			RETURNING id::text`, tenantID, locationID, catalogItemID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCatalogServiceUnavailable
		}
		return err
	})
	return id, err
}

func (s *Store) SetCatalogServiceEnabled(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	serviceID string,
	enabled bool,
) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `
			UPDATE service_offerings service
			SET catalog_link_enabled = $4
			WHERE service.tenant_id = $1 AND service.location_id = $2
			  AND service.id = $3 AND service.catalog_item_id IS NOT NULL
			  AND (
			      NOT $4
			      OR EXISTS (
			          SELECT 1 FROM catalog_items item
			          WHERE item.tenant_id = service.tenant_id
			            AND item.id = service.catalog_item_id
			            AND item.archived_at IS NULL
			            AND item.kind IN ('service', 'package')
			            AND item.duration_minutes IS NOT NULL
			            AND (
			                item.location_scope = 'all'
			                OR EXISTS (
			                    SELECT 1 FROM catalog_item_locations item_location
			                    WHERE item_location.tenant_id = item.tenant_id
			                      AND item_location.catalog_item_id = item.id
			                      AND item_location.location_id = service.location_id
			                )
			            )
			      )
			  )`, tenantID, locationID, serviceID, enabled)
		if err != nil {
			return err
		}
		if err := exactlyOne(result); errors.Is(err, sql.ErrNoRows) && enabled {
			return ErrCatalogServiceUnavailable
		} else {
			return err
		}
	})
}

func (s *Store) AddResource(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	input ResourceInput,
) (id string, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO bookable_resources (tenant_id, location_id, kind, name)
			SELECT $1, location.id, $3, btrim($4)
			FROM locations location
			WHERE location.tenant_id = $1 AND location.id = $2
			RETURNING id::text`, tenantID, locationID, input.Kind, input.Name).Scan(&id)
	})
	return id, err
}

func (s *Store) SetResourceActive(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	resourceID string,
	active bool,
) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `
			UPDATE bookable_resources SET active = $4, updated_at = now()
			WHERE tenant_id = $1 AND location_id = $2 AND id = $3`,
			tenantID, locationID, resourceID, active)
		if err != nil {
			return err
		}
		return exactlyOne(result)
	})
}

func (s *Store) UpsertRequirement(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	input RequirementInput,
) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `
			INSERT INTO service_resource_requirements (
			    tenant_id, location_id, service_id, resource_kind, quantity
			)
			SELECT service.tenant_id, service.location_id, service.id, $4, $5
			FROM service_offerings service
			WHERE service.tenant_id = $1 AND service.location_id = $2
			  AND service.id = $3 AND service.active
			ON CONFLICT (service_id, resource_kind) DO UPDATE
			SET quantity = EXCLUDED.quantity`,
			tenantID, locationID, input.ServiceID, input.Kind, input.Quantity)
		if err != nil {
			return err
		}
		return exactlyOne(result)
	})
}

func (s *Store) DeleteRequirement(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	input RequirementInput,
) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `
			DELETE FROM service_resource_requirements
			WHERE tenant_id = $1 AND location_id = $2
			  AND service_id = $3 AND resource_kind = $4`,
			tenantID, locationID, input.ServiceID, input.Kind)
		if err != nil {
			return err
		}
		return exactlyOne(result)
	})
}

func (s *Store) AddOpeningHour(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	input OpeningHourInput,
) (err error) {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		var locationExists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM locations WHERE tenant_id = $1 AND id = $2)`,
			tenantID, locationID,
		).Scan(&locationExists); err != nil {
			return err
		}
		if !locationExists {
			return sql.ErrNoRows
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO location_opening_hours (
			    tenant_id, location_id, weekday, opens_at, closes_at
			) VALUES ($1, $2, $3, $4::time, $5::time)`,
			tenantID, locationID, input.Weekday, input.OpensAt, input.ClosesAt)
		return err
	})
}

func (s *Store) DeleteOpeningHour(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	input OpeningHourInput,
) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `
			DELETE FROM location_opening_hours
			WHERE tenant_id = $1 AND location_id = $2 AND weekday = $3
			  AND opens_at = $4::time AND closes_at = $5::time`,
			tenantID, locationID, input.Weekday, input.OpensAt, input.ClosesAt)
		if err != nil {
			return err
		}
		return exactlyOne(result)
	})
}

func (s *Store) AddClosure(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	input ClosureInput,
) (id string, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		var timezoneName string
		if err := tx.QueryRow(ctx, `
			SELECT timezone FROM locations WHERE tenant_id = $1 AND id = $2`,
			tenantID, locationID,
		).Scan(&timezoneName); err != nil {
			return err
		}
		zone, err := time.LoadLocation(timezoneName)
		if err != nil {
			return err
		}
		const localDateTime = "2006-01-02 15:04"
		startsValue := input.StartsDate + " " + input.StartsTime
		endsValue := input.EndsDate + " " + input.EndsTime
		startsAt, err := time.ParseInLocation(localDateTime, startsValue, zone)
		if err != nil || startsAt.Format(localDateTime) != startsValue {
			return &ScheduleFieldError{Field: FieldClosureStartDate, Message: "Choisissez un début valide dans le fuseau du site."}
		}
		endsAt, err := time.ParseInLocation(localDateTime, endsValue, zone)
		if err != nil || endsAt.Format(localDateTime) != endsValue {
			return &ScheduleFieldError{Field: FieldClosureEndDate, Message: "Choisissez une fin valide dans le fuseau du site."}
		}
		if !endsAt.After(startsAt) {
			return &ScheduleFieldError{Field: FieldClosureEndTime, Message: "La fin doit être après le début."}
		}
		return tx.QueryRow(ctx, `
			INSERT INTO location_closures (
			    tenant_id, location_id, starts_at, ends_at, reason, created_by_user_id
			) VALUES ($1, $2, $3, $4, NULLIF(btrim($5), ''), $6)
			RETURNING id::text`,
			tenantID, locationID, startsAt, endsAt, input.Reason, userID,
		).Scan(&id)
	})
	return id, err
}

func (s *Store) DeleteClosure(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	closureID string,
) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `
			DELETE FROM location_closures
			WHERE tenant_id = $1 AND location_id = $2 AND id = $3`,
			tenantID, locationID, closureID)
		if err != nil {
			return err
		}
		return exactlyOne(result)
	})
}

func exactlyOne(result pgconn.CommandTag) error {
	if result.RowsAffected() != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) Overview(
	ctx context.Context,
	tenantID string,
	userID string,
) (overview Overview, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
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

		rows, err := tx.Query(ctx, `
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
		overview.Locations, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Location])
		return err
	})
	return overview, err
}

func (s *Store) Get(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
) (location Location, canManage bool, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		_, role, err := membershipContext(ctx, tx, tenantID, userID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrForbidden
			}
			return err
		}
		canManage = role == "owner" || role == "admin"
		row := tx.QueryRow(ctx, `
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
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `
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
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `
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
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `
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
	tx pgx.Tx,
	tenantID string,
	userID string,
) (organization string, role string, err error) {
	err = tx.QueryRow(ctx, `
		SELECT tenant.name, membership.role
		FROM tenant_memberships membership
		JOIN tenants tenant ON tenant.id = membership.tenant_id
		WHERE membership.tenant_id = $1 AND membership.user_id = $2`,
		tenantID, userID,
	).Scan(&organization, &role)
	return organization, role, err
}

func requireManager(ctx context.Context, tx pgx.Tx, tenantID, userID string) error {
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
