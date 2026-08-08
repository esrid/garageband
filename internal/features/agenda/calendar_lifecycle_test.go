package agenda

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/assistanttools"
	"github.com/esrid/garageband/internal/platform/calendar"
	"github.com/esrid/garageband/internal/platform/secrets"
)

// recordingCalendar stands in for Google. Every appointment mutation is
// supposed to reach it, whichever adapter started the mutation.
type recordingCalendar struct {
	upserted []calendar.Event
	deleted  []string
	failWith error
}

func (c *recordingCalendar) Busy(
	context.Context, calendar.AvailabilityRequest,
) (map[string][]calendar.TimeRange, error) {
	return nil, nil
}

func (c *recordingCalendar) UpsertEvent(
	_ context.Context, event calendar.Event,
) (calendar.Event, error) {
	if c.failWith != nil {
		return calendar.Event{}, c.failWith
	}
	c.upserted = append(c.upserted, event)
	return event, nil
}

func (c *recordingCalendar) DeleteEvent(_ context.Context, _, externalEventID string) error {
	if c.failWith != nil {
		return c.failWith
	}
	c.deleted = append(c.deleted, externalEventID)
	return nil
}

// connectCalendar gives the fixture's location a live calendar connection and
// points the lifecycle at a provider the test can read back. The OAuth config
// stays zero: the recorder never uses the HTTP client, so no token is ever
// refreshed.
func connectCalendar(t *testing.T, fixture calendarSyncFixture) (*Store, *recordingCalendar) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	secretStore, err := secrets.NewAESStore(key)
	if err != nil {
		t.Fatal(err)
	}
	var secretRef string
	if err := fixture.runtime.WithinTenantUser(
		t.Context(), fixture.tenantID, fixture.userID, func(tx pgx.Tx) error {
			secretRef, err = secretStore.Store(t.Context(), tx, fixture.tenantID, []byte("refresh-token"))
			return err
		},
	); err != nil {
		t.Fatal(err)
	}
	mustExec(t, fixture.fixtures, `
		INSERT INTO provider_connections (tenant_id, location_id, kind, provider, secret_ref)
		VALUES ($1, $2, 'calendar', 'google', $3)`,
		fixture.tenantID, fixture.locationID, secretRef)

	recorder := &recordingCalendar{}
	previous := newCalendarProvider
	newCalendarProvider = func(*http.Client) calendar.Provider { return recorder }
	t.Cleanup(func() { newCalendarProvider = previous })

	return NewStore(fixture.runtime, CalendarConfig{
		Enabled: true, Secrets: secretStore,
	}), recorder
}

