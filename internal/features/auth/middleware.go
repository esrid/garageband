package auth

import (
	"context"
	"net/http"
)

type ctxKey struct{}

type requestIdentity struct {
	User      User
	tokenHash string
}

// WithUser loads the session user (if any) into the request context. Mounted
// globally in the router; anonymous requests pass through untouched.
func (s *Store) WithUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil {
			if u, err := s.UserByToken(r.Context(), c.Value); err == nil {
				identity := requestIdentity{User: u, tokenHash: hashToken(c.Value)}
				r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, identity))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// UserFrom returns the logged-in user, if any.
func UserFrom(ctx context.Context) (User, bool) {
	identity, ok := identityFrom(ctx)
	return identity.User, ok
}

func identityFrom(ctx context.Context) (requestIdentity, bool) {
	identity, ok := ctx.Value(ctxKey{}).(requestIdentity)
	return identity, ok
}

// TenantFrom returns the session's database-constrained active tenant.
func TenantFrom(ctx context.Context) (string, bool) {
	user, ok := UserFrom(ctx)
	return user.ActiveTenantID, ok && user.ActiveTenantID != ""
}

// LocationFrom returns the session's active site, if one was ever selected.
// Cleared automatically whenever the active tenant changes (a location
// belongs to exactly one tenant), so callers never need to re-validate it
// against the current tenant.
func LocationFrom(ctx context.Context) (string, bool) {
	user, ok := UserFrom(ctx)
	return user.ActiveLocationID, ok && user.ActiveLocationID != ""
}

// RequireTenant protects tenant-owned feature routes. The active tenant is
// guaranteed by the sessions-to-memberships foreign key, and stores must still
// use db.WithinTenant for every tenant query.
func RequireTenant(next http.Handler) http.Handler {
	return RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := TenantFrom(r.Context()); !ok {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// RequireUser redirects anonymous requests to /login. Wrap any handler that
// needs a logged-in user:
//
//	mux.Handle("GET /admin", auth.RequireUser(http.HandlerFunc(h.admin)))
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}
