package db

import (
	"context"
	"embed"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// Migrations live in migrations/*.sql (goose format: -- +goose Up / Down).
// Rule: migration SQL must be dialect-neutral — it runs unchanged on both
// SQLite and PostgreSQL. Stick to TEXT / INTEGER / BOOLEAN columns, TEXT
// primary keys generated in Go, and RFC 3339 TEXT timestamps.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies the embedded goose migrations. Goose splits and runs
// statements itself, in a transaction per migration, on both dialects.
// Verified against https://pkg.go.dev/github.com/pressly/goose/v3 2026-08-01.
func Migrate(ctx context.Context, d *DB) error {
	dialect := goose.DialectSQLite3
	if d.dialect == "postgres" {
		dialect = goose.DialectPostgres
	}
	fsys, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(dialect, d.DB, fsys)
	if err != nil {
		return err
	}
	// No p.Close(): it would close the shared *sql.DB we keep using.
	_, err = p.Up(ctx)
	return err
}
