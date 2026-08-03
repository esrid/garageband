package db_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/esrid/garageband/internal/platform/dbtest"
)

func TestTenantRLSIsolation(t *testing.T) {
	d := dbtest.Open(t)
	role := dbtest.RuntimeRole(t, d)

	tenantA := insertTenant(t, d, "alpha")
	tenantB := insertTenant(t, d, "bravo")
	userA := insertUser(t, d, "alpha-rls@example.com")
	userB := insertUser(t, d, "bravo-rls@example.com")
	insertMembership(t, d, tenantA, userA)
	insertMembership(t, d, tenantB, userB)
	insertLocation(t, d, tenantA, "alpha-shop")
	insertLocation(t, d, tenantB, "bravo-shop")

	assertVisibleLocations := func(tenantID, userID string, want int) {
		t.Helper()
		var got int
		err := d.WithinTenantUser(t.Context(), tenantID, userID, func(tx pgx.Tx) error {
			if err := dbtest.SetLocalRole(t.Context(), tx, role); err != nil {
				return err
			}
			return tx.QueryRow(
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
	assertVisibleLocations(tenantA, userA, 1)
	assertVisibleLocations(tenantB, userB, 1)

	err := d.WithinTenantUser(t.Context(), tenantA, userA, func(tx pgx.Tx) error {
		if err := dbtest.SetLocalRole(t.Context(), tx, role); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), `
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

	err := d.WithinNewTenant(t.Context(), func(tx pgx.Tx, tenantID string) error {
		if err := dbtest.SetLocalRole(t.Context(), tx, role); err != nil {
			return err
		}
		var insertedID string
		if err := tx.QueryRow(t.Context(), `
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

	err := d.WithinUser(t.Context(), userA, func(tx pgx.Tx) error {
		if err := dbtest.SetLocalRole(t.Context(), tx, role); err != nil {
			return err
		}

		var tenants, memberships int
		if err := tx.QueryRow(t.Context(), `SELECT COUNT(*) FROM tenants`).Scan(&tenants); err != nil {
			return err
		}
		if err := tx.QueryRow(t.Context(), `SELECT COUNT(*) FROM tenant_memberships`).Scan(&memberships); err != nil {
			return err
		}
		if tenants != 1 || memberships != 1 {
			t.Fatalf(
				"user-scoped visibility: got %d tenants and %d memberships, want 1 and 1",
				tenants,
				memberships,
			)
		}

		result, err := tx.Exec(
			t.Context(),
			`UPDATE tenants SET name = 'forbidden' WHERE id = $1`,
			tenantB,
		)
		if err != nil {
			return err
		}
		if updated := result.RowsAffected(); updated != 0 {
			t.Fatalf("user-scoped context updated %d tenant rows, want 0", updated)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLocationManagementRLSUsesMembershipRole(t *testing.T) {
	d := dbtest.Open(t)
	role := dbtest.RuntimeRole(t, d)
	ownerID := insertUser(t, d, "rls-location-owner@example.com")
	memberID := insertUser(t, d, "rls-location-member@example.com")
	outsiderID := insertUser(t, d, "rls-location-outsider@example.com")
	tenantID := insertTenant(t, d, "rls-location")
	insertMembershipRole(t, d, tenantID, ownerID, "owner")
	insertMembershipRole(t, d, tenantID, memberID, "member")

	var locationID string
	err := d.WithinTenantUser(t.Context(), tenantID, ownerID, func(tx pgx.Tx) error {
		if err := dbtest.SetLocalRole(t.Context(), tx, role); err != nil {
			return err
		}
		return tx.QueryRow(t.Context(), `
			INSERT INTO locations (tenant_id, slug, name, timezone)
			VALUES ($1, 'owner-site', 'Owner site', 'Europe/Paris')
			RETURNING id`, tenantID).Scan(&locationID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(t.Context(), `
		INSERT INTO user_location_assignments (
			tenant_id, user_id, location_id, assigned_by_user_id
		) VALUES ($1, $2, $3, $4)`,
		tenantID, memberID, locationID, ownerID,
	); err != nil {
		t.Fatal(err)
	}

	err = d.WithinTenantUser(t.Context(), tenantID, memberID, func(tx pgx.Tx) error {
		if err := dbtest.SetLocalRole(t.Context(), tx, role); err != nil {
			return err
		}
		var visible int
		if err := tx.QueryRow(
			t.Context(), `SELECT COUNT(*) FROM locations`,
		).Scan(&visible); err != nil {
			return err
		}
		if visible != 1 {
			t.Fatalf("member sees %d locations, want 1", visible)
		}
		_, err := tx.Exec(t.Context(), `
			INSERT INTO locations (tenant_id, slug, name)
			VALUES ($1, 'member-site', 'Member site')`, tenantID)
		return err
	})
	if err == nil {
		t.Fatal("member unexpectedly created a location")
	}

	err = d.WithinTenantUser(t.Context(), tenantID, outsiderID, func(tx pgx.Tx) error {
		if err := dbtest.SetLocalRole(t.Context(), tx, role); err != nil {
			return err
		}
		var visible int
		if err := tx.QueryRow(
			t.Context(), `SELECT COUNT(*) FROM locations`,
		).Scan(&visible); err != nil {
			return err
		}
		if visible != 0 {
			t.Fatalf("outsider sees %d locations, want 0", visible)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = d.WithinTenantUser(t.Context(), tenantID, ownerID, func(tx pgx.Tx) error {
		if err := dbtest.SetLocalRole(t.Context(), tx, role); err != nil {
			return err
		}
		result, err := tx.Exec(
			t.Context(), `DELETE FROM locations WHERE id = $1`, locationID,
		)
		if err != nil {
			return err
		}
		if deleted := result.RowsAffected(); deleted != 0 {
			t.Fatalf("owner hard-deleted %d locations, want 0", deleted)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCustomerShareRLSLifecycleAndHistoricalVisibility(t *testing.T) {
	d := dbtest.Open(t)
	runtimeRole := dbtest.RuntimeRole(t, d)
	ownerID := insertUser(t, d, "share-owner@example.com")
	homeStaffID := insertUser(t, d, "share-home@example.com")
	receivingStaffID := insertUser(t, d, "share-receiving@example.com")
	tenantID := insertTenant(t, d, "share-lifecycle")
	insertMembershipRole(t, d, tenantID, ownerID, "owner")
	insertMembershipRole(t, d, tenantID, homeStaffID, "member")
	insertMembershipRole(t, d, tenantID, receivingStaffID, "member")
	homeLocationID := insertLocation(t, d, tenantID, "share-home")
	receivingLocationID := insertLocation(t, d, tenantID, "share-receiving")
	insertLocationAssignment(t, d, tenantID, homeStaffID, homeLocationID, ownerID)
	insertLocationAssignment(t, d, tenantID, receivingStaffID, receivingLocationID, ownerID)

	var customerID, vehicleID string
	if err := d.QueryRow(t.Context(), `
		INSERT INTO customers (
			tenant_id, home_location_id, first_name, last_name
		) VALUES ($1, $2, 'Alice', 'Martin') RETURNING id`,
		tenantID, homeLocationID,
	).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(t.Context(), `
		INSERT INTO vehicles (
			tenant_id, location_id, customer_id,
			registration_country, registration_plate
		) VALUES ($1, $2, $3, 'FR', 'AA-123-AA') RETURNING id`,
		tenantID, homeLocationID, customerID,
	).Scan(&vehicleID); err != nil {
		t.Fatal(err)
	}

	visibleCustomers := func(userID string) int {
		t.Helper()
		var count int
		err := d.WithinTenantUser(t.Context(), tenantID, userID, func(tx pgx.Tx) error {
			if err := dbtest.SetLocalRole(t.Context(), tx, runtimeRole); err != nil {
				return err
			}
			return tx.QueryRow(
				t.Context(), `SELECT COUNT(*) FROM customers WHERE id = $1`, customerID,
			).Scan(&count)
		})
		if err != nil {
			t.Fatal(err)
		}
		return count
	}
	if got := visibleCustomers(receivingStaffID); got != 0 {
		t.Fatalf("receiving site sees %d customers before sharing, want 0", got)
	}

	grantedAt := time.Now().UTC().Add(-time.Hour)
	var grantID string
	if err := d.QueryRow(t.Context(), `
		INSERT INTO customer_location_grants (
			tenant_id, customer_id, source_location_id,
			receiving_location_id, granted_by_user_id, granted_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`, tenantID, customerID, homeLocationID,
		receivingLocationID, ownerID, grantedAt,
	).Scan(&grantID); err != nil {
		t.Fatal(err)
	}
	if got := visibleCustomers(receivingStaffID); got != 1 {
		t.Fatalf("receiving site sees %d customers during sharing, want 1", got)
	}
	if _, err := d.Exec(t.Context(), `
		UPDATE customer_location_grants
		SET granted_at = granted_at - interval '1 day'
		WHERE id = $1`, grantID,
	); err == nil {
		t.Fatal("grant audit timestamp was mutable")
	}
	if _, err := d.Exec(t.Context(), `
		UPDATE customers SET home_location_id = $2 WHERE id = $1`,
		customerID, receivingLocationID,
	); err == nil {
		t.Fatal("customer home location was mutable")
	}

	var appointmentID, memoryID string
	appointmentCreatedAt := grantedAt.Add(30 * time.Minute)
	err := d.WithinTenantUser(
		t.Context(), tenantID, receivingStaffID, func(tx pgx.Tx) error {
			if err := dbtest.SetLocalRole(t.Context(), tx, runtimeRole); err != nil {
				return err
			}
			result, err := tx.Exec(t.Context(), `
				UPDATE customers SET first_name = 'Forbidden'
				WHERE id = $1`, customerID)
			if err != nil {
				return err
			}
			if updated := result.RowsAffected(); updated != 0 {
				t.Fatalf("receiving site updated %d canonical customers, want 0", updated)
			}
			return tx.QueryRow(t.Context(), `
				INSERT INTO appointments (
					tenant_id, location_id, customer_id, vehicle_id,
					status, starts_at, ends_at, source, created_at,
					customer_snapshot, vehicle_snapshot
				) VALUES (
					$1, $2, $3, $4, 'draft', now() + interval '1 day',
					now() + interval '1 day 1 hour', 'dashboard', $5,
					'{"first_name":"Mallory"}'::JSONB,
					'{"registration_plate":"FAKE"}'::JSONB
				) RETURNING id`, tenantID, receivingLocationID,
				customerID, vehicleID, appointmentCreatedAt,
			).Scan(&appointmentID)
		})
	if err != nil {
		t.Fatal(err)
	}
	err = d.WithinTenantUser(
		t.Context(), tenantID, receivingStaffID, func(tx pgx.Tx) error {
			if err := dbtest.SetLocalRole(t.Context(), tx, runtimeRole); err != nil {
				return err
			}
			return tx.QueryRow(t.Context(), `
				INSERT INTO customer_memories (
					tenant_id, location_id, customer_id, key, value,
					customer_snapshot, created_at
				) VALUES (
					$1, $2, $3, 'vehicle.preference', '{"value":"morning"}',
					'{"first_name":"Mallory"}'::JSONB, $4
				) RETURNING id`, tenantID, receivingLocationID,
				customerID, appointmentCreatedAt,
			).Scan(&memoryID)
		})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := d.Exec(t.Context(), `
		UPDATE customer_location_grants
		SET revoked_by_user_id = $3, revoked_at = now()
		WHERE tenant_id = $1 AND id = $2`, tenantID, grantID, ownerID,
	); err != nil {
		t.Fatal(err)
	}
	if got := visibleCustomers(receivingStaffID); got != 0 {
		t.Fatalf("receiving site sees %d canonical customers after revocation, want 0", got)
	}

	assertAppointment := func(userID string, want int) {
		t.Helper()
		var count int
		var customerSnapshot, vehicleSnapshot []byte
		err := d.WithinTenantUser(t.Context(), tenantID, userID, func(tx pgx.Tx) error {
			if err := dbtest.SetLocalRole(t.Context(), tx, runtimeRole); err != nil {
				return err
			}
			err := tx.QueryRow(t.Context(), `
				SELECT COUNT(*) FROM appointments WHERE id = $1`, appointmentID,
			).Scan(&count)
			if err != nil || count == 0 {
				return err
			}
			return tx.QueryRow(t.Context(), `
				SELECT customer_snapshot, vehicle_snapshot
				FROM appointments WHERE id = $1`, appointmentID,
			).Scan(&customerSnapshot, &vehicleSnapshot)
		})
		if err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("user %s sees %d retained appointments, want %d", userID, count, want)
		}
		if want == 1 && (len(customerSnapshot) == 0 || len(vehicleSnapshot) == 0) {
			t.Fatal("retained appointment is missing identity snapshots")
		}
		if want == 1 {
			assertSnapshotValue(t, customerSnapshot, "first_name", "Alice")
			assertSnapshotValue(
				t, vehicleSnapshot, "registration_plate", "AA-123-AA",
			)
		}
	}
	assertAppointment(receivingStaffID, 1)
	assertAppointment(homeStaffID, 1)

	assertMemory := func(userID string) {
		t.Helper()
		var snapshot []byte
		err := d.WithinTenantUser(t.Context(), tenantID, userID, func(tx pgx.Tx) error {
			if err := dbtest.SetLocalRole(t.Context(), tx, runtimeRole); err != nil {
				return err
			}
			return tx.QueryRow(t.Context(), `
				SELECT customer_snapshot
				FROM customer_memories WHERE id = $1`, memoryID,
			).Scan(&snapshot)
		})
		if err != nil {
			t.Fatal(err)
		}
		assertSnapshotValue(t, snapshot, "first_name", "Alice")
	}
	assertMemory(receivingStaffID)
	assertMemory(homeStaffID)

	err = d.WithinTenantUser(
		t.Context(), tenantID, receivingStaffID, func(tx pgx.Tx) error {
			if err := dbtest.SetLocalRole(t.Context(), tx, runtimeRole); err != nil {
				return err
			}
			_, err := tx.Exec(t.Context(), `
				INSERT INTO appointments (
					tenant_id, location_id, customer_id, vehicle_id,
					status, starts_at, ends_at, source
				) VALUES (
					$1, $2, $3, $4, 'draft', now() + interval '2 days',
					now() + interval '2 days 1 hour', 'dashboard'
				)`, tenantID, receivingLocationID, customerID, vehicleID)
			return err
		})
	if err == nil {
		t.Fatal("receiving site created a new customer event after revocation")
	}
}

func assertSnapshotValue(t *testing.T, raw []byte, key string, want any) {
	t.Helper()
	var snapshot map[string]any
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if got := snapshot[key]; got != want {
		t.Fatalf("snapshot %q = %#v, want %#v", key, got, want)
	}
}

func TestAppointmentCollisionAndCustomerHistory(t *testing.T) {
	d := dbtest.Open(t)
	tenantID := insertTenant(t, d, "history")
	locationID := insertLocation(t, d, tenantID, "history-shop")

	var customerID, resourceID string
	if err := d.QueryRow(t.Context(), `
		INSERT INTO customers (
			tenant_id, home_location_id, first_name, last_name
		)
		VALUES ($1, $2, 'Alice', 'Martin')
		RETURNING id`, tenantID, locationID).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(t.Context(), `
		INSERT INTO bookable_resources (tenant_id, location_id, kind, name)
		VALUES ($1, $2, 'bay', 'Bay 1')
		RETURNING id`, tenantID, locationID).Scan(&resourceID); err != nil {
		t.Fatal(err)
	}

	vehicles := make([]string, 0, 2)
	for _, plate := range []string{"AA-123-AA", "BB-456-BB"} {
		var vehicleID string
		if err := d.QueryRow(t.Context(), `
			INSERT INTO vehicles (
				tenant_id, location_id, customer_id,
				registration_country, registration_plate
			)
			VALUES ($1, $2, $3, 'FR', $4)
			RETURNING id`, tenantID, locationID, customerID, plate,
		).Scan(&vehicleID); err != nil {
			t.Fatal(err)
		}
		vehicles = append(vehicles, vehicleID)
	}

	start := time.Date(2026, time.August, 3, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	var appointmentID string
	if err := d.QueryRow(t.Context(), `
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

	if _, err := d.Exec(t.Context(), `
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
		if err := d.QueryRow(t.Context(), `
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
		if _, err := d.Exec(t.Context(), `
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
	if err := d.QueryRow(t.Context(), `
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

// TestOwnerScopedWritesRejectMembers pins the boundary a garage needs: the
// mechanic and the front desk run the day-to-day work, but reshaping the
// business — plugging a platform in, renaming the company, adding staff — stays
// with the owner. The reads they need for that daily work must keep working.
func TestOwnerScopedWritesRejectMembers(t *testing.T) {
	d := dbtest.Open(t)
	role := dbtest.RuntimeRole(t, d)
	ownerID := insertUser(t, d, "rls-scope-owner@example.com")
	memberID := insertUser(t, d, "rls-scope-member@example.com")
	strangerID := insertUser(t, d, "rls-scope-stranger@example.com")
	tenantID := insertTenant(t, d, "rls-scope")
	insertMembershipRole(t, d, tenantID, ownerID, "owner")
	insertMembershipRole(t, d, tenantID, memberID, "member")
	locationID := insertLocation(t, d, tenantID, "rls-scope-site")

	asUser := func(userID string, fn func(tx pgx.Tx) error) error {
		return d.WithinTenantUser(t.Context(), tenantID, userID, func(tx pgx.Tx) error {
			if err := dbtest.SetLocalRole(t.Context(), tx, role); err != nil {
				return err
			}
			return fn(tx)
		})
	}

	insertLocationAssignment(t, d, tenantID, memberID, locationID, ownerID)

	var connectionID string
	if err := asUser(ownerID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			INSERT INTO provider_connections (
				tenant_id, location_id, kind, provider, secret_ref
			) VALUES ($1, $2, 'calendar', 'google', 'secret-ref')
			RETURNING id`, tenantID, locationID).Scan(&connectionID)
	}); err != nil {
		t.Fatalf("owner could not connect a platform: %v", err)
	}

	// Booking an appointment syncs the calendar, so a member must still read
	// the connection it was never allowed to create.
	if err := asUser(memberID, func(tx pgx.Tx) error {
		var visible int
		if err := tx.QueryRow(
			t.Context(), `SELECT COUNT(*) FROM provider_connections`,
		).Scan(&visible); err != nil {
			return err
		}
		if visible != 1 {
			t.Fatalf("member sees %d provider connections, want 1", visible)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for name, write := range map[string]string{
		"connect a platform": `
			INSERT INTO provider_connections (
				tenant_id, location_id, kind, provider, secret_ref
			) VALUES ($1, $2, 'llm', 'openai', 'member-secret')`,
		"create an agent": `
			INSERT INTO agents (tenant_id, location_id, name)
			VALUES ($1, $2, 'Member agent')`,
	} {
		err := asUser(memberID, func(tx pgx.Tx) error {
			_, err := tx.Exec(t.Context(), write, tenantID, locationID)
			return err
		})
		if err == nil {
			t.Fatalf("member unexpectedly allowed to %s", name)
		}
	}

	// A DELETE blocked by RLS filters the rows away instead of raising: the
	// surviving connection is what proves the policy held.
	if err := asUser(memberID, func(tx pgx.Tx) error {
		result, err := tx.Exec(t.Context(),
			`DELETE FROM provider_connections WHERE id = $1`, connectionID)
		if err != nil {
			return err
		}
		if removed := result.RowsAffected(); removed != 0 {
			t.Fatalf("member disconnected %d platforms, want 0", removed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := asUser(memberID, func(tx pgx.Tx) error {
		result, err := tx.Exec(t.Context(),
			`UPDATE tenants SET name = 'Renamed by member' WHERE id = $1`, tenantID)
		if err != nil {
			return err
		}
		if renamed := result.RowsAffected(); renamed != 0 {
			t.Fatalf("member renamed %d organizations, want 0", renamed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// The escalation the invite feature will write against: a member must not
	// be able to add staff, least of all promote themselves.
	if err := asUser(memberID, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `
			INSERT INTO tenant_memberships (tenant_id, user_id, role)
			VALUES ($1, $2, 'owner')`, tenantID, strangerID)
		return err
	}); err == nil {
		t.Fatal("member unexpectedly added a membership")
	}
	if err := asUser(memberID, func(tx pgx.Tx) error {
		result, err := tx.Exec(t.Context(), `
			UPDATE tenant_memberships SET role = 'owner'
			WHERE tenant_id = $1 AND user_id = $2`, tenantID, memberID)
		if err != nil {
			return err
		}
		if promoted := result.RowsAffected(); promoted != 0 {
			t.Fatalf("member promoted themselves %d times, want 0", promoted)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := asUser(ownerID, func(tx pgx.Tx) error {
		result, err := tx.Exec(t.Context(), `
			DELETE FROM tenant_memberships
			WHERE tenant_id = $1 AND user_id = $2`, tenantID, ownerID)
		if err != nil {
			return err
		}
		if removed := result.RowsAffected(); removed != 0 {
			t.Fatalf("owner locked themselves out of %d memberships, want 0", removed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := asUser(ownerID, func(tx pgx.Tx) error {
		result, err := tx.Exec(t.Context(), `
			DELETE FROM tenant_memberships
			WHERE tenant_id = $1 AND user_id = $2`, tenantID, memberID)
		if err != nil {
			return err
		}
		if removed := result.RowsAffected(); removed != 1 {
			t.Fatalf("owner removed %d memberships, want 1", removed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestOwnerlessTenantOnlyBootstrapsItsCreator pins the one branch that lets a
// membership be written without an owner to authorize it. Onboarding needs it;
// it must not double as a way into someone else's tenant.
func TestOwnerlessTenantOnlyBootstrapsItsCreator(t *testing.T) {
	d := dbtest.Open(t)
	role := dbtest.RuntimeRole(t, d)
	creatorID := insertUser(t, d, "bootstrap-creator@example.com")
	strangerID := insertUser(t, d, "bootstrap-stranger@example.com")
	tenantID := insertTenant(t, d, "bootstrap-tenant")

	asUser := func(userID string, fn func(tx pgx.Tx) error) error {
		return d.WithinTenantUser(t.Context(), tenantID, userID, func(tx pgx.Tx) error {
			if err := dbtest.SetLocalRole(t.Context(), tx, role); err != nil {
				return err
			}
			return fn(tx)
		})
	}

	if err := asUser(creatorID, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `
			INSERT INTO tenant_memberships (tenant_id, user_id, role)
			VALUES ($1, $2, 'owner')`, tenantID, strangerID)
		return err
	}); err == nil {
		t.Fatal("bootstrap unexpectedly enrolled a third party")
	}

	if err := asUser(creatorID, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `
			INSERT INTO tenant_memberships (tenant_id, user_id, role)
			VALUES ($1, $2, 'member')`, tenantID, creatorID)
		return err
	}); err == nil {
		t.Fatal("bootstrap unexpectedly created a tenant with no owner")
	}

	if err := asUser(creatorID, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `
			INSERT INTO tenant_memberships (tenant_id, user_id, role)
			VALUES ($1, $2, 'owner')`, tenantID, creatorID)
		return err
	}); err != nil {
		t.Fatalf("creator could not claim their own new tenant: %v", err)
	}

	// The branch closes behind the first owner: the tenant is no longer empty.
	if err := asUser(strangerID, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `
			INSERT INTO tenant_memberships (tenant_id, user_id, role)
			VALUES ($1, $2, 'owner')`, tenantID, strangerID)
		return err
	}); err == nil {
		t.Fatal("stranger unexpectedly joined an already-owned tenant")
	}
}

// TestUserEmailIsUniqueAcrossProviders keeps one human on one row: an invited
// employee who later signs in with Google must be merged, not duplicated.
func TestUserEmailIsUniqueAcrossProviders(t *testing.T) {
	d := dbtest.Open(t)
	insertUser(t, d, "shared-address@example.com")

	_, err := d.Exec(t.Context(), `
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('google', 'google-123', 'Shared-Address@example.com', 'Same human')`)
	if err == nil {
		t.Fatal("a second user unexpectedly claimed the same email address")
	}

	// Employees invited without a work address share the empty string, which
	// the partial index must leave alone.
	for _, providerID := range []string{"invite-1", "invite-2"} {
		if _, err := d.Exec(t.Context(), `
			INSERT INTO users (provider, provider_id, email, name)
			VALUES ('invite', $1, '', 'Mechanic')`, providerID); err != nil {
			t.Fatalf("invited employee without an email rejected: %v", err)
		}
	}
}

func insertTenant(t *testing.T, database interface {
	QueryRow(ctx context.Context, query string, args ...any) pgx.Row
}, slug string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(t.Context(),
		`INSERT INTO tenants (slug, name) VALUES ($1, $2) RETURNING id`,
		slug, slug,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertLocation(t *testing.T, database interface {
	QueryRow(ctx context.Context, query string, args ...any) pgx.Row
}, tenantID, slug string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(t.Context(), `
		INSERT INTO locations (tenant_id, slug, name)
		VALUES ($1, $2, $3)
		RETURNING id`, tenantID, slug, slug,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertUser(t *testing.T, database interface {
	QueryRow(ctx context.Context, query string, args ...any) pgx.Row
}, email string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(t.Context(), `
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', $1, $1, 'Test User')
		RETURNING id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertMembership(t *testing.T, database interface {
	Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error)
}, tenantID, userID string) {
	insertMembershipRole(t, database, tenantID, userID, "owner")
}

func insertMembershipRole(t *testing.T, database interface {
	Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error)
}, tenantID, userID, role string) {
	t.Helper()
	if _, err := database.Exec(t.Context(), `
		INSERT INTO tenant_memberships (tenant_id, user_id, role)
		VALUES ($1, $2, $3)`, tenantID, userID, role); err != nil {
		t.Fatal(err)
	}
}

func insertLocationAssignment(
	t *testing.T,
	database interface {
		Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error)
	},
	tenantID string,
	userID string,
	locationID string,
	assignedBy string,
) {
	t.Helper()
	if _, err := database.Exec(t.Context(), `
		INSERT INTO user_location_assignments (
			tenant_id, user_id, location_id, assigned_by_user_id
		) VALUES ($1, $2, $3, $4)`,
		tenantID, userID, locationID, assignedBy,
	); err != nil {
		t.Fatal(err)
	}
}

func nullableAppointment(index int, appointmentID string) any {
	if index == 0 {
		return appointmentID
	}
	return nil
}
