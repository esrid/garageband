package locations_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/esrid/garageband/internal/features/locations"
	"github.com/esrid/garageband/internal/platform/dbtest"
)

// calendarFlow is one location with the Calendar connect flow enabled and a
// token endpoint under the test's control, so the callback can be driven
// without a live Google account.
type calendarFlow struct {
	handler    http.Handler
	locationID string
	// exchanged records what the token endpoint was asked for, which is how
	// the PKCE verifier is checked: it must be the one minted at connect.
	exchanged url.Values
}

func newCalendarFlow(t *testing.T, tokenResponse func(http.ResponseWriter)) *calendarFlow {
	t.Helper()
	database := dbtest.Open(t)
	ownerID := createUser(t, database, "calendar-callback-owner@example.com")
	tenantID := createTenant(t, database, ownerID)
	store := locations.NewStore(database)
	created, err := store.Create(t.Context(), tenantID, ownerID, locations.Input{
		Name: "Atelier Callback", CountryCode: "FR", Timezone: "America/Martinique",
	})
	if err != nil {
		t.Fatal(err)
	}

	flow := &calendarFlow{locationID: created.ID}
	tokenServer := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			flow.exchanged = r.Form
			tokenResponse(w)
		}))
	t.Cleanup(tokenServer.Close)

	flow.handler = locationHandlerWithCalendar(
		store,
		locations.Principal{UserID: ownerID, TenantID: tenantID},
		locations.CalendarConfig{
			Enabled: true,
			Secrets: newTestSecretStore(t),
			OAuth: oauth2.Config{
				ClientID: "test-client",
				Endpoint: oauth2.Endpoint{
					AuthURL:  "https://accounts.google.com/o/oauth2/auth",
					TokenURL: tokenServer.URL,
				},
			},
		},
	)
	return flow
}

// begin walks the connect redirect and hands back the state cookie the browser
// would keep, plus the state Google would echo.
func (f *calendarFlow) begin(t *testing.T) (cookie *http.Cookie, state string) {
	t.Helper()
	response := getLocationPage(f.handler, "/locations/"+f.locationID+"/calendar/connect")
	if response.Code != http.StatusFound {
		t.Fatalf("connect = %d, want 302", response.Code)
	}
	for _, candidate := range response.Result().Cookies() {
		if candidate.Name == "calendar_oauth_state" {
			cookie = candidate
		}
	}
	if cookie == nil {
		t.Fatal("connect set no state cookie")
	}
	if !cookie.HttpOnly || cookie.MaxAge != 600 || cookie.Path != "/oauth/google-calendar" {
		t.Fatalf("state cookie = %+v, want HttpOnly, 600s, scoped to the callback", cookie)
	}
	redirect, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state = redirect.Query().Get("state")
	if state == "" || !strings.HasPrefix(cookie.Value, state+".") {
		t.Fatalf("state %q is not what the cookie carries (%q)", state, cookie.Value)
	}
	return cookie, state
}

func (f *calendarFlow) callback(t *testing.T, query string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/oauth/google-calendar/callback?"+query, nil)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

// TestCalendarCallbackRefusesEveryStateItDidNotMint covers the half of the
// connect flow an attacker controls: the callback is a plain GET on a fixed
// path, so the state cookie is the only thing standing between a forged
// redirect and a calendar connected to someone else's Google account.
func TestCalendarCallbackRefusesEveryStateItDidNotMint(t *testing.T) {
	flow := newCalendarFlow(t, func(w http.ResponseWriter) {
		t.Error("the token endpoint was reached with an unverified state")
	})
	cookie, state := flow.begin(t)

	for _, test := range []struct {
		name   string
		query  string
		cookie *http.Cookie
	}{
		{name: "no cookie at all", query: "state=" + state + "&code=c"},
		{name: "forged state", query: "state=forged&code=c", cookie: cookie},
		{name: "no state echoed", query: "code=c", cookie: cookie},
		{
			name: "cookie without the verifier", query: "state=" + state + "&code=c",
			cookie: &http.Cookie{Name: "calendar_oauth_state", Value: state},
		},
		{
			name: "cookie without the location", query: "state=" + state + "&code=c",
			cookie: &http.Cookie{Name: "calendar_oauth_state", Value: state + ".verifier"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := flow.callback(t, test.query, test.cookie)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", response.Code)
			}
			// A refused attempt must also burn the cookie: leaving it live
			// lets the next forged callback try again with the same state.
			if !clearsStateCookie(response) {
				t.Fatalf("the state cookie survived a refused callback: %v", response.Result().Cookies())
			}
		})
	}
}

// TestCalendarCallbackSendsThePKCEVerifierItMinted pins the other half of the
// exchange: an intercepted authorization code is useless without the verifier,
// and the verifier only ever lives in the cookie.
func TestCalendarCallbackSendsThePKCEVerifierItMinted(t *testing.T) {
	flow := newCalendarFlow(t, func(w http.ResponseWriter) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
	})
	cookie, state := flow.begin(t)
	_, verifier, _ := strings.Cut(cookie.Value, ".")
	verifier, _, _ = strings.Cut(verifier, ".")

	response := flow.callback(t, "state="+state+"&code=the-code", cookie)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("failed exchange = %d, want 502", response.Code)
	}
	if got := flow.exchanged.Get("code_verifier"); got != verifier {
		t.Fatalf("exchanged code_verifier = %q, want the one from the cookie %q", got, verifier)
	}
	if got := flow.exchanged.Get("code"); got != "the-code" {
		t.Fatalf("exchanged code = %q, want the one Google echoed", got)
	}
	if !clearsStateCookie(response) {
		t.Fatal("the state cookie survived a failed exchange")
	}
}

// TestCalendarCallbackRefusesATokenWithoutOfflineAccess covers the answer
// Google gives when consent was already granted: an access token alone, which
// stops working within the hour and would leave a connection that silently
// dies. The flow asks for prompt=consent precisely to avoid it.
func TestCalendarCallbackRefusesATokenWithoutOfflineAccess(t *testing.T) {
	flow := newCalendarFlow(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(
			`{"access_token":"at","token_type":"Bearer","expires_in":3600}`,
		)); err != nil {
			t.Error(err)
		}
	})
	cookie, state := flow.begin(t)

	response := flow.callback(t, "state="+state+"&code=c", cookie)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("token without refresh_token = %d, want 502", response.Code)
	}
	if !strings.Contains(response.Body.String(), "durable") {
		t.Fatalf("body = %q, want it to name the missing lasting access", response.Body.String())
	}
	// ponytail: the success path ends in a call to Google's userinfo endpoint
	// at a hard-coded URL, so it needs a live account and stays out of reach
	// here. calendar_queries_test.go covers what it would have written.
}

// clearsStateCookie reports the expiry the callback must always send back.
func clearsStateCookie(response *httptest.ResponseRecorder) bool {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "calendar_oauth_state" && cookie.MaxAge < 0 {
			return true
		}
	}
	return false
}
