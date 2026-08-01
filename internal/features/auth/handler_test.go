package auth_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/esrid/garageband/internal/features/auth"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
	"github.com/esrid/garageband/internal/platform/oauth"
)

// fakeProvider exercises the oauth.Provider port without any network — the
// reason the port exists.
type fakeProvider struct{ failAuth bool }

func (fakeProvider) Name() string { return "fake" }

func (fakeProvider) AuthCodeURL(state, verifier string) string {
	return "https://fake.example/authorize?state=" + url.QueryEscape(state)
}

func (f fakeProvider) Authenticate(ctx context.Context, code, verifier string) (oauth.UserInfo, error) {
	if f.failAuth {
		return oauth.UserInfo{}, context.DeadlineExceeded
	}
	return oauth.UserInfo{ProviderID: "u1", Email: "u@example.com", Name: "U"}, nil
}

func setup(t *testing.T) http.Handler {
	t.Helper()
	d := dbtest.Open(t)
	store := auth.NewStore(d)
	mux := http.NewServeMux()
	auth.Register(mux, store, []oauth.Provider{fakeProvider{}}, false)
	mux.Handle("GET /private", auth.RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := auth.UserFrom(r.Context())
		if _, err := w.Write([]byte("hello " + u.Email)); err != nil {
			t.Error(err)
		}
	})))
	return store.WithUser(mux)
}

func get(h http.Handler, target string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func login(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	rec := get(h, "/auth/fake", nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("start: got %d, want 302", rec.Code)
	}
	stateCookie := rec.Result().Cookies()[0]
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := loc.Query().Get("state")

	rec = get(h, "/auth/fake/callback?state="+url.QueryEscape(state)+"&code=c", []*http.Cookie{stateCookie})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("callback: got %d body %q", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" && c.Value != "" {
			if !c.HttpOnly {
				t.Fatal("session cookie not HttpOnly")
			}
			return c
		}
	}
	t.Fatal("no session cookie set")
	return nil
}

func TestLoginFlow(t *testing.T) {
	h := setup(t)

	if rec := get(h, "/private", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("anonymous /private: got %d, want 303 redirect to /login", rec.Code)
	}

	session := login(t, h)

	rec := get(h, "/private", []*http.Cookie{session})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "u@example.com") {
		t.Fatalf("authed /private: got %d body %q", rec.Code, rec.Body.String())
	}
}

func TestLogoutRevokesServerSide(t *testing.T) {
	h := setup(t)
	session := login(t, h)

	req := httptest.NewRequest("POST", "/logout", nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("logout: got %d", rec.Code)
	}

	// The old cookie value must be dead even if the client kept it.
	if rec := get(h, "/private", []*http.Cookie{session}); rec.Code != http.StatusSeeOther {
		t.Fatalf("replayed session after logout: got %d, want 303", rec.Code)
	}
}

func TestCallbackRejectsTamperedState(t *testing.T) {
	h := setup(t)
	rec := get(h, "/auth/fake", nil)
	stateCookie := rec.Result().Cookies()[0]

	rec = get(h, "/auth/fake/callback?state=forged&code=c", []*http.Cookie{stateCookie})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("tampered state: got %d, want 400", rec.Code)
	}
}

func TestCallbackWithoutStateCookie(t *testing.T) {
	h := setup(t)
	if rec := get(h, "/auth/fake/callback?state=x&code=c", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing cookie: got %d, want 400", rec.Code)
	}
}

func TestProviderFailure(t *testing.T) {
	d := dbtest.Open(t)
	mux := http.NewServeMux()
	auth.Register(mux, auth.NewStore(d), []oauth.Provider{fakeProvider{failAuth: true}}, false)

	rec := get(mux, "/auth/fake", nil)
	stateCookie := rec.Result().Cookies()[0]
	loc, _ := url.Parse(rec.Header().Get("Location"))

	rec = get(mux, "/auth/fake/callback?state="+url.QueryEscape(loc.Query().Get("state"))+"&code=c", []*http.Cookie{stateCookie})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("provider failure: got %d, want 502", rec.Code)
	}
}

