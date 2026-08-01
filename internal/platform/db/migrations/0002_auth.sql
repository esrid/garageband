-- +goose Up
CREATE TABLE users (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    provider    TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    email       TEXT NOT NULL,
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_id)
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE sessions;
DROP TABLE users;
