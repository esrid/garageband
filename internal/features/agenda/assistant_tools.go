package agenda

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/assistanttools"
)

const (
	ToolCheckAvailability     = "check_availability"
	ToolFindAppointments      = "find_appointments"
	ToolListBookableResources = "list_bookable_resources"
	ToolBookAppointment       = "book_appointment"
	ToolRescheduleAppointment = "reschedule_appointment"
	ToolCancelAppointment     = "cancel_appointment"
)

var checkAvailabilitySchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "service_id": {"type": "string", "description": "Service offering id to check availability for"},
    "date": {"type": "string", "description": "Date to check, YYYY-MM-DD"},
    "resource_ids": {
      "type": "array", "items": {"type": "string"},
      "description": "Bookable resource ids to check. Required, from list_bookable_resources, only when the service is not auto-allocated (see auto_allocated in the result of a first call without resource_ids)."
    }
  },
  "required": ["service_id", "date"],
  "additionalProperties": false
}`)

var listBookableResourcesSchema = json.RawMessage(`{
  "type": "object",
  "properties": {},
  "additionalProperties": false
}`)

var findAppointmentsSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "date": {"type": "string", "description": "Date to look up appointments for, YYYY-MM-DD"}
  },
  "required": ["date"],
  "additionalProperties": false
}`)

var bookAppointmentSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "customer_id": {"type": "string", "description": "Customer id the appointment is for"},
    "vehicle_id": {"type": "string", "description": "Vehicle id, must belong to the customer"},
    "service_id": {"type": "string", "description": "Service offering id"},
    "date": {"type": "string", "description": "YYYY-MM-DD"},
    "start_time": {"type": "string", "description": "HH:MM, 24h"},
    "note": {"type": "string", "description": "Optional note for the workshop"},
    "resource_ids": {
      "type": "array", "items": {"type": "string"},
      "description": "Bookable resource ids to reserve, from list_bookable_resources. Required only when check_availability reported auto_allocated: false for this service."
    }
  },
  "required": ["customer_id", "vehicle_id", "service_id", "date", "start_time"],
  "additionalProperties": false
}`)

var rescheduleAppointmentSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "appointment_id": {"type": "string", "description": "Appointment id to move"},
    "date": {"type": "string", "description": "New date, YYYY-MM-DD"},
    "start_time": {"type": "string", "description": "New start time, HH:MM, 24h"}
  },
  "required": ["appointment_id", "date", "start_time"],
  "additionalProperties": false
}`)

var cancelAppointmentSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "appointment_id": {"type": "string", "description": "Appointment id to cancel"}
  },
  "required": ["appointment_id"],
  "additionalProperties": false
}`)

type checkAvailabilityInput struct {
	ServiceID   string   `json:"service_id"`
	Date        string   `json:"date"`
	ResourceIDs []string `json:"resource_ids,omitempty"`
}

type findAppointmentsInput struct {
	Date string `json:"date"`
}

type listBookableResourcesInput struct{}

type bookAppointmentInput struct {
	CustomerID  string   `json:"customer_id"`
	VehicleID   string   `json:"vehicle_id"`
	ServiceID   string   `json:"service_id"`
	Date        string   `json:"date"`
	StartTime   string   `json:"start_time"`
	Note        string   `json:"note,omitempty"`
	ResourceIDs []string `json:"resource_ids,omitempty"`
}

type rescheduleAppointmentInput struct {
	AppointmentID string `json:"appointment_id"`
	Date          string `json:"date"`
	StartTime     string `json:"start_time"`
}

type cancelAppointmentInput struct {
	AppointmentID string `json:"appointment_id"`
}

type availabilitySlotOutput struct {
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
}

type availabilityToolOutput struct {
	Date               string                   `json:"date"`
	ScheduleConfigured bool                     `json:"schedule_configured"`
	OpenThisDay        bool                     `json:"open_this_day"`
	AutoAllocated      bool                     `json:"auto_allocated"`
	Slots              []availabilitySlotOutput `json:"slots"`
}

type appointmentOutput struct {
	ID           string `json:"id"`
	StartsAt     string `json:"starts_at"`
	EndsAt       string `json:"ends_at"`
	CustomerName string `json:"customer_name,omitempty"`
	VehicleLabel string `json:"vehicle_label,omitempty"`
	ServiceName  string `json:"service_name,omitempty"`
	ResourceName string `json:"resource_name,omitempty"`
	Status       string `json:"status"`
	Note         string `json:"note,omitempty"`
}

type appointmentWriteOutput struct {
	ID   string `json:"id"`
	Date string `json:"date"`
}

type bookableResourceOutput struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func (s *Store) Definitions() []assistanttools.Definition {
	return []assistanttools.Definition{
		{
			Name:        ToolCheckAvailability,
			Description: "Check open appointment slots for a service at the scoped location on a given date.",
			InputSchema: checkAvailabilitySchema, Consequence: assistanttools.ConsequenceRead,
		},
		{
			Name:        ToolFindAppointments,
			Description: "List the scoped location's appointments on a given date.",
			InputSchema: findAppointmentsSchema, Consequence: assistanttools.ConsequenceRead,
		},
		{
			Name:        ToolListBookableResources,
			Description: "List the scoped location's bookable resources (bays, technicians, equipment, calendars). Call this before booking a service that check_availability reported as not auto-allocated.",
			InputSchema: listBookableResourcesSchema, Consequence: assistanttools.ConsequenceRead,
		},
		{
			Name:                 ToolBookAppointment,
			Description:          "Propose booking a new appointment for a customer's vehicle at the scoped location.",
			InputSchema:          bookAppointmentSchema,
			Consequence:          assistanttools.ConsequenceWrite,
			ConfirmationRequired: true,
		},
		{
			Name:                 ToolRescheduleAppointment,
			Description:          "Propose moving an existing appointment to a new date and time.",
			InputSchema:          rescheduleAppointmentSchema,
			Consequence:          assistanttools.ConsequenceWrite,
			ConfirmationRequired: true,
		},
		{
			Name:                 ToolCancelAppointment,
			Description:          "Propose cancelling an existing appointment.",
			InputSchema:          cancelAppointmentSchema,
			Consequence:          assistanttools.ConsequenceWrite,
			ConfirmationRequired: true,
		},
	}
}

func (s *Store) Preview(
	ctx context.Context,
	scope assistanttools.Scope,
	name string,
	input json.RawMessage,
) (assistanttools.Preview, error) {
	switch name {
	case ToolCheckAvailability:
		parsed, canonical, err := parseCheckAvailabilityInput(input)
		if err != nil {
			return assistanttools.Preview{}, err
		}
		result, err := s.Availability(
			ctx, scope.TenantID, scope.UserID, scope.LocationID,
			parsed.ServiceID, parsed.ResourceIDs, parsed.Date,
		)
		if err != nil {
			return assistanttools.Preview{}, mapAvailabilityError(err)
		}
		return assistanttools.Preview{
			Summary: availabilitySummary(parsed.Date, result), Input: canonical,
		}, nil
	case ToolFindAppointments:
		parsed, canonical, err := parseFindAppointmentsInput(input)
		if err != nil {
			return assistanttools.Preview{}, err
		}
		day, err := s.Day(ctx, scope.TenantID, scope.UserID, scope.LocationID, parsed.Date)
		if err != nil {
			return assistanttools.Preview{}, mapAssistantToolFieldError(err)
		}
		return assistanttools.Preview{
			Summary: appointmentsSummary(parsed.Date, day.Appointments), Input: canonical,
		}, nil
	case ToolListBookableResources:
		if err := parseListBookableResourcesInput(input); err != nil {
			return assistanttools.Preview{}, err
		}
		resources, err := s.listBookableResources(ctx, scope)
		if err != nil {
			return assistanttools.Preview{}, err
		}
		return assistanttools.Preview{
			Summary: bookableResourcesSummary(resources), Input: json.RawMessage(`{}`),
		}, nil
	case ToolBookAppointment:
		parsed, canonical, err := parseBookAppointmentInput(input)
		if err != nil {
			return assistanttools.Preview{}, err
		}
		summary, err := bookAppointmentSummary(ctx, s, scope, parsed)
		if err != nil {
			return assistanttools.Preview{}, err
		}
		return assistanttools.Preview{Summary: summary, Input: canonical}, nil
	case ToolRescheduleAppointment:
		parsed, canonical, err := parseRescheduleAppointmentInput(input)
		if err != nil {
			return assistanttools.Preview{}, err
		}
		form, err := s.Form(ctx, scope.TenantID, scope.UserID, parsed.AppointmentID, "", scope.LocationID)
		if err != nil {
			return assistanttools.Preview{}, mapAgendaWriteError(err)
		}
		if !form.Cancellable {
			return assistanttools.Preview{}, &assistanttools.ToolError{
				Code: "conflict", Field: FieldDate,
				Message: "Ce rendez-vous est déjà annulé ou terminé et ne peut plus être déplacé.",
			}
		}
		return assistanttools.Preview{
			Summary: rescheduleSummary(form, parsed.Date, parsed.StartTime), Input: canonical,
		}, nil
	case ToolCancelAppointment:
		parsed, canonical, err := parseCancelAppointmentInput(input)
		if err != nil {
			return assistanttools.Preview{}, err
		}
		form, err := s.Form(ctx, scope.TenantID, scope.UserID, parsed.AppointmentID, "", scope.LocationID)
		if err != nil {
			return assistanttools.Preview{}, mapAgendaWriteError(err)
		}
		return assistanttools.Preview{Summary: cancelSummary(form), Input: canonical}, nil
	default:
		return assistanttools.Preview{}, assistanttools.ErrUnknownTool
	}
}

func (s *Store) Execute(
	ctx context.Context,
	scope assistanttools.Scope,
	name string,
	input json.RawMessage,
) (assistanttools.Result, error) {
	switch name {
	case ToolCheckAvailability:
		parsed, _, err := parseCheckAvailabilityInput(input)
		if err != nil {
			return assistanttools.Result{}, err
		}
		result, err := s.Availability(
			ctx, scope.TenantID, scope.UserID, scope.LocationID,
			parsed.ServiceID, parsed.ResourceIDs, parsed.Date,
		)
		if err != nil {
			return assistanttools.Result{}, mapAvailabilityError(err)
		}
		output, err := json.Marshal(newAvailabilityToolOutput(parsed.Date, result))
		if err != nil {
			return assistanttools.Result{}, err
		}
		return assistanttools.Result{
			Summary: availabilitySummary(parsed.Date, result), Output: output,
		}, nil
	case ToolFindAppointments:
		parsed, _, err := parseFindAppointmentsInput(input)
		if err != nil {
			return assistanttools.Result{}, err
		}
		day, err := s.Day(ctx, scope.TenantID, scope.UserID, scope.LocationID, parsed.Date)
		if err != nil {
			return assistanttools.Result{}, mapAssistantToolFieldError(err)
		}
		appointments := make([]appointmentOutput, 0, len(day.Appointments))
		affected := make([]assistanttools.AffectedRecord, 0, len(day.Appointments))
		for _, appointment := range day.Appointments {
			appointments = append(appointments, appointmentOutput{
				ID: appointment.ID, StartsAt: appointment.StartsAt.Format(time.RFC3339),
				EndsAt:       appointment.EndsAt.Format(time.RFC3339),
				CustomerName: appointment.CustomerName, VehicleLabel: appointment.VehicleLabel,
				ServiceName: appointment.ServiceName, ResourceName: appointment.ResourceName,
				Status: appointment.Status, Note: appointment.Note,
			})
			affected = append(affected, assistanttools.AffectedRecord{
				Kind: "appointment", ID: appointment.ID,
			})
		}
		output, err := json.Marshal(struct {
			Date         string              `json:"date"`
			Appointments []appointmentOutput `json:"appointments"`
		}{Date: parsed.Date, Appointments: appointments})
		if err != nil {
			return assistanttools.Result{}, err
		}
		return assistanttools.Result{
			Summary: appointmentsSummary(parsed.Date, day.Appointments),
			Output:  output, AffectedRecords: affected,
		}, nil
	case ToolListBookableResources:
		resources, err := s.listBookableResources(ctx, scope)
		if err != nil {
			return assistanttools.Result{}, err
		}
		output, err := json.Marshal(struct {
			Resources []bookableResourceOutput `json:"resources"`
		}{Resources: resources})
		if err != nil {
			return assistanttools.Result{}, err
		}
		return assistanttools.Result{
			Summary: bookableResourcesSummary(resources), Output: output,
		}, nil
	case ToolBookAppointment:
		parsed, _, err := parseBookAppointmentInput(input)
		if err != nil {
			return assistanttools.Result{}, err
		}
		output, affected, err := assistanttools.WithReceipt(ctx, s.db, scope, ToolBookAppointment, func(tx pgx.Tx) (json.RawMessage, []assistanttools.AffectedRecord, error) {
			id, _, err := saveAppointment(ctx, tx, scope.TenantID, "", SaveInput{
				LocationID: scope.LocationID, CustomerID: parsed.CustomerID,
				VehicleID: parsed.VehicleID, ServiceID: parsed.ServiceID,
				ResourceIDs: parsed.ResourceIDs,
				Date:        parsed.Date, StartTime: parsed.StartTime, Note: parsed.Note,
			})
			if err != nil {
				return nil, nil, mapAgendaWriteError(err)
			}
			return appointmentWriteResult(id, parsed.Date)
		})
		if err != nil {
			return assistanttools.Result{}, err
		}
		// Same post-commit reconciliation the week grid gets. Reading the id
		// back from the receipt rather than the closure covers the retry that
		// short-circuits: a second attempt re-pushes instead of skipping.
		s.reconcileAffectedCalendar(ctx, scope, affected)
		return assistanttools.Result{
			Summary: "Rendez-vous réservé le " + parsed.Date + " à " + parsed.StartTime + ".",
			Output:  output, AffectedRecords: affected,
		}, nil
	case ToolRescheduleAppointment:
		parsed, _, err := parseRescheduleAppointmentInput(input)
		if err != nil {
			return assistanttools.Result{}, err
		}
		form, err := s.Form(ctx, scope.TenantID, scope.UserID, parsed.AppointmentID, "", scope.LocationID)
		if err != nil {
			return assistanttools.Result{}, mapAgendaWriteError(err)
		}
		output, affected, err := assistanttools.WithReceipt(ctx, s.db, scope, ToolRescheduleAppointment, func(tx pgx.Tx) (json.RawMessage, []assistanttools.AffectedRecord, error) {
			id, _, err := saveAppointment(ctx, tx, scope.TenantID, parsed.AppointmentID, SaveInput{
				LocationID: form.LocationID, CustomerID: form.Customer.ID,
				VehicleID: form.Values.VehicleID, ServiceID: form.Values.ServiceID,
				ResourceID: form.Values.ResourceID, ResourceIDs: form.Values.ResourceIDs,
				Date: parsed.Date, StartTime: parsed.StartTime, Note: form.Values.Note,
			})
			if err != nil {
				return nil, nil, mapAgendaWriteError(err)
			}
			return appointmentWriteResult(id, parsed.Date)
		})
		if err != nil {
			return assistanttools.Result{}, err
		}
		s.reconcileCalendar(ctx, scope.TenantID, scope.UserID, parsed.AppointmentID)
		return assistanttools.Result{
			Summary: "Rendez-vous déplacé au " + parsed.Date + " à " + parsed.StartTime + ".",
			Output:  output, AffectedRecords: affected,
		}, nil
	case ToolCancelAppointment:
		parsed, _, err := parseCancelAppointmentInput(input)
		if err != nil {
			return assistanttools.Result{}, err
		}
		output, affected, err := assistanttools.WithReceipt(ctx, s.db, scope, ToolCancelAppointment, func(tx pgx.Tx) (json.RawMessage, []assistanttools.AffectedRecord, error) {
			date, _, err := cancelAppointment(ctx, tx, scope.TenantID, parsed.AppointmentID)
			if err != nil {
				return nil, nil, mapAgendaWriteError(err)
			}
			return appointmentWriteResult(parsed.AppointmentID, date)
		})
		if err != nil {
			return assistanttools.Result{}, err
		}
		s.reconcileCalendarRemoval(ctx, scope.TenantID, scope.UserID, parsed.AppointmentID)
		return assistanttools.Result{
			Summary: "Rendez-vous annulé.", Output: output, AffectedRecords: affected,
		}, nil
	default:
		return assistanttools.Result{}, assistanttools.ErrUnknownTool
	}
}

// reconcileAffectedCalendar pushes every appointment a tool call reports as
// written, which is the one place the created id is known on both the first
// attempt and a replayed one.
func (s *Store) reconcileAffectedCalendar(
	ctx context.Context, scope assistanttools.Scope, affected []assistanttools.AffectedRecord,
) {
	for _, record := range affected {
		if record.Kind == "appointment" && record.ID != "" {
			s.reconcileCalendar(ctx, scope.TenantID, scope.UserID, record.ID)
		}
	}
}

func appointmentWriteResult(id string, date string) (json.RawMessage, []assistanttools.AffectedRecord, error) {
	output, err := json.Marshal(appointmentWriteOutput{ID: id, Date: date})
	if err != nil {
		return nil, nil, err
	}
	return output, []assistanttools.AffectedRecord{{Kind: "appointment", ID: id}}, nil
}

func newAvailabilityToolOutput(date string, result AvailabilityResult) availabilityToolOutput {
	slots := make([]availabilitySlotOutput, 0, len(result.Slots))
	for _, slot := range result.Slots {
		slots = append(slots, availabilitySlotOutput{
			StartsAt: slot.StartsAt.Format(time.RFC3339),
			EndsAt:   slot.EndsAt.Format(time.RFC3339),
		})
	}
	return availabilityToolOutput{
		Date: date, ScheduleConfigured: result.ScheduleConfigured,
		OpenThisDay: result.OpenThisDay, AutoAllocated: result.AutoAllocated, Slots: slots,
	}
}

func availabilitySummary(date string, result AvailabilityResult) string {
	switch {
	case !result.ScheduleConfigured:
		return "Aucun horaire n’est configuré pour ce site."
	case !result.OpenThisDay:
		return "L’atelier est fermé le " + date + "."
	case len(result.Slots) == 0:
		return "Aucun créneau disponible le " + date + "."
	default:
		return strconv.Itoa(len(result.Slots)) + " créneaux disponibles le " + date + "."
	}
}

func appointmentsSummary(date string, appointments []Appointment) string {
	if len(appointments) == 0 {
		return "Aucun rendez-vous le " + date + "."
	}
	return strconv.Itoa(len(appointments)) + " rendez-vous le " + date + "."
}

// bookAppointmentSummary resolves the customer/vehicle/service names for the
// preview without booking anything — Form already loads exactly this for the
// human booking screen, so this reuses it instead of adding new queries.
func bookAppointmentSummary(
	ctx context.Context,
	s *Store,
	scope assistanttools.Scope,
	parsed bookAppointmentInput,
) (string, error) {
	form, err := s.Form(ctx, scope.TenantID, scope.UserID, "", parsed.CustomerID, scope.LocationID)
	if err != nil {
		return "", mapAgendaWriteError(err)
	}
	if !form.Customer.Chosen() {
		return "", &assistanttools.ToolError{
			Code: "invalid_arguments", Field: FieldCustomer, Message: "Client introuvable à cet établissement.",
		}
	}
	serviceName := optionLabel(form.Services, parsed.ServiceID)
	if serviceName == "" {
		return "", &assistanttools.ToolError{
			Code: "invalid_arguments", Field: FieldService,
			Message: "Choisissez une prestation disponible dans cet établissement.",
		}
	}
	vehicleName := optionLabel(form.Vehicles, parsed.VehicleID)
	if vehicleName == "" {
		return "", &assistanttools.ToolError{
			Code: "invalid_arguments", Field: FieldVehicle,
			Message: "Choisissez un véhicule appartenant à ce client.",
		}
	}
	summary := "Réserver « " + serviceName + " » pour " + form.Customer.Label +
		" (" + vehicleName + ") le " + parsed.Date + " à " + parsed.StartTime + "."
	if len(parsed.ResourceIDs) > 0 {
		names := make([]string, 0, len(parsed.ResourceIDs))
		for _, id := range parsed.ResourceIDs {
			if name := optionLabel(form.Resources, id); name != "" {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			summary += " Ressources : " + strings.Join(names, ", ") + "."
		}
	}
	if parsed.Note != "" {
		summary += " Note : " + parsed.Note
	}
	return summary, nil
}

// listBookableResources reuses the same rows the human booking form offers
// (loadResourceOptions), plus kind, so the model can tell a bay from a
// technician instead of picking a bare id.
func (s *Store) listBookableResources(
	ctx context.Context, scope assistanttools.Scope,
) (resources []bookableResourceOutput, err error) {
	err = s.db.WithinTenantUser(ctx, scope.TenantID, scope.UserID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id::text, name, kind
			FROM bookable_resources
			WHERE tenant_id = $1 AND location_id = $2 AND active
			ORDER BY kind, name, id`, scope.TenantID, scope.LocationID)
		if err != nil {
			return err
		}
		resources, err = pgx.CollectRows(rows, pgx.RowToStructByPos[bookableResourceOutput])
		return err
	})
	return resources, err
}

