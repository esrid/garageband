package voice_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/esrid/garageband/internal/features/voice"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
)

type fixture struct {
	fixtures     *db.DB
	store        *voice.Store
	tenantID     string
	ownerID      string
	memberID     string
	locationID   string
	connectionID string
}

func TestSubaccountIsOnePerOrganization(t *testing.T) {
	f := newFixture(t)

	if _, found, err := f.store.Account(t.Context(), f.tenantID, f.ownerID); err != nil {
		t.Fatal(err)
	} else if found {
		t.Fatal("a fresh organization already has a subaccount")
	}

	if err := f.store.LinkAccount(t.Context(), f.tenantID, f.ownerID, "AC123"); err != nil {
		t.Fatal(err)
	}
	account, found, err := f.store.Account(t.Context(), f.tenantID, f.ownerID)
	if err != nil || !found {
		t.Fatalf("account = %+v, found = %v, err = %v", account, found, err)
	}
	if account.SubaccountSID != "AC123" || account.Status != "active" {
		t.Fatalf("account = %+v", account)
	}

	// A retried provisioning run must not split one garage across two carrier
	// accounts; the table refuses rather than the caller remembering to check.
	err = f.store.LinkAccount(t.Context(), f.tenantID, f.ownerID, "AC456")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("second subaccount = %v", err)
	}
}

// Provisioning is owner work: a member who can see the number must not be able
// to buy or release one.
func TestProvisioningIsOwnerScoped(t *testing.T) {
	f := newFixture(t)

	if err := f.store.LinkAccount(t.Context(), f.tenantID, f.memberID, "AC999"); err == nil {
		t.Fatal("a member linked a carrier subaccount")
	}

	bundleID := f.approvedBundle(t)
	if _, err := f.store.AttachNumber(t.Context(), f.tenantID, f.memberID, voice.Number{
		LocationID:   f.locationID,
		ConnectionID: f.connectionID,
		BundleID:     bundleID,
		E164:         "+33123456789",
		ProviderSID:  "PN1",
	}); err == nil {
		t.Fatal("a member bought a number")
	}
}

