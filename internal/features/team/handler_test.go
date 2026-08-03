package team_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/esrid/garageband/internal/features/team"
)

const (
	actorID   = "01980000-0000-7000-8000-000000000001"
	tenantID  = "01980000-0000-7000-8000-000000000002"
	memberID  = "01980000-0000-7000-8000-000000000003"
	locationA = "01980000-0000-7000-8000-000000000004"
	locationB = "01980000-0000-7000-8000-000000000005"
)

func TestTeamHTTPFlow(t *testing.T) {
	var gotTarget string
	var gotLocations []string
	handler := teamHandler(
		func(context.Context, team.Principal) (team.Page, error) {
			return team.Page{Organization: "Garage Central", CanManage: true}, nil
		},
		func(_ context.Context, _ team.Principal, target string, locations []string) error {
			gotTarget = target
			gotLocations = slices.Clone(locations)
			return nil
		},
	)

	response := request(handler, http.MethodGet, "/team?saved=1", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /team = %d, want 200", response.Code)
	}
	for _, want := range []string{"Garage Central", "Les accès aux sites ont été enregistrés."} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("GET /team body is missing %q", want)
		}
	}

	form := url.Values{team.FieldLocations: {locationA, locationB}}
	response = request(handler, http.MethodPost, "/team/"+memberID+"/locations", form)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/team?saved=1" {
		t.Fatalf("POST = %d location %q", response.Code, response.Header().Get("Location"))
	}
	if gotTarget != memberID || !slices.Equal(gotLocations, []string{locationA, locationB}) {
		t.Fatalf("replacement = target %q locations %v", gotTarget, gotLocations)
	}

	response = request(handler, http.MethodPost, "/team/"+memberID+"/locations", nil)
	if response.Code != http.StatusSeeOther || len(gotLocations) != 0 {
		t.Fatalf("empty replacement = %d locations %v", response.Code, gotLocations)
	}
}

func TestTeamHTTPValidationAndAuthorization(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		form       url.Values
		replaceErr error
		wantStatus int
	}{
		{name: "invalid target", target: "/team/not-a-uuid/locations", wantStatus: http.StatusNotFound},
		{
			name: "invalid location", target: "/team/" + memberID + "/locations",
			form:       url.Values{team.FieldLocations: {"not-a-uuid"}},
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "forbidden", target: "/team/" + memberID + "/locations",
			replaceErr: team.ErrForbidden, wantStatus: http.StatusForbidden,
		},
		{
			name: "missing member", target: "/team/" + memberID + "/locations",
			replaceErr: sql.ErrNoRows, wantStatus: http.StatusNotFound,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			handler := teamHandler(
				func(context.Context, team.Principal) (team.Page, error) { return team.Page{}, nil },
				func(context.Context, team.Principal, string, []string) error {
					called = true
					return test.replaceErr
				},
			)
			response := request(handler, http.MethodPost, test.target, test.form)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if (test.name == "invalid target" || test.name == "invalid location") && called {
				t.Fatal("replacement called after invalid input")
			}
		})
	}
}

func TestTeamHTTPPageFailure(t *testing.T) {
	handler := teamHandler(
		func(context.Context, team.Principal) (team.Page, error) {
			return team.Page{}, errors.New("load failed")
		},
		func(context.Context, team.Principal, string, []string) error { return nil },
	)
	response := request(handler, http.MethodGet, "/team", nil)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("GET failure = %d, want 500", response.Code)
	}
}

func teamHandler(load team.PageLoader, replace team.AssignmentReplacer) http.Handler {
	mux := http.NewServeMux()
	team.Register(
		mux,
		func(next http.Handler) http.Handler { return next },
		team.Deps{
			Principal: func(context.Context) (team.Principal, bool) {
				return team.Principal{UserID: actorID, TenantID: tenantID}, true
			},
			LoadPage:           load,
			ReplaceAssignments: replace,
			InviteStaff: func(context.Context, team.Principal, string, []string) (team.Invitation, error) {
				return team.Invitation{}, nil
			},
			ReissueInvite: func(context.Context, team.Principal, string) (team.Invitation, error) {
				return team.Invitation{}, nil
			},
			RenameStaff: func(context.Context, team.Principal, string, string) error { return nil },
			RemoveStaff: func(context.Context, team.Principal, string) error { return nil },
		},
	)
	return mux
}

