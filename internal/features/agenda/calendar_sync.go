package agenda

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/esrid/garageband/internal/platform/calendar"
	"github.com/esrid/garageband/internal/platform/secrets"
)

// CalendarConfig bundles what pushing appointments to a connected Google
// Calendar needs. Enabled false (no OAuth client or encryption key
// configured) turns every push into a silent no-op, mirroring
// locations.CalendarConfig - the two are kept separate structs rather than
// shared, since a feature never imports another feature's package.
type CalendarConfig struct {
	OAuth   oauth2.Config
	Secrets secrets.Store
	Enabled bool
}

// googleCalendarID is always "primary": connecting a Google account means its
// primary calendar, the same account-level choice locations.ConnectCalendar
// already makes - there is no per-location secondary-calendar picker.
const googleCalendarID = "primary"

// SyncAppointmentCalendar pushes the appointment's current state to the
// location's connected Google Calendar, if any. A location with no
// connection is a no-op, not an error - most locations will never connect
// one. Garageband is the source of truth: this only ever pushes: it never
// reads back edits made directly in Google Calendar.
//
// ponytail: assistant-booked appointments (book/reschedule/cancel_appointment
// tools) don't call this yet - Store has no CalendarConfig of its own and
// Execute's signature is fixed by the assistanttools.Executor interface.
// Add when assistant-side booking needs calendar parity too, by threading
// CalendarConfig through NewStore instead of a per-call parameter.
func (s *Store) SyncAppointmentCalendar(
	ctx context.Context, tenantID, userID, appointmentID string, calendarCfg CalendarConfig,
) error {
	if !calendarCfg.Enabled {
		return nil
	}
	var pushErr error
	err := s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		connectionID, secretRef, ok, err := activeCalendarConnection(ctx, tx, tenantID, appointmentID)
		if err != nil || !ok {
			return err
		}
		event, err := loadAppointmentEvent(ctx, tx, tenantID, appointmentID)
		if err != nil {
			return err
		}
		refreshToken, err := calendarCfg.Secrets.Resolve(ctx, tx, secretRef)
		if err != nil {
			return err
		}
		event.CalendarID = googleCalendarID
		event.ExternalID = calendarEventID(appointmentID)
		client := calendarCfg.OAuth.Client(ctx, &oauth2.Token{RefreshToken: string(refreshToken)})
		saved, pushed := calendar.NewGoogle(client).UpsertEvent(ctx, event)
		pushErr = pushed
		return recordCalendarSync(ctx, tx, tenantID, appointmentID, connectionID, googleCalendarID, saved.ExternalID, pushed)
	})
	if err != nil {
		return err
	}
	return pushErr
}

// RemoveAppointmentCalendarEvent deletes the appointment's event from its
// location's connected Google Calendar, if one was ever synced. No synced
// event (never connected, or never successfully pushed) is a no-op.
func (s *Store) RemoveAppointmentCalendarEvent(
	ctx context.Context, tenantID, userID, appointmentID string, calendarCfg CalendarConfig,
) error {
	if !calendarCfg.Enabled {
		return nil
	}
	var deleteErr error
	err := s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		connectionID, secretRef, externalEventID, ok, err := syncedCalendarEvent(ctx, tx, tenantID, appointmentID)
		if err != nil || !ok {
			return err
		}
		refreshToken, err := calendarCfg.Secrets.Resolve(ctx, tx, secretRef)
		if err != nil {
			return err
		}
		client := calendarCfg.OAuth.Client(ctx, &oauth2.Token{RefreshToken: string(refreshToken)})
		deleteErr = calendar.NewGoogle(client).DeleteEvent(ctx, googleCalendarID, externalEventID)
		return recordCalendarSync(ctx, tx, tenantID, appointmentID, connectionID, googleCalendarID, externalEventID, deleteErr)
	})
	if err != nil {
		return err
	}
	return deleteErr
}

// calendarEventID derives a Google-safe event id from the appointment id, so
// repeated syncs of the same appointment always touch the same event
// (idempotent upsert, not accumulate). Google requires ids to use only
// lowercase a-v and 0-9 (base32hex); a hyphen-stripped, lowercased UUID hex
// string already satisfies that since a-f is a subset of a-v. Verified
// against https://developers.google.com/calendar/api/v3/reference/events/insert
// 2026-08-03.
func calendarEventID(appointmentID string) string {
	return strings.ReplaceAll(strings.ToLower(appointmentID), "-", "")
}

