package db

import (
	"context"
	"embed"
	"errors"
	"io/fs"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Migrations live in migrations/*.sql (goose format: -- +goose Up / Down) and
// may use native PostgreSQL features.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies the embedded goose migrations. Goose splits and runs
// statements itself, in a transaction per migration. It needs a
// database/sql.DB, which the app's pgxpool.Pool doesn't provide, so this
// opens a short-lived stdlib connection (same config, e.g. same search_path)
// scoped to this call only.
// Verified against https://pkg.go.dev/github.com/pressly/goose/v3 and
// https://pkg.go.dev/github.com/jackc/pgx/v5/stdlib 2026-08-02.
func Migrate(ctx context.Context, d *DB) (err error) {
	fsys, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	sqldb := stdlib.OpenDB(*d.Config().ConnConfig)
	defer func() { err = errors.Join(err, sqldb.Close()) }()

	p, err := goose.NewProvider(goose.DialectPostgres, sqldb, fsys)
	if err != nil {
		return err
	}
	_, err = p.Up(ctx)
	return err
}
