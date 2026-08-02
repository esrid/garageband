package calendar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestProvider(t *testing.T, handler http.HandlerFunc) *googleProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &googleProvider{client: server.Client(), baseURL: server.URL}
}

func TestUpsertEventUpdatesFirstThenFallsBackToInsertOn404(t *testing.T) {
	var calls []string
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": 404}})
		case http.MethodPost:
			var body googleEvent
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.ID != "appt123" {
				t.Fatalf("insert body id = %q, want appt123", body.ID)
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(googleEvent{ID: "appt123"})
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})

	event := Event{
		ExternalID: "appt123", CalendarID: "primary", Title: "Révision - Clio",
		Start: time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
	}
	result, err := provider.UpsertEvent(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalID != "appt123" {
		t.Fatalf("result external id = %q", result.ExternalID)
	}
	if len(calls) != 2 || calls[0] != "PUT /calendars/primary/events/appt123" ||
		calls[1] != "POST /calendars/primary/events" {
		t.Fatalf("calls = %v, want update-then-insert-on-404", calls)
	}
}

func TestUpsertEventUpdatesInPlaceWhenTheEventAlreadyExists(t *testing.T) {
	calls := 0
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPut || r.URL.Path != "/calendars/primary/events/appt123" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(googleEvent{ID: "appt123"})
	})

	event := Event{
		ExternalID: "appt123", CalendarID: "primary", Title: "Révision - Clio",
		Start: time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
	}
	if _, err := provider.UpsertEvent(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (no fallback insert needed)", calls)
	}
}

func TestUpsertEventRejectsAMissingExternalID(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no request should be sent without an external id")
	})
	if _, err := provider.UpsertEvent(t.Context(), Event{CalendarID: "primary"}); err == nil {
		t.Fatal("expected an error for a missing ExternalID")
	}
}

func TestDeleteEventIsIdempotentOnAlreadyGone(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Fatalf("unexpected method %s", r.Method)
			}
			w.WriteHeader(status)
		})
		if err := provider.DeleteEvent(t.Context(), "primary", "appt123"); err != nil {
			t.Fatalf("delete with status %d = %v, want nil (idempotent)", status, err)
		}
	}
}

func TestDeleteEventSucceeds(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	if err := provider.DeleteEvent(t.Context(), "primary", "appt123"); err != nil {
		t.Fatal(err)
	}
}

func TestBusyParsesFreeBusyResponse(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/freeBusy" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body freeBusyRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Items) != 1 || body.Items[0].ID != "primary" {
			t.Fatalf("freebusy request items = %#v", body.Items)
		}
		_ = json.NewEncoder(w).Encode(freeBusyResponse{
			Calendars: map[string]freeBusyCalendarResult{
				"primary": {Busy: []freeBusyInterval{
					{Start: "2026-08-12T09:00:00Z", End: "2026-08-12T10:00:00Z"},
				}},
			},
		})
	})

	result, err := provider.Busy(t.Context(), AvailabilityRequest{
		CalendarIDs: []string{"primary"},
		Start:       time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC),
		End:         time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result["primary"]) != 1 {
		t.Fatalf("busy ranges = %#v", result)
	}
	want := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	if !result["primary"][0].Start.Equal(want) {
		t.Fatalf("busy start = %v, want %v", result["primary"][0].Start, want)
	}
}

func TestBusyReportsPerCalendarErrors(t *testing.T) {
	provider := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(freeBusyResponse{
			Calendars: map[string]freeBusyCalendarResult{
				"unknown": {Errors: []freeBusyCalendarError{{Domain: "global", Reason: "notFound"}}},
			},
		})
	})
	if _, err := provider.Busy(t.Context(), AvailabilityRequest{CalendarIDs: []string{"unknown"}}); err == nil {
		t.Fatal("expected an error for a notFound calendar")
	}
}
