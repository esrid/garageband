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
		func(context.Context) (team.Principal, bool) {
			return team.Principal{UserID: actorID, TenantID: tenantID}, true
		},
		load,
		replace,
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
