package team_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/esrid/garageband/internal/features/team"
	"github.com/esrid/garageband/internal/platform/dbtest"
)

// baseURL is what the screen builds an invitation link from.
const baseURL = "https://app.example"

// unknownUserID is well-formed but belongs to nobody, which is how the screen
// tells "not yours to change" apart from "not a user id at all".
const unknownUserID = "01980000-0000-7000-8000-0000000000ff"

type teamFixture struct {
	store                       *team.Store
	tenantID, ownerID, memberID string
	locationA, locationB        string
}

func newTeamFixture(t *testing.T) teamFixture {
	t.Helper()
	fixtures, runtime := dbtest.OpenRuntime(t)
	ownerID := createUser(t, fixtures, "http-owner@example.com")
	memberID := createUser(t, fixtures, "http-member@example.com")
	tenantID := createTenant(t, fixtures, ownerID)
	addMembership(t, fixtures, tenantID, memberID, "member")
	return teamFixture{
		store:     team.NewStore(runtime),
		tenantID:  tenantID,
		ownerID:   ownerID,
		memberID:  memberID,
		locationA: createLocation(t, fixtures, tenantID, "http-a"),
		locationB: createLocation(t, fixtures, tenantID, "http-b"),
	}
}

// teamMux serves the feature as the given user, which is the only difference
// between an owner who may change access and a member who may not.
func teamMux(fixture teamFixture, userID string) *http.ServeMux {
	mux := http.NewServeMux()
	team.Register(
		mux,
		fixture.store,
		func(next http.Handler) http.Handler { return next },
		func(context.Context) (team.Principal, bool) {
			return team.Principal{UserID: userID, TenantID: fixture.tenantID}, true
		},
		baseURL,
	)
	return mux
}

func TestTeamHTTPFlow(t *testing.T) {
	fixture := newTeamFixture(t)
	mux := teamMux(fixture, fixture.ownerID)

	response := request(mux, http.MethodGet, "/team?saved=1", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /team = %d, want 200", response.Code)
	}
	for _, want := range []string{"Team Garage", "Les accès aux sites ont été enregistrés."} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("GET /team body is missing %q", want)
		}
	}

	form := url.Values{team.FieldLocations: {fixture.locationA, fixture.locationB}}
	response = request(mux, http.MethodPost, "/team/"+fixture.memberID+"/locations", form)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/team?saved=1" {
		t.Fatalf("POST = %d location %q", response.Code, response.Header().Get("Location"))
	}
	if got := memberLocations(t, fixture); !slices.Equal(
		got, sorted(fixture.locationA, fixture.locationB),
	) {
		t.Fatalf("assignments after POST = %v", got)
	}

	response = request(mux, http.MethodPost, "/team/"+fixture.memberID+"/locations", nil)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("empty replacement = %d", response.Code)
	}
	if got := memberLocations(t, fixture); len(got) != 0 {
		t.Fatalf("empty replacement left %v", got)
	}
}

func TestTeamHTTPValidationAndAuthorization(t *testing.T) {
	fixture := newTeamFixture(t)
	if err := fixture.store.ReplaceLocationAssignments(
		t.Context(), fixture.tenantID, fixture.ownerID, fixture.memberID,
		[]string{fixture.locationA},
	); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		actorID    string
		target     string
		form       url.Values
		wantStatus int
	}{
		{
			name: "invalid target", target: "/team/not-a-uuid/locations",
			wantStatus: http.StatusNotFound,
		},
		{
			name: "invalid location", target: "/team/" + fixture.memberID + "/locations",
			form:       url.Values{team.FieldLocations: {"not-a-uuid"}},
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "unknown location", target: "/team/" + fixture.memberID + "/locations",
			form:       url.Values{team.FieldLocations: {unknownUserID}},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "member cannot assign", actorID: fixture.memberID,
			target:     "/team/" + fixture.memberID + "/locations",
			wantStatus: http.StatusForbidden,
		},
		{
			name: "owner is not staff", target: "/team/" + fixture.ownerID + "/locations",
			wantStatus: http.StatusForbidden,
		},
		{
			name: "missing member", target: "/team/" + unknownUserID + "/locations",
			wantStatus: http.StatusNotFound,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actorID := test.actorID
			if actorID == "" {
				actorID = fixture.ownerID
			}
			response := request(teamMux(fixture, actorID), http.MethodPost, test.target, test.form)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			// Whatever was refused, the assignment it aimed at must survive.
			if got := memberLocations(t, fixture); !slices.Equal(got, []string{fixture.locationA}) {
				t.Fatalf("a refused request changed assignments to %v", got)
			}
		})
	}
}

