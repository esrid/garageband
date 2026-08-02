package agenda

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/esrid/garageband/internal/platform/assistanttools"
)

const (
	ToolCheckAvailability = "check_availability"
	ToolFindAppointments  = "find_appointments"
)

var checkAvailabilitySchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "service_id": {"type": "string", "description": "Service offering id to check availability for"},
    "date": {"type": "string", "description": "Date to check, YYYY-MM-DD"}
  },
  "required": ["service_id", "date"],
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

type checkAvailabilityInput struct {
	ServiceID string `json:"service_id"`
	Date      string `json:"date"`
}

type findAppointmentsInput struct {
	Date string `json:"date"`
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
			parsed.ServiceID, nil, parsed.Date,
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
			parsed.ServiceID, nil, parsed.Date,
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
	default:
		return assistanttools.Result{}, assistanttools.ErrUnknownTool
	}
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
// would see on the booking form.
func mapAssistantToolFieldError(err error) error {
	var fieldErr *FieldError
	if errors.As(err, &fieldErr) {
		return &assistanttools.ToolError{
			Code: "invalid_arguments", Field: fieldErr.Field, Message: fieldErr.Message,
		}
	}
	return err
}

// mapAvailabilityError additionally covers a limit this tool doesn't lift:
// a service without automatic resource allocation needs a specific
// bookable resource chosen first, and this tool has no way to name one —
// the model has no visibility into internal resource ids. That is a scope
// boundary, not a bug: the booking screen still handles it.
func mapAvailabilityError(err error) error {
	var fieldErr *FieldError
	if errors.As(err, &fieldErr) && fieldErr.Field == FieldResource {
		return &assistanttools.ToolError{
			Code: "unsupported", Field: FieldResource,
			Message: "Cette prestation nécessite de choisir une ressource spécifique (pont, technicien…) ; vérifiez sa disponibilité depuis l’agenda.",
		}
	}
	return mapAssistantToolFieldError(err)
}
