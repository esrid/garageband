// Package calendar defines the port used to query availability and synchronize
// garage appointments with an external calendar.
package calendar

import (
	"context"
	"time"
)

type TimeRange struct {
	Start time.Time
	End   time.Time
}

type AvailabilityRequest struct {
	CalendarIDs []string
	Start       time.Time
	End         time.Time
	TimeZone    string
}

type Event struct {
	ExternalID  string
	CalendarID  string
	Title       string
	Description string
	Location    string
	Start       time.Time
	End         time.Time
	TimeZone    string
	Private     bool
}

type Provider interface {
	Busy(ctx context.Context, request AvailabilityRequest) (map[string][]TimeRange, error)
	UpsertEvent(ctx context.Context, event Event) (Event, error)
	DeleteEvent(ctx context.Context, calendarID, externalEventID string) error
}
