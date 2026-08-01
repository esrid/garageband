package todos_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/esrid/garageband/internal/features/todos"
	"github.com/esrid/garageband/internal/platform/db"
)

func setup(t *testing.T) *http.ServeMux {
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
	mux := http.NewServeMux()
	todos.Register(mux, todos.NewStore(d))
	return mux
}

func do(mux *http.ServeMux, method, target string, form url.Values) *httptest.ResponseRecorder {
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestCreateListToggleDelete(t *testing.T) {
	mux := setup(t)

	if rec := do(mux, "POST", "/todos", url.Values{"title": {"buy milk"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("create: got %d, want 303", rec.Code)
	}

	rec := do(mux, "GET", "/todos", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "buy milk") {
		t.Fatalf("list: got %d, body contains title: %v", rec.Code, strings.Contains(rec.Body.String(), "buy milk"))
	}

	var items []todos.Todo
	rec = do(mux, "GET", "/api/todos", nil)
	if err := json.NewDecoder(rec.Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "buy milk" || items[0].Done {
		t.Fatalf("json list: got %+v", items)
	}

	if rec := do(mux, "POST", "/todos/"+items[0].ID+"/toggle", url.Values{}); rec.Code != http.StatusSeeOther {
		t.Fatalf("toggle: got %d, want 303", rec.Code)
	}
	rec = do(mux, "GET", "/api/todos", nil)
	items = nil
	if err := json.NewDecoder(rec.Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
	if !items[0].Done {
		t.Fatal("toggle: todo still not done")
	}

	if rec := do(mux, "POST", "/todos/"+items[0].ID+"/delete", url.Values{}); rec.Code != http.StatusSeeOther {
		t.Fatalf("delete: got %d, want 303", rec.Code)
	}
	rec = do(mux, "GET", "/api/todos", nil)
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("after delete: got %s, want []", body)
	}
}

func TestCreateRejectsBadTitle(t *testing.T) {
	mux := setup(t)
	if rec := do(mux, "POST", "/todos", url.Values{"title": {"   "}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank title: got %d, want 400", rec.Code)
	}
	if rec := do(mux, "POST", "/todos", url.Values{"title": {strings.Repeat("x", 201)}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("long title: got %d, want 400", rec.Code)
	}
}

func TestMutateUnknownID(t *testing.T) {
	mux := setup(t)
	if rec := do(mux, "POST", "/todos/nope/toggle", url.Values{}); rec.Code != http.StatusNotFound {
		t.Fatalf("toggle unknown: got %d, want 404", rec.Code)
	}
}