// TestAssistantWritesReachTheSameCalendarAsTheWeekGrid is the point of putting
// every mutation behind one lifecycle: an appointment booked by the assistant
// used to be invisible in the garage's Google Calendar, while the identical
// booking made on screen showed up. Same store, same reconciliation, both ways.
func TestAssistantWritesReachTheSameCalendarAsTheWeekGrid(t *testing.T) {
	fixture, _ := newCalendarSyncFixture(t)
	store, recorder := connectCalendar(t, fixture)
	scope := assistanttools.Scope{
		TenantID: fixture.tenantID, UserID: fixture.userID,
		LocationID: fixture.locationID, IdempotencyKey: "key-book",
	}

	booked, err := store.Execute(t.Context(), scope, ToolBookAppointment, bookInput(t, fixture, "2026-08-13", "10:00"))
	if err != nil {
		t.Fatal(err)
	}
	if len(recorder.upserted) != 1 {
		t.Fatalf("assistant booking pushed %d events, want 1", len(recorder.upserted))
	}
	appointmentID := affectedAppointmentID(t, booked)
	pushed := recorder.upserted[0]
	if pushed.ExternalID != calendarEventID(appointmentID) {
		t.Fatalf("pushed event id = %q, want the one derived from %q", pushed.ExternalID, appointmentID)
	}
	if pushed.Title != "Révision — Alice Martin" || pushed.TimeZone != "America/Martinique" {
		t.Fatalf("pushed event = %+v", pushed)
	}
	if got := syncStatus(t, fixture, appointmentID); got != "synced" {
		t.Fatalf("sync status after assistant booking = %q, want synced", got)
	}

	// A retried call replays the receipt instead of booking twice, and pushes
	// again rather than leaving a calendar that missed the first attempt.
	if _, err := store.Execute(t.Context(), scope, ToolBookAppointment, bookInput(t, fixture, "2026-08-13", "10:00")); err != nil {
		t.Fatal(err)
	}
	if len(recorder.upserted) != 2 {
		t.Fatalf("replayed booking pushed %d events in total, want 2", len(recorder.upserted))
	}
	if recorder.upserted[1].ExternalID != pushed.ExternalID {
		t.Fatal("the replay pushed a different event, so the retry would duplicate the booking")
	}

	// Rescheduling through the assistant moves the same event, not a new one.
	rescheduleScope := scope
	rescheduleScope.IdempotencyKey = "key-move"
	moveInput, err := json.Marshal(map[string]string{
		"appointment_id": appointmentID, "date": "2026-08-14", "start_time": "11:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Execute(t.Context(), rescheduleScope, ToolRescheduleAppointment, moveInput); err != nil {
		t.Fatal(err)
	}
	if len(recorder.upserted) != 3 {
		t.Fatalf("assistant reschedule pushed %d events in total, want 3", len(recorder.upserted))
	}
	moved := recorder.upserted[2]
	if moved.ExternalID != pushed.ExternalID {
		t.Fatal("rescheduling created a second calendar event instead of moving the first")
	}
	if moved.Start.Equal(pushed.Start) {
		t.Fatalf("rescheduled event still starts at %v", moved.Start)
	}

	// Cancelling through the assistant takes the event off the calendar.
	cancelScope := scope
	cancelScope.IdempotencyKey = "key-cancel"
	cancelInput, err := json.Marshal(map[string]string{"appointment_id": appointmentID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Execute(t.Context(), cancelScope, ToolCancelAppointment, cancelInput); err != nil {
		t.Fatal(err)
	}
	if len(recorder.deleted) != 1 || recorder.deleted[0] != pushed.ExternalID {
		t.Fatalf("assistant cancellation deleted %v, want [%s]", recorder.deleted, pushed.ExternalID)
	}
	if got := syncStatus(t, fixture, appointmentID); got != "deleted" {
		t.Fatalf("sync status after assistant cancellation = %q, want deleted", got)
	}
}

// TestACalendarThatRefusesThePushDoesNotUndoTheBooking pins the failure policy
// the lifecycle now owns for every adapter: the appointment is committed and
// the customer keeps it; only the calendar row records the problem.
func TestACalendarThatRefusesThePushDoesNotUndoTheBooking(t *testing.T) {
	fixture, _ := newCalendarSyncFixture(t)
	store, recorder := connectCalendar(t, fixture)
	recorder.failWith = errors.New("google says no")

	id, _, err := store.Save(t.Context(), fixture.tenantID, fixture.userID, "", SaveInput{
		LocationID: fixture.locationID, CustomerID: fixture.customerID,
		VehicleID: fixture.vehicleID, ServiceID: fixture.serviceID,
		ResourceID: fixture.resourceID,
		Date:       "2026-08-17", StartTime: "14:00",
	})
	if err != nil {
		t.Fatalf("a refused calendar push failed the booking: %v", err)
	}
	if got := syncStatus(t, fixture, id); got != "error" {
		t.Fatalf("sync status after a refused push = %q, want error", got)
	}
	var lastError string
	if err := fixture.fixtures.QueryRow(t.Context(), `
		SELECT COALESCE(last_error, '') FROM appointment_calendar_events
		WHERE tenant_id = $1 AND appointment_id = $2`, fixture.tenantID, id,
	).Scan(&lastError); err != nil {
		t.Fatal(err)
	}
	if lastError != "google says no" {
		t.Fatalf("last_error = %q, want the provider's own refusal", lastError)
	}
}

func bookInput(t *testing.T, fixture calendarSyncFixture, date, startTime string) json.RawMessage {
	t.Helper()
	input, err := json.Marshal(map[string]any{
		"customer_id": fixture.customerID, "vehicle_id": fixture.vehicleID,
		"service_id": fixture.serviceID, "resource_ids": []string{fixture.resourceID},
		"date": date, "start_time": startTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func affectedAppointmentID(t *testing.T, result assistanttools.Result) string {
	t.Helper()
	for _, record := range result.AffectedRecords {
		if record.Kind == "appointment" {
			return record.ID
		}
	}
	t.Fatal("the tool reported no appointment")
	return ""
}

func syncStatus(t *testing.T, fixture calendarSyncFixture, appointmentID string) string {
	t.Helper()
	var status string
	if err := fixture.fixtures.QueryRow(t.Context(), `
		SELECT sync_status FROM appointment_calendar_events
		WHERE tenant_id = $1 AND appointment_id = $2`,
		fixture.tenantID, appointmentID,
	).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}
