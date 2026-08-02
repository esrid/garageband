// Package db opens and provides the PostgreSQL database used by the app.
// Verified against https://pkg.go.dev/github.com/jackc/pgx/v5 and
// https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool 2026-08-02.
package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	*pgxpool.Pool
}

// PgError extracts the underlying *pgconn.PgError from err, if any, so
// features can switch on a constraint's Code/ConstraintName instead of
// hand-rolling validation Go-side. PostgreSQL constraints are the source of
// truth; this only decodes what it already rejected.
func PgError(err error) (*pgconn.PgError, bool) {
	var pgErr *pgconn.PgError
	return pgErr, errors.As(err, &pgErr)
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
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	return OpenConfig(config)
}

// OpenConfig connects using a parsed pgx pool configuration. It is primarily
// useful for tests that set an isolated search_path or connection hooks.
func OpenConfig(config *pgxpool.Config) (*DB, error) {
	if config == nil {
		return nil, errors.New("PostgreSQL config is required")
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{Pool: pool}, nil
}

// Close releases the pool's connections.
func (d *DB) Close() error {
	d.Pool.Close()
	return nil
}

// ExecOne runs a write that must affect exactly one row. It returns
// sql.ErrNoRows when nothing was affected so handlers can map it to a 404.
func (d *DB) ExecOne(ctx context.Context, query string, args ...any) error {
	tag, err := d.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
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
	fn func(pgx.Tx) error,
) (err error) {
	if strings.TrimSpace(tenantID) == "" {
		return ErrTenantRequired
	}
	if fn == nil {
		return errors.New("tenant transaction function is required")
	}
	tx, err := d.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			err = errors.Join(err, rollbackErr)
		}
	}()

	if _, err = tx.Exec(
		ctx,
		`SELECT set_config('app.current_tenant_id', $1, true)`,
		tenantID,
	); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// WithinTenantUser runs fn with both the tenant and authenticated user set as
// transaction-local PostgreSQL context. Tenant-owned writes whose RLS policy
// depends on a membership role must use this scope.
func (d *DB) WithinTenantUser(
	ctx context.Context,
	tenantID string,
	userID string,
	fn func(pgx.Tx) error,
) (err error) {
	if strings.TrimSpace(tenantID) == "" {
		return ErrTenantRequired
	}
	if strings.TrimSpace(userID) == "" {
		return ErrUserRequired
	}
	if fn == nil {
		return errors.New("tenant transaction function is required")
	}
	tx, err := d.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			err = errors.Join(err, rollbackErr)
		}
	}()

	if _, err = tx.Exec(ctx, `
		SELECT set_config('app.current_tenant_id', $1, true),
		       set_config('app.current_user_id', $2, true)`,
		tenantID, userID,
	); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// WithinUser runs fn with a transaction-local user identity. It is used only
// for pre-tenant workspace discovery; tenant data access still requires
// WithinTenant.
func (d *DB) WithinUser(
	ctx context.Context,
	userID string,
	fn func(pgx.Tx) error,
) (err error) {
	if strings.TrimSpace(userID) == "" {
		return ErrUserRequired
	}
	if fn == nil {
		return errors.New("user transaction function is required")
	}
	tx, err := d.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			err = errors.Join(err, rollbackErr)
		}
	}()

	if _, err = tx.Exec(
		ctx,
		`SELECT set_config('app.current_user_id', $1, true)`,
		userID,
	); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// WithinNewTenant obtains a UUIDv7 from PostgreSQL, establishes it as the RLS
// context, and runs fn in the same transaction. The callback must use tenantID
// as the id of the tenant it inserts.
func (d *DB) WithinNewTenant(
	ctx context.Context,
	fn func(tx pgx.Tx, tenantID string) error,
) (err error) {
	return d.withinNewTenant(ctx, "", fn)
}

// WithinNewTenantUser provisions a tenant while exposing the creating user to
// role-aware RLS policies. The callback must insert that user's membership
// before inserting other tables whose writes require an owner/admin role.
func (d *DB) WithinNewTenantUser(
	ctx context.Context,
	userID string,
	fn func(tx pgx.Tx, tenantID string) error,
) error {
	if strings.TrimSpace(userID) == "" {
		return ErrUserRequired
	}
	return d.withinNewTenant(ctx, userID, fn)
}

func (d *DB) withinNewTenant(
	ctx context.Context,
	userID string,
	fn func(tx pgx.Tx, tenantID string) error,
) (err error) {
	if fn == nil {
		return errors.New("tenant transaction function is required")
	}
	tx, err := d.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			err = errors.Join(err, rollbackErr)
		}
	}()

	var tenantID string
	if err = tx.QueryRow(ctx, `SELECT uuidv7()::text`).Scan(&tenantID); err != nil {
		return err
	}
	if _, err = tx.Exec(
		ctx,
		`SELECT set_config('app.current_tenant_id', $1, true)`,
		tenantID,
	); err != nil {
		return err
	}
	if userID != "" {
		if _, err = tx.Exec(
			ctx,
			`SELECT set_config('app.current_user_id', $1, true)`,
			userID,
		); err != nil {
			return err
		}
	}
	if err = fn(tx, tenantID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