func bookableResourcesSummary(resources []bookableResourceOutput) string {
	if len(resources) == 0 {
		return "Aucune ressource réservable n’est configurée pour ce site."
	}
	return strconv.Itoa(len(resources)) + " ressources réservables à ce site."
}

func optionLabel(options []Option, value string) string {
	for _, option := range options {
		if option.Value == value {
			return option.Label
		}
	}
	return ""
}

func rescheduleSummary(form FormPage, newDate string, newStartTime string) string {
	return "Déplacer le rendez-vous de " + form.Customer.Label + " du " +
		form.Values.Date + " " + form.Values.StartTime + " au " +
		newDate + " " + newStartTime + "."
}

func cancelSummary(form FormPage) string {
	if !form.Cancellable {
		return "Le rendez-vous de " + form.Customer.Label + " du " + form.Values.Date +
			" " + form.Values.StartTime + " est déjà annulé ou terminé."
	}
	return "Annuler le rendez-vous de " + form.Customer.Label + " du " +
		form.Values.Date + " à " + form.Values.StartTime + "."
}

func parseCheckAvailabilityInput(input json.RawMessage) (checkAvailabilityInput, json.RawMessage, error) {
	var parsed checkAvailabilityInput
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return parsed, nil, agendaToolInputError(FieldService, "Les arguments proposés sont invalides.", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return parsed, nil, agendaToolInputError(FieldService, "Les arguments proposés sont invalides.", err)
	}
	parsed.ServiceID = strings.TrimSpace(parsed.ServiceID)
	parsed.Date = strings.TrimSpace(parsed.Date)
	if parsed.ServiceID == "" {
		return parsed, nil, agendaToolInputError(FieldService, "Précisez la prestation à vérifier.", nil)
	}
	if _, err := time.Parse(DateLayout, parsed.Date); err != nil {
		return parsed, nil, agendaToolInputError(FieldDate, "Précisez une date valide (AAAA-MM-JJ).", nil)
	}
	parsed.ResourceIDs = trimResourceIDs(parsed.ResourceIDs)
	canonical, err := json.Marshal(parsed)
	return parsed, canonical, err
}

