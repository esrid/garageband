package customers

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/db"
)

const searchLimit = 50

var nonPhoneCharacters = regexp.MustCompile(`[^0-9+]`)
var nonPlateCharacters = regexp.MustCompile(`[-[:space:]]`)
var vinPattern = regexp.MustCompile(`^[A-HJ-NPR-Z0-9]{17}$`)

type Store struct{ db *db.DB }

func NewStore(database *db.DB) *Store { return &Store{db: database} }

func (s *Store) Search(
	ctx context.Context,
	tenantID string,
	userID string,
	query string,
) (page Page, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(
			ctx, `SELECT name FROM tenants WHERE id = $1`, tenantID,
		).Scan(&page.Organization); err != nil {
			return err
		}

		page.Query = query
		trimmed := strings.TrimSpace(query)
		if trimmed == "" {
			return nil
		}
		namePattern := "%" + escapeLike(strings.ToLower(trimmed)) + "%"
		phone := normalizePhoneSearch(trimmed)
		email := normalizeEmailSearch(trimmed)
		plate := normalizePlateSearch(trimmed)
		vin := normalizeVINSearch(trimmed)

		rows, err := tx.Query(ctx, `
			SELECT customer.id, customer.home_location_id,
			       COALESCE(customer.first_name, ''),
			       COALESCE(customer.last_name, ''),
			       COALESCE(customer.company_name, ''),
			       COALESCE((
			           SELECT contact.value
			           FROM customer_contacts contact
			           WHERE contact.tenant_id = customer.tenant_id
			             AND contact.customer_id = customer.id
			             AND contact.kind = 'phone'
			             AND contact.is_primary
			             AND contact.deleted_at IS NULL
			           ORDER BY contact.created_at, contact.id
			           LIMIT 1
			       ), ''),
			       COALESCE((
			           SELECT contact.value
			           FROM customer_contacts contact
			           WHERE contact.tenant_id = customer.tenant_id
			             AND contact.customer_id = customer.id
			             AND contact.kind = 'email'
			             AND contact.is_primary
			             AND contact.deleted_at IS NULL
			           ORDER BY contact.created_at, contact.id
			           LIMIT 1
			       ), ''),
			       home_location.name,
			       NOT app_current_user_can_access_location(customer.home_location_id),
			       COALESCE((
			           SELECT jsonb_agg(
			               jsonb_build_object(
			                   'plate', COALESCE(vehicle.registration_plate, ''),
			                   'model', btrim(
			                       COALESCE(vehicle.make, '') || ' ' ||
			                       COALESCE(vehicle.model, '')
			                   )
			               ) ORDER BY vehicle.created_at, vehicle.id
			           )
			           FROM vehicles vehicle
			           WHERE vehicle.tenant_id = customer.tenant_id
			             AND vehicle.customer_id = customer.id
			             AND vehicle.deleted_at IS NULL
			       ), '[]'::JSONB)
			FROM customers customer
			JOIN locations home_location
			  ON home_location.tenant_id = customer.tenant_id
			 AND home_location.id = customer.home_location_id
			WHERE customer.tenant_id = $1
			  AND customer.deleted_at IS NULL
			  AND (
			      customer.search_name ILIKE $2 ESCAPE '\'
			      OR ($3 <> '' AND EXISTS (
			          SELECT 1 FROM customer_contacts contact
			          WHERE contact.tenant_id = customer.tenant_id
			            AND contact.customer_id = customer.id
			            AND contact.normalized_value = $3
			            AND contact.deleted_at IS NULL
			      ))
			      OR ($4 <> '' AND EXISTS (
			          SELECT 1 FROM customer_contacts contact
			          WHERE contact.tenant_id = customer.tenant_id
			            AND contact.customer_id = customer.id
			            AND contact.normalized_value = $4
			            AND contact.deleted_at IS NULL
			      ))
			      OR ($5 <> '' AND EXISTS (
			          SELECT 1 FROM vehicles vehicle
			          WHERE vehicle.tenant_id = customer.tenant_id
			            AND vehicle.customer_id = customer.id
			            AND vehicle.registration_plate_compact = $5
			            AND vehicle.deleted_at IS NULL
			      ))
			      OR ($6 <> '' AND EXISTS (
			          SELECT 1 FROM vehicles vehicle
			          WHERE vehicle.tenant_id = customer.tenant_id
			            AND vehicle.customer_id = customer.id
			            AND vehicle.vin = $6
			            AND vehicle.deleted_at IS NULL
			      ))
			  )
			ORDER BY COALESCE(customer.company_name, ''),
			         COALESCE(customer.last_name, ''),
			         COALESCE(customer.first_name, ''), customer.id
			LIMIT $7`,
			tenantID, namePattern, phone, email, plate, vin, searchLimit,
		)
		if err != nil {
			return err
		}
		page.Customers, err = pgx.CollectRows(rows, scanSearchCustomer)
		return err
	})
	return page, err
}

