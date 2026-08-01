package agenda

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/ui"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrForbidden = errors.New("appointment management is forbidden")

type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Message }

type ConflictError struct {
	Resource string
	StartsAt time.Time
	EndsAt   time.Time
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s is already booked", e.Resource)
}

type SaveInput struct {
	LocationID string
	CustomerID string
	VehicleID  string
	ServiceID  string
	ResourceID string
	Date       string
	StartTime  string
	Note       string
}

type Store struct{ db *db.DB }

func NewStore(database *db.DB) *Store { return &Store{db: database} }

type locationRow struct {
	ID       string
	Name     string
	Timezone *time.Location
}

func (s *Store) Day(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	dateValue string,
) (page Day, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) (returnErr error) {
		organization, locations, location, err := loadLocationContext(
			ctx, tx, tenantID, locationID,
		)
		if err != nil {
			return err
		}
		page.Organization = organization
		page.LocationID = location.ID
		page.LocationName = location.Name
		page.Locations = locations
		page.CanManage = true
		page.Date = parseDay(dateValue, location.Timezone)

		start := page.Date
		end := start.AddDate(0, 0, 1)
		rows, err := tx.QueryContext(ctx, `
			SELECT appointment.id,
			       appointment.starts_at,
			       appointment.ends_at,
			       CASE WHEN customer.id IS NULL THEN NULL
			            ELSE appointment.customer_id END,
			       COALESCE(
			           NULLIF(btrim(concat_ws(' ', customer.first_name, customer.last_name)), ''),
			           NULLIF(btrim(customer.company_name), ''),
			           NULLIF(btrim(concat_ws(' ',
			               appointment.customer_snapshot->>'first_name',
			               appointment.customer_snapshot->>'last_name'
			           )), ''),
			           NULLIF(btrim(appointment.customer_snapshot->>'company_name'), ''),
			           'Client'
			       ),
			       COALESCE(
			           NULLIF(btrim(concat_ws(' ', vehicle.registration_plate,
			               vehicle.make, vehicle.model)), ''),
			           NULLIF(btrim(concat_ws(' ',
			               appointment.vehicle_snapshot->>'registration_plate',
			               appointment.vehicle_snapshot->>'make',
			               appointment.vehicle_snapshot->>'model'
			           )), ''),
			           ''
			       ),
			       COALESCE(service.name, ''),
			       COALESCE(resource.name, ''),
			       appointment.status,
			       appointment.source,
			       COALESCE(appointment.customer_note, '')
			FROM appointments appointment
			LEFT JOIN customers customer
			  ON customer.tenant_id = appointment.tenant_id
			 AND customer.id = appointment.customer_id
			LEFT JOIN vehicles vehicle
			  ON vehicle.tenant_id = appointment.tenant_id
			 AND vehicle.id = appointment.vehicle_id
			LEFT JOIN service_offerings service
			  ON service.tenant_id = appointment.tenant_id
			 AND service.id = appointment.service_id
			LEFT JOIN bookable_resources resource
			  ON resource.tenant_id = appointment.tenant_id
			 AND resource.id = appointment.resource_id
			WHERE appointment.tenant_id = $1
			  AND appointment.location_id = $2
			  AND appointment.starts_at >= $3
			  AND appointment.starts_at < $4
			ORDER BY appointment.starts_at, appointment.id`,
			tenantID, location.ID, start, end,
		)
		if err != nil {
			return err
		}
		defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
		for rows.Next() {
			var appointment Appointment
			var customerID sql.NullString
			if err := rows.Scan(
				&appointment.ID, &appointment.StartsAt, &appointment.EndsAt,
				&customerID, &appointment.CustomerName, &appointment.VehicleLabel,
				&appointment.ServiceName, &appointment.ResourceName,
				&appointment.Status, &appointment.Source, &appointment.Note,
			); err != nil {
				return err
			}
			appointment.CustomerID = customerID.String
			appointment.StartsAt = appointment.StartsAt.In(location.Timezone)
			appointment.EndsAt = appointment.EndsAt.In(location.Timezone)
			page.Appointments = append(page.Appointments, appointment)
		}
		return rows.Err()
	})
	return page, err
}