func parseFindAppointmentsInput(input json.RawMessage) (findAppointmentsInput, json.RawMessage, error) {
	var parsed findAppointmentsInput
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return parsed, nil, agendaToolInputError(FieldDate, "Les arguments proposés sont invalides.", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return parsed, nil, agendaToolInputError(FieldDate, "Les arguments proposés sont invalides.", err)
	}
	parsed.Date = strings.TrimSpace(parsed.Date)
	if _, err := time.Parse(DateLayout, parsed.Date); err != nil {
		return parsed, nil, agendaToolInputError(FieldDate, "Précisez une date valide (AAAA-MM-JJ).", nil)
	}
	canonical, err := json.Marshal(parsed)
	return parsed, canonical, err
}

func parseBookAppointmentInput(input json.RawMessage) (bookAppointmentInput, json.RawMessage, error) {
	var parsed bookAppointmentInput
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return parsed, nil, agendaToolInputError(FieldDate, "Les arguments proposés sont invalides.", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return parsed, nil, agendaToolInputError(FieldDate, "Les arguments proposés sont invalides.", err)
	}
	parsed.CustomerID = strings.TrimSpace(parsed.CustomerID)
	parsed.VehicleID = strings.TrimSpace(parsed.VehicleID)
	parsed.ServiceID = strings.TrimSpace(parsed.ServiceID)
	parsed.Date = strings.TrimSpace(parsed.Date)
	parsed.StartTime = strings.TrimSpace(parsed.StartTime)
	switch {
	case parsed.CustomerID == "":
		return parsed, nil, agendaToolInputError(FieldCustomer, "Précisez le client.", nil)
	case parsed.VehicleID == "":
		return parsed, nil, agendaToolInputError(FieldVehicle, "Précisez le véhicule.", nil)
	case parsed.ServiceID == "":
		return parsed, nil, agendaToolInputError(FieldService, "Précisez la prestation.", nil)
	}
	if _, err := time.Parse(DateLayout, parsed.Date); err != nil {
		return parsed, nil, agendaToolInputError(FieldDate, "Précisez une date valide (AAAA-MM-JJ).", nil)
	}
	if _, err := time.Parse("15:04", parsed.StartTime); err != nil {
		return parsed, nil, agendaToolInputError(FieldStartTime, "Précisez une heure valide (HH:MM).", nil)
	}
	parsed.ResourceIDs = trimResourceIDs(parsed.ResourceIDs)
	canonical, err := json.Marshal(parsed)
	return parsed, canonical, err
}

