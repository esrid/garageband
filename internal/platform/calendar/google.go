package calendar

// Verified against https://developers.google.com/calendar/api/v3/reference
// (events.insert, events.update, events.delete, freebusy.query) and
// https://developers.google.com/identity/protocols/oauth2/scopes#calendar
// 2026-08-03. No google.golang.org/api dependency: these are three small
// JSON endpoints, and golang.org/x/oauth2 (already a dependency, via the
// login OAuth provider) supplies an authorized *http.Client directly.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const googleCalendarBaseURL = "https://www.googleapis.com/calendar/v3"

// ScopeEvents is the OAuth scope this adapter needs: "View and edit events
// on all your calendars." Not the broader calendar/calendar.acls scopes,
// which this adapter never uses.
const ScopeEvents = "https://www.googleapis.com/auth/calendar.events"

type googleProvider struct {
	client  *http.Client
	baseURL string // overridden in tests only; always googleCalendarBaseURL in production
}

// NewGoogle builds a Provider from an already-authorized client, typically
// (*oauth2.Config).Client(ctx, token) with a stored refresh token: token
// refresh is the oauth2 package's job, not this adapter's.
func NewGoogle(client *http.Client) Provider {
	return &googleProvider{client: client, baseURL: googleCalendarBaseURL}
}

type googleEventDateTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone,omitempty"`
}

type googleEvent struct {
	ID          string              `json:"id,omitempty"`
	Summary     string              `json:"summary,omitempty"`
	Description string              `json:"description,omitempty"`
	Location    string              `json:"location,omitempty"`
	Start       googleEventDateTime `json:"start"`
	End         googleEventDateTime `json:"end"`
	Visibility  string              `json:"visibility,omitempty"`
	Status      string              `json:"status,omitempty"`
	HTMLLink    string              `json:"htmlLink,omitempty"`
}

func (g *googleProvider) UpsertEvent(ctx context.Context, event Event) (Event, error) {
	if event.ExternalID == "" {
		return Event{}, fmt.Errorf("calendar: UpsertEvent requires a caller-assigned ExternalID for idempotency")
	}
	body := googleEvent{
		ID:          event.ExternalID,
		Summary:     event.Title,
		Description: event.Description,
		Location:    event.Location,
		Start:       googleEventDateTime{DateTime: event.Start.Format(time.RFC3339), TimeZone: event.TimeZone},
		End:         googleEventDateTime{DateTime: event.End.Format(time.RFC3339), TimeZone: event.TimeZone},
	}
	if event.Private {
		body.Visibility = "private"
	}

	updateURL := g.baseURL + "/calendars/" + pathEscape(event.CalendarID) +
		"/events/" + pathEscape(event.ExternalID)
	var result googleEvent
	err := g.do(ctx, http.MethodPut, updateURL, body, &result)
	if isNotFound(err) {
		// The event was deleted on Google's side out of band (or this is the
		// first sync): recreate it with the same id, so the reference this
		// application already stored stays valid.
		insertURL := g.baseURL + "/calendars/" + pathEscape(event.CalendarID) + "/events"
		err = g.do(ctx, http.MethodPost, insertURL, body, &result)
	}
	if err != nil {
		return Event{}, err
	}
	upserted := event
	upserted.ExternalID = result.ID
	return upserted, nil
}

func (g *googleProvider) DeleteEvent(ctx context.Context, calendarID, externalEventID string) error {
	deleteURL := g.baseURL + "/calendars/" + pathEscape(calendarID) + "/events/" + pathEscape(externalEventID)
	err := g.do(ctx, http.MethodDelete, deleteURL, nil, nil)
	if isNotFound(err) || isGone(err) {
		// Already gone is the goal of a delete: idempotent either way.
		return nil
	}
	return err
}

type freeBusyRequestItem struct {
	ID string `json:"id"`
}

type freeBusyRequest struct {
	TimeMin  string                `json:"timeMin"`
	TimeMax  string                `json:"timeMax"`
	TimeZone string                `json:"timeZone,omitempty"`
	Items    []freeBusyRequestItem `json:"items"`
}

type freeBusyInterval struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type freeBusyCalendarError struct {
	Domain string `json:"domain"`
	Reason string `json:"reason"`
}

type freeBusyCalendarResult struct {
	Busy   []freeBusyInterval      `json:"busy"`
	Errors []freeBusyCalendarError `json:"errors,omitempty"`
}

type freeBusyResponse struct {
	Calendars map[string]freeBusyCalendarResult `json:"calendars"`
}

func (g *googleProvider) Busy(ctx context.Context, request AvailabilityRequest) (map[string][]TimeRange, error) {
	items := make([]freeBusyRequestItem, 0, len(request.CalendarIDs))
	for _, id := range request.CalendarIDs {
		items = append(items, freeBusyRequestItem{ID: id})
	}
	body := freeBusyRequest{
		TimeMin: request.Start.Format(time.RFC3339), TimeMax: request.End.Format(time.RFC3339),
		TimeZone: request.TimeZone, Items: items,
	}
	var result freeBusyResponse
	if err := g.do(ctx, http.MethodPost, g.baseURL+"/freeBusy", body, &result); err != nil {
		return nil, err
	}
	busy := make(map[string][]TimeRange, len(result.Calendars))
	for calendarID, calendarResult := range result.Calendars {
		if len(calendarResult.Errors) > 0 {
			return nil, fmt.Errorf("calendar: freeBusy error for %s: %s", calendarID, calendarResult.Errors[0].Reason)
		}
		ranges := make([]TimeRange, 0, len(calendarResult.Busy))
		for _, interval := range calendarResult.Busy {
			start, err := time.Parse(time.RFC3339, interval.Start)
			if err != nil {
				return nil, err
			}
			end, err := time.Parse(time.RFC3339, interval.End)
			if err != nil {
				return nil, err
			}
			ranges = append(ranges, TimeRange{Start: start, End: end})
		}
		busy[calendarID] = ranges
	}
	return busy, nil
}

type googleAPIError struct {
	statusCode int
	message    string
}

func (e *googleAPIError) Error() string {
	return fmt.Sprintf("calendar: google api error %d: %s", e.statusCode, e.message)
}

func isNotFound(err error) bool { return statusCode(err) == http.StatusNotFound }
func isGone(err error) bool     { return statusCode(err) == http.StatusGone }

func statusCode(err error) int {
	var apiErr *googleAPIError
	if !errors.As(err, &apiErr) {
		return 0
	}
	return apiErr.statusCode
}

func (g *googleProvider) do(ctx context.Context, method, requestURL string, requestBody any, into any) error {
	var reader io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(resp.Body)
		return &googleAPIError{statusCode: resp.StatusCode, message: string(payload)}
	}
	if into == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

func pathEscape(value string) string { return url.PathEscape(value) }
