package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/esrid/garageband/internal/features/auth"
	"github.com/esrid/garageband/internal/platform/db"
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
	d, err := db.Open("file:" + t.TempDir() + "/test.db?_fk=on")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.Migrate(t.Context(), d); err != nil {
		t.Fatal(err)
	}
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
	d, err := db.Open("file:" + t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.Migrate(t.Context(), d); err != nil {
		t.Fatal(err)
	}
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