func parseRescheduleAppointmentInput(input json.RawMessage) (rescheduleAppointmentInput, json.RawMessage, error) {
	var parsed rescheduleAppointmentInput
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return parsed, nil, agendaToolInputError(FieldDate, "Les arguments proposés sont invalides.", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return parsed, nil, agendaToolInputError(FieldDate, "Les arguments proposés sont invalides.", err)
	}
	parsed.AppointmentID = strings.TrimSpace(parsed.AppointmentID)
	parsed.Date = strings.TrimSpace(parsed.Date)
	parsed.StartTime = strings.TrimSpace(parsed.StartTime)
	if parsed.AppointmentID == "" {
		return parsed, nil, agendaToolInputError(FieldDate, "Précisez le rendez-vous à déplacer.", nil)
	}
	if _, err := time.Parse(DateLayout, parsed.Date); err != nil {
		return parsed, nil, agendaToolInputError(FieldDate, "Précisez une date valide (AAAA-MM-JJ).", nil)
	}
	if _, err := time.Parse("15:04", parsed.StartTime); err != nil {
		return parsed, nil, agendaToolInputError(FieldStartTime, "Précisez une heure valide (HH:MM).", nil)
	}
	canonical, err := json.Marshal(parsed)
	return parsed, canonical, err
}

