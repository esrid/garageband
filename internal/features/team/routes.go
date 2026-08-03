package team

import (
	"context"
	"errors"
	"net/http"
)

var (
	ErrForbidden    = errors.New("team access modification is forbidden")
	ErrNameRequired = errors.New("staff member name is required")
)

type Middleware func(http.Handler) http.Handler

type Principal struct {
	UserID   string
	TenantID string
}

type PrincipalResolver func(context.Context) (Principal, bool)
type PageLoader func(context.Context, Principal) (Page, error)
type AssignmentReplacer func(context.Context, Principal, string, []string) error

// Invitation is the one-time credential this screen hands over. Code is what a
// person types on a machine nobody can send a link to; Link is the same secret
// as something to tap.
type Invitation struct {
	Link string
	Code string
}

// StaffInviter enrols an employee and returns the credential they use once.
// It is returned rather than stored: this screen shows it once.
type StaffInviter func(ctx context.Context, p Principal, name string, locationIDs []string) (Invitation, error)

// InviteReissuer mints a fresh credential for someone already on the team, for
// a second screen or a lost code.
type InviteReissuer func(ctx context.Context, p Principal, targetUserID string) (Invitation, error)

// StaffRemover takes someone out of the organization.
type StaffRemover func(ctx context.Context, p Principal, targetUserID string) error

// Deps are the ways this screen reaches the outside world. They are values
// rather than an interface so the feature stays testable without a database and
// without importing the store.
type Deps struct {
	Principal          PrincipalResolver
	LoadPage           PageLoader
	ReplaceAssignments AssignmentReplacer
	InviteStaff        StaffInviter
	ReissueInvite      InviteReissuer
	RemoveStaff        StaffRemover
}

func Register(mux *http.ServeMux, requireTenant Middleware, deps Deps) {
	h := &handler{deps: deps}
	mux.Handle("GET /team", requireTenant(http.HandlerFunc(h.index)))
	mux.Handle("POST /team/invite", requireTenant(http.HandlerFunc(h.invite)))
	mux.Handle(
		"POST /team/{userID}/locations",
		requireTenant(http.HandlerFunc(h.replace)),
	)
	mux.Handle(
		"POST /team/{userID}/code",
		requireTenant(http.HandlerFunc(h.reissue)),
	)
	mux.Handle(
		"POST /team/{userID}/revoke",
		requireTenant(http.HandlerFunc(h.revoke)),
	)
}
