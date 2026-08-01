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
	}
	h.render(w, r, Show(page), http.StatusOK)
}

func (h *handler) newAppointment(w http.ResponseWriter, r *http.Request) {
	h.form(w, r, "", r.URL.Query().Get(FieldCustomer), r.URL.Query().Get(FieldLocation))
}

func (h *handler) editAppointment(w http.ResponseWriter, r *http.Request) {
	appointmentID := r.PathValue("appointmentID")
	if !uuidPattern.MatchString(appointmentID) {
		http.NotFound(w, r)
		return
	}
	h.form(w, r, appointmentID, "", "")
}

func (h *handler) form(
	w http.ResponseWriter,
	r *http.Request,
	appointmentID string,
	customerID string,
	locationID string,
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
	page, err := h.store.Form(
		r.Context(), principal.TenantID, principal.UserID,
		appointmentID, customerID, locationID,
	)
	if err != nil {
		h.handleReadError(w, r, "load appointment form", err)
		return
	}
	h.render(w, r, Form(page), http.StatusOK)
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	h.save(w, r, "")
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
		LocationID: strings.TrimSpace(r.FormValue(FieldLocation)),
		CustomerID: strings.TrimSpace(r.FormValue(FieldCustomer)),
		VehicleID:  strings.TrimSpace(r.FormValue(FieldVehicle)),
		ServiceID:  strings.TrimSpace(r.FormValue(FieldService)),
		ResourceID: strings.TrimSpace(r.FormValue(FieldResource)),
		Date:       strings.TrimSpace(r.FormValue(FieldDate)),
		StartTime:  strings.TrimSpace(r.FormValue(FieldStartTime)),
		Note:       strings.TrimSpace(r.FormValue(FieldNote)),
	}
	fieldErrors := validateInput(input)
	if len(fieldErrors) != 0 {
		h.renderSubmitted(w, r, principal, appointmentID, input, fieldErrors,
			Notice{Kind: NoticeInvalid, Message: "Corrigez les champs signalés."},
			http.StatusUnprocessableEntity,
		)
		return
	}
	date, err := h.store.Save(
		r.Context(), principal.TenantID, principal.UserID, appointmentID, input,
	)
	if err != nil {
		var fieldError *FieldError
		var conflict *ConflictError
		switch {
		case errors.As(err, &fieldError):
			h.renderSubmitted(w, r, principal, appointmentID, input,
				map[string]string{fieldError.Field: fieldError.Message},
				Notice{Kind: NoticeInvalid, Message: "Corrigez les champs signalés."},
				http.StatusUnprocessableEntity,
			)
		case errors.As(err, &conflict):
			h.renderSubmitted(w, r, principal, appointmentID, input, nil,
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
	http.Redirect(w, r, fmt.Sprintf(
		"/agenda?%s=%s&%s=%s&saved=1", FieldLocation, input.LocationID, FieldDate, date,
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
		ServiceID: input.ServiceID, ResourceID: input.ResourceID, Note: input.Note,
	}
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
		FieldResource: input.ResourceID,
	} {
		if !uuidPattern.MatchString(value) {
			errorsByField[field] = "Choisissez une valeur valide."
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
}

type PrincipalResolver func(context.Context) (Principal, bool)