func TestWorkspaceActivationIsSessionScopedAndMembershipBound(t *testing.T) {
	database := dbtest.Open(t)
	store := auth.NewStore(database)
	owner := createTestUser(t, database, "owner@example.com")
	other := createTestUser(t, database, "other@example.com")
	ownedTenant := createWorkspace(t, database, owner, "owned-garage", "Owned Garage")
	otherTenant := createWorkspace(t, database, other, "other-garage", "Other Garage")

	token, err := store.CreateSession(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	otherSessionToken, err := store.CreateSession(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	session := &http.Cookie{Name: "session", Value: token}
	mux := http.NewServeMux()
	auth.Register(mux, store, nil, false)
	mux.Handle("GET /tenant-only", auth.RequireTenant(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := auth.TenantFrom(r.Context())
		if !ok {
			t.Error("active tenant missing")
		}
		if _, err := w.Write([]byte(tenantID)); err != nil {
			t.Error(err)
		}
	})))
	handler := store.WithUser(mux)

	if response := get(handler, "/tenant-only", []*http.Cookie{session}); response.Code != http.StatusSeeOther {
		t.Fatalf("tenant-only before activation = %d, want 303", response.Code)
	}

	response := post(handler, "/workspaces/"+otherTenant+"/activate", session)
	if response.Code != http.StatusNotFound {
		t.Fatalf("activate non-member tenant = %d, want 404", response.Code)
	}
	user, err := store.UserByToken(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	if user.ActiveTenantID != "" {
		t.Fatalf("non-member tenant became active: %s", user.ActiveTenantID)
	}

	response = post(handler, "/workspaces/"+ownedTenant+"/activate", session)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("activate member tenant = %d, body = %q", response.Code, response.Body.String())
	}
	user, err = store.UserByToken(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	if user.ActiveTenantID != ownedTenant {
		t.Fatalf("active tenant = %q, want %q", user.ActiveTenantID, ownedTenant)
	}
	otherSessionUser, err := store.UserByToken(t.Context(), otherSessionToken)
	if err != nil {
		t.Fatal(err)
	}
	if otherSessionUser.ActiveTenantID != "" {
		t.Fatalf("activation leaked to another session: %s", otherSessionUser.ActiveTenantID)
	}
	response = get(handler, "/tenant-only", []*http.Cookie{session})
	if response.Code != http.StatusOK || response.Body.String() != ownedTenant {
		t.Fatalf("tenant-only after activation = %d %q", response.Code, response.Body.String())
	}

	workspaces, err := store.Workspaces(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != 1 || workspaces[0].ID != ownedTenant || workspaces[0].Role != "owner" {
		t.Fatalf("workspaces = %#v", workspaces)
	}

	// PostgreSQL clears active_tenant_id when the backing membership disappears.
	if err := database.WithinTenant(t.Context(), ownedTenant, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `
			DELETE FROM tenant_memberships
			WHERE tenant_id = $1 AND user_id = $2`, ownedTenant, owner)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	user, err = store.UserByToken(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	if user.ActiveTenantID != "" {
		t.Fatalf("active tenant survived membership deletion: %s", user.ActiveTenantID)
	}
}

func post(handler http.Handler, target string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, target, nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func createTestUser(t *testing.T, database *db.DB, email string) string {
	t.Helper()
	var userID string
	if err := database.QueryRow(`
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', $1, $1, 'Test User')
		RETURNING id`, email).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return userID
}

func createWorkspace(t *testing.T, database *db.DB, userID, slug, name string) string {
	t.Helper()
	var tenantID string
	err := database.WithinNewTenant(t.Context(), func(tx *sql.Tx, newTenantID string) error {
		tenantID = newTenantID
		if _, err := tx.ExecContext(t.Context(), `
			INSERT INTO tenants (id, slug, name) VALUES ($1, $2, $3)`,
			newTenantID, slug, name,
		); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), `
			INSERT INTO tenant_memberships (tenant_id, user_id, role)
			VALUES ($1, $2, 'owner')`, newTenantID, userID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tenantID
}
