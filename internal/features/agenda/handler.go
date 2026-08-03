package agenda

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/a-h/templ"
)

const maxAppointmentNoteRunes = 2000

var uuidPattern = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

type handler struct {
	store     *Store
	principal PrincipalResolver
	calendar  CalendarConfig
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	locationID := r.URL.Query().Get(FieldLocation)
	if locationID != "" && !uuidPattern.MatchString(locationID) {
		http.NotFound(w, r)
		return
	}
	if locationID == "" {
		locationID = principal.ActiveLocationID
	}
	page, err := h.store.Day(
		r.Context(), principal.TenantID, principal.UserID,
		locationID, r.URL.Query().Get(FieldDate),
	)
	if err != nil {
		h.handleReadError(w, r, "load agenda", err)
		return
	}
	switch {
	case r.URL.Query().Get("saved") == "1":
		page.Notice = Notice{Kind: NoticeSuccess, Message: "Le rendez-vous est enregistré."}
	case r.URL.Query().Get("cancelled") == "1":
		page.Notice = Notice{Kind: NoticeSuccess, Message: "Le rendez-vous est annulé."}
	case r.URL.Query().Get("reminded") == "1":
		page.Notice = Notice{Kind: NoticeSuccess, Message: "Le rappel est enregistré."}
	case r.URL.Query().Get("confirmed") == "1":
		page.Notice = Notice{Kind: NoticeSuccess, Message: "Le rendez-vous est confirmé."}
	}
	h.render(w, r, Show(page), http.StatusOK)
}

func (h *handler) week(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	locationID := r.URL.Query().Get(FieldLocation)
	if locationID != "" && !uuidPattern.MatchString(locationID) {
		http.NotFound(w, r)
		return
	}
	if locationID == "" {
		locationID = principal.ActiveLocationID
	}
	page, err := h.store.Week(
		r.Context(), principal.TenantID, principal.UserID,
		locationID, r.URL.Query().Get(FieldDate),
	)
	if err != nil {
		h.handleReadError(w, r, "load agenda week", err)
		return
	}
	switch {
	case r.URL.Query().Get("saved") == "1":
		// Shared with the day view's own "saved=1": a booking made or edited
		// from the week grid now redirects here too (see save()'s view
		// branching), so this must read as a generic confirmation, not
		// "moved" - that word is reserved for the drag-and-drop flag below.
		page.Notice = Notice{Kind: NoticeSuccess, Message: "Le rendez-vous est enregistré."}
	case r.URL.Query().Get("moved") == "1":
		page.Notice = Notice{Kind: NoticeSuccess, Message: "Le rendez-vous a été déplacé."}
	case r.URL.Query().Get("moveError") == NoticeConflict:
		page.Notice = Notice{Kind: NoticeConflict, Message: "Ce créneau est déjà pris ; le rendez-vous n'a pas été déplacé."}
	case r.URL.Query().Get("moveError") == NoticeInvalid:
		page.Notice = Notice{Kind: NoticeInvalid, Message: "Date ou heure invalide ; le rendez-vous n'a pas été déplacé."}
	case r.URL.Query().Get("moveError") == NoticeError:
		page.Notice = Notice{Kind: NoticeError, Message: "Le déplacement a échoué ; réessayez."}
	}
	h.render(w, r, ShowWeek(page), http.StatusOK)
}

func (h *handler) newAppointment(w http.ResponseWriter, r *http.Request) {
	h.form(w, r, "", r.URL.Query().Get(FieldCustomer), r.URL.Query().Get(FieldLocation),
		r.URL.Query().Get(FieldDate), r.URL.Query().Get(FieldStartTime),
		returnView(r.URL.Query().Get(FieldView)),
	)
}

func (h *handler) editAppointment(w http.ResponseWriter, r *http.Request) {
	appointmentID := r.PathValue("appointmentID")
	if !uuidPattern.MatchString(appointmentID) {
		http.NotFound(w, r)
		return
	}
	h.form(w, r, appointmentID, "", "", "", "", "")
}

