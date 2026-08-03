// Package agenda renders the day agenda of a workshop and the form that books
// an appointment into it.
//
// It owns no data and talks to no database: a handler builds these view models
// from the store. The contract is written down in docs/agenda-ui-contract.md.
// The models are local on purpose — a feature never imports another feature.
//
// Every time handed to this package is already in the workshop's timezone.
// Only the handler knows which location the day belongs to, so converting is
// its job, and the views format what they are given.
package agenda

import (
	"strconv"
	"strings"
	"time"
)

// DateLayout is how a day travels in a URL and in the form: ISO, sortable, and
// the format <input type="date"> speaks natively.
const DateLayout = "2006-01-02"

// Form field names, which are also what the handler parses.
const (
	FieldLocation  = "location_id"
	FieldCustomer  = "customer_id"
	FieldDate      = "date"
	FieldStartTime = "start_time"
	FieldVehicle   = "vehicle_id"
	FieldService   = "service_id"
	FieldResource  = "resource_id"
	FieldNote      = "note"
)

// Notice kinds. The view derives the heading from the kind, so French copy
// stays in the view layer instead of leaking into handlers.
const (
	NoticeError    = "error"    // the store or an upstream service failed
	NoticeInvalid  = "invalid"  // the submitted form needs corrections
	NoticeConflict = "conflict" // the slot is already taken
	NoticeSuccess  = "success"  // the booking went through
)

// Notice is a single server-side outcome shown at the top of a screen.
type Notice struct {
	Kind    string
	Message string
}

func (n Notice) Empty() bool { return strings.TrimSpace(n.Message) == "" }

// Appointment is one booking as the agenda shows it.
type Appointment struct {
	ID           string
	StartsAt     time.Time
	EndsAt       time.Time
	CustomerID   string
	CustomerName string
	VehicleLabel string
	ServiceName  string
	ResourceName string
	Status       string // appointments.status
	Source       string // appointments.source
	Note         string
}

// Day backs the agenda screen.
type Day struct {
	Organization string
	LocationID   string
	LocationName string
	Locations    []Option
	Date         time.Time
	Appointments []Appointment
	CanManage    bool // false hides booking and editing
	Notice       Notice
}

// PreviousDate and NextDate drive the day navigation. They are computed here
// so the template never does arithmetic.
func (d Day) PreviousDate() string { return d.Date.AddDate(0, 0, -1).Format(DateLayout) }
func (d Day) NextDate() string     { return d.Date.AddDate(0, 0, 1).Format(DateLayout) }
func (d Day) DateValue() string    { return d.Date.Format(DateLayout) }

func (d Day) Path(date string) string {
	if d.LocationID == "" {
		return "/agenda?date=" + date
	}
	return "/agenda?location_id=" + d.LocationID + "&date=" + date
}

func (d Day) NewPath() string {
	if d.LocationID == "" {
		return "/agenda/new"
	}
	return "/agenda/new?location_id=" + d.LocationID
}

func (d Day) WeekViewPath() string {
	if d.LocationID == "" {
		return "/agenda/week?date=" + d.DateValue()
	}
	return "/agenda/week?location_id=" + d.LocationID + "&date=" + d.DateValue()
}

// Booked counts the appointments that still occupy the workshop. A cancelled
// or no-show entry stays visible for the record but does not fill the day.
func (d Day) Booked() int {
	count := 0
	for _, appointment := range d.Appointments {
		if appointment.Occupies() {
			count++
		}
	}
	return count
}

// Occupies reports whether this appointment still holds its slot, matching the
// statuses the database counts in its double-booking exclusion constraint.
func (a Appointment) Occupies() bool {
	switch a.Status {
	case "pending", "confirmed", "in_progress":
		return true
	}
	return false
}

// Week backs the weekly grid screen: the same appointments Day would list one
// date at a time, laid out across a Monday-to-Sunday range instead.
type Week struct {
	Organization string
	LocationID   string
	LocationName string
	Locations    []Option
	WeekStart    time.Time // Monday, in the location's timezone
	Appointments []Appointment
	CanManage    bool
	Notice       Notice
}

// weekGridDefaultStartHour and weekGridDefaultEndHour bound the grid when no
// appointment falls outside them; a booking earlier or later widens the grid
// instead of being clipped out of view.
const (
	weekGridDefaultStartHour = 7
	weekGridDefaultEndHour   = 19
	weekGridSlotMinutes      = 30
)

