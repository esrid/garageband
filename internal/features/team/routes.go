package team

import (
	"context"
	"errors"
	"net/http"
)

var ErrForbidden = errors.New("team access modification is forbidden")

type Middleware func(http.Handler) http.Handler

type Principal struct {
	UserID   string
	TenantID string
}

type PrincipalResolver func(context.Context) (Principal, bool)
type PageLoader func(context.Context, Principal) (Page, error)
type AssignmentReplacer func(context.Context, Principal, string, []string) error

func Register(
	mux *http.ServeMux,
	requireTenant Middleware,
	principal PrincipalResolver,
	loadPage PageLoader,
	replaceAssignments AssignmentReplacer,
) {
	h := &handler{
		principal: principal, loadPage: loadPage,
		replaceAssignments: replaceAssignments,
	}
	mux.Handle("GET /team", requireTenant(http.HandlerFunc(h.index)))
	mux.Handle(
		"POST /team/{userID}/locations",
		requireTenant(http.HandlerFunc(h.replace)),
	)
}
