# template

Minimal, modular Go web template — Django-style feature apps on the standard library.

Stack: `net/http` (Go 1.22+ routing), [templ](https://templ.guide) components, `database/sql` with SQLite (default) or PostgreSQL, [goose](https://github.com/pressly/goose) migrations, `log/slog`. No framework.

## Quick start

```sh
make dev        # templ generate + migrate + run on :8080
make test
make build      # binaries in bin/
```

Requires Go 1.24+ (templ runs via the `go tool` directive — nothing else to install).

## Configuration

Environment variables only (12-factor):

| Var | Default | Notes |
|---|---|---|
| `ADDR` | `:8080` | listen address |
| `DATABASE_URL` | `file:app.db?_fk=on&_journal=WAL` | SQLite path/URI, or `postgres://...` to use PostgreSQL |
| `BASE_URL` | `http://localhost:8080` | public origin, used for OAuth callback URLs |
| `COOKIE_SECURE` | `true` | set `false` only for local http development |
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | — | enables "Continue with google" on `/login`; register `BASE_URL` + `/auth/google/callback` as the redirect URI in the Google console |

Swapping databases is just changing `DATABASE_URL`. Queries are written once
with `?` placeholders; `db.R` rewrites them to `$1..$n` for PostgreSQL.
Writes that must hit exactly one row (update/delete by id) go through
`db.ExecOne`, which returns `sql.ErrNoRows` for handlers to map to a 404.

## Layout

```
cmd/web/            entry point: wire config, call app.Run
cmd/migrate/        one-off migration command (≈ manage.py migrate)
internal/app/       composition root: router, middleware, server, config
internal/features/  every feature "app" lives here, one package each
  auth/             OAuth login, server-side sessions, RequireUser middleware
  todos/            demo feature — copy this to add one
internal/platform/  adapters to external things (hexagonal spirit)
  db/               database: open, dialect handling, goose runner
    migrations/     goose SQL files, embedded, dialect-neutral
  oauth/            OAuth port (Provider interface) + adapters (google.go)
internal/ui/        shared templ layout
web/static/         embedded assets: tokens.css (design tokens) + app.css
```

`features/` never import each other's internals; `platform/` packages never
import `features/`. New external service (mailer, payment API, object store)?
New package under `internal/platform/`, consumed by features via their
constructors.

## Django equivalences

| Django | Here |
|---|---|
| app | package under `internal/features/` |
| urls.py | `routes.go` (`Register(mux, ...)`) |
| views.py | `handler.go` |
| models.py / ORM | `queries.go` (explicit SQL) |
| templates | `views.templ` (templ components) |
| settings.py | `internal/app/config.go` (env vars) |
| manage.py migrate | `cmd/migrate` (goose) |

## Adding a feature

1. Copy `internal/features/todos/` to `internal/features/<feature>/`, rename types and routes.
2. Add `internal/platform/db/migrations/NNNN_<feature>.sql` with `-- +goose Up` / `-- +goose Down` (dialect-neutral SQL: TEXT/INTEGER/BOOLEAN columns, TEXT ids generated in Go via `rand.Text()`, RFC 3339 TEXT timestamps).
3. Register it in `internal/app/router.go`: `<feature>.Register(mux, <feature>.NewStore(database))`.
4. Need JSON? Add a `GET /api/...` handler like `listJSON` — same store, `encoding/json`.
5. Business logic outgrowing handlers? Add a `service.go` between handler and store — not before.

## Auth

OAuth login only — no passwords to store, so no password-storage risk.
Session handling follows the OWASP Session Management Cheat Sheet: opaque
tokens with ~260 bits of entropy, only the SHA-256 hash at rest, `HttpOnly` +
`Secure` + `SameSite=Lax` cookies, a fresh token on every login, server-side
revocation on logout, absolute expiry (7 days). The OAuth flow uses `state`
(CSRF) plus PKCE (S256); cross-origin `POST`s are rejected globally by
`http.CrossOriginProtection`.

Protect a route:

```go
mux.Handle("GET /admin", auth.RequireUser(http.HandlerFunc(h.admin)))
```

Read the current user in any handler: `auth.UserFrom(r.Context())`.

Add a provider (the port makes this a one-file change): write
`internal/platform/oauth/<name>.go` implementing `oauth.Provider`, append it
in `Config.OAuthProviders`. Tests inject a fake `Provider` — see
`internal/features/auth/handler_test.go`.

## Renaming the module

```sh
go mod edit -module github.com/you/yourapp
grep -rl 'github.com/esrid/template' --include='*.go' --include='*.templ' . | xargs sed -i '' 's|github.com/esrid/template|github.com/you/yourapp|g'
make generate
```