func activeCalendarConnection(
	ctx context.Context, tx pgx.Tx, tenantID, appointmentID string,
) (connectionID string, secretRef string, ok bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT connection.id, connection.secret_ref
		FROM provider_connections connection
		JOIN appointments appointment
		  ON appointment.tenant_id = connection.tenant_id
		 AND appointment.location_id = connection.location_id
		WHERE connection.tenant_id = $1 AND appointment.id = $2
		  AND connection.kind = 'calendar' AND connection.provider = 'google'
		  AND connection.status = 'active'`,
		tenantID, appointmentID,
	).Scan(&connectionID, &secretRef)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return connectionID, secretRef, true, nil
}

func syncedCalendarEvent(
	ctx context.Context, tx pgx.Tx, tenantID, appointmentID string,
) (connectionID string, secretRef string, externalEventID string, ok bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT event.connection_id, connection.secret_ref, event.external_event_id
		FROM appointment_calendar_events event
		JOIN provider_connections connection
		  ON connection.tenant_id = event.tenant_id AND connection.id = event.connection_id
		WHERE event.tenant_id = $1 AND event.appointment_id = $2 AND event.sync_status <> 'deleted'`,
		tenantID, appointmentID,
	).Scan(&connectionID, &secretRef, &externalEventID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	return connectionID, secretRef, externalEventID, true, nil
}

// loadAppointmentEvent builds the calendar event content from the
// appointment and its related records, reusing the same customer/vehicle
// name fallback (live record, else the appointment's own snapshot) as
// Store.Day's listing query.
func loadAppointmentEvent(
	ctx context.Context, tx pgx.Tx, tenantID, appointmentID string,
) (calendar.Event, error) {
	var event calendar.Event
	var customerName, vehicleLabel, serviceName, locationName string
	if err := tx.QueryRow(ctx, `
		SELECT appointment.starts_at, appointment.ends_at, location.timezone, location.name,
		       COALESCE(
		           NULLIF(btrim(concat_ws(' ', customer.first_name, customer.last_name)), ''),
		           NULLIF(btrim(customer.company_name), ''),
		           NULLIF(btrim(concat_ws(' ',
		               appointment.customer_snapshot->>'first_name',
		               appointment.customer_snapshot->>'last_name'
		           )), ''),
		           NULLIF(btrim(appointment.customer_snapshot->>'company_name'), ''),
		           'Client'
		       ),
		       COALESCE(
		           NULLIF(btrim(concat_ws(' ', vehicle.registration_plate,
		               vehicle.make, vehicle.model)), ''),
		           NULLIF(btrim(concat_ws(' ',
		               appointment.vehicle_snapshot->>'registration_plate',
		               appointment.vehicle_snapshot->>'make',
		               appointment.vehicle_snapshot->>'model'
		           )), ''),
		           ''
		       ),
		       COALESCE(service.name, '')
		FROM appointments appointment
		JOIN locations location
		  ON location.tenant_id = appointment.tenant_id AND location.id = appointment.location_id
		LEFT JOIN customers customer
		  ON customer.tenant_id = appointment.tenant_id AND customer.id = appointment.customer_id
		LEFT JOIN vehicles vehicle
		  ON vehicle.tenant_id = appointment.tenant_id AND vehicle.id = appointment.vehicle_id
		LEFT JOIN service_offerings service
		  ON service.tenant_id = appointment.tenant_id AND service.id = appointment.service_id
		WHERE appointment.tenant_id = $1 AND appointment.id = $2`,
		tenantID, appointmentID,
	).Scan(
		&event.Start, &event.End, &event.TimeZone, &locationName,
		&customerName, &vehicleLabel, &serviceName,
	); err != nil {
		return calendar.Event{}, err
	}
	event.Title = customerName
	if serviceName != "" {
		event.Title = serviceName + " — " + customerName
	}
	event.Description = vehicleLabel
	event.Location = locationName
	event.Private = true
	return event, nil
}

// recordCalendarSync writes the outcome of a push/delete attempt so the next
// sync (or a future reconciliation pass) knows the last known state. It never
// returns the push/delete error itself - only genuine failures writing this
// bookkeeping row do, so the caller's transaction still commits (and the
// error status survives) even when the push to Google failed.
func recordCalendarSync(
	ctx context.Context, tx pgx.Tx,
	tenantID, appointmentID, connectionID, externalCalendarID, externalEventID string,
	pushErr error,
) error {
	status, lastError := "synced", ""
	if pushErr != nil {
		status, lastError = "error", pushErr.Error()
	} else if externalEventID == "" {
		status = "deleted"
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO appointment_calendar_events (
		    tenant_id, appointment_id, connection_id, external_calendar_id,
		    external_event_id, sync_status, last_synced_at, last_error
		) VALUES ($1, $2, $3, $4, $5, $6, CASE WHEN $6 = 'error' THEN NULL ELSE now() END, NULLIF($7, ''))
		ON CONFLICT (appointment_id, connection_id) DO UPDATE SET
		    external_calendar_id = EXCLUDED.external_calendar_id,
		    external_event_id = COALESCE(
		        NULLIF(EXCLUDED.external_event_id, ''), appointment_calendar_events.external_event_id
		    ),
		    sync_status = EXCLUDED.sync_status,
		    last_synced_at = CASE WHEN EXCLUDED.sync_status = 'error'
		        THEN appointment_calendar_events.last_synced_at ELSE now() END,
		    last_error = EXCLUDED.last_error,
		    updated_at = now()`,
		tenantID, appointmentID, connectionID, externalCalendarID, externalEventID, status, lastError,
	)
	return err
}
