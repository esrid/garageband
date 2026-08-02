package locations

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/secrets"
)

// CalendarAccount reports whether a location has an active Google Calendar
// connection and, when it does, which Google account it is connected to
// (empty when the one-off email lookup at connect time failed - display-only,
// never blocks the connection itself). Never exposes the token.
func (s *Store) CalendarAccount(
	ctx context.Context, tenantID, userID, locationID string,
) (account string, connected bool, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT external_account_id FROM provider_connections
			WHERE tenant_id = $1 AND location_id = $2
			  AND kind = 'calendar' AND provider = 'google' AND status = 'active'`,
			tenantID, locationID,
		).Scan(&account)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		connected = true
		return nil
	})
	return account, connected, err
}

// ConnectCalendar records a location's Google Calendar connection: the
// refresh token is encrypted through secretStore before anything touches
// provider_connections, so a raw token is never in a Go string longer than
// this call. A location may hold only one Google calendar connection at a
// time - reconnecting replaces the previous one rather than accumulating
// rows, matching the "connect" action being idempotent from the owner's
// point of view (there is only ever one "the calendar" for a location).
func (s *Store) ConnectCalendar(
	ctx context.Context, tenantID, userID, locationID string,
	secretStore secrets.Store, refreshToken, externalAccountID string,
) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		if err := deleteCalendarConnection(ctx, tx, tenantID, locationID, secretStore); err != nil {
			return err
		}
		secretRef, err := secretStore.Store(ctx, tx, tenantID, []byte(refreshToken))
		if err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `
			INSERT INTO provider_connections (
			    tenant_id, location_id, kind, provider, external_account_id, secret_ref
			)
			SELECT $1, location.id, 'calendar', 'google', $3, $4
			FROM locations location
			WHERE location.tenant_id = $1 AND location.id = $2`,
			tenantID, locationID, externalAccountID, secretRef)
		if err != nil {
			return err
		}
		return exactlyOne(result)
	})
}

// DisconnectCalendar removes the connection and its secret. Disconnecting an
// already-disconnected location is a no-op, not an error: the end state
// ("no connection") is what matters, not whether this call did anything.
func (s *Store) DisconnectCalendar(
	ctx context.Context, tenantID, userID, locationID string, secretStore secrets.Store,
) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		return deleteCalendarConnection(ctx, tx, tenantID, locationID, secretStore)
	})
}

func deleteCalendarConnection(
	ctx context.Context, tx pgx.Tx, tenantID, locationID string, secretStore secrets.Store,
) error {
	var secretRef string
	err := tx.QueryRow(ctx, `
		DELETE FROM provider_connections
		WHERE tenant_id = $1 AND location_id = $2 AND kind = 'calendar' AND provider = 'google'
		RETURNING secret_ref`, tenantID, locationID,
	).Scan(&secretRef)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return secretStore.Delete(ctx, tx, secretRef)
}