func (w Week) PreviousWeek() string { return w.WeekStart.AddDate(0, 0, -7).Format(DateLayout) }
func (w Week) NextWeek() string     { return w.WeekStart.AddDate(0, 0, 7).Format(DateLayout) }

func (w Week) Path(date string) string {
	if w.LocationID == "" {
		return "/agenda/week?date=" + date
	}
	return "/agenda/week?location_id=" + w.LocationID + "&date=" + date
}

func (w Week) DayViewPath() string {
	if w.LocationID == "" {
		return "/agenda"
	}
	return "/agenda?location_id=" + w.LocationID
}

func (w Week) NewPath() string {
	if w.LocationID == "" {
		return "/agenda/new"
	}
	return "/agenda/new?location_id=" + w.LocationID
}

// Days is the week's seven dates, Monday first.
func (w Week) Days() []time.Time {
	days := make([]time.Time, 7)
	for i := range days {
		days[i] = w.WeekStart.AddDate(0, 0, i)
	}
	return days
}

// AppointmentsOn returns one day's bookings, already in start-time order
// (guaranteed by the query Week.Appointments came from).
func (w Week) AppointmentsOn(day time.Time) []Appointment {
	var dayAppointments []Appointment
	for _, appointment := range w.Appointments {
		y1, m1, d1 := appointment.StartsAt.Date()
		y2, m2, d2 := day.Date()
		if y1 == y2 && m1 == m2 && d1 == d2 {
			dayAppointments = append(dayAppointments, appointment)
		}
	}
	return dayAppointments
}

// GridStartHour and GridEndHour are the grid's visible bounds: the default
// working window, widened to fit every appointment that falls outside it.
func (w Week) GridStartHour() int {
	hour := weekGridDefaultStartHour
	for _, appointment := range w.Appointments {
		if h := appointment.StartsAt.Hour(); h < hour {
			hour = h
		}
	}
	return hour
}

func (w Week) GridEndHour() int {
	hour := weekGridDefaultEndHour
	for _, appointment := range w.Appointments {
		end := appointment.EndsAt
		h := end.Hour()
		if end.Minute() > 0 {
			h++
		}
		if h > hour {
			hour = h
		}
	}
	return hour
}

// HourLabels are the grid gutter's row labels, one per hour, top to bottom.
func (w Week) HourLabels() []string {
	labels := make([]string, 0, w.GridEndHour()-w.GridStartHour())
	for hour := w.GridStartHour(); hour < w.GridEndHour(); hour++ {
		labels = append(labels, strconv.Itoa(hour)+":00")
	}
	return labels
}

// SlotCount is the grid's total number of half-hour rows.
func (w Week) SlotCount() int {
	return (w.GridEndHour() - w.GridStartHour()) * (60 / weekGridSlotMinutes)
}

// GridRowStyle is the CSS grid-row line range for one appointment, as an
// inline style value ready for a templ style attribute. Grid lines are
// 1-indexed and the header row occupies line 1, so slots start at line 2.
func (w Week) GridRowStyle(a Appointment) string {
	startMinutes := (a.StartsAt.Hour()-w.GridStartHour())*60 + a.StartsAt.Minute()
	durationMinutes := int(a.EndsAt.Sub(a.StartsAt).Minutes())
	startLine := startMinutes/weekGridSlotMinutes + 2
	span := durationMinutes / weekGridSlotMinutes
	if span < 1 {
		span = 1
	}
	return "grid-row: " + strconv.Itoa(startLine) + " / " + strconv.Itoa(startLine+span)
}

// CustomerRef is the customer a booking is for, resolved before the form opens.
type CustomerRef struct {
	ID    string
	Label string
}

func (c CustomerRef) Chosen() bool { return strings.TrimSpace(c.ID) != "" }

// Option is one entry of a select control.
type Option struct {
	Value string
	Label string
}

type Slot struct {
	Value string
	Label string
}

// FormValues holds the editable fields, keyed like the POST body.
type FormValues struct {
	Date        string // DateLayout
	StartTime   string // "15:04"
	VehicleID   string
	ServiceID   string
	ResourceID  string
	ResourceIDs []string
	Note        string
}

func (v FormValues) ResourceSelected(resourceID string) bool {
	if len(v.ResourceIDs) == 0 {
		return v.ResourceID == resourceID
	}
	for _, selectedID := range v.ResourceIDs {
		if selectedID == resourceID {
			return true
		}
	}
	return false
}

