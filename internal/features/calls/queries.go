package calls

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/db"
)

type Store struct{ db *db.DB }

func NewStore(database *db.DB) *Store { return &Store{db: database} }

func (s *Store) Inbox(
	ctx context.Context,
	tenantID string,
	userID string,
	filter string,
) (inbox Inbox, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(
			ctx, `SELECT name FROM tenants WHERE id = $1`, tenantID,
		).Scan(&inbox.Organization); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT call.id::text, call.started_at, call.ended_at,
			       call.direction, call.status,
			       CASE WHEN call.direction = 'inbound'
			            THEN call.from_e164 ELSE call.to_e164 END,
			       CASE WHEN customer.id IS NULL THEN NULL ELSE call.customer_id::text END,
			       COALESCE(
			           NULLIF(btrim(concat_ws(' ', customer.first_name, customer.last_name)), ''),
			           NULLIF(btrim(customer.company_name), ''), ''
			       ),
			       location.name, location.timezone,
			       COALESCE(call.summary, ''), COALESCE(call.outcome, ''),
			       call.recording_uri IS NOT NULL AND btrim(call.recording_uri) <> ''
			FROM calls call
			JOIN locations location
			  ON location.tenant_id = call.tenant_id
			 AND location.id = call.location_id
			LEFT JOIN customers customer
			  ON customer.tenant_id = call.tenant_id
			 AND customer.id = call.customer_id
			WHERE call.tenant_id = $1
			ORDER BY call.started_at DESC, call.id DESC`, tenantID,
		)
		if err != nil {
			return err
		}
		calls, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Call, error) { return scanCall(row) })
		if err != nil {
			return err
		}
		for _, call := range calls {
			if filter != FilterNeedsAttention || call.NeedsAttention() {
				inbox.Calls = append(inbox.Calls, call)
			}
		}
		return nil
	})
	if filter == FilterNeedsAttention {
		inbox.Filter = filter
	}
	return inbox, err
}

func (s *Store) Transcript(
	ctx context.Context,
	tenantID string,
	userID string,
	callID string,
) (transcript Transcript, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(
			ctx, `SELECT name FROM tenants WHERE id = $1`, tenantID,
		).Scan(&transcript.Organization); err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `
			SELECT call.id::text, call.started_at, call.ended_at,
			       call.direction, call.status,
			       CASE WHEN call.direction = 'inbound'
			            THEN call.from_e164 ELSE call.to_e164 END,
			       CASE WHEN customer.id IS NULL THEN NULL ELSE call.customer_id::text END,
			       COALESCE(
			           NULLIF(btrim(concat_ws(' ', customer.first_name, customer.last_name)), ''),
			           NULLIF(btrim(customer.company_name), ''), ''
			       ),
			       location.name, location.timezone,
			       COALESCE(call.summary, ''), COALESCE(call.outcome, ''),
			       call.recording_uri IS NOT NULL AND btrim(call.recording_uri) <> ''
			FROM calls call
			JOIN locations location
			  ON location.tenant_id = call.tenant_id
			 AND location.id = call.location_id
			LEFT JOIN customers customer
			  ON customer.tenant_id = call.tenant_id
			 AND customer.id = call.customer_id
			WHERE call.tenant_id = $1 AND call.id = $2`, tenantID, callID,
		)
		call, err := scanCall(row)
		if err != nil {
			return err
		}
		transcript.Call = call
		zone := call.StartedAt.Location()

		rows, err := tx.Query(ctx, `
			SELECT speaker, content, occurred_at
			FROM call_messages
			WHERE tenant_id = $1 AND call_id = $2
			ORDER BY sequence`, tenantID, callID,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var message Message
			if err := rows.Scan(&message.Speaker, &message.Content, &message.OccurredAt); err != nil {
				return err
			}
			message.OccurredAt = message.OccurredAt.In(zone)
			transcript.Messages = append(transcript.Messages, message)
		}
		return rows.Err()
	})
	return transcript, err
}

type rowScanner interface {
	Scan(...any) error
}

func scanCall(row rowScanner) (call Call, err error) {
	var endedAt sql.NullTime
	var customerID sql.NullString
	var timezoneName, callerE164 string
	if err := row.Scan(
		&call.ID, &call.StartedAt, &endedAt, &call.Direction, &call.Status,
		&callerE164, &customerID, &call.CustomerName, &call.LocationName,
		&timezoneName, &call.Summary, &call.Outcome, &call.HasRecording,
	); err != nil {
		return Call{}, err
	}
	zone, err := time.LoadLocation(timezoneName)
	if err != nil {
		return Call{}, err
	}
	call.StartedAt = call.StartedAt.In(zone)
	if endedAt.Valid {
		call.EndedAt = endedAt.Time.In(zone)
	}
	call.CustomerID = customerID.String
	call.CallerNumber = formatPhone(callerE164)
	return call, nil
}

func formatPhone(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 12 && strings.HasPrefix(value, "+33") {
		digits := "0" + value[3:]
		parts := make([]string, 0, 5)
		for len(digits) >= 2 {
			parts = append(parts, digits[:2])
			digits = digits[2:]
		}
		return strings.Join(parts, " ")
	}
	return value
}
