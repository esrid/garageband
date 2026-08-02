package agenda

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/ui"
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
	LocationID  string
	CustomerID  string
	VehicleID   string
	ServiceID   string
	ResourceID  string
	ResourceIDs []string
	Date        string
	StartTime   string
	Note        string
}

type AvailabilitySlot struct {
	StartsAt time.Time
	EndsAt   time.Time
}

type AvailabilityResult struct {
	ScheduleConfigured bool
	OpenThisDay        bool
	AutoAllocated      bool
	Slots              []AvailabilitySlot
}

type resourceRequirement struct {
	Kind     string
	Quantity int
}

type Store struct{ db *db.DB }

func NewStore(database *db.DB) *Store { return &Store{db: database} }

func (s *Store) Availability(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	serviceID string,
	resourceIDs []string,
	dateValue string,
) (result AvailabilityResult, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		var timezoneName string
		if err := tx.QueryRow(ctx, `
			SELECT timezone, availability_schedule_enabled FROM locations
			WHERE tenant_id = $1 AND id = $2 AND status = 'active'
			  AND app_current_user_can_access_location(id)`, tenantID, locationID,
		).Scan(&timezoneName, &result.ScheduleConfigured); err != nil {
			return err
		}
		zone, err := time.LoadLocation(timezoneName)
		if err != nil {
			return err
		}
		day, err := time.ParseInLocation(DateLayout, dateValue, zone)
		if err != nil {
			return &FieldError{Field: FieldDate, Message: "Choisissez une date valide."}
		}
		var duration, before, after int
		if err := tx.QueryRow(ctx, `
			SELECT duration_minutes, buffer_before_minutes, buffer_after_minutes
			FROM service_offerings
			WHERE tenant_id = $1 AND location_id = $2 AND id = $3 AND active`,
			tenantID, locationID, serviceID,
		).Scan(&duration, &before, &after); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return &FieldError{Field: FieldService, Message: "Choisissez une prestation disponible dans cet établissement."}
			}
			return err
		}
		requirements, err := loadResourceRequirements(ctx, tx, tenantID, locationID, serviceID)
		if err != nil {
			return err
		}
		result.AutoAllocated = len(requirements) != 0
		if !result.AutoAllocated {
			var resourceCount int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM bookable_resources
				WHERE tenant_id = $1 AND location_id = $2 AND id::text = ANY($3::text[]) AND active`,
				tenantID, locationID, resourceIDs,
			).Scan(&resourceCount); err != nil {
				return err
			}
			if resourceCount == 0 || resourceCount != len(resourceIDs) {
				return &FieldError{Field: FieldResource, Message: "Choisissez uniquement des ressources disponibles dans cet établissement."}
			}
		}

		weekday := int(day.Weekday())
		if !result.ScheduleConfigured {
			return nil
		}
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
			    SELECT 1 FROM location_opening_hours
			    WHERE tenant_id = $1 AND location_id = $2 AND weekday = $3
			)`, tenantID, locationID, weekday,
		).Scan(&result.OpenThisDay); err != nil {
			return err
		}
		if !result.OpenThisDay {
			return nil
		}

		totalMinutes := duration + before + after
		rows, err := tx.Query(ctx, `
			WITH location_context AS (
			    SELECT timezone FROM locations
			    WHERE tenant_id = $1 AND id = $2
			), opening_windows AS (
			    SELECT (($4::date + opening.opens_at) AT TIME ZONE location_context.timezone) AS window_start,
			           (($4::date + opening.closes_at) AT TIME ZONE location_context.timezone) AS window_end
			    FROM location_opening_hours opening
			    CROSS JOIN location_context
			    WHERE opening.tenant_id = $1 AND opening.location_id = $2 AND opening.weekday = $5
			), candidates AS (
			    SELECT slot_start,
			           slot_start + make_interval(mins => $6) AS slot_end
			    FROM opening_windows
			    CROSS JOIN LATERAL generate_series(
			        window_start,
			        window_end - make_interval(mins => $6),
			        interval '15 minutes'
			    ) slot_start
			)
			SELECT candidate.slot_start, candidate.slot_end
			FROM candidates candidate
			WHERE candidate.slot_start >= now()
			  AND (
			      ($7::boolean AND NOT EXISTS (
			          SELECT 1
			          FROM service_resource_requirements requirement
			          WHERE requirement.tenant_id = $1
			            AND requirement.location_id = $2
			            AND requirement.service_id = $8
			            AND (
			                SELECT count(*)
			                FROM bookable_resources resource
			                WHERE resource.tenant_id = $1
			                  AND resource.location_id = $2
			                  AND resource.kind = requirement.resource_kind
			                  AND resource.active
			                  AND NOT EXISTS (
			                      SELECT 1
			                      FROM appointment_resource_reservations reservation
			                      WHERE reservation.tenant_id = resource.tenant_id
			                        AND reservation.resource_id = resource.id
			                        AND reservation.status IN ('pending', 'confirmed', 'in_progress')
			                        AND reservation.occupied_during && tstzrange(candidate.slot_start, candidate.slot_end, '[)')
			                  )
			            ) < requirement.quantity
			      ))
			      OR (NOT $7::boolean AND NOT EXISTS (
			          SELECT 1 FROM appointment_resource_reservations reservation
			          WHERE reservation.tenant_id = $1
			            AND reservation.location_id = $2
			            AND reservation.resource_id::text = ANY($3::text[])
			            AND reservation.status IN ('pending', 'confirmed', 'in_progress')
			            AND reservation.occupied_during && tstzrange(candidate.slot_start, candidate.slot_end, '[)')
			      ))
			  )
			  AND NOT EXISTS (
			      SELECT 1 FROM location_closures closure
			      WHERE closure.tenant_id = $1 AND closure.location_id = $2
			        AND closure.closed_during && tstzrange(candidate.slot_start, candidate.slot_end, '[)')
			  )
			ORDER BY candidate.slot_start
			LIMIT 96`, tenantID, locationID, resourceIDs, day.Format(DateLayout), weekday, totalMinutes, result.AutoAllocated, serviceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var slot AvailabilitySlot
			if err := rows.Scan(&slot.StartsAt, &slot.EndsAt); err != nil {
				return err
			}
			slot.StartsAt, slot.EndsAt = slot.StartsAt.In(zone), slot.EndsAt.In(zone)
			result.Slots = append(result.Slots, slot)
		}
		return rows.Err()
	})
	return result, err
}

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
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
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
		rows, err := tx.Query(ctx, `
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
			       COALESCE((
			           SELECT string_agg(reserved_resource.name, ', ' ORDER BY reserved_resource.name)
			           FROM appointment_resource_reservations reservation
			           JOIN bookable_resources reserved_resource
			             ON reserved_resource.tenant_id = reservation.tenant_id
			            AND reserved_resource.id = reservation.resource_id
			           WHERE reservation.tenant_id = appointment.tenant_id
			             AND reservation.appointment_id = appointment.id
			       ), resource.name, ''),
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
		defer rows.Close()
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
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if appointmentID != "" {
			if err := tx.QueryRow(ctx, `
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
			if err := tx.QueryRow(ctx, `
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
			resourceRows, err := tx.Query(ctx, `
				SELECT resource_id::text
				FROM appointment_resource_reservations
				WHERE tenant_id = $1 AND appointment_id = $2
				ORDER BY resource_id`, tenantID, appointmentID)
			if err != nil {
				return err
			}
			page.Values.ResourceIDs, err = pgx.CollectRows(resourceRows, pgx.RowTo[string])
			if err != nil {
				return err
			}
			if len(page.Values.ResourceIDs) == 0 && resource.Valid {
				page.Values.ResourceIDs = []string{resource.String}
			}
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
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		var timezoneName string
		if err := tx.QueryRow(ctx, `
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
		if err := tx.QueryRow(ctx, `
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
		endsAt := startsAt.Add(time.Duration(duration+before+after) * time.Minute)
		requirements, err := loadResourceRequirements(ctx, tx, tenantID, input.LocationID, input.ServiceID)
		if err != nil {
			return err
		}
		var resourceIDs, resourceNames []string
		if len(requirements) != 0 {
			resourceIDs, resourceNames, err = allocateRequiredResources(
				ctx, tx, tenantID, input.LocationID, appointmentID,
				startsAt, endsAt, requirements,
			)
		} else {
			resourceIDs = selectedResourceIDs(input)
			resourceNames, err = lockSelectedResources(ctx, tx, tenantID, input.LocationID, resourceIDs)
		}
		if err != nil {
			return err
		}
		input.ResourceID = resourceIDs[0]
		resourceName := strings.Join(resourceNames, ", ")

		if appointmentID == "" {
			if err := tx.QueryRow(ctx, `
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
				if fieldError := workingTimeFieldError(err); fieldError != nil {
					return fieldError
				}
				return err
			}
			if err := insertAppointmentResources(
				ctx, tx, tenantID, input.LocationID, appointmentID,
				resourceIDs, startsAt, endsAt, "pending",
			); err != nil {
				if isExclusionViolation(err) {
					return &ConflictError{Resource: resourceName, StartsAt: startsAt, EndsAt: endsAt}
				}
				return err
			}
			return nil
		}

		if _, err := tx.Exec(ctx, `
			DELETE FROM appointment_resource_reservations
			WHERE tenant_id = $1 AND appointment_id = $2`, tenantID, appointmentID); err != nil {
			return err
		}

		var appointmentStatus string
		err = tx.QueryRow(ctx, `
			UPDATE appointments
			SET service_id = $1, resource_id = $2, starts_at = $3, ends_at = $4,
			    customer_note = NULLIF($5, ''), updated_at = now()
			WHERE tenant_id = $6 AND id = $7
			  AND location_id = $8 AND customer_id = $9 AND vehicle_id = $10
			  AND status NOT IN ('cancelled', 'completed')
			RETURNING status`,
			input.ServiceID, input.ResourceID, startsAt, endsAt, input.Note,
			tenantID, appointmentID, input.LocationID, input.CustomerID, input.VehicleID,
		).Scan(&appointmentStatus)
		if err != nil {
			if isExclusionViolation(err) {
				return &ConflictError{Resource: resourceName, StartsAt: startsAt, EndsAt: endsAt}
			}
			if fieldError := workingTimeFieldError(err); fieldError != nil {
				return fieldError
			}
			return err
		}
		if err := insertAppointmentResources(
			ctx, tx, tenantID, input.LocationID, appointmentID,
			resourceIDs, startsAt, endsAt, appointmentStatus,
		); err != nil {
			if isExclusionViolation(err) {
				return &ConflictError{Resource: resourceName, StartsAt: startsAt, EndsAt: endsAt}
			}
			return err
		}
		return nil
	})
	return date, err
}

