package secrets_test

import (
	"crypto/rand"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
	"github.com/esrid/garageband/internal/platform/secrets"
)

func TestAESStoreRejectsAShortKey(t *testing.T) {
	if _, err := secrets.NewAESStore([]byte("too-short")); err == nil {
		t.Fatal("expected an error for a non-32-byte key")
	}
}

func TestAESStoreRoundTripsThroughRLS(t *testing.T) {
	fixtures, runtime := dbtest.OpenRuntime(t)
	tenantID := insertTenant(t, fixtures, "secrets-tenant", "Garage Secrets")
	ownerID := insertUser(t, fixtures, "secrets-owner@example.com")
	memberID := insertUser(t, fixtures, "secrets-member@example.com")
	insertMembership(t, fixtures, tenantID, ownerID, "owner")
	insertMembership(t, fixtures, tenantID, memberID, "member")

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	store, err := secrets.NewAESStore(key)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("refresh-token-value")
	var reference string
	if err := runtime.WithinTenantUser(t.Context(), tenantID, ownerID, func(tx pgx.Tx) error {
		var err error
		reference, err = store.Store(t.Context(), tx, tenantID, plaintext)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if reference == "" {
		t.Fatal("empty secret reference")
	}

	// A regular member can resolve it: resolution is a side effect of an
	// already-authorized action, not a privileged operation on its own.
	var resolved []byte
	if err := runtime.WithinTenantUser(t.Context(), tenantID, memberID, func(tx pgx.Tx) error {
		var err error
		resolved, err = store.Resolve(t.Context(), tx, reference)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if string(resolved) != string(plaintext) {
		t.Fatalf("resolved = %q, want %q", resolved, plaintext)
	}

	// A regular member cannot write or delete a secret: the RLS policy, not
	// application code, enforces this.
	if err := runtime.WithinTenantUser(t.Context(), tenantID, memberID, func(tx pgx.Tx) error {
		_, err := store.Store(t.Context(), tx, tenantID, plaintext)
		return err
	}); err == nil {
		t.Fatal("member store succeeded, want an RLS rejection")
	}

	if err := runtime.WithinTenantUser(t.Context(), tenantID, ownerID, func(tx pgx.Tx) error {
		return store.Delete(t.Context(), tx, reference)
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WithinTenantUser(t.Context(), tenantID, ownerID, func(tx pgx.Tx) error {
		_, err := store.Resolve(t.Context(), tx, reference)
		return err
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("resolve after delete = %v, want pgx.ErrNoRows", err)
	}
}

func insertUser(t *testing.T, database *db.DB, email string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(t.Context(), `
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', $1, $1, 'Test User') RETURNING id`, email,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertTenant(t *testing.T, database *db.DB, slug, name string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(t.Context(), `
		INSERT INTO tenants (slug, name) VALUES ($1, $2) RETURNING id`, slug, name,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertMembership(t *testing.T, database *db.DB, tenantID, userID, role string) {
	t.Helper()
	if _, err := database.Exec(t.Context(), `
		INSERT INTO tenant_memberships (tenant_id, user_id, role)
		VALUES ($1, $2, $3)`, tenantID, userID, role,
	); err != nil {
		t.Fatal(err)
	}
}
