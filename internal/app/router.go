package app

import (
	"net/http"

	"github.com/esrid/garageband/internal/features/auth"
	"github.com/esrid/garageband/internal/features/todos"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/web"
)

// NewRouter composes feature routers and global middleware. Each feature
// registers its own routes via its Register function; no business logic here.
func NewRouter(cfg Config, database *db.DB) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(web.Static)))
	mux.Handle("GET /{$}", http.RedirectHandler("/todos", http.StatusFound))

	authStore := auth.NewStore(database)
	auth.Register(mux, authStore, cfg.OAuthProviders(), cfg.CookieSecure)
	todos.Register(mux, todos.NewStore(database))

	// Stdlib CSRF protection (Go 1.25+): blocks cross-origin non-safe methods
	// via Sec-Fetch-Site/Origin; requests without those headers pass.
	csrf := http.NewCrossOriginProtection()

	return withRecover(withLogging(csrf.Handler(authStore.WithUser(mux))))
}
