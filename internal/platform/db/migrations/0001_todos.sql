-- Dialect-neutral: runs on both SQLite and PostgreSQL.
-- +goose Up
CREATE TABLE todos (
    id         TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    done       BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE todos;