// returnView narrows the "view" query/form value to the one screen it is
// allowed to name - same allowlist-of-one spirit as every other loosely
// typed input this handler parses.
func returnView(value string) string {
	if value == "week" {
		return "week"
	}
	return ""
}

func (h *handler) form(
	w http.ResponseWriter,
	r *http.Request,
	appointmentID string,
	customerID string,
	locationID string,
	date string,
	startTime string,
	view string,
) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	if (customerID != "" && !uuidPattern.MatchString(customerID)) ||
		(locationID != "" && !uuidPattern.MatchString(locationID)) {
		http.NotFound(w, r)
		return
	}
	if locationID == "" {
		locationID = principal.ActiveLocationID
	}
	page, err := h.store.Form(
		r.Context(), principal.TenantID, principal.UserID,
		appointmentID, customerID, locationID,
	)
	if err != nil {
		h.handleReadError(w, r, "load appointment form", err)
		return
	}
	// A click-to-create slot in the week grid already knows when; only a new
	// booking (never an edit) takes this prefill, and only when it parses.
	if appointmentID == "" {
		if _, err := time.Parse(DateLayout, date); err == nil {
			page.Values.Date = date
		}
		if _, err := time.Parse("15:04", startTime); err == nil {
			page.Values.StartTime = startTime
		}
		page.ReturnView = view
		// Nobody to book for yet: skip straight to the customer picker
		// instead of showing an intermediate "choose a client" screen with
		// nothing else to do on it. The Form template still renders that
		// card as a fallback for a tampered/incomplete POST resubmission
		// (see renderSubmitted), where redirecting mid-submit would be the
		// wrong move - but a fresh GET never needs to show it.
		if !page.Ready() {
			http.Redirect(w, r, page.CustomerPickerPath(), http.StatusSeeOther)
			return
		}
	}
	h.render(w, r, Form(page), http.StatusOK)
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	h.save(w, r, "")
}

func (h *handler) availability(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulaire invalide.", http.StatusBadRequest)
		return
	}
	input := SaveInput{
		LocationID:  strings.TrimSpace(r.FormValue(FieldLocation)),
		CustomerID:  strings.TrimSpace(r.FormValue(FieldCustomer)),
		VehicleID:   strings.TrimSpace(r.FormValue(FieldVehicle)),
		ServiceID:   strings.TrimSpace(r.FormValue(FieldService)),
		ResourceIDs: formResourceIDs(r),
		Date:        strings.TrimSpace(r.FormValue(FieldDate)),
		StartTime:   strings.TrimSpace(r.FormValue(FieldStartTime)),
		Note:        strings.TrimSpace(r.FormValue(FieldNote)),
	}
	input.ResourceID = firstResourceID(input.ResourceIDs)
	if !uuidPattern.MatchString(input.LocationID) ||
		(input.CustomerID != "" && !uuidPattern.MatchString(input.CustomerID)) {
		http.NotFound(w, r)
		return
	}
	page, err := h.store.Form(r.Context(), principal.TenantID, principal.UserID, "", input.CustomerID, input.LocationID)
	if err != nil {
		h.handleReadError(w, r, "load availability form", err)
		return
	}
	page.Values = FormValues{Date: input.Date, StartTime: input.StartTime, VehicleID: input.VehicleID, ServiceID: input.ServiceID, ResourceID: input.ResourceID, ResourceIDs: input.ResourceIDs, Note: input.Note}
	page.ReturnView = returnView(r.FormValue(FieldView))
	page.AvailabilitySearched = true
	if !uuidPattern.MatchString(input.ServiceID) ||
		(len(input.ResourceIDs) != 0 && !validResourceIDs(input.ResourceIDs)) || input.Date == "" {
		page.FieldErrors = map[string]string{}
		if !uuidPattern.MatchString(input.ServiceID) {
			page.FieldErrors[FieldService] = "Choisissez une prestation."
		}
		if len(input.ResourceIDs) != 0 && !validResourceIDs(input.ResourceIDs) {
			page.FieldErrors[FieldResource] = "Choisissez uniquement des ressources valides."
		}
		if input.Date == "" {
			page.FieldErrors[FieldDate] = "Choisissez une date."
		}
		page.Notice = Notice{Kind: NoticeInvalid, Message: "Choisissez une date et une prestation valides."}
		h.render(w, r, Form(page), http.StatusUnprocessableEntity)
		return
	}
	availability, err := h.store.Availability(r.Context(), principal.TenantID, principal.UserID, input.LocationID, input.ServiceID, input.ResourceIDs, input.Date)
	if err != nil {
		var fieldError *FieldError
		if errors.As(err, &fieldError) {
			page.FieldErrors = map[string]string{fieldError.Field: fieldError.Message}
			page.Notice = Notice{Kind: NoticeInvalid, Message: "La disponibilité n’a pas pu être calculée."}
			h.render(w, r, Form(page), http.StatusUnprocessableEntity)
			return
		}
		h.handleReadError(w, r, "search availability", err)
		return
	}
	page.ScheduleConfigured = availability.ScheduleConfigured
	page.OpenThisDay = availability.OpenThisDay
	page.AutomaticallyAllocated = availability.AutoAllocated
	for _, available := range availability.Slots {
		page.AvailableSlots = append(page.AvailableSlots, Slot{
			Value: available.StartsAt.Format("15:04"),
			Label: available.StartsAt.Format("15:04") + "–" + available.EndsAt.Format("15:04"),
		})
	}
	h.render(w, r, Form(page), http.StatusOK)
}

