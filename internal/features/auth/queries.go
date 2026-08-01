package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/oauth"
)

// sessionTTL is an absolute expiry (OWASP session management).
// ponytail: no idle timeout — add a last_seen column if you need one.
const sessionTTL = 7 * 24 * time.Hour

type User struct {
	ID             string
	Provider       string
	Email          string
	Name           string
	ActiveTenantID string
	CreatedAt      time.Time
}

type Workspace struct {
	ID   string
	Slug string
	Name string
	Role string
}

type Store struct{ db *db.DB }

func NewStore(d *db.DB) *Store { return &Store{db: d} }

// UpsertUser creates or refreshes the user identified by (provider, provider
// id).
func (s *Store) UpsertUser(ctx context.Context, provider string, info oauth.UserInfo) (User, error) {
	u := User{Provider: provider, Email: info.Email, Name: info.Name}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO users (provider, provider_id, email, name, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (provider, provider_id)
		DO UPDATE SET
			email = excluded.email,
			name = excluded.name,
			updated_at = now()
		RETURNING id, email, name, created_at`,
		provider, info.ProviderID, info.Email, info.Name, time.Now().UTC(),
	).Scan(&u.ID, &u.Email, &u.Name, &u.CreatedAt)
	return u, err
}

// CreateSession issues a new opaque token (~260 bits of entropy — OWASP wants
// at least 64) and stores only its SHA-256 hash, so a leaked table cannot be
// replayed as cookies.
func (s *Store) CreateSession(ctx context.Context, userID string) (string, error) {
	token := rand.Text() + rand.Text()
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES ($1, $2, $3, $4)`,
		hashToken(token), userID, now.Add(sessionTTL), now)
	if err != nil {
		return "", err
	}
	// Opportunistic cleanup; ponytail: move to a periodic job if the table grows.
	// A failure must not fail the login, but it must not be silent either.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < $1`, now); err != nil {
		slog.Error("purge expired sessions", "err", err)
	}
	return token, nil
}

// UserByToken resolves a session token, enforcing expiry server-side.
// Returns sql.ErrNoRows for unknown or expired tokens.
func (s *Store) UserByToken(ctx context.Context, token string) (User, error) {
	var u User
	var expires time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.provider, u.email, u.name,
		       COALESCE(se.active_tenant_id::text, ''), u.created_at,
		       se.expires_at
		FROM sessions se JOIN users u ON u.id = se.user_id
		WHERE se.token_hash = $1`, hashToken(token),
	).Scan(
		&u.ID, &u.Provider, &u.Email, &u.Name, &u.ActiveTenantID,
		&u.CreatedAt, &expires,
	)
	if err != nil {
		return User{}, err
	}
	if time.Now().UTC().After(expires) {
		if err := s.DeleteSession(ctx, token); err != nil {
			slog.Error("delete expired session", "err", err)
		}
		return User{}, sql.ErrNoRows
	}
	return u, nil
}

// Workspaces lists only tenants the user belongs to. WithinUser supplies the
// separate RLS identity needed before an active tenant has been selected.
func (s *Store) Workspaces(ctx context.Context, userID string) (workspaces []Workspace, err error) {
	err = s.db.WithinUser(ctx, userID, func(tx *sql.Tx) (returnErr error) {
		rows, err := tx.QueryContext(ctx, `
			SELECT tenant.id, tenant.slug, tenant.name, membership.role
			FROM tenant_memberships membership
			JOIN tenants tenant ON tenant.id = membership.tenant_id
			WHERE membership.user_id = $1
			ORDER BY tenant.name, tenant.id`, userID)
		if err != nil {
			return err
		}
		defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
		for rows.Next() {
			var workspace Workspace
			if err := rows.Scan(
				&workspace.ID, &workspace.Slug, &workspace.Name, &workspace.Role,
			); err != nil {
				return err
			}
			workspaces = append(workspaces, workspace)
		}
		return rows.Err()
	})
	return workspaces, err
}

// ActivateTenant changes only the session represented by ctx. Membership is
// checked under tenant RLS in the same transaction, while the database's
// composite foreign key makes the authorization invariant permanent.
func (s *Store) ActivateTenant(ctx context.Context, tenantID string) error {
	identity, ok := identityFrom(ctx)
	if !ok {
		return sql.ErrNoRows
	}
	return s.db.WithinTenant(ctx, tenantID, func(tx *sql.Tx) error {
		var role string
		if err := tx.QueryRowContext(ctx, `
			SELECT role
			FROM tenant_memberships
			WHERE tenant_id = $1 AND user_id = $2`,
			tenantID, identity.User.ID,
		).Scan(&role); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE sessions
			SET active_tenant_id = $1
			WHERE token_hash = $2 AND user_id = $3 AND expires_at > now()`,
			tenantID, identity.tokenHash, identity.User.ID,
		)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			return sql.ErrNoRows
		}
		return nil
	})
}

// DeleteSession revokes a session server-side (logout). Idempotent.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashToken(token))
	return err
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