func (s *Store) Profile(
	ctx context.Context,
	tenantID string,
	userID string,
	customerID string,
) (profile Profile, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(
			ctx, `SELECT name FROM tenants WHERE id = $1`, tenantID,
		).Scan(&profile.Organization); err != nil {
			return err
		}

		if err := tx.QueryRow(ctx, `
			SELECT customer.id, customer.home_location_id,
			       COALESCE(customer.first_name, ''),
			       COALESCE(customer.last_name, ''),
			       COALESCE(customer.company_name, ''),
			       COALESCE((
			           SELECT contact.value FROM customer_contacts contact
			           WHERE contact.tenant_id = customer.tenant_id
			             AND contact.customer_id = customer.id
			             AND contact.kind = 'phone' AND contact.is_primary
			             AND contact.deleted_at IS NULL
			           ORDER BY contact.created_at, contact.id LIMIT 1
			       ), ''),
			       COALESCE((
			           SELECT contact.value FROM customer_contacts contact
			           WHERE contact.tenant_id = customer.tenant_id
			             AND contact.customer_id = customer.id
			             AND contact.kind = 'email' AND contact.is_primary
			             AND contact.deleted_at IS NULL
			           ORDER BY contact.created_at, contact.id LIMIT 1
			       ), ''),
			       home_location.name,
			       NOT app_current_user_can_access_location(customer.home_location_id)
			FROM customers customer
			JOIN locations home_location
			  ON home_location.tenant_id = customer.tenant_id
			 AND home_location.id = customer.home_location_id
			WHERE customer.tenant_id = $1 AND customer.id = $2
			  AND customer.deleted_at IS NULL`, tenantID, customerID,
		).Scan(
			&profile.Customer.ID, &profile.Customer.HomeLocationID,
			&profile.Customer.FirstName, &profile.Customer.LastName,
			&profile.Customer.CompanyName, &profile.Customer.Phone,
			&profile.Customer.Email, &profile.Customer.HomeLocationName,
			&profile.Customer.Shared,
		); err != nil {
			return err
		}
		profile.CanEdit = !profile.Customer.Shared

		vehicleRows, err := tx.Query(ctx, `
			SELECT id, COALESCE(registration_plate, ''),
			       COALESCE(make, ''), COALESCE(model, ''),
			       COALESCE(EXTRACT(YEAR FROM first_registration_on)::INTEGER, 0),
			       COALESCE(vin, '')
			FROM vehicles
			WHERE tenant_id = $1 AND customer_id = $2 AND deleted_at IS NULL
			ORDER BY created_at, id`, tenantID, customerID)
		if err != nil {
			return err
		}
		profile.Vehicles, err = pgx.CollectRows(vehicleRows, pgx.RowToStructByPos[ProfileVehicle])
		if err != nil {
			return err
		}

		eventRows, err := tx.Query(ctx, `
			SELECT event_id, event_kind, event_at, title, vehicle_label,
			       status, location_name, authored_here, amount_cents, currency
			FROM (
			    SELECT appointment.id AS event_id,
			           'appointment'::TEXT AS event_kind,
			           appointment.starts_at AS event_at,
			           COALESCE(service.name, appointment.customer_note, '') AS title,
			           COALESCE(vehicle.registration_plate, '') AS vehicle_label,
			           appointment.status, location.name AS location_name,
			           app_current_user_can_access_location(appointment.location_id)
			               AS authored_here,
			           0::INTEGER AS amount_cents, ''::TEXT AS currency
			    FROM appointments appointment
			    JOIN locations location
			      ON location.tenant_id = appointment.tenant_id
			     AND location.id = appointment.location_id
			    LEFT JOIN service_offerings service
			      ON service.tenant_id = appointment.tenant_id
			     AND service.id = appointment.service_id
			    LEFT JOIN vehicles vehicle
			      ON vehicle.tenant_id = appointment.tenant_id
			     AND vehicle.id = appointment.vehicle_id
			    WHERE appointment.tenant_id = $1
			      AND appointment.customer_id = $2
			    UNION ALL
			    SELECT repair.id, 'repair'::TEXT, repair.opened_at,
			           COALESCE(
			               NULLIF(btrim(repair.work_performed), ''),
			               NULLIF(btrim(repair.diagnosis), ''),
			               NULLIF(btrim(repair.customer_complaint), ''), ''
			           ),
			           COALESCE(vehicle.registration_plate, ''), repair.status,
			           location.name,
			           app_current_user_can_access_location(repair.location_id),
			           repair.total_cents, repair.currency
			    FROM repair_orders repair
			    JOIN locations location
			      ON location.tenant_id = repair.tenant_id
			     AND location.id = repair.location_id
			    LEFT JOIN vehicles vehicle
			      ON vehicle.tenant_id = repair.tenant_id
			     AND vehicle.id = repair.vehicle_id
			    WHERE repair.tenant_id = $1 AND repair.customer_id = $2
			) event
			ORDER BY event_at DESC, event_id DESC`, tenantID, customerID)
		if err != nil {
			return err
		}
		profile.Timeline, err = pgx.CollectRows(eventRows, pgx.RowToStructByPos[Event])
		if err != nil {
			return err
		}

		memoryRows, err := tx.Query(ctx, `
			SELECT key,
			       CASE jsonb_typeof(value)
			           WHEN 'string' THEN value #>> '{}'
			           ELSE value::TEXT
			       END,
			       status, COALESCE(confidence::DOUBLE PRECISION, 0)
			FROM customer_memories
			WHERE tenant_id = $1 AND customer_id = $2
			ORDER BY created_at DESC, id DESC`, tenantID, customerID)
		if err != nil {
			return err
		}
		profile.Memories, err = pgx.CollectRows(memoryRows, pgx.RowToStructByPos[Memory])
		return err
	})
	return profile, err
}