func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	appointmentID := r.PathValue("appointmentID")
	if !uuidPattern.MatchString(appointmentID) {
		http.NotFound(w, r)
		return
	}
	h.save(w, r, appointmentID)
}

func (h *handler) save(w http.ResponseWriter, r *http.Request, appointmentID string) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulaire invalide.", http.StatusBadRequest)
		return
	}
	input := SaveInput{
		LocationID:  strings.TrimSpace(r.FormValue(FieldLocation)),
		CustomerID:  strings.TrimSpace(r.FormValue(FieldCustomer)),
		VehicleID:   strings.TrimSpace(r.FormValue(FieldVehicle)),
		ServiceID:   strings.TrimSpace(r.FormValue(FieldService)),
		ResourceIDs: formResourceIDs(r),
		Date:        strings.TrimSpace(r.FormValue(FieldDate)),
		StartTime:   strings.TrimSpace(r.FormValue(FieldStartTime)),
		Note:        strings.TrimSpace(r.FormValue(FieldNote)),
	}
	input.ResourceID = firstResourceID(input.ResourceIDs)
	view := returnView(r.FormValue(FieldView))
	fieldErrors := validateInput(input)
	if len(fieldErrors) != 0 {
		h.renderSubmitted(w, r, principal, appointmentID, input, view, fieldErrors,
			Notice{Kind: NoticeInvalid, Message: "Corrigez les champs signalés."},
			http.StatusUnprocessableEntity,
		)
		return
	}
	id, date, err := h.store.Save(
		r.Context(), principal.TenantID, principal.UserID, appointmentID, input,
	)
	if err != nil {
		var fieldError *FieldError
		var conflict *ConflictError
		switch {
		case errors.As(err, &fieldError):
			h.renderSubmitted(w, r, principal, appointmentID, input, view,
				map[string]string{fieldError.Field: fieldError.Message},
				Notice{Kind: NoticeInvalid, Message: "Corrigez les champs signalés."},
				http.StatusUnprocessableEntity,
			)
		case errors.As(err, &conflict):
			h.renderSubmitted(w, r, principal, appointmentID, input, view, nil,
				Notice{Kind: NoticeConflict, Message: conflictMessage(conflict)},
				http.StatusConflict,
			)
		case errors.Is(err, sql.ErrNoRows):
			http.NotFound(w, r)
		default:
			h.fail(w, "save appointment", err)
		}
		return
	}
	if pushErr := h.store.SyncAppointmentCalendar(
		r.Context(), principal.TenantID, principal.UserID, id, h.calendar,
	); pushErr != nil {
		slog.Error("sync appointment calendar", "err", pushErr)
	}
	http.Redirect(w, r,
		agendaViewPath(view, input.LocationID, date)+"&saved=1",
		http.StatusSeeOther,
	)
}

