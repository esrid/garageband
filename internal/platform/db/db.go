// Package db opens the database from a single DSN and papers over the two
// supported dialects: SQLite (modernc.org/sqlite) and PostgreSQL (pgx).
// Verified against https://pkg.go.dev/modernc.org/sqlite and
// https://pkg.go.dev/github.com/jackc/pgx/v5/stdlib 2026-08-01.
package db

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // registers driver "pgx"
	_ "modernc.org/sqlite"             // registers driver "sqlite"
)

type DB struct {
	*sql.DB
	dialect string // "sqlite" or "postgres"
}

// Open picks the driver from the DSN: postgres:// or postgresql:// means pgx,
// anything else is treated as a SQLite path or file: URI.
func Open(dsn string) (*DB, error) {
	driver, dialect := "sqlite", "sqlite"
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		driver, dialect = "pgx", "postgres"
	}
	sqldb, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	if err := sqldb.Ping(); err != nil {
		return nil, errors.Join(err, sqldb.Close())
	}
	return &DB{DB: sqldb, dialect: dialect}, nil
}

// ExecOne runs a write (with ? placeholders, rewritten via R) that must
// affect exactly one row. It returns sql.ErrNoRows when nothing was affected
// — typically an unknown id — so handlers can map it to a 404.
func (d *DB) ExecOne(ctx context.Context, query string, args ...any) error {
	res, err := d.ExecContext(ctx, d.R(query), args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// R rewrites ? placeholders to $1..$n so queries can be written once with ?
// and still run on PostgreSQL.
// ponytail: naive scan — no ? inside string literals in your SQL. Move to sqlc
// if queries outgrow this.
func (d *DB) R(query string) string {
	if d.dialect != "postgres" {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
