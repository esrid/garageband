package db_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
)

func TestOpenRequiresDatabaseURL(t *testing.T) {
	if _, err := db.Open(""); err == nil {
		t.Fatal("empty DATABASE_URL unexpectedly accepted")
	}
}

func TestExecOne(t *testing.T) {
	d := dbtest.Open(t)
	if _, err := d.Exec(t.Context(), `CREATE TABLE exec_one_test (id UUID PRIMARY KEY DEFAULT uuidv7())`); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecOne(t.Context(), `INSERT INTO exec_one_test DEFAULT VALUES`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := d.ExecOne(
		t.Context(),
		`DELETE FROM exec_one_test WHERE id = $1`,
		"00000000-0000-0000-0000-000000000000",
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown id: got %v, want sql.ErrNoRows", err)
	}
}

func TestWithinTenant(t *testing.T) {
	d := dbtest.Open(t)
	const tenantID = "0198a421-8b51-7f34-a723-4c1b49a4174e"

	var got string
	err := d.WithinTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(
			t.Context(), `SELECT current_setting('app.current_tenant_id')`,
		).Scan(&got)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != tenantID {
		t.Fatalf("tenant context: got %q, want %q", got, tenantID)
	}
}

func TestWithinTenantRequiresID(t *testing.T) {
	d := dbtest.Open(t)
	if err := d.WithinTenant(t.Context(), "", func(pgx.Tx) error {
		return nil
	}); !errors.Is(err, db.ErrTenantRequired) {
		t.Fatalf("got %v, want ErrTenantRequired", err)
	}
}

func TestWithinUser(t *testing.T) {
	d := dbtest.Open(t)
	const userID = "0198a421-8b51-7f34-a723-4c1b49a4174e"

	var got string
	err := d.WithinUser(t.Context(), userID, func(tx pgx.Tx) error {
		return tx.QueryRow(
			t.Context(), `SELECT current_setting('app.current_user_id')`,
		).Scan(&got)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != userID {
		t.Fatalf("user context: got %q, want %q", got, userID)
	}
}

func TestWithinUserRequiresID(t *testing.T) {
	d := dbtest.Open(t)
	if err := d.WithinUser(t.Context(), "", func(pgx.Tx) error {
		return nil
	}); !errors.Is(err, db.ErrUserRequired) {
		t.Fatalf("got %v, want ErrUserRequired", err)
	}
}

func TestWithinTenantUser(t *testing.T) {
	d := dbtest.Open(t)
	const tenantID = "0198a421-8b51-7f34-a723-4c1b49a4174e"
	const userID = "0198a421-8b51-7f34-a723-4c1b49a4174f"

	var gotTenant, gotUser string
	err := d.WithinTenantUser(t.Context(), tenantID, userID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `
			SELECT current_setting('app.current_tenant_id'),
			       current_setting('app.current_user_id')`,
		).Scan(&gotTenant, &gotUser)
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotTenant != tenantID || gotUser != userID {
		t.Fatalf(
			"principal context: got tenant %q user %q, want tenant %q user %q",
			gotTenant, gotUser, tenantID, userID,
		)
	}
}

func TestWithinTenantUserRequiresPrincipal(t *testing.T) {
	d := dbtest.Open(t)
	callback := func(pgx.Tx) error { return nil }
	if err := d.WithinTenantUser(
		t.Context(), "", "0198a421-8b51-7f34-a723-4c1b49a4174f", callback,
	); !errors.Is(err, db.ErrTenantRequired) {
		t.Fatalf("missing tenant: got %v, want ErrTenantRequired", err)
	}
	if err := d.WithinTenantUser(
		t.Context(), "0198a421-8b51-7f34-a723-4c1b49a4174e", "", callback,
	); !errors.Is(err, db.ErrUserRequired) {
		t.Fatalf("missing user: got %v, want ErrUserRequired", err)
	}
	if err := d.WithinNewTenantUser(
		t.Context(), "", func(pgx.Tx, string) error { return nil },
	); !errors.Is(err, db.ErrUserRequired) {
		t.Fatalf("new tenant missing user: got %v, want ErrUserRequired", err)
	}
}
