# Agent guide

Rules for AI agents working in this repo. The README explains the project to
humans; this file tells you how to extend it without breaking its conventions.

## Structure — where code goes

```
cmd/                entry points only: parse env, wire, run. No logic.
internal/app/       composition root. Only routing composition, middleware,
                    server, config. Never business logic.
internal/features/  ONE package per feature. To add a feature, copy
                    internal/features/todos/ and rename. Every feature owns:
                      routes.go    route registration (full paths, method patterns)
                      handler.go   HTTP only: decode, validate, call store, respond
                      queries.go   explicit SQL (no ORM)
                      views.templ  templ components
                      *_test.go    httptest against the feature mux
internal/platform/  adapters to external systems (DB, OAuth, future: mailer,
                    payments…). One package per system.
internal/ui/        shared templ layout only.
web/static/         tokens.css (design tokens) + app.css.
```

Hard boundaries — never violate:

- A feature never imports another feature.
- `platform/` never imports `features/` or `app/`.
- Business logic never lives in `app/` or `cmd/`.
- New external service ⇒ new `internal/platform/<name>/` package, injected
  into features via their constructors. If providers are swappable, define
  the port (interface) in the platform package (see `oauth.Provider`).

## Database

- Write queries once with `?` placeholders; wrap with `s.db.R(...)` — it
  rewrites to `$N` for PostgreSQL. Never put a literal `?` inside SQL strings.
- Writes that must affect exactly one row: use `db.ExecOne`; it returns
  `sql.ErrNoRows` — map it to 404 in handlers.
- Migrations: `internal/platform/db/migrations/NNNN_name.sql`, goose format
  (`-- +goose Up` / `-- +goose Down`), strictly dialect-neutral (SQLite AND
  PostgreSQL): TEXT/INTEGER/BOOLEAN columns, TEXT ids generated in Go with
  `rand.Text()`, fixed-width RFC 3339 UTC TEXT timestamps.
- Never edit an applied migration; add a new one.

## Auth

- Protect a route: `mux.Handle("GET /x", auth.RequireUser(http.HandlerFunc(h.x)))`.
- Current user: `auth.UserFrom(r.Context())`.
- New OAuth provider = one file in `internal/platform/oauth/` implementing
  `oauth.Provider`, appended in `Config.OAuthProviders`. Nothing else changes.
- Don't hand-roll sessions, CSRF, or crypto — sessions live in
  `features/auth`, CSRF is `http.CrossOriginProtection` in the router.

## Conventions

- Stdlib first: net/http, html escaping via templ, encoding/json, log/slog.
  No framework, no new dependency for what a few lines can do.
- Check EVERY error, including deferred `Close` (named return + join, or
  slog for best-effort cleanup). The repo is errcheck-clean; keep it so.
- Config = env vars read once in `internal/app/config.go`. Never call
  `os.Getenv` anywhere else.
- CSS: components consume `var(--...)` from tokens.css only; no hard-coded
  theme values.
- Every page wraps its content in the shared layout with `ui.PageInfo` —
  never emit `<head>` tags yourself:
  `@ui.Layout(ui.PageInfo{Title: "…", Description: "…"}) { … }`.
  Only `Title` is required; set `Description` on public pages,
  `NoIndex: true` on private ones (login, admin), and `Canonical`/`Image`/
  `Type` when the page is shareable. Empty fields render no tags.
- Code, commits, and docs are written in English.

## Workflow

- After editing `.templ` files: `make generate` (commit the `*_templ.go`).
- Before claiming done: `make test` (runs generate + `go test ./...`) and
  `go vet ./...` must pass.
- Run locally: `make dev` (generate + migrate + serve on :8080).
