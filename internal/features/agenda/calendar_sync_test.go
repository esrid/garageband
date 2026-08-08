package agenda

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
)

func insertReturningID(t *testing.T, database *db.DB, query string, args ...any) string {
	t.Helper()
	var id string
	if err := database.QueryRow(t.Context(), query, args...).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustExec(t *testing.T, database *db.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestCalendarEventIDIsBase32HexSafe(t *testing.T) {
	id := calendarEventID("019FC47F-333D-7211-BAF1-3CCAB25A8013")
	if id != "019fc47f333d7211baf13ccab25a8013" {
		t.Fatalf("calendarEventID = %q", id)
	}
	for _, r := range id {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'v')) {
			t.Fatalf("calendarEventID contains an out-of-range character: %q", id)
		}
	}
}

// calendarSyncFixture mirrors agendaFixture (queries_test.go, package
// agenda_test) but lives in-package so the unexported calendar-sync helpers
// under test are reachable without a network call to Google.
type calendarSyncFixture struct {
	fixtures      *db.DB // RLS-bypassing pool, admin setup only
	runtime       *db.DB // the pool the store itself uses, under row security
	tenantID      string
	userID        string
	locationID    string
	customerID    string
	vehicleID     string
	serviceID     string
	resourceID    string
	appointmentID string
}

func newCalendarSyncFixture(t *testing.T) (calendarSyncFixture, *Store) {
	t.Helper()
	fixtures, runtime := dbtest.OpenRuntime(t)
	store := NewStore(runtime, CalendarConfig{})

	userID := insertReturningID(t, fixtures, `
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', 'calendar-sync-owner', 'calendar-sync@example.com', 'Owner')
		RETURNING id::text`)
	tenantID := insertReturningID(t, fixtures, `
		INSERT INTO tenants (slug, name) VALUES ('calendar-sync-garage', 'Garage') RETURNING id::text`)
	mustExec(t, fixtures, `
		INSERT INTO tenant_memberships (tenant_id, user_id, role) VALUES ($1, $2, 'owner')`,
		tenantID, userID)
	locationID := insertReturningID(t, fixtures, `
		INSERT INTO locations (tenant_id, slug, name, timezone)
		VALUES ($1, 'sync-loc', 'Atelier Sync', 'America/Martinique') RETURNING id::text`,
		tenantID)
	customerID := insertReturningID(t, fixtures, `
		INSERT INTO customers (tenant_id, home_location_id, first_name, last_name)
		VALUES ($1, $2, 'Alice', 'Martin') RETURNING id::text`, tenantID, locationID)
	vehicleID := insertReturningID(t, fixtures, `
		INSERT INTO vehicles (tenant_id, customer_id, location_id, registration_plate, make, model)
		VALUES ($1, $2, $3, 'AA-123-AA', 'Renault', 'Clio') RETURNING id::text`,
		tenantID, customerID, locationID)
	serviceID := insertReturningID(t, fixtures, `
		INSERT INTO service_offerings (tenant_id, location_id, code, name, duration_minutes)
		VALUES ($1, $2, 'revision', 'Révision', 60) RETURNING id::text`,
		tenantID, locationID)
	resourceID := insertReturningID(t, fixtures, `
		INSERT INTO bookable_resources (tenant_id, location_id, kind, name)
		VALUES ($1, $2, 'bay', 'Pont principal') RETURNING id::text`, tenantID, locationID)

	appointmentID, _, err := store.Save(t.Context(), tenantID, userID, "", SaveInput{
		LocationID: locationID, CustomerID: customerID, VehicleID: vehicleID,
		ServiceID: serviceID, ResourceID: resourceID,
		Date: "2026-08-12", StartTime: "09:00",
	})
	if err != nil {
		t.Fatal(err)
	}

	return calendarSyncFixture{
		fixtures: fixtures, runtime: runtime, tenantID: tenantID, userID: userID,
		locationID: locationID, customerID: customerID, vehicleID: vehicleID,
		serviceID: serviceID, resourceID: resourceID, appointmentID: appointmentID,
	}, store
}

