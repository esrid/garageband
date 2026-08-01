package locations_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/esrid/garageband/internal/features/locations"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
)

func TestLocationLifecycleAndRoleCapabilities(t *testing.T) {
	database := dbtest.Open(t)
	ownerID := createUser(t, database, "location-owner@example.com")
	adminID := createUser(t, database, "location-admin@example.com")
	memberID := createUser(t, database, "location-member@example.com")
	tenantID := createTenant(t, database, ownerID)
	addMembership(t, database, tenantID, adminID, "admin")
	addMembership(t, database, tenantID, memberID, "member")
	store := locations.NewStore(database)

	created, err := store.Create(t.Context(), tenantID, ownerID, locations.Input{
		Name:         "  Atelier Nord  ",
		SIRET:        "12345678901234",
		PhoneE164:    "+596596123456",
		Email:        "CONTACT@EXAMPLE.COM",
		WebsiteURL:   "https://garage.example.com/nord",
		AddressLine1: "  10 rue des Ateliers  ",
		PostalCode:   "97200",
		City:         "Fort-de-France",
		CountryCode:  "fr",
		Timezone:     "America/Martinique",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Atelier Nord" || created.Email != "contact@example.com" ||
		created.CountryCode != "FR" || created.AddressLine1 != "10 rue des Ateliers" {
		t.Fatalf("location was not normalized: %#v", created)
	}
	if created.Status != "active" {
		t.Fatalf("new location status = %q, want active", created.Status)
	}
	if _, err := database.Exec(`
		INSERT INTO user_location_assignments (
			tenant_id, user_id, location_id, assigned_by_user_id
		) VALUES ($1, $2, $3, $4)`,
		tenantID, memberID, created.ID, ownerID,
	); err != nil {
		t.Fatal(err)
	}

	overview, err := store.Overview(t.Context(), tenantID, memberID)
	if err != nil {
		t.Fatal(err)
	}
	if overview.MembershipRole != "member" || overview.CanManage {
		t.Fatalf("member capability = %#v", overview)
	}
	if len(overview.Locations) != 1 || overview.Locations[0].ID != created.ID {
		t.Fatalf("member locations = %#v", overview.Locations)
	}
	if _, err := store.Create(
		t.Context(), tenantID, memberID, locations.Input{},
	); !errors.Is(err, locations.ErrForbidden) {
		t.Fatalf("member create: got %v, want ErrForbidden", err)
	}

	updated, err := store.Update(t.Context(), tenantID, adminID, created.ID, locations.Input{
		Name:         "Atelier Centre",
		SIRET:        "12345678901234",
		Email:        "centre@example.com",
		WebsiteURL:   "https://garage.example.com/centre",
		AddressLine1: "20 avenue du Centre",
		PostalCode:   "97200",
		City:         "Fort-de-France",
		CountryCode:  "FR",
		Timezone:     "America/Martinique",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Atelier Centre" || updated.PhoneE164 != "" {
		t.Fatalf("updated location = %#v", updated)
	}

	inactive, err := store.SetStatus(
		t.Context(), tenantID, adminID, created.ID, "inactive",
	)
	if err != nil {
		t.Fatal(err)
	}
	if inactive.Status != "inactive" {
		t.Fatalf("status = %q, want inactive", inactive.Status)
	}
	reopened, err := store.SetStatus(
		t.Context(), tenantID, ownerID, created.ID, "active",
	)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != "active" {
		t.Fatalf("status = %q, want active", reopened.Status)
	}
}

func TestLocationDatabaseValidationAndTenantBoundary(t *testing.T) {
	database := dbtest.Open(t)
	ownerID := createUser(t, database, "validation-owner@example.com")
	otherOwnerID := createUser(t, database, "other-owner@example.com")
	tenantID := createTenant(t, database, ownerID)
	otherTenantID := createTenant(t, database, otherOwnerID)
	store := locations.NewStore(database)

	valid := locations.Input{
		Name: "Atelier", CountryCode: "FR", Timezone: "Europe/Paris",
	}
	created, err := store.Create(t.Context(), tenantID, ownerID, valid)
	if err != nil {
		t.Fatal(err)
	}

	invalidTimezone := valid
	invalidTimezone.Timezone = "Mars/Olympus_Mons"
	if _, err := store.Create(
		t.Context(), tenantID, ownerID, invalidTimezone,
	); err == nil {
		t.Fatal("unknown timezone unexpectedly accepted")
	}

	invalidWebsite := valid
	invalidWebsite.WebsiteURL = "javascript:alert(1)"
	if _, err := store.Create(
		t.Context(), tenantID, ownerID, invalidWebsite,
	); err == nil {
		t.Fatal("invalid website unexpectedly accepted")
	}

	if _, err := store.SetStatus(
		t.Context(), tenantID, ownerID, created.ID, "deleted",
	); err == nil {
		t.Fatal("invalid status unexpectedly accepted")
	}
	if _, err := store.Update(
		t.Context(), otherTenantID, otherOwnerID, created.ID, valid,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-tenant update: got %v, want sql.ErrNoRows", err)
	}
	if _, err := store.Overview(
		t.Context(), tenantID, otherOwnerID,
	); !errors.Is(err, locations.ErrForbidden) {
		t.Fatalf("non-member overview: got %v, want ErrForbidden", err)
	}
}

func TestLocationScheduleLifecycleAndManagerBoundary(t *testing.T) {
	fixtures, runtime := dbtest.OpenRuntime(t)
	ownerID := createUser(t, fixtures, "schedule-owner@example.com")
	memberID := createUser(t, fixtures, "schedule-member@example.com")
	tenantID := createTenant(t, fixtures, ownerID)
	addMembership(t, fixtures, tenantID, memberID, "member")
	store := locations.NewStore(runtime)
	location, err := store.Create(t.Context(), tenantID, ownerID, locations.Input{
		Name: "Atelier", CountryCode: "FR", Timezone: "America/Martinique",
	})
	if err != nil {
		t.Fatal(err)
	}
	resourceID, err := store.AddResource(t.Context(), tenantID, ownerID, location.ID, locations.ResourceInput{
		Kind: "technician", Name: "Alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	var serviceID string
	if err := fixtures.QueryRow(`
		INSERT INTO service_offerings (
		    tenant_id, location_id, code, name, duration_minutes
		) VALUES ($1, $2, 'diagnostic', 'Diagnostic', 45)
		RETURNING id::text`, tenantID, location.ID).Scan(&serviceID); err != nil {
		t.Fatal(err)
	}
	requirement := locations.RequirementInput{ServiceID: serviceID, Kind: "technician", Quantity: 1}
	if err := store.UpsertRequirement(t.Context(), tenantID, ownerID, location.ID, requirement); err != nil {
		t.Fatal(err)
	}

	hour := locations.OpeningHourInput{Weekday: 1, OpensAt: "08:00", ClosesAt: "12:00"}
	if err := store.AddOpeningHour(t.Context(), tenantID, ownerID, location.ID, hour); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOpeningHour(t.Context(), tenantID, ownerID, location.ID, locations.OpeningHourInput{
		Weekday: 1, OpensAt: "11:00", ClosesAt: "13:00",
	}); err == nil {
		t.Fatal("overlapping opening window unexpectedly accepted")
	}
	closureID, err := store.AddClosure(t.Context(), tenantID, ownerID, location.ID, locations.ClosureInput{
		StartsDate: "2030-08-12", StartsTime: "10:00",
		EndsDate: "2030-08-12", EndsTime: "12:00", Reason: "Réunion",
	})
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := store.Schedule(t.Context(), tenantID, ownerID, location.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !schedule.Enabled || len(schedule.OpeningHours) != 1 || len(schedule.Closures) != 1 ||
		len(schedule.Resources) != 1 || len(schedule.Services) != 1 ||
		len(schedule.Services[0].Requirements) != 1 {
		t.Fatalf("schedule = %#v", schedule)
	}
	if schedule.Closures[0].StartsAt.Format("2006-01-02 15:04 -07:00") != "2030-08-12 10:00 -04:00" {
		t.Fatalf("local closure = %v", schedule.Closures[0].StartsAt)
	}
	if err := store.AddOpeningHour(t.Context(), tenantID, memberID, location.ID, locations.OpeningHourInput{
		Weekday: 2, OpensAt: "08:00", ClosesAt: "12:00",
	}); !errors.Is(err, locations.ErrForbidden) {
		t.Fatalf("member add hour = %v, want ErrForbidden", err)
	}
	if _, err := store.AddResource(t.Context(), tenantID, memberID, location.ID, locations.ResourceInput{
		Kind: "bay", Name: "Pont interdit",
	}); !errors.Is(err, locations.ErrForbidden) {
		t.Fatalf("member add resource = %v, want ErrForbidden", err)
	}
	if err := store.DeleteRequirement(t.Context(), tenantID, ownerID, location.ID, requirement); err != nil {
		t.Fatal(err)
	}
	if err := store.SetResourceActive(t.Context(), tenantID, ownerID, location.ID, resourceID, false); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteClosure(t.Context(), tenantID, ownerID, location.ID, closureID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteOpeningHour(t.Context(), tenantID, ownerID, location.ID, hour); err != nil {
		t.Fatal(err)
	}
	schedule, err = store.Schedule(t.Context(), tenantID, ownerID, location.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !schedule.Enabled || len(schedule.OpeningHours) != 0 || len(schedule.Closures) != 0 {
		t.Fatalf("empty configured schedule = %#v", schedule)
	}
}

func createUser(t *testing.T, database *db.DB, email string) string {
	t.Helper()
	var userID string
	if err := database.QueryRow(`
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', $1, $1, 'Test User')
		RETURNING id`, email).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return userID
}

func createTenant(t *testing.T, database *db.DB, ownerID string) string {
	t.Helper()
	var tenantID string
	err := database.WithinNewTenantUser(t.Context(), ownerID, func(tx *sql.Tx, id string) error {
		tenantID = id
		if _, err := tx.ExecContext(t.Context(), `
			INSERT INTO tenants (id, slug, name)
			VALUES ($1::uuid, 'tenant-' || left(replace($1::text, '-', ''), 12), 'Garage')`, id,
		); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), `
			INSERT INTO tenant_memberships (tenant_id, user_id, role)
			VALUES ($1, $2, 'owner')`, id, ownerID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tenantID
}

func addMembership(
	t *testing.T,
	database *db.DB,
	tenantID string,
	userID string,
	role string,
) {
	t.Helper()
	if _, err := database.Exec(`
		INSERT INTO tenant_memberships (tenant_id, user_id, role)
		VALUES ($1, $2, $3)`, tenantID, userID, role); err != nil {
		t.Fatal(err)
	}
}
