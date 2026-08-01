// Package db opens and provides the PostgreSQL database used by the app.
// Verified against https://pkg.go.dev/github.com/jackc/pgx/v5/stdlib
// 2026-08-01.
package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type DB struct {
	*sql.DB
}

var (
	ErrTenantRequired = errors.New("tenant id is required")
	ErrUserRequired   = errors.New("user id is required")
)

// Open connects to PostgreSQL using either a URL or pgx keyword DSN.
func Open(dsn string) (*DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	return OpenConfig(config)
}

// OpenConfig connects using a parsed pgx configuration. It is primarily useful
// for tests that set an isolated search_path.
func OpenConfig(config *pgx.ConnConfig) (*DB, error) {
	if config == nil {
		return nil, errors.New("PostgreSQL config is required")
	}
	sqldb := stdlib.OpenDB(*config)
	if err := sqldb.Ping(); err != nil {
		return nil, errors.Join(err, sqldb.Close())
	}
	return &DB{DB: sqldb}, nil
}

// ExecOne runs a write that must affect exactly one row. It returns
// sql.ErrNoRows when nothing was affected so handlers can map it to a 404.
func (d *DB) ExecOne(ctx context.Context, query string, args ...any) error {
	res, err := d.ExecContext(ctx, query, args...)
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

// WithinTenant runs fn in a transaction whose PostgreSQL RLS context is scoped
// to tenantID. set_config(..., true) is transaction-local, so a pooled
// connection cannot leak one tenant's context into another request.
func (d *DB) WithinTenant(
	ctx context.Context,
	tenantID string,
	fn func(*sql.Tx) error,
) (err error) {
	if strings.TrimSpace(tenantID) == "" {
		return ErrTenantRequired
	}
	if fn == nil {
		return errors.New("tenant transaction function is required")
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil &&
			!errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()

	if _, err = tx.ExecContext(
		ctx,
		`SELECT set_config('app.current_tenant_id', $1, true)`,
		tenantID,
	); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// WithinUser runs fn with a transaction-local user identity. It is used only
// for pre-tenant workspace discovery; tenant data access still requires
// WithinTenant.
func (d *DB) WithinUser(
	ctx context.Context,
	userID string,
	fn func(*sql.Tx) error,
) (err error) {
	if strings.TrimSpace(userID) == "" {
		return ErrUserRequired
	}
	if fn == nil {
		return errors.New("user transaction function is required")
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil &&
			!errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()

	if _, err = tx.ExecContext(
		ctx,
		`SELECT set_config('app.current_user_id', $1, true)`,
		userID,
	); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// WithinNewTenant obtains a UUIDv7 from PostgreSQL, establishes it as the RLS
// context, and runs fn in the same transaction. The callback must use tenantID
// as the id of the tenant it inserts.
func (d *DB) WithinNewTenant(
	ctx context.Context,
	fn func(tx *sql.Tx, tenantID string) error,
) (err error) {
	if fn == nil {
		return errors.New("tenant transaction function is required")
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil &&
			!errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()

	var tenantID string
	if err = tx.QueryRowContext(ctx, `SELECT uuidv7()::text`).Scan(&tenantID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(
		ctx,
		`SELECT set_config('app.current_tenant_id', $1, true)`,
		tenantID,
	); err != nil {
		return err
	}
	if err = fn(tx, tenantID); err != nil {
		return err
	}
	return tx.Commit()
}
