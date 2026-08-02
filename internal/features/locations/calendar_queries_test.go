package locations_test

import (
	"crypto/rand"
	"errors"
	"testing"

	"github.com/esrid/garageband/internal/features/locations"
	"github.com/esrid/garageband/internal/platform/dbtest"
	"github.com/esrid/garageband/internal/platform/secrets"
)

func newTestSecretStore(t *testing.T) secrets.Store {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := secrets.NewAESStore(key)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestCalendarConnectionLifecycleAndManagerBoundary(t *testing.T) {
	database := dbtest.Open(t)
	ownerID := createUser(t, database, "calendar-owner@example.com")
	memberID := createUser(t, database, "calendar-member@example.com")
	tenantID := createTenant(t, database, ownerID)
	addMembership(t, database, tenantID, memberID, "member")
	store := locations.NewStore(database)
	secretStore := newTestSecretStore(t)

	created, err := store.Create(t.Context(), tenantID, ownerID, locations.Input{
		Name: "Atelier Nord", PhoneE164: "+596596123456", Email: "contact@example.com",
		AddressLine1: "10 rue des Ateliers", PostalCode: "97200", City: "Fort-de-France",
		CountryCode: "FR", Timezone: "America/Martinique",
	})
	if err != nil {
		t.Fatal(err)
	}

	if account, connected, err := store.CalendarAccount(t.Context(), tenantID, ownerID, created.ID); err != nil || connected || account != "" {
		t.Fatalf("account before connect = %q connected %v, %v", account, connected, err)
	}

	// A member cannot connect a calendar, only owners/admins.
	if err := store.ConnectCalendar(
		t.Context(), tenantID, memberID, created.ID, secretStore, "refresh-token", "owner@gmail.com",
	); !errors.Is(err, locations.ErrForbidden) {
		t.Fatalf("member connect = %v, want ErrForbidden", err)
	}

	if err := store.ConnectCalendar(
		t.Context(), tenantID, ownerID, created.ID, secretStore, "refresh-token", "owner@gmail.com",
	); err != nil {
		t.Fatal(err)
	}
	if account, connected, err := store.CalendarAccount(t.Context(), tenantID, ownerID, created.ID); err != nil || !connected || account != "owner@gmail.com" {
		t.Fatalf("account after connect = %q connected %v, %v", account, connected, err)
	}

	// Reconnecting replaces the previous connection rather than accumulating
	// rows, and does not leak the old encrypted secret.
	var connectionCount int
	if err := database.QueryRow(t.Context(), `
		SELECT count(*) FROM provider_connections
		WHERE tenant_id = $1 AND location_id = $2 AND kind = 'calendar'`,
		tenantID, created.ID,
	).Scan(&connectionCount); err != nil {
		t.Fatal(err)
	}
	if connectionCount != 1 {
		t.Fatalf("calendar connections = %d, want 1", connectionCount)
	}
	if err := store.ConnectCalendar(
		t.Context(), tenantID, ownerID, created.ID, secretStore, "refresh-token-2", "owner@gmail.com",
	); err != nil {
		t.Fatal(err)
	}
	var secretCount int
	if err := database.QueryRow(t.Context(), `
		SELECT count(*) FROM encrypted_secrets WHERE tenant_id = $1`, tenantID,
	).Scan(&secretCount); err != nil {
		t.Fatal(err)
	}
	if secretCount != 1 {
		t.Fatalf("encrypted secrets = %d, want 1 (old one deleted on reconnect)", secretCount)
	}

	// A member cannot disconnect either.
	if err := store.DisconnectCalendar(
		t.Context(), tenantID, memberID, created.ID, secretStore,
	); !errors.Is(err, locations.ErrForbidden) {
		t.Fatalf("member disconnect = %v, want ErrForbidden", err)
	}

	if err := store.DisconnectCalendar(t.Context(), tenantID, ownerID, created.ID, secretStore); err != nil {
		t.Fatal(err)
	}
	if account, connected, err := store.CalendarAccount(t.Context(), tenantID, ownerID, created.ID); err != nil || connected || account != "" {
		t.Fatalf("account after disconnect = %q connected %v, %v", account, connected, err)
	}
	if err := database.QueryRow(t.Context(), `
		SELECT count(*) FROM encrypted_secrets WHERE tenant_id = $1`, tenantID,
	).Scan(&secretCount); err != nil {
		t.Fatal(err)
	}
	if secretCount != 0 {
		t.Fatalf("encrypted secrets after disconnect = %d, want 0", secretCount)
	}

	// Disconnecting an already-disconnected location is a no-op, not an error.
	if err := store.DisconnectCalendar(t.Context(), tenantID, ownerID, created.ID, secretStore); err != nil {
		t.Fatal(err)
	}

	// A connection with no account on record (the display-only email lookup
	// failed at connect time) must still report as connected: "connected"
	// tracks row presence, never the account string.
	if err := store.ConnectCalendar(
		t.Context(), tenantID, ownerID, created.ID, secretStore, "refresh-token-3", "",
	); err != nil {
		t.Fatal(err)
	}
	if account, connected, err := store.CalendarAccount(t.Context(), tenantID, ownerID, created.ID); err != nil || !connected || account != "" {
		t.Fatalf("account with empty email = %q connected %v, %v", account, connected, err)
	}
}