// move is the week grid's drag-and-drop target: only date/start_time change,
// everything else about the appointment stays exactly as it was.
func (h *handler) move(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	appointmentID := r.PathValue("appointmentID")
	if !uuidPattern.MatchString(appointmentID) {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulaire invalide.", http.StatusBadRequest)
		return
	}
	locationID := strings.TrimSpace(r.FormValue(FieldLocation))
	date := strings.TrimSpace(r.FormValue(FieldDate))
	startTime := strings.TrimSpace(r.FormValue(FieldStartTime))

	newDate, err := h.store.Reschedule(
		r.Context(), principal.TenantID, principal.UserID, appointmentID, date, startTime,
	)
	if err != nil {
		var fieldError *FieldError
		var conflict *ConflictError
		kind := NoticeError
		switch {
		case errors.As(err, &fieldError):
			kind = NoticeInvalid
		case errors.As(err, &conflict):
			kind = NoticeConflict
		case errors.Is(err, sql.ErrNoRows):
			http.NotFound(w, r)
			return
		default:
			h.fail(w, "move appointment", err)
			return
		}
		// Nothing changed server-side on any of these errors: reloading the
		// week the drag started in is the "snap back" a failed drop needs.
		http.Redirect(w, r, fmt.Sprintf(
			"/agenda/week?%s=%s&%s=%s&moveError=%s",
			FieldLocation, locationID, FieldDate, date, kind,
		), http.StatusSeeOther)
		return
	}
	if pushErr := h.store.SyncAppointmentCalendar(
		r.Context(), principal.TenantID, principal.UserID, appointmentID, h.calendar,
	); pushErr != nil {
		slog.Error("sync appointment calendar", "err", pushErr)
	}
	http.Redirect(w, r, fmt.Sprintf(
		"/agenda/week?%s=%s&%s=%s&moved=1", FieldLocation, locationID, FieldDate, newDate,
	), http.StatusSeeOther)
}

// remind records the outcome of a staffer's manual call and drops the
// appointment off the reminder queue either way. It does not place a call or
// send anything itself - there is no telephony integration wired up (see
// internal/platform/telephony).
func (h *handler) remind(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	appointmentID := r.PathValue("appointmentID")
	if !uuidPattern.MatchString(appointmentID) {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulaire invalide.", http.StatusBadRequest)
		return
	}
	confirmed := r.FormValue(FieldOutcome) == OutcomeConfirmed
	date, locationID, err := h.store.MarkReminded(
		r.Context(), principal.TenantID, principal.UserID, appointmentID, confirmed,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.fail(w, "mark appointment reminded", err)
		return
	}
	remindedFlag := "reminded=1"
	if confirmed {
		remindedFlag = "confirmed=1"
	}
	http.Redirect(w, r, fmt.Sprintf(
		"/agenda?%s=%s&%s=%s&%s", FieldLocation, locationID, FieldDate, date, remindedFlag,
	), http.StatusSeeOther)
}

func (h *handler) cancel(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	appointmentID := r.PathValue("appointmentID")
	if !uuidPattern.MatchString(appointmentID) {
		http.NotFound(w, r)
		return
	}
	date, locationID, err := h.store.Cancel(
		r.Context(), principal.TenantID, principal.UserID, appointmentID,
	)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.NotFound(w, r)
		case errors.Is(err, ErrForbidden):
			http.Error(w, "Ce rendez-vous ne peut pas être annulé.", http.StatusForbidden)
		default:
			h.fail(w, "cancel appointment", err)
		}
		return
	}
	if pushErr := h.store.RemoveAppointmentCalendarEvent(
		r.Context(), principal.TenantID, principal.UserID, appointmentID, h.calendar,
	); pushErr != nil {
		slog.Error("remove appointment calendar event", "err", pushErr)
	}
	http.Redirect(w, r, fmt.Sprintf(
		"/agenda?%s=%s&%s=%s&cancelled=1",
		FieldLocation, locationID, FieldDate, date,
	), http.StatusSeeOther)
}

