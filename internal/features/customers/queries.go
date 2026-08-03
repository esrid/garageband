package customers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/db"
)

const searchLimit = 50

// ErrForbidden means the actor is not an owner/admin and therefore may not
// manage sharing or offboard a customer, regardless of location access.
var ErrForbidden = errors.New("customer management is forbidden")

// FieldError names one quick-create field that failed validation or hit a
// database constraint, so the handler can attach it to the right input
// instead of a page-level notice.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Message }

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
		if err != nil {
			return err
		}

		if err := tx.QueryRow(
			ctx, `SELECT app_current_user_manages_tenant()`,
		).Scan(&profile.CanManage); err != nil {
			return err
		}
		if !profile.CanManage {
			return nil
		}

		grantRows, err := tx.Query(ctx, `
			SELECT grant_row.id, location.name,
			       COALESCE(granter.name, granter.email),
			       grant_row.granted_at,
			       COALESCE(revoker.name, revoker.email, ''),
			       grant_row.revoked_at
			FROM customer_location_grants grant_row
			JOIN locations location
			  ON location.tenant_id = grant_row.tenant_id
			 AND location.id = grant_row.receiving_location_id
			JOIN users granter ON granter.id = grant_row.granted_by_user_id
			LEFT JOIN users revoker ON revoker.id = grant_row.revoked_by_user_id
			WHERE grant_row.tenant_id = $1 AND grant_row.customer_id = $2
			ORDER BY grant_row.granted_at DESC, grant_row.id DESC`, tenantID, customerID)
		if err != nil {
			return err
		}
		profile.Grants, err = pgx.CollectRows(grantRows, pgx.RowToStructByPos[Grant])
		if err != nil {
			return err
		}

		optionRows, err := tx.Query(ctx, `
			SELECT location.id, location.name
			FROM locations location
			WHERE location.tenant_id = $1 AND location.status = 'active'
			  AND location.id <> $3
			  AND NOT EXISTS (
			      SELECT 1 FROM customer_location_grants grant_row
			      WHERE grant_row.tenant_id = location.tenant_id
			        AND grant_row.customer_id = $2
			        AND grant_row.receiving_location_id = location.id
			        AND grant_row.revoked_at IS NULL
			  )
			ORDER BY location.name, location.id`,
			tenantID, customerID, profile.Customer.HomeLocationID)
		if err != nil {
			return err
		}
		profile.ShareOptions, err = pgx.CollectRows(optionRows, pgx.RowToStructByPos[LocationOption])
		return err
	})
	return profile, err
}

// requireCustomerManager gates sharing and offboarding to owners/admins,
// asking PostgreSQL's own app_current_user_manages_tenant() rather than
// re-deriving the role in Go — the same trick catalog.requireCatalogManager
// uses for the same reason: one source of truth for "who manages this
// tenant."
func requireCustomerManager(ctx context.Context, tx pgx.Tx) error {
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT app_current_user_manages_tenant()`).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

// Grant shares customer's dossier with receivingLocationID. The row's own
// source_location_id must be the customer's current home location (a
// database foreign key, not a Go check), so this reads it from customers
// rather than trusting a caller-supplied value.
func (s *Store) Grant(
	ctx context.Context, tenantID, userID, customerID, receivingLocationID string,
) (grantID string, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireCustomerManager(ctx, tx); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO customer_location_grants (
			    tenant_id, customer_id, source_location_id, receiving_location_id, granted_by_user_id
			)
			SELECT $1, customer.id, customer.home_location_id, $3, $4
			FROM customers customer
			WHERE customer.tenant_id = $1 AND customer.id = $2 AND customer.deleted_at IS NULL
			RETURNING id`,
			tenantID, customerID, receivingLocationID, userID,
		).Scan(&grantID)
	})
	return grantID, err
}

// Revoke ends one grant. The row itself is never deleted — revoked_at is the
// audit trail the profile screen shows.
func (s *Store) Revoke(ctx context.Context, tenantID, userID, grantID string) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireCustomerManager(ctx, tx); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE customer_location_grants
			SET revoked_by_user_id = $3, revoked_at = now()
			WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL`,
			tenantID, grantID, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

// Offboard soft-deletes a customer who has left and their active contacts.
// The active-contact unique index only applies WHERE deleted_at IS NULL, so
// the freed phone/email becomes assignable to a new customer on its own —
// no separate release step. Vehicles, timeline, and memories are untouched:
// this is a customer no longer served, not a GDPR erasure.
func (s *Store) Offboard(ctx context.Context, tenantID, userID, customerID string) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireCustomerManager(ctx, tx); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE customers SET deleted_at = now(), updated_at = now()
			WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL`,
			tenantID, customerID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return sql.ErrNoRows
		}
		_, err = tx.Exec(ctx, `
			UPDATE customer_contacts SET deleted_at = now()
			WHERE tenant_id = $1 AND customer_id = $2 AND deleted_at IS NULL`,
			tenantID, customerID)
		return err
	})
}

// CreateInput books a brand new customer plus, in the same write, their
// first vehicle - the booking form's vehicle picker has nothing to offer
// without one, and there is no separate "add a vehicle" step today.
// FirstName/LastName/Phone/Plate arrive already trimmed and normalized
// (the handler validates field-by-field before this ever runs, same split
// as agenda.validateInput before agenda.Store.Save).
type CreateInput struct {
	HomeLocationID string
	FirstName      string
	LastName       string
	Phone          string // normalized E.164, or ""
	Plate          string // normalized (uppercase, no space/dash)
}

// Create inserts the customer, an optional primary phone contact, and a
// vehicle, in one transaction. home_location_id must be a site the caller
// can access - the customer_insert RLS policy enforces that, so a bad
// HomeLocationID surfaces as a 42501 here rather than a silent write to a
// site the actor has no business owning a dossier at.
func (s *Store) Create(
	ctx context.Context, tenantID, userID string, input CreateInput,
) (id string, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO customers (tenant_id, home_location_id, first_name, last_name)
			VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''))
			RETURNING id`,
			tenantID, input.HomeLocationID, input.FirstName, input.LastName,
		).Scan(&id); err != nil {
			return mapCreateError(err)
		}
		if input.Phone != "" {
			if _, err := tx.Exec(ctx, `
				INSERT INTO customer_contacts (tenant_id, customer_id, kind, value, normalized_value, is_primary)
				VALUES ($1, $2, 'phone', $3, $3, true)`,
				tenantID, id, input.Phone,
			); err != nil {
				return mapCreateError(err)
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO vehicles (tenant_id, customer_id, location_id, registration_plate)
			VALUES ($1, $2, $3, $4)`,
			tenantID, id, input.HomeLocationID, input.Plate,
		); err != nil {
			return mapCreateError(err)
		}
		return nil
	})
	return id, err
}

// mapCreateError turns a constraint violation into a *FieldError the
// quick-create form can attach to the right input, the same way agenda maps
// its own write conflicts. Anything else (including a rejected RLS write)
// passes through unchanged for the handler's generic failure path.
func mapCreateError(err error) error {
	pgErr, ok := db.PgError(err)
	if !ok || pgErr.Code != "23505" {
		return err
	}
	switch pgErr.ConstraintName {
	case "customer_contacts_active_value_unique":
		return &FieldError{Field: FieldNewPhone, Message: "Ce téléphone est déjà utilisé par un autre client."}
	case "vehicles_active_plate_unique":
		return &FieldError{Field: FieldNewPlate, Message: "Cette plaque est déjà utilisée par un autre véhicule."}
	}
	return err
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