func TestActiveCalendarConnectionFindsOnlyActiveGoogleCalendarConnections(t *testing.T) {
	fixture, store := newCalendarSyncFixture(t)

	err := store.db.WithinTenantUser(t.Context(), fixture.tenantID, fixture.userID, func(tx pgx.Tx) error {
		_, _, ok, err := activeCalendarConnection(t.Context(), tx, fixture.tenantID, fixture.appointmentID)
		if err != nil {
			return err
		}
		if ok {
			t.Fatal("connection found before any was inserted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var connectionID string
	if err := fixture.fixtures.QueryRow(t.Context(), `
		INSERT INTO provider_connections (tenant_id, location_id, kind, provider, secret_ref)
		VALUES ($1, $2, 'calendar', 'google', 'secret-ref-1') RETURNING id::text`,
		fixture.tenantID, fixture.locationID,
	).Scan(&connectionID); err != nil {
		t.Fatal(err)
	}

	err = store.db.WithinTenantUser(t.Context(), fixture.tenantID, fixture.userID, func(tx pgx.Tx) error {
		id, secretRef, ok, err := activeCalendarConnection(t.Context(), tx, fixture.tenantID, fixture.appointmentID)
		if err != nil {
			return err
		}
		if !ok || id != connectionID || secretRef != "secret-ref-1" {
			t.Fatalf("connection = id %q secretRef %q ok %v, want %q secret-ref-1 true", id, secretRef, ok, connectionID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.fixtures.Exec(t.Context(), `
		UPDATE provider_connections SET status = 'disabled' WHERE id = $1`, connectionID,
	); err != nil {
		t.Fatal(err)
	}
	err = store.db.WithinTenantUser(t.Context(), fixture.tenantID, fixture.userID, func(tx pgx.Tx) error {
		_, _, ok, err := activeCalendarConnection(t.Context(), tx, fixture.tenantID, fixture.appointmentID)
		if err != nil {
			return err
		}
		if ok {
			t.Fatal("disabled connection reported as active")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoadAppointmentEventBuildsTitleAndTimeZoneFromTheBooking(t *testing.T) {
	fixture, store := newCalendarSyncFixture(t)

	err := store.db.WithinTenantUser(t.Context(), fixture.tenantID, fixture.userID, func(tx pgx.Tx) error {
		event, err := loadAppointmentEvent(t.Context(), tx, fixture.tenantID, fixture.appointmentID)
		if err != nil {
			return err
		}
		if event.Title != "Révision — Alice Martin" {
			t.Fatalf("event title = %q", event.Title)
		}
		if event.Location != "Atelier Sync" {
			t.Fatalf("event location = %q", event.Location)
		}
		if event.TimeZone != "America/Martinique" {
			t.Fatalf("event time zone = %q", event.TimeZone)
		}
		if !event.Private {
			t.Fatal("event must default to private")
		}
		if !event.End.After(event.Start) {
			t.Fatalf("event end %v is not after start %v", event.End, event.Start)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRecordCalendarSyncTracksSyncedErrorAndDeletedTransitions(t *testing.T) {
	fixture, store := newCalendarSyncFixture(t)
	var connectionID string
	if err := fixture.fixtures.QueryRow(t.Context(), `
		INSERT INTO provider_connections (tenant_id, location_id, kind, provider, secret_ref)
		VALUES ($1, $2, 'calendar', 'google', 'secret-ref-1') RETURNING id::text`,
		fixture.tenantID, fixture.locationID,
	).Scan(&connectionID); err != nil {
		t.Fatal(err)
	}

	run := func(externalEventID string, pushErr error) (status, lastError string, lastSyncedAt *time.Time) {
		t.Helper()
		err := store.db.WithinTenantUser(t.Context(), fixture.tenantID, fixture.userID, func(tx pgx.Tx) error {
			return recordCalendarSync(
				t.Context(), tx, fixture.tenantID, fixture.appointmentID, connectionID,
				googleCalendarID, externalEventID, pushErr,
			)
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := fixture.fixtures.QueryRow(t.Context(), `
			SELECT sync_status, coalesce(last_error, ''), last_synced_at
			FROM appointment_calendar_events
			WHERE tenant_id = $1 AND appointment_id = $2 AND connection_id = $3`,
			fixture.tenantID, fixture.appointmentID, connectionID,
		).Scan(&status, &lastError, &lastSyncedAt); err != nil {
			t.Fatal(err)
		}
		return status, lastError, lastSyncedAt
	}

	status, lastError, firstSyncedAt := run("evt-1", nil)
	if status != "synced" || lastError != "" || firstSyncedAt == nil {
		t.Fatalf("after first sync: status %q lastError %q lastSyncedAt %v", status, lastError, firstSyncedAt)
	}

	status, lastError, syncedAtAfterError := run("", errPushFailedForTest)
	if status != "error" || lastError != errPushFailedForTest.Error() {
		t.Fatalf("after failed push: status %q lastError %q", status, lastError)
	}
	if syncedAtAfterError == nil || !syncedAtAfterError.Equal(*firstSyncedAt) {
		t.Fatalf("last_synced_at moved on a failed push: %v, want unchanged %v", syncedAtAfterError, firstSyncedAt)
	}

	var externalEventID string
	if err := fixture.fixtures.QueryRow(t.Context(), `
		SELECT external_event_id FROM appointment_calendar_events
		WHERE tenant_id = $1 AND appointment_id = $2 AND connection_id = $3`,
		fixture.tenantID, fixture.appointmentID, connectionID,
	).Scan(&externalEventID); err != nil {
		t.Fatal(err)
	}
	if externalEventID != "evt-1" {
		t.Fatalf("external_event_id lost on a failed push = %q, want evt-1 preserved", externalEventID)
	}

	status, lastError, syncedAtAfterRecovery := run("evt-1", nil)
	if status != "synced" || lastError != "" {
		t.Fatalf("after recovery: status %q lastError %q", status, lastError)
	}
	if syncedAtAfterRecovery == nil || !syncedAtAfterRecovery.After(*firstSyncedAt) {
		t.Fatalf("last_synced_at did not advance on recovery: %v, want after %v", syncedAtAfterRecovery, firstSyncedAt)
	}

	status, _, _ = run("", nil)
	if status != "deleted" {
		t.Fatalf("after delete: status %q, want deleted", status)
	}

	err := store.db.WithinTenantUser(t.Context(), fixture.tenantID, fixture.userID, func(tx pgx.Tx) error {
		_, _, _, ok, err := syncedCalendarEvent(t.Context(), tx, fixture.tenantID, fixture.appointmentID)
		if err != nil {
			return err
		}
		if ok {
			t.Fatal("deleted event still reported as synced")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

var errPushFailedForTest = &pushTestError{"google unavailable"}

type pushTestError struct{ message string }

func (e *pushTestError) Error() string { return e.message }
