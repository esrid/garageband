package ui_test

import (
	"testing"
	"time"

	"github.com/esrid/garageband/internal/ui"
)

func TestFormatDateAndDayHeadingAreFrench(t *testing.T) {
	at := time.Date(2026, 3, 12, 9, 30, 0, 0, time.UTC) // a Thursday
	if got := ui.FormatDate(at); got != "12 mars 2026" {
		t.Errorf("FormatDate() = %q", got)
	}
	if got := ui.FormatDayHeading(at); got != "jeudi 12 mars 2026" {
		t.Errorf("FormatDayHeading() = %q", got)
	}
	// August exercises the accented month, January and December the array ends.
	if got := ui.FormatDate(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)); got != "1 août 2026" {
		t.Errorf("August = %q", got)
	}
	if got := ui.FormatDate(time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)); got != "31 janvier 2026" {
		t.Errorf("January = %q", got)
	}
	if got := ui.FormatDate(time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)); got != "25 décembre 2026" {
		t.Errorf("December = %q", got)
	}
	// Sunday is index 0 in Go's Weekday; getting that wrong shifts every day.
	if got := ui.FormatDayHeading(time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)); got != "dimanche 15 mars 2026" {
		t.Errorf("Sunday = %q", got)
	}
}

func TestFormatDateHandlesTheZeroTime(t *testing.T) {
	if got := ui.FormatDate(time.Time{}); got != "Date inconnue" {
		t.Errorf("zero date = %q", got)
	}
	if got := ui.FormatDayHeading(time.Time{}); got != "Date inconnue" {
		t.Errorf("zero heading = %q", got)
	}
	if got := ui.FormatTime(time.Time{}); got != "--:--" {
		t.Errorf("zero time = %q", got)
	}
}

func TestFormatTimeIsPaddedSoColumnsAlign(t *testing.T) {
	if got := ui.FormatTime(time.Date(2026, 3, 12, 9, 5, 0, 0, time.UTC)); got != "09:05" {
		t.Errorf("FormatTime() = %q", got)
	}
	if got := ui.FormatTime(time.Date(2026, 3, 12, 14, 30, 0, 0, time.UTC)); got != "14:30" {
		t.Errorf("FormatTime() = %q", got)
	}
	from := time.Date(2026, 3, 12, 9, 30, 0, 0, time.UTC)
	to := time.Date(2026, 3, 12, 10, 15, 0, 0, time.UTC)
	if got := ui.FormatTimeRange(from, to); got != "09:30 – 10:15" {
		t.Errorf("FormatTimeRange() = %q", got)
	}
	// An open-ended entry shows its start rather than a broken range.
	if got := ui.FormatTimeRange(from, time.Time{}); got != "09:30" {
		t.Errorf("open range = %q", got)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[int]string{45: "45 min", 60: "1 h", 90: "1 h 30", 125: "2 h 05", 0: "", -5: ""}
	for minutes, want := range cases {
		if got := ui.FormatDuration(minutes); got != want {
			t.Errorf("FormatDuration(%d) = %q, want %q", minutes, got, want)
		}
	}
}
