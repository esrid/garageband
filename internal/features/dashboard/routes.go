package dashboard

import (
	"context"
	"net/http"
)

type Middleware func(http.Handler) http.Handler

type User struct {
	ID             string
	ActiveTenantID string
}

type Workspace struct {
	ID   string
	Name string
	Role string
}

type UserResolver func(context.Context) (User, bool)
type WorkspaceLister func(context.Context, string) ([]Workspace, error)

func Register(
	mux *http.ServeMux,
	requireUser Middleware,
	user UserResolver,
	workspaces WorkspaceLister,
) {
	h := &handler{user: user, workspaces: workspaces}
	mux.Handle("GET /{$}", requireUser(http.HandlerFunc(h.index)))
}