func (s *Store) Form(
	ctx context.Context,
	tenantID string,
	userID string,
	appointmentID string,
	customerID string,
	locationID string,
) (page FormPage, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) error {
		if appointmentID != "" {
			if err := tx.QueryRowContext(ctx, `
				SELECT location_id::text
				FROM appointments
				WHERE tenant_id = $1 AND id = $2
				  AND app_current_user_can_access_location(location_id)`,
				tenantID, appointmentID,
			).Scan(&locationID); err != nil {
				return err
			}
		}

		organization, locations, location, err := loadLocationContext(
			ctx, tx, tenantID, locationID,
		)
		if err != nil {
			return err
		}
		page.ID = appointmentID
		page.Organization = organization
		page.LocationID = location.ID
		page.LocationName = location.Name
		page.Locations = locations
		page.CanManage = true
		page.FieldErrors = make(map[string]string)

		if appointmentID == "" {
			page.Values.Date = time.Now().In(location.Timezone).Format(DateLayout)
			page.Values.StartTime = "09:00"
			if customerID != "" {
				customer, err := loadCustomer(ctx, tx, tenantID, customerID)
				if err != nil {
					return err
				}
				page.Customer = customer
			}
		} else {
			var startsAt time.Time
			var customer, vehicle, service, resource sql.NullString
			var status string
			if err := tx.QueryRowContext(ctx, `
				SELECT appointment.customer_id::text,
				       COALESCE(
				           NULLIF(btrim(concat_ws(' ', customer.first_name, customer.last_name)), ''),
				           NULLIF(btrim(customer.company_name), ''),
				           NULLIF(btrim(concat_ws(' ',
				               appointment.customer_snapshot->>'first_name',
				               appointment.customer_snapshot->>'last_name'
				           )), ''),
				           NULLIF(btrim(appointment.customer_snapshot->>'company_name'), ''),
				           'Client'
				       ),
				       appointment.vehicle_id::text,
				       appointment.service_id::text,
				       appointment.resource_id::text,
				       appointment.starts_at,
				       COALESCE(appointment.customer_note, ''),
				       appointment.status
				FROM appointments appointment
				LEFT JOIN customers customer
				  ON customer.tenant_id = appointment.tenant_id
				 AND customer.id = appointment.customer_id
				WHERE appointment.tenant_id = $1 AND appointment.id = $2`,
				tenantID, appointmentID,
			).Scan(
				&customer, &page.Customer.Label, &vehicle,
				&service, &resource, &startsAt,
				&page.Values.Note, &status,
			); err != nil {
				return err
			}
			page.Customer.ID = customer.String
			page.Values.VehicleID = vehicle.String
			page.Values.ServiceID = service.String
			page.Values.ResourceID = resource.String
			page.Values.Date = startsAt.In(location.Timezone).Format(DateLayout)
			page.Values.StartTime = startsAt.In(location.Timezone).Format("15:04")
			page.Cancellable = status != "cancelled" && status != "completed"
		}

		if page.Customer.ID != "" {
			vehicles, err := loadVehicleOptions(ctx, tx, tenantID, page.Customer.ID)
			if err != nil {
				return err
			}
			page.Vehicles = vehicles
		}
		if page.Services, err = loadServiceOptions(ctx, tx, tenantID, location.ID); err != nil {
			return err
		}
		if page.Resources, err = loadResourceOptions(ctx, tx, tenantID, location.ID); err != nil {
			return err
		}
		return nil
	})
	return page, err
}

