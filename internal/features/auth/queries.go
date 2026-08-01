package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/esrid/template/internal/platform/db"
	"github.com/esrid/template/internal/platform/oauth"
)

// sessionTTL is an absolute expiry (OWASP session management).
// ponytail: no idle timeout — add a last_seen column if you need one.
const sessionTTL = 7 * 24 * time.Hour

// timeFormat is fixed-width RFC 3339 UTC so TEXT comparison in SQL is safe.
const timeFormat = time.RFC3339

type User struct {
	ID        string
	Provider  string
	Email     string
	Name      string
	CreatedAt time.Time
}

type Store struct{ db *db.DB }

func NewStore(d *db.DB) *Store { return &Store{db: d} }

// UpsertUser creates or refreshes the user identified by (provider, provider
// id). ON CONFLICT ... RETURNING runs on both SQLite (3.35+) and PostgreSQL.
func (s *Store) UpsertUser(ctx context.Context, provider string, info oauth.UserInfo) (User, error) {
	u := User{Provider: provider, Email: info.Email, Name: info.Name}
	var created string
	err := s.db.QueryRowContext(ctx, s.db.R(`
		INSERT INTO users (id, provider, provider_id, email, name, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (provider, provider_id)
		DO UPDATE SET email = excluded.email, name = excluded.name
		RETURNING id, email, name, created_at`),
		rand.Text(), provider, info.ProviderID, info.Email, info.Name,
		time.Now().UTC().Format(timeFormat),
	).Scan(&u.ID, &u.Email, &u.Name, &created)
	if err != nil {
		return User{}, err
	}
	u.CreatedAt, err = time.Parse(timeFormat, created)
	return u, err
}

// CreateSession issues a new opaque token (~260 bits of entropy — OWASP wants
// at least 64) and stores only its SHA-256 hash, so a leaked table cannot be
// replayed as cookies.
func (s *Store) CreateSession(ctx context.Context, userID string) (string, error) {
	token := rand.Text() + rand.Text()
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		s.db.R(`INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`),
		hashToken(token), userID, now.Add(sessionTTL).Format(timeFormat), now.Format(timeFormat))
	if err != nil {
		return "", err
	}
	// Opportunistic cleanup; ponytail: move to a periodic job if the table grows.
	// A failure must not fail the login, but it must not be silent either.
	if _, err := s.db.ExecContext(ctx, s.db.R(`DELETE FROM sessions WHERE expires_at < ?`), now.Format(timeFormat)); err != nil {
		slog.Error("purge expired sessions", "err", err)
	}
	return token, nil
}

// UserByToken resolves a session token, enforcing expiry server-side.
// Returns sql.ErrNoRows for unknown or expired tokens.
func (s *Store) UserByToken(ctx context.Context, token string) (User, error) {
	var u User
	var expires, created string
	err := s.db.QueryRowContext(ctx, s.db.R(`
		SELECT u.id, u.provider, u.email, u.name, u.created_at, se.expires_at
		FROM sessions se JOIN users u ON u.id = se.user_id
		WHERE se.token_hash = ?`), hashToken(token),
	).Scan(&u.ID, &u.Provider, &u.Email, &u.Name, &created, &expires)
	if err != nil {
		return User{}, err
	}
	exp, err := time.Parse(timeFormat, expires)
	if err != nil {
		return User{}, err
	}
	if time.Now().UTC().After(exp) {
		if err := s.DeleteSession(ctx, token); err != nil {
			slog.Error("delete expired session", "err", err)
		}
		return User{}, sql.ErrNoRows
	}
	u.CreatedAt, err = time.Parse(timeFormat, created)
	return u, err
}

// DeleteSession revokes a session server-side (logout). Idempotent.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, s.db.R(`DELETE FROM sessions WHERE token_hash = ?`), hashToken(token))
	return err
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
