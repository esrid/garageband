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
	ID           string
	Name         string
	Duration     int
	Requirements []ServiceRequirement
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
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) (returnErr error) {
		organization, role, err := membershipContext(ctx, tx, tenantID, userID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrForbidden
			}
			return err
		}
		schedule.Organization = organization
		schedule.CanManage = role == "owner" || role == "admin"
		row := tx.QueryRowContext(ctx, `
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
		if err := tx.QueryRowContext(ctx, `
			SELECT availability_schedule_enabled
			FROM locations WHERE tenant_id = $1 AND id = $2`,
			tenantID, locationID,
		).Scan(&schedule.Enabled); err != nil {
			return err
		}

		hourRows, err := tx.QueryContext(ctx, `
			SELECT weekday, to_char(opens_at, 'HH24:MI'), to_char(closes_at, 'HH24:MI')
			FROM location_opening_hours
			WHERE tenant_id = $1 AND location_id = $2
			ORDER BY weekday, opens_at`, tenantID, locationID)
		if err != nil {
			return err
		}
		for hourRows.Next() {
			var opening OpeningHour
			if err := hourRows.Scan(&opening.Weekday, &opening.OpensAt, &opening.ClosesAt); err != nil {
				return errors.Join(err, hourRows.Close())
			}
			schedule.OpeningHours = append(schedule.OpeningHours, opening)
		}
		if err := errors.Join(hourRows.Err(), hourRows.Close()); err != nil {
			return err
		}

		zone, err := time.LoadLocation(schedule.Location.Timezone)
		if err != nil {
			return err
		}
		closureRows, err := tx.QueryContext(ctx, `
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
				return errors.Join(err, closureRows.Close())
			}
			closure.StartsAt = closure.StartsAt.In(zone)
			closure.EndsAt = closure.EndsAt.In(zone)
			schedule.Closures = append(schedule.Closures, closure)
		}
		if err := errors.Join(closureRows.Err(), closureRows.Close()); err != nil {
			return err
		}

		resourceRows, err := tx.QueryContext(ctx, `
			SELECT id::text, kind, name, active
			FROM bookable_resources
			WHERE tenant_id = $1 AND location_id = $2
			ORDER BY active DESC, kind, name, id`, tenantID, locationID)
		if err != nil {
			return err
		}
		for resourceRows.Next() {
			var resource WorkshopResource
			if err := resourceRows.Scan(&resource.ID, &resource.Kind, &resource.Name, &resource.Active); err != nil {
				return errors.Join(err, resourceRows.Close())
			}
			schedule.Resources = append(schedule.Resources, resource)
		}
		if err := errors.Join(resourceRows.Err(), resourceRows.Close()); err != nil {
			return err
		}

		serviceRows, err := tx.QueryContext(ctx, `
			SELECT service.id::text, service.name, service.duration_minutes,
			       requirement.resource_kind, requirement.quantity
			FROM service_offerings service
			LEFT JOIN service_resource_requirements requirement
			  ON requirement.tenant_id = service.tenant_id
			 AND requirement.service_id = service.id
			WHERE service.tenant_id = $1 AND service.location_id = $2 AND service.active
			ORDER BY service.name, service.id, requirement.resource_kind`, tenantID, locationID)
		if err != nil {
			return err
		}
		serviceIndexes := make(map[string]int)
		for serviceRows.Next() {
			var id, name string
			var duration int
			var kind sql.NullString
			var quantity sql.NullInt64
			if err := serviceRows.Scan(&id, &name, &duration, &kind, &quantity); err != nil {
				return errors.Join(err, serviceRows.Close())
			}
			index, exists := serviceIndexes[id]
			if !exists {
				index = len(schedule.Services)
				serviceIndexes[id] = index
				schedule.Services = append(schedule.Services, SchedulingService{ID: id, Name: name, Duration: duration})
			}
			if kind.Valid {
				schedule.Services[index].Requirements = append(schedule.Services[index].Requirements, ServiceRequirement{Kind: kind.String, Quantity: int(quantity.Int64)})
			}
		}
		return errors.Join(serviceRows.Err(), serviceRows.Close())
	})
	return schedule, err
}

func (s *Store) AddResource(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	input ResourceInput,
) (id string, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `
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
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
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
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
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
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
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
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		var locationExists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM locations WHERE tenant_id = $1 AND id = $2)`,
			tenantID, locationID,
		).Scan(&locationExists); err != nil {
			return err
		}
		if !locationExists {
			return sql.ErrNoRows
		}
		_, err := tx.ExecContext(ctx, `
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
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
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
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		var timezoneName string
		if err := tx.QueryRowContext(ctx, `
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
		return tx.QueryRowContext(ctx, `
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
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			DELETE FROM location_closures
			WHERE tenant_id = $1 AND location_id = $2 AND id = $3`,
			tenantID, locationID, closureID)
		if err != nil {
			return err
		}
		return exactlyOne(result)
	})
}

func exactlyOne(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}

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