func (h *handler) renderSubmitted(
	w http.ResponseWriter,
	r *http.Request,
	principal Principal,
	appointmentID string,
	input SaveInput,
	view string,
	fieldErrors map[string]string,
	notice Notice,
	status int,
) {
	page, err := h.store.Form(
		r.Context(), principal.TenantID, principal.UserID,
		appointmentID, input.CustomerID, input.LocationID,
	)
	if err != nil {
		h.handleReadError(w, r, "reload appointment form", err)
		return
	}
	page.Values = FormValues{
		Date: input.Date, StartTime: input.StartTime, VehicleID: input.VehicleID,
		ServiceID: input.ServiceID, ResourceID: input.ResourceID,
		ResourceIDs: input.ResourceIDs, Note: input.Note,
	}
	page.ReturnView = view
	page.FieldErrors = fieldErrors
	page.Notice = notice
	h.render(w, r, Form(page), status)
}

func validateInput(input SaveInput) map[string]string {
	errorsByField := make(map[string]string)
	for field, value := range map[string]string{
		FieldLocation: input.LocationID,
		FieldCustomer: input.CustomerID,
		FieldVehicle:  input.VehicleID,
		FieldService:  input.ServiceID,
	} {
		if !uuidPattern.MatchString(value) {
			errorsByField[field] = "Choisissez une valeur valide."
		}
	}
	if len(input.ResourceIDs) != 0 {
		for _, resourceID := range input.ResourceIDs {
			if !uuidPattern.MatchString(resourceID) {
				errorsByField[FieldResource] = "Choisissez uniquement des ressources valides."
				break
			}
		}
	}
	if input.Date == "" {
		errorsByField[FieldDate] = "Choisissez une date."
	}
	if input.StartTime == "" {
		errorsByField[FieldStartTime] = "Choisissez une heure de début."
	}
	if utf8.RuneCountInString(input.Note) > maxAppointmentNoteRunes {
		errorsByField[FieldNote] = "La note est trop longue."
	}
	return errorsByField
}

func formResourceIDs(r *http.Request) []string {
	seen := make(map[string]struct{})
	var resourceIDs []string
	for _, value := range r.Form[FieldResource] {
		resourceID := strings.TrimSpace(value)
		if resourceID == "" {
			continue
		}
		if _, duplicate := seen[resourceID]; duplicate {
			continue
		}
		seen[resourceID] = struct{}{}
		resourceIDs = append(resourceIDs, resourceID)
	}
	return resourceIDs
}

func firstResourceID(resourceIDs []string) string {
	if len(resourceIDs) == 0 {
		return ""
	}
	return resourceIDs[0]
}

func validResourceIDs(resourceIDs []string) bool {
	if len(resourceIDs) == 0 {
		return false
	}
	for _, resourceID := range resourceIDs {
		if !uuidPattern.MatchString(resourceID) {
			return false
		}
	}
	return true
}

func (h *handler) resolve(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	principal, ok := h.principal(r.Context())
	if !ok || principal.UserID == "" || principal.TenantID == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return Principal{}, false
	}
	return principal, true
}

func (h *handler) handleReadError(
	w http.ResponseWriter,
	r *http.Request,
	operation string,
	err error,
) {
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	h.fail(w, operation, err)
}

func (h *handler) fail(w http.ResponseWriter, operation string, err error) {
	slog.Error(operation, "err", err)
	http.Error(w, "Impossible de charger l’agenda.", http.StatusInternalServerError)
}

func (h *handler) render(
	w http.ResponseWriter,
	r *http.Request,
	component templ.Component,
	status int,
) {
	w.WriteHeader(status)
	if err := component.Render(r.Context(), w); err != nil {
		slog.Error("render agenda", "err", err)
	}
}

type Middleware func(http.Handler) http.Handler

type Principal struct {
	UserID   string
	TenantID string
	// ActiveLocationID is the session's current site (empty until one has
	// ever been picked), used as the default when a page doesn't name one
	// explicitly.
	ActiveLocationID string
}

type PrincipalResolver func(context.Context) (Principal, bool)