func (s *Store) Save(
	ctx context.Context,
	tenantID string,
	userID string,
	appointmentID string,
	input SaveInput,
) (date string, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) error {
		var timezoneName string
		if err := tx.QueryRowContext(ctx, `
			SELECT timezone
			FROM locations
			WHERE tenant_id = $1 AND id = $2 AND status = 'active'
			  AND app_current_user_can_access_location(id)`,
			tenantID, input.LocationID,
		).Scan(&timezoneName); err != nil {
			return err
		}
		zone, err := time.LoadLocation(timezoneName)
		if err != nil {
			return err
		}
		startsAt, err := time.ParseInLocation(
			DateLayout+" 15:04", input.Date+" "+input.StartTime, zone,
		)
		if err != nil {
			return &FieldError{Field: FieldDate, Message: "Choisissez une date et une heure valides."}
		}
		date = startsAt.Format(DateLayout)

		if err := requireVehicle(ctx, tx, tenantID, input.CustomerID, input.VehicleID); err != nil {
			return err
		}
		var duration, before, after int
		if err := tx.QueryRowContext(ctx, `
			SELECT duration_minutes, buffer_before_minutes, buffer_after_minutes
			FROM service_offerings
			WHERE tenant_id = $1 AND location_id = $2 AND id = $3 AND active`,
			tenantID, input.LocationID, input.ServiceID,
		).Scan(&duration, &before, &after); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return &FieldError{Field: FieldService, Message: "Choisissez une prestation disponible dans cet établissement."}
			}
			return err
		}
		var resourceName string
		if err := tx.QueryRowContext(ctx, `
			SELECT name FROM bookable_resources
			WHERE tenant_id = $1 AND location_id = $2 AND id = $3 AND active`,
			tenantID, input.LocationID, input.ResourceID,
		).Scan(&resourceName); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return &FieldError{Field: FieldResource, Message: "Choisissez une ressource disponible dans cet établissement."}
			}
			return err
		}
		endsAt := startsAt.Add(time.Duration(duration+before+after) * time.Minute)

		if appointmentID == "" {
			if err := tx.QueryRowContext(ctx, `
				INSERT INTO appointments (
				    tenant_id, location_id, customer_id, vehicle_id, service_id,
				    resource_id, starts_at, ends_at, source, customer_note
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'dashboard', NULLIF($9, ''))
				RETURNING id::text`,
				tenantID, input.LocationID, input.CustomerID, input.VehicleID,
				input.ServiceID, input.ResourceID, startsAt, endsAt, input.Note,
			).Scan(&appointmentID); err != nil {
				if isExclusionViolation(err) {
					return &ConflictError{Resource: resourceName, StartsAt: startsAt, EndsAt: endsAt}
				}
				return err
			}
			return nil
		}

		result, err := tx.ExecContext(ctx, `
			UPDATE appointments
			SET service_id = $1, resource_id = $2, starts_at = $3, ends_at = $4,
			    customer_note = NULLIF($5, ''), updated_at = now()
			WHERE tenant_id = $6 AND id = $7
			  AND location_id = $8 AND customer_id = $9 AND vehicle_id = $10
			  AND status NOT IN ('cancelled', 'completed')`,
			input.ServiceID, input.ResourceID, startsAt, endsAt, input.Note,
			tenantID, appointmentID, input.LocationID, input.CustomerID, input.VehicleID,
		)
		if err != nil {
			if isExclusionViolation(err) {
				return &ConflictError{Resource: resourceName, StartsAt: startsAt, EndsAt: endsAt}
			}
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return sql.ErrNoRows
		}
		return nil
	})
	return date, err
}

func (s *Store) Cancel(
	ctx context.Context,
	tenantID string,
	userID string,
	appointmentID string,
) (date string, locationID string, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx *sql.Tx) error {
		var startsAt time.Time
		var timezoneName, status string
		if err := tx.QueryRowContext(ctx, `
			SELECT appointment.starts_at, location.timezone,
			       appointment.location_id::text, appointment.status
			FROM appointments appointment
			JOIN locations location
			  ON location.tenant_id = appointment.tenant_id
			 AND location.id = appointment.location_id
			WHERE appointment.tenant_id = $1 AND appointment.id = $2
			  AND app_current_user_can_access_location(appointment.location_id)`,
			tenantID, appointmentID,
		).Scan(&startsAt, &timezoneName, &locationID, &status); err != nil {
			return err
		}
		if status == "completed" {
			return ErrForbidden
		}
		if status != "cancelled" {
			result, err := tx.ExecContext(ctx, `
				UPDATE appointments
				SET status = 'cancelled', cancelled_at = now(), updated_at = now()
				WHERE tenant_id = $1 AND id = $2`, tenantID, appointmentID,
			)
			if err != nil {
				return err
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if affected != 1 {
				return sql.ErrNoRows
			}
		}
		zone, err := time.LoadLocation(timezoneName)
		if err != nil {
			return err
		}
		date = startsAt.In(zone).Format(DateLayout)
		return nil
	})
	return date, locationID, err
}

