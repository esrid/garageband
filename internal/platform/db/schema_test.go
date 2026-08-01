package db_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/esrid/garageband/internal/platform/dbtest"
)

func TestTenantRLSIsolation(t *testing.T) {
	d := dbtest.Open(t)
	role := dbtest.RuntimeRole(t, d)

	tenantA := insertTenant(t, d, "alpha")
	tenantB := insertTenant(t, d, "bravo")
	insertLocation(t, d, tenantA, "alpha-shop")
	insertLocation(t, d, tenantB, "bravo-shop")

	assertVisibleLocations := func(tenantID string, want int) {
		t.Helper()
		var got int
		err := d.WithinTenant(t.Context(), tenantID, func(tx *sql.Tx) error {
			if err := dbtest.SetLocalRole(t.Context(), tx, role); err != nil {
				return err
			}
			return tx.QueryRowContext(
				t.Context(),
				`SELECT COUNT(*) FROM locations`,
			).Scan(&got)
		})
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("tenant %s sees %d locations, want %d", tenantID, got, want)
		}
	}
	assertVisibleLocations(tenantA, 1)
	assertVisibleLocations(tenantB, 1)

	err := d.WithinTenant(t.Context(), tenantA, func(tx *sql.Tx) error {
		if err := dbtest.SetLocalRole(t.Context(), tx, role); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), `
			INSERT INTO locations (tenant_id, slug, name)
			VALUES ($1, 'wrong-tenant', 'Wrong tenant')`, tenantB)
		return err
	})
	if err == nil {
		t.Fatal("RLS unexpectedly allowed a cross-tenant insert")
	}
}