func request(handler http.Handler, method, target string, form url.Values) *httptest.ResponseRecorder {
	body := ""
	if form != nil {
		body = form.Encode()
	}
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// TestTeamInviteRendersLinkOnce pins the one thing this screen must never get
// wrong: the invitation link exists only in the response that created it.
func TestTeamInviteRendersLinkOnce(t *testing.T) {
	var gotName string
	var gotLocations []string
	mux := http.NewServeMux()
	team.Register(mux, func(next http.Handler) http.Handler { return next }, team.Deps{
		Principal: func(context.Context) (team.Principal, bool) {
			return team.Principal{UserID: actorID, TenantID: tenantID}, true
		},
		LoadPage: func(context.Context, team.Principal) (team.Page, error) {
			return team.Page{Organization: "Garage Central", CanManage: true}, nil
		},
		ReplaceAssignments: func(context.Context, team.Principal, string, []string) error { return nil },
		InviteStaff: func(_ context.Context, _ team.Principal, name string, locations []string) (team.Invitation, error) {
			gotName = name
			gotLocations = slices.Clone(locations)
			return team.Invitation{
				Link: "https://app.example/rejoindre/SECRETCODE12",
				Code: "SECRETCODE12",
			}, nil
		},
		ReissueInvite: func(context.Context, team.Principal, string) (team.Invitation, error) {
			return team.Invitation{}, nil
		},
		RenameStaff: func(context.Context, team.Principal, string, string) error { return nil },
		RemoveStaff: func(context.Context, team.Principal, string) error { return nil },
	})

	form := url.Values{
		team.FieldName:      {"  Karim Mécano  "},
		team.FieldLocations: {locationA},
	}
	response := request(mux, http.MethodPost, "/team/invite", form)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /team/invite = %d, want 200", response.Code)
	}
	if gotName != "Karim Mécano" || !slices.Equal(gotLocations, []string{locationA}) {
		t.Fatalf("invite = name %q locations %v", gotName, gotLocations)
	}
	if !strings.Contains(response.Body.String(), "https://app.example/rejoindre/SECRETCODE12") {
		t.Fatal("the invitation link is missing from the page that minted it")
	}
	// The code is shown grouped, because it gets read out loud.
	if !strings.Contains(response.Body.String(), "SECR-ETCO-DE12") {
		t.Fatal("the typed code is missing from the page that minted it")
	}

	// A later view of the screen cannot show it again: it was never stored.
	response = request(mux, http.MethodGet, "/team", nil)
	if strings.Contains(response.Body.String(), "SECRETCODE12") {
		t.Fatal("the invitation code came back on a later page load")
	}

	// A nameless employee is rejected without ever reaching the store.
	gotName = "unchanged"
	response = request(mux, http.MethodPost, "/team/invite", url.Values{team.FieldName: {"   "}})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blank name = %d, want 422", response.Code)
	}
	if gotName != "unchanged" {
		t.Fatal("the store was called with a blank name")
	}
}

// TestTeamRevokeRoundTrip covers removal, including the refusals the store
// reports for the owner and for someone who no longer exists.
func TestTeamRevokeRoundTrip(t *testing.T) {
	for _, test := range []struct {
		name       string
		removeErr  error
		wantStatus int
	}{
		{name: "removed", wantStatus: http.StatusSeeOther},
		{name: "forbidden", removeErr: team.ErrForbidden, wantStatus: http.StatusForbidden},
		{name: "unknown", removeErr: sql.ErrNoRows, wantStatus: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			var gotTarget string
			mux := http.NewServeMux()
			team.Register(mux, func(next http.Handler) http.Handler { return next }, team.Deps{
				Principal: func(context.Context) (team.Principal, bool) {
					return team.Principal{UserID: actorID, TenantID: tenantID}, true
				},
				LoadPage: func(context.Context, team.Principal) (team.Page, error) {
					return team.Page{}, nil
				},
				ReplaceAssignments: func(context.Context, team.Principal, string, []string) error { return nil },
				InviteStaff: func(context.Context, team.Principal, string, []string) (team.Invitation, error) {
					return team.Invitation{}, nil
				},
				ReissueInvite: func(context.Context, team.Principal, string) (team.Invitation, error) {
					return team.Invitation{}, nil
				},
				RenameStaff: func(context.Context, team.Principal, string, string) error { return nil },
				RemoveStaff: func(_ context.Context, _ team.Principal, target string) error {
					gotTarget = target
					return test.removeErr
				},
			})
			response := request(mux, http.MethodPost, "/team/"+memberID+"/revoke", nil)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if gotTarget != memberID {
				t.Fatalf("target = %q, want %q", gotTarget, memberID)
			}
		})
	}
}