func loadLocationContext(
	ctx context.Context,
	tx *sql.Tx,
	tenantID string,
	requestedID string,
) (organization string, options []Option, selected locationRow, err error) {
	if err = tx.QueryRowContext(
		ctx, `SELECT name FROM tenants WHERE id = $1`, tenantID,
	).Scan(&organization); err != nil {
		return "", nil, locationRow{}, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id::text, name, timezone
		FROM locations
		WHERE tenant_id = $1 AND status = 'active'
		  AND app_current_user_can_access_location(id)
		ORDER BY name, id`, tenantID,
	)
	if err != nil {
		return "", nil, locationRow{}, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var found bool
	for rows.Next() {
		var row locationRow
		var timezoneName string
		if err := rows.Scan(&row.ID, &row.Name, &timezoneName); err != nil {
			return "", nil, locationRow{}, err
		}
		row.Timezone, err = time.LoadLocation(timezoneName)
		if err != nil {
			return "", nil, locationRow{}, err
		}
		options = append(options, Option{Value: row.ID, Label: row.Name})
		if (!found && requestedID == "") || row.ID == requestedID {
			selected = row
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return "", nil, locationRow{}, err
	}
	if !found {
		return "", nil, locationRow{}, sql.ErrNoRows
	}
	return organization, options, selected, nil
}

func loadCustomer(
	ctx context.Context,
	tx *sql.Tx,
	tenantID string,
	customerID string,
) (customer CustomerRef, err error) {
	err = tx.QueryRowContext(ctx, `
		SELECT id::text,
		       COALESCE(
		           NULLIF(btrim(concat_ws(' ', first_name, last_name)), ''),
		           NULLIF(btrim(company_name), ''), 'Client'
		       )
		FROM customers
		WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL`,
		tenantID, customerID,
	).Scan(&customer.ID, &customer.Label)
	return customer, err
}

func loadVehicleOptions(
	ctx context.Context,
	tx *sql.Tx,
	tenantID string,
	customerID string,
) (options []Option, err error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id::text,
		       btrim(concat_ws(' ', registration_plate, make, model))
		FROM vehicles
		WHERE tenant_id = $1 AND customer_id = $2 AND deleted_at IS NULL
		ORDER BY created_at, id`, tenantID, customerID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var option Option
		if err := rows.Scan(&option.Value, &option.Label); err != nil {
			return nil, err
		}
		options = append(options, option)
	}
	return options, rows.Err()
}

func loadServiceOptions(
	ctx context.Context,
	tx *sql.Tx,
	tenantID string,
	locationID string,
) (options []Option, err error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id::text, name, duration_minutes
		FROM service_offerings
		WHERE tenant_id = $1 AND location_id = $2 AND active
		ORDER BY name, id`, tenantID, locationID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var option Option
		var duration int
		if err := rows.Scan(&option.Value, &option.Label, &duration); err != nil {
			return nil, err
		}
		option.Label += " · " + ui.FormatDuration(duration)
		options = append(options, option)
	}
	return options, rows.Err()
}

func loadResourceOptions(
	ctx context.Context,
	tx *sql.Tx,
	tenantID string,
	locationID string,
) (options []Option, err error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id::text, name
		FROM bookable_resources
		WHERE tenant_id = $1 AND location_id = $2 AND active
		ORDER BY name, id`, tenantID, locationID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var option Option
		if err := rows.Scan(&option.Value, &option.Label); err != nil {
			return nil, err
		}
		options = append(options, option)
	}
	return options, rows.Err()
}

func requireVehicle(
	ctx context.Context,
	tx *sql.Tx,
	tenantID string,
	customerID string,
	vehicleID string,
) error {
	var exists bool
	err := tx.QueryRowContext(ctx, `
		SELECT true
		FROM vehicles
		WHERE tenant_id = $1 AND id = $2 AND customer_id = $3
		  AND deleted_at IS NULL`, tenantID, vehicleID, customerID,
	).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return &FieldError{Field: FieldVehicle, Message: "Choisissez un véhicule appartenant à ce client."}
	}
	return err
}

func parseDay(value string, zone *time.Location) time.Time {
	day, err := time.ParseInLocation(DateLayout, value, zone)
	if err == nil {
		return day
	}
	now := time.Now().In(zone)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
}

func isExclusionViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23P01"
}

func conflictMessage(conflict *ConflictError) string {
	return fmt.Sprintf(
		"%s est déjà réservé entre %s et %s.",
		strings.TrimSpace(conflict.Resource),
		conflict.StartsAt.Format("15:04"), conflict.EndsAt.Format("15:04"),
	)
}
