package ui

import (
	"strconv"
	"time"
)

// French date and time formatting, shared by every screen.
//
// Go has no locale support, so a French product has to carry its own month and
// weekday names. This lives here rather than in a feature because features are
// forbidden from importing each other, and three screens already need it.
//
// Times are rendered exactly as handed over: converting to the location's
// timezone is the caller's job, since only it knows which workshop the times
// belong to.

var frenchMonths = [...]string{
	"janvier", "février", "mars", "avril", "mai", "juin",
	"juillet", "août", "septembre", "octobre", "novembre", "décembre",
}

var frenchWeekdays = [...]string{
	"dimanche", "lundi", "mardi", "mercredi",
	"jeudi", "vendredi", "samedi",
}

// FormatDate writes "12 mars 2026". The zero time says so rather than
// rendering year 1.
func FormatDate(at time.Time) string {
	if at.IsZero() {
		return "Date inconnue"
	}
	return strconv.Itoa(at.Day()) + " " + frenchMonths[int(at.Month())-1] + " " + strconv.Itoa(at.Year())
}

// FormatDayHeading writes "jeudi 12 mars 2026", for a screen that is about one
// particular day and where the weekday is what a garage plans around.
func FormatDayHeading(at time.Time) string {
	if at.IsZero() {
		return "Date inconnue"
	}
	return frenchWeekdays[int(at.Weekday())] + " " + FormatDate(at)
}

// FormatWeekdayShort writes "lun.", "mar.", … for a compact column header
// where the full weekday name would wrap.
func FormatWeekdayShort(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	name := frenchWeekdays[int(at.Weekday())]
	return name[:3] + "."
}

// FormatTime writes "09:30", zero-padded so a column of times lines up.
func FormatTime(at time.Time) string {
	if at.IsZero() {
		return "--:--"
	}
	return pad2(at.Hour()) + ":" + pad2(at.Minute())
}

// FormatTimeRange writes "09:30 – 10:15" with an en dash, the French
// typographic convention for a span.
func FormatTimeRange(from time.Time, to time.Time) string {
	if to.IsZero() {
		return FormatTime(from)
	}
	return FormatTime(from) + " – " + FormatTime(to)
}

// FormatDuration writes a service length the way a garage says it: "1 h 30",
// "45 min", "2 h".
func FormatDuration(minutes int) string {
	if minutes <= 0 {
		return ""
	}
	hours := minutes / 60
	rest := minutes % 60
	switch {
	case hours == 0:
		return strconv.Itoa(rest) + " min"
	case rest == 0:
		return strconv.Itoa(hours) + " h"
	default:
		return strconv.Itoa(hours) + " h " + pad2(rest)
	}
}

func pad2(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}
