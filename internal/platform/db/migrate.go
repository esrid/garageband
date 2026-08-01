package db

import (
	"context"
	"embed"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// Migrations live in migrations/*.sql (goose format: -- +goose Up / Down) and
// may use native PostgreSQL features.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies the embedded goose migrations. Goose splits and runs
// statements itself, in a transaction per migration.
// Verified against https://pkg.go.dev/github.com/pressly/goose/v3 2026-08-01.
func Migrate(ctx context.Context, d *DB) error {
	fsys, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, d.DB, fsys)
	if err != nil {
		return err
	}
	// No p.Close(): it would close the shared *sql.DB we keep using.
	_, err = p.Up(ctx)
	return err
}