func parseCancelAppointmentInput(input json.RawMessage) (cancelAppointmentInput, json.RawMessage, error) {
	var parsed cancelAppointmentInput
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return parsed, nil, agendaToolInputError(FieldDate, "Les arguments proposés sont invalides.", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return parsed, nil, agendaToolInputError(FieldDate, "Les arguments proposés sont invalides.", err)
	}
	parsed.AppointmentID = strings.TrimSpace(parsed.AppointmentID)
	if parsed.AppointmentID == "" {
		return parsed, nil, agendaToolInputError(FieldDate, "Précisez le rendez-vous à annuler.", nil)
	}
	canonical, err := json.Marshal(parsed)
	return parsed, canonical, err
}

// parseListBookableResourcesInput takes no fields, but is still strict about
// unknown ones and trailing data, the same way every other tool input here is
// parsed — no canonical value to return, the empty object is its own input.
func parseListBookableResourcesInput(input json.RawMessage) error {
	var parsed listBookableResourcesInput
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return agendaToolInputError("", "Les arguments proposés sont invalides.", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return agendaToolInputError("", "Les arguments proposés sont invalides.", err)
	}
	return nil
}

func trimResourceIDs(values []string) []string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			trimmed = append(trimmed, value)
		}
	}
	return trimmed
}

