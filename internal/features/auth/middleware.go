package auth

import (
	"context"
	"net/http"
)

type ctxKey struct{}

// WithUser loads the session user (if any) into the request context. Mounted
// globally in the router; anonymous requests pass through untouched.
func (s *Store) WithUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil {
			if u, err := s.UserByToken(r.Context(), c.Value); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, u))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// UserFrom returns the logged-in user, if any.
func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
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
