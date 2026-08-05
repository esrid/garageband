package voice_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/esrid/garageband/internal/features/voice"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
)

type fixture struct {
	fixtures     *db.DB
	runtime      *db.DB
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

// The runtime writes a call while it is happening: a tenant is set, no user
// is, because the caller is anonymous and no employee is signed in. Migration
// 0032 is what lets that write land at all.
func TestCallIsRecordedWithoutASignedInUser(t *testing.T) {
	f := newFixture(t)
	route := f.route(t)
	started := time.Now().UTC().Truncate(time.Second)

	callID, err := f.store.StartCall(
		t.Context(), route, "CA-1", "+596696000000", "+33123456789", started,
	)
	if err != nil {
		t.Fatal(err)
	}

	// A socket that reconnects for the same call must find its row, not open a
	// second one.
	again, err := f.store.StartCall(
		t.Context(), route, "CA-1", "+596696000000", "+33123456789", started,
	)
	if err != nil || again != callID {
		t.Fatalf("reconnect opened %q instead of %q (err %v)", again, callID, err)
	}

	if err := f.store.AppendTurns(t.Context(), route.TenantID, callID, []voice.Turn{
		{Role: voice.RoleAgent, Text: "Bonjour, garage Central."},
		{Role: voice.RoleCaller, Text: "Vous fermez à quelle heure ?"},
	}, started); err != nil {
		t.Fatal(err)
	}
	// A second flush continues the numbering rather than colliding with it.
	if err := f.store.AppendTurns(t.Context(), route.TenantID, callID, []voice.Turn{
		{Role: voice.RoleAgent, Text: "Jusqu'à 18 heures."},
	}, started.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	var stored int
	if err := f.fixtures.QueryRow(t.Context(), `
		SELECT count(*) FROM call_messages WHERE call_id = $1`, callID,
	).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 3 {
		t.Fatalf("stored %d turns, want 3", stored)
	}

	if err := f.store.EndCall(
		t.Context(), route.TenantID, callID, "completed", started.Add(time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := f.fixtures.QueryRow(t.Context(),
		`SELECT status FROM calls WHERE id = $1`, callID,
	).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "completed" {
		t.Fatalf("status = %q", status)
	}
	// Ending twice is not a second ending.
	if err := f.store.EndCall(
		t.Context(), route.TenantID, callID, "failed", started.Add(2*time.Minute),
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second end = %v", err)
	}
}

// The runtime may read the conversation it is holding — RETURNING and the
// sequence lookup need it — and nothing else. A dossier stays out of reach
// without a signed-in user, which is the whole point of the narrow policy.
func TestCallRuntimeReachesItsCallAndNothingElse(t *testing.T) {
	f := newFixture(t)
	route := f.route(t)
	if _, err := f.store.StartCall(
		t.Context(), route, "CA-2", "+596696000000", "+33123456789", time.Now(),
	); err != nil {
		t.Fatal(err)
	}
	f.insertCustomer(t)

	var calls, customers int
	if err := f.runtime.WithinTenant(t.Context(), route.TenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM calls`).Scan(&calls); err != nil {
			return err
		}
		return tx.QueryRow(t.Context(), `SELECT count(*) FROM customers`).Scan(&customers)
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("the runtime sees %d of its own calls, want 1", calls)
	}
	if customers != 0 {
		t.Fatalf("a user-less reader reached %d customer dossiers", customers)
	}
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	fixtures, runtime := dbtest.OpenRuntime(t)
	f := fixture{fixtures: fixtures, runtime: runtime, store: voice.NewStore(runtime)}
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

// route provisions an active number for the site and returns everything a call
// carries in its signed webhook URL.
func (f fixture) route(t *testing.T) voice.Route {
	t.Helper()
	numberID := insertID(t, f.fixtures, `
		INSERT INTO phone_numbers (
		    tenant_id, location_id, telephony_connection_id,
		    phone_e164, external_number_id, status
		) VALUES ($1, $2, $3, '+33123456789', 'PN-route', 'active')
		RETURNING id::text`, f.tenantID, f.locationID, f.connectionID)
	var agentID string
	if err := f.fixtures.QueryRow(t.Context(), `
		SELECT id::text FROM agents WHERE tenant_id = $1 AND location_id = $2`,
		f.tenantID, f.locationID,
	).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	return voice.Route{
		TenantID:   f.tenantID,
		LocationID: f.locationID,
		AgentID:    agentID,
		NumberID:   numberID,
	}
}

func (f fixture) insertCustomer(t *testing.T) {
	t.Helper()
	exec(t, f.fixtures, `
		INSERT INTO customers (tenant_id, home_location_id, first_name, last_name)
		VALUES ($1, $2, 'Test', 'Client')`, f.tenantID, f.locationID)
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