func selectedResourceIDs(input SaveInput) []string {
	values := input.ResourceIDs
	if len(values) == 0 && input.ResourceID != "" {
		values = []string{input.ResourceID}
	}
	seen := make(map[string]struct{}, len(values))
	resourceIDs := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		resourceIDs = append(resourceIDs, value)
	}
	return resourceIDs
}

func loadResourceRequirements(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	locationID string,
	serviceID string,
) (requirements []resourceRequirement, err error) {
	rows, err := tx.Query(ctx, `
		SELECT resource_kind, quantity
		FROM service_resource_requirements
		WHERE tenant_id = $1 AND location_id = $2 AND service_id = $3
		ORDER BY resource_kind`, tenantID, locationID, serviceID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[resourceRequirement])
}

func allocateRequiredResources(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	locationID string,
	appointmentID string,
	startsAt time.Time,
	endsAt time.Time,
	requirements []resourceRequirement,
) (resourceIDs []string, names []string, err error) {
	for _, requirement := range requirements {
		rows, err := tx.Query(ctx, `
			SELECT resource.id::text, resource.name
			FROM bookable_resources resource
			WHERE resource.tenant_id = $1
			  AND resource.location_id = $2
			  AND resource.kind = $3
			  AND resource.active
			  AND NOT EXISTS (
			      SELECT 1
			      FROM appointment_resource_reservations reservation
			      WHERE reservation.tenant_id = resource.tenant_id
			        AND reservation.resource_id = resource.id
			        AND reservation.status IN ('pending', 'confirmed', 'in_progress')
			        AND reservation.occupied_during && tstzrange($4, $5, '[)')
			        AND ($6 = '' OR reservation.appointment_id::text <> $6)
			  )
			ORDER BY resource.id
			LIMIT $7
			FOR UPDATE SKIP LOCKED`,
			tenantID, locationID, requirement.Kind, startsAt, endsAt,
			appointmentID, requirement.Quantity)
		if err != nil {
			return nil, nil, err
		}
		found := 0
		for rows.Next() {
			var resourceID, name string
			if err := rows.Scan(&resourceID, &name); err != nil {
				rows.Close()
				return nil, nil, err
			}
			resourceIDs = append(resourceIDs, resourceID)
			names = append(names, name)
			found++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
		if found != requirement.Quantity {
			return nil, nil, &ConflictError{
				Resource: "L’ensemble de ressources requis",
				StartsAt: startsAt,
				EndsAt:   endsAt,
			}
		}
	}
	return resourceIDs, names, nil
}

func lockSelectedResources(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	locationID string,
	resourceIDs []string,
) (names []string, err error) {
	if len(resourceIDs) == 0 {
		return nil, &FieldError{Field: FieldResource, Message: "Choisissez au moins une ressource."}
	}
	rows, err := tx.Query(ctx, `
		SELECT name
		FROM bookable_resources
		WHERE tenant_id = $1 AND location_id = $2
		  AND id::text = ANY($3::text[]) AND active
		ORDER BY id
		FOR UPDATE`, tenantID, locationID, resourceIDs)
	if err != nil {
		return nil, err
	}
	names, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if len(names) != len(resourceIDs) {
		return nil, &FieldError{Field: FieldResource, Message: "Choisissez uniquement des ressources disponibles dans cet établissement."}
	}
	return names, nil
}

func insertAppointmentResources(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	locationID string,
	appointmentID string,
	resourceIDs []string,
	startsAt time.Time,
	endsAt time.Time,
	status string,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO appointment_resource_reservations (
		    tenant_id, location_id, appointment_id, resource_id,
		    starts_at, ends_at, status
		)
		SELECT $1, $2, $3, resource.id, $5, $6, $7
		FROM unnest($4::text[]) selected(resource_id)
		JOIN bookable_resources resource
		  ON resource.tenant_id = $1 AND resource.location_id = $2
		 AND resource.id::text = selected.resource_id
		ON CONFLICT (appointment_id, resource_id) DO UPDATE
		SET starts_at = EXCLUDED.starts_at,
		    ends_at = EXCLUDED.ends_at,
		    status = EXCLUDED.status`,
		tenantID, locationID, appointmentID, resourceIDs, startsAt, endsAt, status)
	return err
}

func (s *Store) Cancel(
	ctx context.Context,
	tenantID string,
	userID string,
	appointmentID string,
) (date string, locationID string, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		var startsAt time.Time
		var timezoneName, status string
		if err := tx.QueryRow(ctx, `
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
			result, err := tx.Exec(ctx, `
				UPDATE appointments
				SET status = 'cancelled', cancelled_at = now(), updated_at = now()
				WHERE tenant_id = $1 AND id = $2`, tenantID, appointmentID,
			)
			if err != nil {
				return err
			}
			if result.RowsAffected() != 1 {
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
	tx pgx.Tx,
	tenantID string,
	requestedID string,
) (organization string, options []Option, selected locationRow, err error) {
	if err = tx.QueryRow(
		ctx, `SELECT name FROM tenants WHERE id = $1`, tenantID,
	).Scan(&organization); err != nil {
		return "", nil, locationRow{}, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, name, timezone
		FROM locations
		WHERE tenant_id = $1 AND status = 'active'
		  AND app_current_user_can_access_location(id)
		ORDER BY name, id`, tenantID,
	)
	if err != nil {
		return "", nil, locationRow{}, err
	}
	defer rows.Close()
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
	tx pgx.Tx,
	tenantID string,
	customerID string,
) (customer CustomerRef, err error) {
	err = tx.QueryRow(ctx, `
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
	tx pgx.Tx,
	tenantID string,
	customerID string,
) (options []Option, err error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text,
		       btrim(concat_ws(' ', registration_plate, make, model))
		FROM vehicles
		WHERE tenant_id = $1 AND customer_id = $2 AND deleted_at IS NULL
		ORDER BY created_at, id`, tenantID, customerID,
	)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Option])
}

func loadServiceOptions(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	locationID string,
) (options []Option, err error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, name, duration_minutes
		FROM service_offerings
		WHERE tenant_id = $1 AND location_id = $2 AND active
		ORDER BY name, id`, tenantID, locationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
	tx pgx.Tx,
	tenantID string,
	locationID string,
) (options []Option, err error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, name
		FROM bookable_resources
		WHERE tenant_id = $1 AND location_id = $2 AND active
		ORDER BY name, id`, tenantID, locationID,
	)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Option])
}

func requireVehicle(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	customerID string,
	vehicleID string,
) error {
	var exists bool
	err := tx.QueryRow(ctx, `
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

func workingTimeFieldError(err error) *FieldError {
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "23514" {
		return nil
	}
	switch pgError.ConstraintName {
	case "appointments_within_opening_hours":
		return &FieldError{Field: FieldStartTime, Message: "Ce créneau est en dehors des horaires d’ouverture."}
	case "appointments_avoid_closures":
		return &FieldError{Field: FieldStartTime, Message: "L’atelier est fermé pendant ce créneau."}
	}
	return nil
}

func conflictMessage(conflict *ConflictError) string {
	return fmt.Sprintf(
		"%s est déjà réservé entre %s et %s.",
		strings.TrimSpace(conflict.Resource),
		conflict.StartsAt.Format("15:04"), conflict.EndsAt.Format("15:04"),
	)
}