// TestTeamInviteRendersCodeOnce pins the one thing this screen must never get
// wrong: the invitation exists only in the response that created it.
func TestTeamInviteRendersCodeOnce(t *testing.T) {
	fixture := newTeamFixture(t)
	mux := teamMux(fixture, fixture.ownerID)

	form := url.Values{
		team.FieldName:      {"  Karim Mécano  "},
		team.FieldLocations: {fixture.locationA},
	}
	response := request(mux, http.MethodPost, "/team/invite", form)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /team/invite = %d, want 200", response.Code)
	}
	body := response.Body.String()
	match := regexp.MustCompile(
		regexp.QuoteMeta(baseURL) + `/rejoindre/([A-Z2-7]{12})`,
	).FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("no invitation link in the page that minted it: %q", body)
	}
	code := match[1]
	// The code is shown grouped too, because it gets read out loud.
	if grouped := code[:4] + "-" + code[4:8] + "-" + code[8:]; !strings.Contains(body, grouped) {
		t.Fatalf("the typed code %q is missing from the page that minted it", grouped)
	}
	// The trimmed name is what was enrolled, and the invitation is theirs.
	if !strings.Contains(body, "Karim Mécano") {
		t.Fatal("the invited person is not named on the page")
	}

	// A later view of the screen cannot show the code again: only its hash
	// was stored.
	response = request(mux, http.MethodGet, "/team", nil)
	if strings.Contains(response.Body.String(), code) {
		t.Fatal("the invitation code came back on a later page load")
	}
	if !strings.Contains(response.Body.String(), "Karim Mécano") {
		t.Fatal("the invited person is missing from the team list")
	}

	// A nameless employee is rejected without ever reaching the database.
	before := memberCount(t, fixture)
	response = request(mux, http.MethodPost, "/team/invite", url.Values{team.FieldName: {"   "}})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blank name = %d, want 422", response.Code)
	}
	if after := memberCount(t, fixture); after != before {
		t.Fatalf("a blank name enrolled someone: %d members, want %d", after, before)
	}
}

// TestTeamReissueRendersANewCode covers the second screen and the lost code.
func TestTeamReissueRendersANewCode(t *testing.T) {
	fixture := newTeamFixture(t)
	mux := teamMux(fixture, fixture.ownerID)
	invite, err := fixture.store.InviteStaff(
		t.Context(), fixture.tenantID, fixture.ownerID, "Sophie Accueil", nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	response := request(mux, http.MethodPost, "/team/"+invite.UserID+"/code", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("POST code = %d, want 200", response.Code)
	}
	body := response.Body.String()
	if strings.Contains(body, invite.Token) {
		t.Fatal("reissuing showed the code it just replaced")
	}
	if !regexp.MustCompile(
		regexp.QuoteMeta(baseURL) + `/rejoindre/[A-Z2-7]{12}`,
	).MatchString(body) {
		t.Fatalf("no new invitation link after reissuing: %q", body)
	}
	// Reissuing knows an id, not a person: the page must still name them.
	if !strings.Contains(body, "Sophie Accueil") {
		t.Fatal("the reissued code is not attributed to anyone")
	}

	// The owner signs in through Google; a staff code would be a way in.
	response = request(mux, http.MethodPost, "/team/"+fixture.ownerID+"/code", nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("reissuing for the owner = %d, want 403", response.Code)
	}
}

func TestTeamRenameAndRevokeRoundTrip(t *testing.T) {
	fixture := newTeamFixture(t)
	mux := teamMux(fixture, fixture.ownerID)
	invite, err := fixture.store.InviteStaff(
		t.Context(), fixture.tenantID, fixture.ownerID, "acceuil", nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	response := request(mux, http.MethodPost, "/team/"+invite.UserID+"/name",
		url.Values{team.FieldName: {"  Accueil  "}})
	if response.Code != http.StatusSeeOther ||
		response.Header().Get("Location") != "/team?saved=renamed" {
		t.Fatalf("rename = %d location %q", response.Code, response.Header().Get("Location"))
	}
	response = request(mux, http.MethodPost, "/team/"+invite.UserID+"/name",
		url.Values{team.FieldName: {"   "}})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("blank rename = %d, want 422", response.Code)
	}

	for _, test := range []struct {
		name       string
		target     string
		wantStatus int
	}{
		{name: "owner", target: fixture.ownerID, wantStatus: http.StatusForbidden},
		{name: "unknown", target: unknownUserID, wantStatus: http.StatusNotFound},
		{name: "staff", target: invite.UserID, wantStatus: http.StatusSeeOther},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := request(mux, http.MethodPost, "/team/"+test.target+"/revoke", nil)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}

	page, err := fixture.store.Page(t.Context(), fixture.tenantID, fixture.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range page.Members {
		if member.UserID == invite.UserID {
			t.Fatal("the removed person is still on the team")
		}
	}
}

// memberLocations reads back what the fixture's member actually reaches.
func memberLocations(t *testing.T, fixture teamFixture) []string {
	t.Helper()
	page, err := fixture.store.Page(t.Context(), fixture.tenantID, fixture.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	return findMember(t, page.Members, fixture.memberID).LocationIDs
}

func memberCount(t *testing.T, fixture teamFixture) int {
	t.Helper()
	page, err := fixture.store.Page(t.Context(), fixture.tenantID, fixture.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	return len(page.Members)
}

func sorted(ids ...string) []string {
	slices.Sort(ids)
	return ids
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
