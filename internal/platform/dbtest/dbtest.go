// Package dbtest creates an isolated PostgreSQL schema for integration tests.
// Verified against https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool
// 2026-08-02.
package dbtest

import (
	"context"
	"crypto/rand"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

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

	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := db.OpenConfig(adminConfig)
	if err != nil {
		t.Fatal(err)
	}

	schema := "test_" + strings.ToLower(rand.Text())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(
		context.Background(),
		"CREATE SCHEMA "+quotedSchema,
	); err != nil {
		if closeErr := admin.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal(err)
	}

	testConfig := adminConfig.Copy()
	if testConfig.ConnConfig.RuntimeParams == nil {
		testConfig.ConnConfig.RuntimeParams = make(map[string]string)
	}
	testConfig.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	database, err := db.OpenConfig(testConfig)
	if err != nil {
		if _, dropErr := admin.Exec(
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
		if _, err := admin.Exec(
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

// OpenRuntime returns separate fixture and application connections to the same
// isolated schema. The application connection SET ROLEs to a non-superuser on
// every physical connection, so store tests cannot accidentally bypass forced
// RLS with Testcontainers' administrative bootstrap user.
func OpenRuntime(t testing.TB) (fixtures *db.DB, runtime *db.DB) {
	t.Helper()
	fixtures = Open(t)
	runtimeRole := RuntimeRole(t, fixtures)

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := fixtures.QueryRow(context.Background(), `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	quotedRole := pgx.Identifier{runtimeRole}.Sanitize()
	previousAfterConnect := config.ConnConfig.AfterConnect
	config.ConnConfig.AfterConnect = func(ctx context.Context, connection *pgconn.PgConn) error {
		if previousAfterConnect != nil {
			if err := previousAfterConnect(ctx, connection); err != nil {
				return err
			}
		}
		return connection.Exec(ctx, `SET ROLE `+quotedRole).Close()
	}
	runtime, err = db.OpenConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixtures, runtime
}

// RuntimeRole creates a temporary non-superuser role with application-level
// table privileges. RLS tests must not run as a superuser because PostgreSQL
// superusers always bypass row security.
func RuntimeRole(t testing.TB, database *db.DB) string {
	t.Helper()
	role := "test_runtime_" + strings.ToLower(rand.Text())
	quotedRole := pgx.Identifier{role}.Sanitize()
	ctx := context.Background()

	var schema string
	if err := database.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := database.Exec(ctx, `CREATE ROLE `+quotedRole+` NOLOGIN`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := database.Exec(ctx, `DROP OWNED BY `+quotedRole); err != nil {
			t.Error(err)
		}
		if _, err := database.Exec(ctx, `DROP ROLE `+quotedRole); err != nil {
			t.Error(err)
		}
	})

	if _, err := database.Exec(
		ctx, `GRANT USAGE ON SCHEMA `+quotedSchema+` TO `+quotedRole,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		ctx, `GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA `+
			quotedSchema+` TO `+quotedRole,
	); err != nil {
		t.Fatal(err)
	}
	return role
}

func SetLocalRole(ctx context.Context, tx pgx.Tx, role string) error {
	quotedRole := pgx.Identifier{role}.Sanitize()
	_, err := tx.Exec(ctx, `SET LOCAL ROLE `+quotedRole)
	return err
}
