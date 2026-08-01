-- Dialect-neutral: runs on both SQLite and PostgreSQL.
-- +goose Up
CREATE TABLE users (
    id          TEXT PRIMARY KEY,
    provider    TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    email       TEXT NOT NULL,
    name        TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    UNIQUE (provider, provider_id)
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE sessions;
DROP TABLE users;