// FormPage backs the booking form; the same screen creates and edits.
type FormPage struct {
	ID                     string // empty when booking a new appointment
	Organization           string
	LocationID             string
	LocationName           string
	Locations              []Option
	Customer               CustomerRef
	Vehicles               []Option
	Services               []Option
	Resources              []Option
	AvailableSlots         []Slot
	AvailabilitySearched   bool
	AutomaticallyAllocated bool
	ScheduleConfigured     bool
	OpenThisDay            bool
	Values                 FormValues
	FieldErrors            map[string]string
	Notice                 Notice
	CanManage              bool
	// Cancellable is false for an appointment already cancelled or finished:
	// there is nothing left to call off.
	Cancellable bool
}

func (p FormPage) IsNew() bool { return strings.TrimSpace(p.ID) == "" }

func (p FormPage) Error(field string) string { return p.FieldErrors[field] }

func (p FormPage) HasError(field string) bool { return p.FieldErrors[field] != "" }

// Ready reports whether the form can be shown at all. Without a customer there
// is nobody to book for, and the screen says so instead of rendering a form
// that cannot be submitted.
func (p FormPage) Ready() bool { return p.Customer.Chosen() }

func formTitle(p FormPage) string {
	if p.IsNew() {
		return "Nouveau rendez-vous"
	}
	return "Modifier le rendez-vous"
}

func formActionPath(p FormPage) string {
	if p.IsNew() {
		return "/agenda"
	}
	return "/agenda/" + p.ID
}

func agendaPath(p FormPage) string {
	path := "/agenda"
	if p.LocationID != "" {
		path += "?location_id=" + p.LocationID
	}
	if p.Values.Date != "" {
		separator := "?"
		if p.LocationID != "" {
			separator = "&"
		}
		path += separator + "date=" + p.Values.Date
	}
	return path
}

func cancelPath(p FormPage) string { return "/agenda/" + p.ID + "/cancel" }

func appointmentPath(appointment Appointment) string { return "/agenda/" + appointment.ID }

func customerPath(appointment Appointment) string { return "/customers/" + appointment.CustomerID }

// statusLabel translates appointments.status.
func statusLabel(status string) string {
	switch status {
	case "draft":
		return "Brouillon"
	case "pending":
		return "À confirmer"
	case "confirmed":
		return "Confirmé"
	case "in_progress":
		return "En cours"
	case "completed":
		return "Terminé"
	case "cancelled":
		return "Annulé"
	case "no_show":
		return "Non venu"
	}
	return status
}

// sourceLabel says where a booking came from. "Pris par l'agent" is worth
// showing: it tells the desk nobody in the workshop typed it.
func sourceLabel(source string) string {
	switch source {
	case "agent":
		return "Pris par l'agent"
	case "calendar":
		return "Depuis le calendrier"
	case "import":
		return "Importé"
	}
	return ""
}

// bookedSummary counts the occupied slots in words, agreeing in number.
func bookedSummary(count int) string {
	if count == 0 {
		return "Aucun rendez-vous"
	}
	if count == 1 {
		return "1 rendez-vous"
	}
	return strconv.Itoa(count) + " rendez-vous"
}

func noticeTitle(kind string) string {
	switch kind {
	case NoticeSuccess:
		return "C'est enregistré"
	case NoticeConflict:
		return "Ce créneau est déjà pris"
	case NoticeInvalid:
		return "Vérifiez les informations ci-dessous"
	default:
		return "Action impossible pour le moment"
	}
}

func noticeColor(kind string) string {
	switch kind {
	case NoticeSuccess:
		return "alert-success"
	case NoticeInvalid:
		return "alert-error"
	default:
		return "alert-warning"
	}
}

// ariaInvalid renders the attribute value; "false" is the ARIA-defined way to
// say a control is currently valid.
func ariaInvalid(hasError bool) string {
	if hasError {
		return "true"
	}
	return "false"
}

// describedBy links a control to its hint and, when present, its error, so a
// screen reader announces the problem with the field.
func describedBy(field string, hasHint bool, hasError bool) string {
	ids := make([]string, 0, 2)
	if hasHint {
		ids = append(ids, field+"-hint")
	}
	if hasError {
		ids = append(ids, field+"-error")
	}
	return strings.Join(ids, " ")
}