func scanSearchCustomer(row pgx.CollectableRow) (Customer, error) {
	var customer Customer
	var vehiclesJSON []byte
	if err := row.Scan(
		&customer.ID, &customer.HomeLocationID,
		&customer.FirstName, &customer.LastName, &customer.CompanyName,
		&customer.Phone, &customer.Email, &customer.HomeLocationName,
		&customer.Shared, &vehiclesJSON,
	); err != nil {
		return Customer{}, err
	}
	if err := json.Unmarshal(vehiclesJSON, &customer.Vehicles); err != nil {
		return Customer{}, err
	}
	return customer, nil
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func normalizePhoneSearch(value string) string {
	phone := nonPhoneCharacters.ReplaceAllString(value, "")
	switch {
	case strings.HasPrefix(phone, "00"):
		phone = "+" + strings.TrimPrefix(phone, "00")
	case strings.HasPrefix(phone, "0") && len(phone) == 10:
		phone = "+33" + phone[1:]
	case !strings.HasPrefix(phone, "+") && len(phone) >= 8:
		phone = "+" + phone
	}
	if len(phone) < 9 || len(phone) > 16 || !strings.HasPrefix(phone, "+") {
		return ""
	}
	return phone
}

func normalizeEmailSearch(value string) string {
	email := strings.ToLower(strings.TrimSpace(value))
	if !strings.Contains(email, "@") {
		return ""
	}
	return email
}

func normalizePlateSearch(value string) string {
	plate := strings.ToUpper(strings.TrimSpace(value))
	return nonPlateCharacters.ReplaceAllString(plate, "")
}

func normalizeVINSearch(value string) string {
	vin := strings.ToUpper(strings.TrimSpace(value))
	if !vinPattern.MatchString(vin) {
		return ""
	}
	return vin
}
