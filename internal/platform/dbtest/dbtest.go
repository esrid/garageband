// Package dbtest creates an isolated PostgreSQL schema for integration tests.
package dbtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/db"
)

func Open(t testing.TB) *db.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}

	adminConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := db.OpenConfig(adminConfig)
	if err != nil {
		t.Fatal(err)
	}

	schema := "test_" + strings.ToLower(rand.Text())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.ExecContext(
		context.Background(),
		"CREATE SCHEMA "+quotedSchema,
	); err != nil {
		if closeErr := admin.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal(err)
	}

	testConfig := adminConfig.Copy()
	if testConfig.RuntimeParams == nil {
		testConfig.RuntimeParams = make(map[string]string)
	}
	testConfig.RuntimeParams["search_path"] = schema + ",public"
	database, err := db.OpenConfig(testConfig)
	if err != nil {
		if _, dropErr := admin.ExecContext(
			context.Background(),
			"DROP SCHEMA "+quotedSchema+" CASCADE",
		); dropErr != nil {
			t.Error(dropErr)
		}
		if closeErr := admin.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
		if _, err := admin.ExecContext(
			context.Background(),
			"DROP SCHEMA "+quotedSchema+" CASCADE",
		); err != nil {
			t.Error(err)
		}
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})

	if err := db.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	return database
}

// RuntimeRole creates a temporary non-superuser role with application-level
// table privileges. RLS tests must not run as a superuser because PostgreSQL
// superusers always bypass row security.
func RuntimeRole(t testing.TB, database *db.DB) string {
	t.Helper()
	role := "test_runtime_" + strings.ToLower(rand.Text())
	quotedRole := pgx.Identifier{role}.Sanitize()

	var schema string
	if err := database.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := database.Exec(`CREATE ROLE ` + quotedRole + ` NOLOGIN`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := database.Exec(`DROP OWNED BY ` + quotedRole); err != nil {
			t.Error(err)
		}
		if _, err := database.Exec(`DROP ROLE ` + quotedRole); err != nil {
			t.Error(err)
		}
	})

	if _, err := database.Exec(
		`GRANT USAGE ON SCHEMA ` + quotedSchema + ` TO ` + quotedRole,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA ` +
			quotedSchema + ` TO ` + quotedRole,
	); err != nil {
		t.Fatal(err)
	}
	return role
}

func SetLocalRole(ctx context.Context, tx *sql.Tx, role string) error {
	quotedRole := pgx.Identifier{role}.Sanitize()
	_, err := tx.ExecContext(ctx, `SET LOCAL ROLE `+quotedRole)
	return err
}
