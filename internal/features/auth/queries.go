package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/oauth"
)

// sessionTTL is an absolute expiry (OWASP session management).
// ponytail: no idle timeout — add a last_seen column if you need one.
const sessionTTL = 7 * 24 * time.Hour

// staffSessionTTL is longer because an invited employee has no way back in on
// their own: their link was single-use and they own no password, no email and
// no Google account. Their device is the credential, and the owner revokes it
// from the team screen. Signing them out weekly would mean re-inviting weekly.
const staffSessionTTL = 90 * 24 * time.Hour

type User struct {
	ID               string
	Provider         string
	Email            string
	Name             string
	ActiveTenantID   string
	ActiveLocationID string
	CreatedAt        time.Time
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
	err := s.db.QueryRow(ctx, `
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
// replayed as cookies. activeTenantID may be empty, which leaves the session on
// the workspace picker; when set, the sessions-to-memberships foreign key is
// what guarantees the user actually belongs there.
func (s *Store) CreateSession(
	ctx context.Context,
	userID string,
	activeTenantID string,
	ttl time.Duration,
) (string, error) {
	token := rand.Text() + rand.Text()
	now := time.Now().UTC()
	var tenant any
	if activeTenantID != "" {
		tenant = activeTenantID
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, active_tenant_id, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		hashToken(token), userID, tenant, now.Add(ttl), now)
	if err != nil {
		return "", err
	}
	// Opportunistic cleanup; ponytail: move to a periodic job if the table grows.
	// A failure must not fail the login, but it must not be silent either.
	if _, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE expires_at < $1`, now); err != nil {
		slog.Error("purge expired sessions", "err", err)
	}
	return token, nil
}

// UserByToken resolves a session token, enforcing expiry server-side.
// Returns sql.ErrNoRows for unknown or expired tokens.
func (s *Store) UserByToken(ctx context.Context, token string) (User, error) {
	var u User
	var expires time.Time
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.provider, u.email, u.name,
		       COALESCE(se.active_tenant_id::text, ''),
		       COALESCE(se.active_location_id::text, ''), u.created_at,
		       se.expires_at
		FROM sessions se JOIN users u ON u.id = se.user_id
		WHERE se.token_hash = $1`, hashToken(token),
	).Scan(
		&u.ID, &u.Provider, &u.Email, &u.Name, &u.ActiveTenantID,
		&u.ActiveLocationID, &u.CreatedAt, &expires,
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
	err = s.db.WithinUser(ctx, userID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT tenant.id, tenant.slug, tenant.name, membership.role
			FROM tenant_memberships membership
			JOIN tenants tenant ON tenant.id = membership.tenant_id
			WHERE membership.user_id = $1
			ORDER BY tenant.name, tenant.id`, userID)
		if err != nil {
			return err
		}
		workspaces, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Workspace])
		return err
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
	return s.db.WithinTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var role string
		if err := tx.QueryRow(ctx, `
			SELECT role
			FROM tenant_memberships
			WHERE tenant_id = $1 AND user_id = $2`,
			tenantID, identity.User.ID,
		).Scan(&role); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `
			UPDATE sessions
			SET active_tenant_id = $1, active_location_id = NULL
			WHERE token_hash = $2 AND user_id = $3 AND expires_at > now()`,
			tenantID, identity.tokenHash, identity.User.ID,
		)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return sql.ErrNoRows
		}
		return nil
	})
}

// ActivateLocation changes only the session represented by ctx, mirroring
// ActivateTenant. Unlike a tenant, location access isn't one membership row
// (it's owner/admin implicit access, or an explicit user_location_assignments
// row), so the check goes through the same
// app_current_user_can_access_location() function every other location read
// already uses, instead of a composite foreign key.
func (s *Store) ActivateLocation(ctx context.Context, tenantID, locationID string) error {
	identity, ok := identityFrom(ctx)
	if !ok {
		return sql.ErrNoRows
	}
	return s.db.WithinTenantUser(ctx, tenantID, identity.User.ID, func(tx pgx.Tx) error {
		var accessible bool
		if err := tx.QueryRow(ctx, `
			SELECT app_current_user_can_access_location($1)
			  AND EXISTS (SELECT 1 FROM locations WHERE tenant_id = $2 AND id = $1)`,
			locationID, tenantID,
		).Scan(&accessible); err != nil {
			return err
		}
		if !accessible {
			return sql.ErrNoRows
		}
		result, err := tx.Exec(ctx, `
			UPDATE sessions
			SET active_location_id = $1
			WHERE token_hash = $2 AND user_id = $3 AND expires_at > now()`,
			locationID, identity.tokenHash, identity.User.ID,
		)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return sql.ErrNoRows
		}
		return nil
	})
}

// Invitation is what an employee is shown before they commit to joining. It
// carries no identifier: the token in the URL is the only handle.
type Invitation struct {
	Organization string
	Name         string
}

// InvitationByToken previews a live invitation without consuming it. Link
// previewers — the ones a phone messenger fires when the owner pastes the URL
// into a chat — must not be able to burn an employee's only way in, which is
// why looking is a separate step from accepting.
//
// Reading the organization's name needs its own tenant scope: staff_invites is
// outside row security, but tenants is not, and the visitor is anonymous.
func (s *Store) InvitationByToken(ctx context.Context, token string) (Invitation, error) {
	var tenantID string
	var invitation Invitation
	err := s.db.QueryRow(ctx, `
		SELECT invite.tenant_id, user_account.name
		FROM staff_invites invite
		JOIN users user_account ON user_account.id = invite.user_id
		WHERE invite.token_hash = $1
		  AND invite.accepted_at IS NULL
		  AND invite.expires_at > now()`, hashToken(normalizeInviteCode(token)),
	).Scan(&tenantID, &invitation.Name)
	if err != nil {
		return Invitation{}, err
	}
	err = s.db.WithinTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT name FROM tenants WHERE id = $1`, tenantID,
		).Scan(&invitation.Organization)
	})
	if err != nil {
		return Invitation{}, err
	}
	return invitation, nil
}

// AcceptInvitation consumes the invitation and hands back a session already
// pointing at the garage that issued it. The UPDATE is the single-use gate:
// only one caller can move accepted_at away from NULL, so a link opened twice
// cannot mint two sessions.
func (s *Store) AcceptInvitation(ctx context.Context, token string) (sessionToken string, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			err = errors.Join(err, rollbackErr)
		}
	}()

	var tenantID, userID string
	if err = tx.QueryRow(ctx, `
		UPDATE staff_invites
		SET accepted_at = now()
		WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > now()
		RETURNING tenant_id, user_id`, hashToken(normalizeInviteCode(token)),
	).Scan(&tenantID, &userID); err != nil {
		return "", err
	}

	sessionToken = rand.Text() + rand.Text()
	now := time.Now().UTC()
	if _, err = tx.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, active_tenant_id, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		hashToken(sessionToken), userID, tenantID, now.Add(staffSessionTTL), now,
	); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return sessionToken, nil
}

// DeleteSession revokes a session server-side (logout). Idempotent.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashToken(token))
	return err
}

// normalizeInviteCode mirrors how the code was minted: RFC 4648 base32, upper
// case, no separators. It lets an employee type what they see — dashes, lower
// case, a stray space — instead of a string that must be transcribed exactly.
func normalizeInviteCode(raw string) string {
	var normalized strings.Builder
	for _, r := range strings.ToUpper(raw) {
		if (r >= 'A' && r <= 'Z') || (r >= '2' && r <= '7') {
			normalized.WriteRune(r)
		}
	}
	return normalized.String()
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