func TestNumberLifecycleFreesTheE164OnRelease(t *testing.T) {
	f := newFixture(t)
	bundleID := f.approvedBundle(t)

	numberID, err := f.store.AttachNumber(t.Context(), f.tenantID, f.ownerID, voice.Number{
		LocationID:     f.locationID,
		ConnectionID:   f.connectionID,
		BundleID:       bundleID,
		E164:           "+33123456789",
		ProviderSID:    "PN1",
		WhatsAppSender: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// It lands as provisioning: the number exists at the carrier before its
	// webhooks are wired, and that gap is a state rather than a lie.
	if got := f.status(t, numberID); got != "provisioning" {
		t.Fatalf("status after attach = %q", got)
	}
	if err := f.store.ActivateNumber(t.Context(), f.tenantID, f.ownerID, numberID); err != nil {
		t.Fatal(err)
	}
	if got := f.status(t, numberID); got != "active" {
		t.Fatalf("status after activation = %q", got)
	}

	// While it is held, the same E.164 cannot be bought twice.
	_, err = f.store.AttachNumber(t.Context(), f.tenantID, f.ownerID, voice.Number{
		LocationID: f.locationID, ConnectionID: f.connectionID, BundleID: bundleID,
		E164: "+33123456789", ProviderSID: "PN2",
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate held number = %v", err)
	}

	if err := f.store.ReleaseNumber(t.Context(), f.tenantID, f.ownerID, numberID); err != nil {
		t.Fatal(err)
	}
	if got := f.status(t, numberID); got != "released" {
		t.Fatalf("status after release = %q", got)
	}
	// Releasing twice is not a second release.
	if err := f.store.ReleaseNumber(
		t.Context(), f.tenantID, f.ownerID, numberID,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("releasing twice = %v", err)
	}

	// The released row stays for its history, and the number is buyable again
	// without a separate cleanup step.
	if _, err := f.store.AttachNumber(t.Context(), f.tenantID, f.ownerID, voice.Number{
		LocationID: f.locationID, ConnectionID: f.connectionID, BundleID: bundleID,
		E164: "+33123456789", ProviderSID: "PN3",
	}); err != nil {
		t.Fatalf("rebuying a released number: %v", err)
	}
}

func TestBundleRecordsTheReviewItIsWaitingOn(t *testing.T) {
	f := newFixture(t)
	submitted := time.Now().UTC().Truncate(time.Second)

	if _, err := f.store.RecordBundle(t.Context(), f.tenantID, f.ownerID, voice.Bundle{
		SID: "BU1", ISOCountry: "FR", NumberType: "local",
		Status: "pending-review", SubmittedAt: &submitted,
	}); err != nil {
		t.Fatal(err)
	}

	// One live file per country and number type: a second submission for the
	// same pair is a mistake, not a queue.
	_, err := f.store.RecordBundle(t.Context(), f.tenantID, f.ownerID, voice.Bundle{
		SID: "BU2", ISOCountry: "FR", NumberType: "local",
		Status: "draft", SubmittedAt: &submitted,
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("second live bundle = %v", err)
	}

	reviewed := submitted.Add(48 * time.Hour)
	if err := f.store.SyncBundleStatus(
		t.Context(), f.tenantID, f.ownerID, "BU1", "twilio-rejected",
		"address could not be verified", &reviewed,
	); err != nil {
		t.Fatal(err)
	}

	var status, reason string
	if err := f.fixtures.QueryRow(t.Context(), `
		SELECT status, failure_reason FROM regulatory_bundles WHERE bundle_sid = 'BU1'`,
	).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "twilio-rejected" || reason != "address could not be verified" {
		t.Fatalf("status = %q, reason = %q", status, reason)
	}

	// An approval carries no reason, whatever the caller passes: the table
	// refuses to keep a rejection's excuse on an approved file.
	if err := f.store.SyncBundleStatus(
		t.Context(), f.tenantID, f.ownerID, "BU1", "twilio-approved",
		"address could not be verified", &reviewed,
	); err != nil {
		t.Fatal(err)
	}
	var lingering *string
	if err := f.fixtures.QueryRow(t.Context(), `
		SELECT failure_reason FROM regulatory_bundles WHERE bundle_sid = 'BU1'`,
	).Scan(&lingering); err != nil {
		t.Fatal(err)
	}
	if lingering != nil {
		t.Fatalf("approved bundle kept a failure reason: %q", *lingering)
	}
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	fixtures, runtime := dbtest.OpenRuntime(t)
	f := fixture{fixtures: fixtures, store: voice.NewStore(runtime)}
	f.ownerID = insertID(t, fixtures, `
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', 'voice-owner', 'voice-owner@example.com', 'Voice Owner')
		RETURNING id::text`)
	f.memberID = insertID(t, fixtures, `
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', 'voice-member', 'voice-member@example.com', 'Voice Member')
		RETURNING id::text`)
	f.tenantID = insertID(t, fixtures, `
		INSERT INTO tenants (slug, name)
		VALUES ('voice-garage', 'Garage Voice') RETURNING id::text`)
	exec(t, fixtures, `
		INSERT INTO tenant_memberships (tenant_id, user_id, role)
		VALUES ($1, $2, 'owner'), ($1, $3, 'member')`,
		f.tenantID, f.ownerID, f.memberID)
	f.locationID = insertID(t, fixtures, `
		INSERT INTO locations (tenant_id, slug, name, timezone)
		VALUES ($1, 'central', 'Atelier Central', 'Europe/Paris')
		RETURNING id::text`, f.tenantID)
	exec(t, fixtures, `
		INSERT INTO user_location_assignments (
		    tenant_id, user_id, location_id, assigned_by_user_id
		) VALUES ($1, $2, $3, $4)`,
		f.tenantID, f.memberID, f.locationID, f.ownerID)
	f.connectionID = insertID(t, fixtures, `
		INSERT INTO provider_connections (
		    tenant_id, location_id, kind, provider, external_account_id, secret_ref
		) VALUES ($1, $2, 'telephony', 'twilio', 'AC123', 'secret/test')
		RETURNING id::text`, f.tenantID, f.locationID)
	return f
}

func (f fixture) approvedBundle(t *testing.T) string {
	t.Helper()
	return insertID(t, f.fixtures, `
		INSERT INTO regulatory_bundles (
		    tenant_id, bundle_sid, iso_country, number_type, status, submitted_at
		) VALUES ($1, 'BU-approved', 'FR', 'local', 'twilio-approved', now())
		RETURNING id::text`, f.tenantID)
}

func (f fixture) status(t *testing.T, numberID string) string {
	t.Helper()
	var status string
	if err := f.fixtures.QueryRow(t.Context(),
		`SELECT status FROM phone_numbers WHERE id = $1`, numberID,
	).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func insertID(t *testing.T, database *db.DB, query string, args ...any) string {
	t.Helper()
	var id string
	if err := database.QueryRow(t.Context(), query, args...).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func exec(t *testing.T, database *db.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}