func TestRuntimeRoleCanCreateTenantOnlyInsideNewTenantScope(t *testing.T) {
	d := dbtest.Open(t)
	role := dbtest.RuntimeRole(t, d)

	err := d.WithinNewTenant(t.Context(), func(tx *sql.Tx, tenantID string) error {
		if err := dbtest.SetLocalRole(t.Context(), tx, role); err != nil {
			return err
		}
		var insertedID string
		if err := tx.QueryRowContext(t.Context(), `
			INSERT INTO tenants (id, slug, name)
			VALUES ($1, 'onboarded', 'Onboarded garage')
			RETURNING id`, tenantID).Scan(&insertedID); err != nil {
			return err
		}
		if insertedID != tenantID {
			t.Fatalf("inserted tenant: got %s, want %s", insertedID, tenantID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUserScopedWorkspaceDiscoveryIsRLSIsolated(t *testing.T) {
	d := dbtest.Open(t)
	role := dbtest.RuntimeRole(t, d)

	userA := insertUser(t, d, "workspace-a@example.com")
	userB := insertUser(t, d, "workspace-b@example.com")
	tenantA := insertTenant(t, d, "workspace-alpha")
	tenantB := insertTenant(t, d, "workspace-bravo")
	insertMembership(t, d, tenantA, userA)
	insertMembership(t, d, tenantB, userB)

	err := d.WithinUser(t.Context(), userA, func(tx *sql.Tx) error {
		if err := dbtest.SetLocalRole(t.Context(), tx, role); err != nil {
			return err
		}

		var tenants, memberships int
		if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM tenants`).Scan(&tenants); err != nil {
			return err
		}
		if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM tenant_memberships`).Scan(&memberships); err != nil {
			return err
		}
		if tenants != 1 || memberships != 1 {
			t.Fatalf(
				"user-scoped visibility: got %d tenants and %d memberships, want 1 and 1",
				tenants,
				memberships,
			)
		}

		result, err := tx.ExecContext(
			t.Context(),
			`UPDATE tenants SET name = 'forbidden' WHERE id = $1`,
			tenantB,
		)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 0 {
			t.Fatalf("user-scoped context updated %d tenant rows, want 0", updated)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAppointmentCollisionAndCustomerHistory(t *testing.T) {
	d := dbtest.Open(t)
	tenantID := insertTenant(t, d, "history")
	locationID := insertLocation(t, d, tenantID, "history-shop")

	var customerID, resourceID string
	if err := d.QueryRow(`
		INSERT INTO customers (tenant_id, first_name, last_name)
		VALUES ($1, 'Alice', 'Martin')
		RETURNING id`, tenantID).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`
		INSERT INTO bookable_resources (tenant_id, location_id, kind, name)
		VALUES ($1, $2, 'bay', 'Bay 1')
		RETURNING id`, tenantID, locationID).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}

	vehicles := make([]string, 0, 2)
	for _, plate := range []string{"AA-123-AA", "BB-456-BB"} {
		var vehicleID string
		if err := d.QueryRow(`
			INSERT INTO vehicles (
				tenant_id, customer_id, registration_country, registration_plate
			)
			VALUES ($1, $2, 'FR', $3)
			RETURNING id`, tenantID, customerID, plate).Scan(&vehicleID); err != nil {
			t.Fatal(err)
		}
		vehicles = append(vehicles, vehicleID)
	}

	start := time.Date(2026, time.August, 3, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	var appointmentID string
	if err := d.QueryRow(`
		INSERT INTO appointments (
			tenant_id, location_id, customer_id, vehicle_id, resource_id,
			status, starts_at, ends_at, source
		)
		VALUES ($1, $2, $3, $4, $5, 'confirmed', $6, $7, 'agent')
		RETURNING id`,
		tenantID, locationID, customerID, vehicles[0], resourceID, start, end,
	).Scan(&appointmentID); err != nil {
		t.Fatal(err)
	}

	if _, err := d.Exec(`
		INSERT INTO appointments (
			tenant_id, location_id, customer_id, vehicle_id, resource_id,
			status, starts_at, ends_at, source
		)
		VALUES ($1, $2, $3, $4, $5, 'confirmed', $6, $7, 'agent')`,
		tenantID, locationID, customerID, vehicles[1], resourceID,
		start.Add(30*time.Minute), end.Add(30*time.Minute),
	); err == nil {
		t.Fatal("overlapping appointment unexpectedly accepted")
	}

	for i, vehicleID := range vehicles {
		var repairOrderID string
		if err := d.QueryRow(`
			INSERT INTO repair_orders (
				tenant_id, location_id, customer_id, vehicle_id,
				appointment_id, status, subtotal_cents, tax_cents,
				total_cents, approved_at, completed_at
			)
			VALUES (
				$1, $2, $3, $4, $5, 'completed', 10000, 2000,
				12000, now(), now()
			)
			RETURNING id`,
			tenantID, locationID, customerID, vehicleID,
			nullableAppointment(i, appointmentID),
		).Scan(&repairOrderID); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`
			INSERT INTO repair_order_items (
				tenant_id, repair_order_id, kind, description, quantity,
				unit_price_cents, tax_rate, line_subtotal_cents,
				line_tax_cents, line_total_cents
			)
			VALUES (
				$1, $2, 'labor', 'Workshop labor', 1,
				10000, 0.2, 10000, 2000, 12000
			)`, tenantID, repairOrderID); err != nil {
			t.Fatal(err)
		}
	}

	var repairs int
	if err := d.QueryRow(`
		SELECT COUNT(*) FROM repair_orders
		WHERE tenant_id = $1 AND customer_id = $2`,
		tenantID, customerID,
	).Scan(&repairs); err != nil {
		t.Fatal(err)
	}
	if repairs != 2 {
		t.Fatalf("customer repair history: got %d orders, want 2", repairs)
	}
}

func insertTenant(t *testing.T, database interface {
	QueryRow(query string, args ...any) *sql.Row
}, slug string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(
		`INSERT INTO tenants (slug, name) VALUES ($1, $2) RETURNING id`,
		slug, slug,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertLocation(t *testing.T, database interface {
	QueryRow(query string, args ...any) *sql.Row
}, tenantID, slug string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(`
		INSERT INTO locations (tenant_id, slug, name)
		VALUES ($1, $2, $3)
		RETURNING id`, tenantID, slug, slug,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertUser(t *testing.T, database interface {
	QueryRow(query string, args ...any) *sql.Row
}, email string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(`
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', $1, $1, 'Test User')
		RETURNING id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertMembership(t *testing.T, database interface {
	Exec(query string, args ...any) (sql.Result, error)
}, tenantID, userID string) {
	t.Helper()
	if _, err := database.Exec(`
		INSERT INTO tenant_memberships (tenant_id, user_id, role)
		VALUES ($1, $2, 'owner')`, tenantID, userID); err != nil {
		t.Fatal(err)
	}
}

func nullableAppointment(index int, appointmentID string) any {
	if index == 0 {
		return appointmentID
	}
	return nil
}