func agendaToolInputError(field string, message string, cause error) error {
	toolErr := &assistanttools.ToolError{Code: "invalid_arguments", Field: field, Message: message}
	if cause == nil {
		return toolErr
	}
	return errors.Join(toolErr, cause)
}

// mapAssistantToolFieldError translates the store's own validation errors
// (already backed by database constraints and RLS) into the assistant's
// error contract, so the model gets the same French field message a human
// would see on the booking form. FieldResource ("Choisissez au moins une
// ressource.") is not special-cased: list_bookable_resources lets the model
// resolve it the same way a human would, by picking one and retrying.
func mapAssistantToolFieldError(err error) error {
	var fieldErr *FieldError
	if !errors.As(err, &fieldErr) {
		return err
	}
	return &assistanttools.ToolError{
		Code: "invalid_arguments", Field: fieldErr.Field, Message: fieldErr.Message,
	}
}

// mapAvailabilityError exists only so callers can name what they mean;
// check_availability's FieldResource case is handled identically to every
// other agenda tool by mapAssistantToolFieldError.
func mapAvailabilityError(err error) error { return mapAssistantToolFieldError(err) }

// mapAgendaWriteError covers the write-path errors save/cancel can raise on
// top of FieldError: a real double-booking conflict (a database exclusion
// constraint, not a Go-side check), an appointment already past editing, and
// a stale proposal — the appointment changed between preview and confirm, so
// the write's own WHERE clause matched nothing instead of silently
// overwriting something else.
func mapAgendaWriteError(err error) error {
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		return &assistanttools.ToolError{Code: "conflict", Message: conflictMessage(conflict)}
	}
	if errors.Is(err, ErrForbidden) {
		return &assistanttools.ToolError{
			Code: "conflict", Message: "Ce rendez-vous est déjà terminé et ne peut plus être modifié.",
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return &assistanttools.ToolError{
			Code: "conflict", Message: "Ce rendez-vous a changé depuis l’aperçu. Préparez une nouvelle demande.",
		}
	}
	return mapAssistantToolFieldError(err)
}
