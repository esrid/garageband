package agents

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/esrid/garageband/internal/platform/db"
)

var (
	ErrForbidden = errors.New("agent management is forbidden")
	ErrNotReady  = errors.New("agent provider configuration is incomplete")
)

type Input struct {
	Name     string
	Greeting string
	Prompt   string
	Fallback string
	Locale   string
	LLM      string
	STT      string
	TTS      string
}

type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Message }

type Store struct{ db *db.DB }

func NewStore(database *db.DB) *Store { return &Store{db: database} }

type agentRow struct {
	Agent
	LocationID string
	LLM        sql.NullString
	STT        sql.NullString
	TTS        sql.NullString
}

func (s *Store) List(
	ctx context.Context,
	tenantID string,
	userID string,
) (page Index, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		role, err := loadOrganizationRole(ctx, tx, tenantID, userID, &page.Organization)
		if err != nil {
			return err
		}
		page.CanManage = role == "owner" || role == "admin"

		rows, err := tx.Query(ctx, `
			SELECT agent.id::text, agent.name, location.id::text,
			       location.name, agent.status,
			       agent.llm_connection_id::text,
			       agent.speech_to_text_connection_id::text,
			       agent.text_to_speech_connection_id::text
			FROM agents agent
			JOIN locations location
			  ON location.tenant_id = agent.tenant_id
			 AND location.id = agent.location_id
			WHERE agent.tenant_id = $1 AND agent.status <> 'archived'
			ORDER BY location.name, location.id`, tenantID,
		)
		if err != nil {
			return err
		}
		var records []agentRow
		for rows.Next() {
			var record agentRow
			if err := rows.Scan(
				&record.ID, &record.Name, &record.LocationID,
				&record.LocationName, &record.Status,
				&record.LLM, &record.STT, &record.TTS,
			); err != nil {
				rows.Close()
				return err
			}
			records = append(records, record)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, record := range records {
			record.Missing, err = missingConnections(ctx, tx, tenantID, record)
			if err != nil {
				return err
			}
			record.Numbers, err = agentNumbers(ctx, tx, tenantID, record.ID)
			if err != nil {
				return err
			}
			page.Agents = append(page.Agents, record.Agent)
		}
		return nil
	})
	return page, err
}

func (s *Store) Form(
	ctx context.Context,
	tenantID string,
	userID string,
	agentID string,
) (page FormPage, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		role, err := loadOrganizationRole(ctx, tx, tenantID, userID, &page.Organization)
		if err != nil {
			return err
		}
		page.CanManage = role == "owner" || role == "admin"
		page.ID = agentID
		page.FieldErrors = make(map[string]string)
		var llm, stt, tts sql.NullString
		var locationID string
		if err := tx.QueryRow(ctx, `
			SELECT agent.name, agent.greeting, agent.system_prompt,
			       agent.fallback_message, agent.locale, agent.status,
			       location.id::text, location.name,
			       agent.llm_connection_id::text,
			       agent.speech_to_text_connection_id::text,
			       agent.text_to_speech_connection_id::text
			FROM agents agent
			JOIN locations location
			  ON location.tenant_id = agent.tenant_id
			 AND location.id = agent.location_id
			WHERE agent.tenant_id = $1 AND agent.id = $2
			  AND agent.status <> 'archived'`, tenantID, agentID,
		).Scan(
			&page.Values.Name, &page.Values.Greeting, &page.Values.Prompt,
			&page.Values.Fallback, &page.Values.Locale, &page.Status,
			&locationID, &page.LocationName, &llm, &stt, &tts,
		); err != nil {
			return err
		}
		page.Values.LLM = llm.String
		page.Values.STT = stt.String
		page.Values.TTS = tts.String
		if page.Numbers, err = agentNumbers(ctx, tx, tenantID, agentID); err != nil {
			return err
		}
		if page.LLMConnections, err = connectionOptions(
			ctx, tx, tenantID, locationID, KindLLM,
		); err != nil {
			return err
		}
		if page.STTConnections, err = connectionOptions(
			ctx, tx, tenantID, locationID, KindSTT,
		); err != nil {
			return err
		}
		if page.TTSConnections, err = connectionOptions(
			ctx, tx, tenantID, locationID, KindTTS,
		); err != nil {
			return err
		}
		page.Locales = localeOptions()
		return nil
	})
	return page, err
}

func (s *Store) Save(
	ctx context.Context,
	tenantID string,
	userID string,
	agentID string,
	input Input,
) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx); err != nil {
			return err
		}
		var locationID string
		if err := tx.QueryRow(ctx, `
			SELECT location_id::text FROM agents
			WHERE tenant_id = $1 AND id = $2 AND status <> 'archived'`,
			tenantID, agentID,
		).Scan(&locationID); err != nil {
			return err
		}
		for _, selection := range []struct {
			field string
			kind  string
			id    string
		}{
			{FieldLLM, KindLLM, input.LLM},
			{FieldSTT, KindSTT, input.STT},
			{FieldTTS, KindTTS, input.TTS},
		} {
			if selection.id == "" {
				continue
			}
			if err := requireConnection(
				ctx, tx, tenantID, locationID, selection.kind, selection.id,
			); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return &FieldError{
						Field:   selection.field,
						Message: "Choisissez une connexion active de cet établissement.",
					}
				}
				return err
			}
		}
		result, err := tx.Exec(ctx, `
			UPDATE agents
			SET name = $1, greeting = $2, system_prompt = $3,
			    fallback_message = $4, locale = $5,
			    llm_connection_id = $6,
			    speech_to_text_connection_id = $7,
			    text_to_speech_connection_id = $8,
			    updated_at = now()
			WHERE tenant_id = $9 AND id = $10 AND status <> 'archived'`,
			input.Name, input.Greeting, input.Prompt, input.Fallback, input.Locale,
			nullUUID(input.LLM), nullUUID(input.STT), nullUUID(input.TTS), tenantID, agentID,
		)
		if err != nil {
			return err
		}
		return requireOne(result)
	})
}

func (s *Store) Activate(
	ctx context.Context,
	tenantID string,
	userID string,
	agentID string,
) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx); err != nil {
			return err
		}
		var ready bool
		if err := tx.QueryRow(ctx, `
			SELECT
			    llm.status = 'active'
			    AND stt.status = 'active'
			    AND tts.status = 'active'
			FROM agents agent
			JOIN provider_connections llm
			  ON llm.tenant_id = agent.tenant_id
			 AND llm.location_id = agent.location_id
			 AND llm.id = agent.llm_connection_id AND llm.kind = 'llm'
			JOIN provider_connections stt
			  ON stt.tenant_id = agent.tenant_id
			 AND stt.location_id = agent.location_id
			 AND stt.id = agent.speech_to_text_connection_id
			 AND stt.kind = 'speech_to_text'
			JOIN provider_connections tts
			  ON tts.tenant_id = agent.tenant_id
			 AND tts.location_id = agent.location_id
			 AND tts.id = agent.text_to_speech_connection_id
			 AND tts.kind = 'text_to_speech'
			WHERE agent.tenant_id = $1 AND agent.id = $2
			  AND agent.status <> 'archived'`, tenantID, agentID,
		).Scan(&ready); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotReady
			}
			return err
		}
		if !ready {
			return ErrNotReady
		}
		result, err := tx.Exec(ctx, `
			UPDATE agents SET status = 'active', updated_at = now()
			WHERE tenant_id = $1 AND id = $2 AND status <> 'archived'`,
			tenantID, agentID,
		)
		if err != nil {
			return err
		}
		return requireOne(result)
	})
}

func (s *Store) Pause(
	ctx context.Context,
	tenantID string,
	userID string,
	agentID string,
) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireManager(ctx, tx); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `
			UPDATE agents SET status = 'paused', updated_at = now()
			WHERE tenant_id = $1 AND id = $2 AND status <> 'archived'`,
			tenantID, agentID,
		)
		if err != nil {
			return err
		}
		return requireOne(result)
	})
}

func loadOrganizationRole(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	userID string,
	organization *string,
) (role string, err error) {
	err = tx.QueryRow(ctx, `
		SELECT tenant.name, membership.role
		FROM tenants tenant
		JOIN tenant_memberships membership ON membership.tenant_id = tenant.id
		WHERE tenant.id = $1 AND membership.user_id = $2`,
		tenantID, userID,
	).Scan(organization, &role)
	return role, err
}

func missingConnections(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	record agentRow,
) ([]string, error) {
	missing := make([]string, 0, 3)
	for _, connection := range []struct {
		kind string
		id   sql.NullString
	}{
		{KindLLM, record.LLM},
		{KindSTT, record.STT},
		{KindTTS, record.TTS},
	} {
		if !connection.id.Valid {
			missing = append(missing, connection.kind)
			continue
		}
		if err := requireConnection(
			ctx, tx, tenantID, record.LocationID, connection.kind, connection.id.String,
		); errors.Is(err, sql.ErrNoRows) {
			missing = append(missing, connection.kind)
		} else if err != nil {
			return nil, err
		}
	}
	return missing, nil
}

func requireConnection(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	locationID string,
	kind string,
	connectionID string,
) error {
	var exists bool
	return tx.QueryRow(ctx, `
		SELECT true FROM provider_connections
		WHERE tenant_id = $1 AND location_id = $2 AND kind = $3 AND id = $4
		  AND status = 'active'`, tenantID, locationID, kind, connectionID,
	).Scan(&exists)
}

func connectionOptions(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	locationID string,
	kind string,
) (options []Option, err error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text,
		       provider || COALESCE(' · ' || NULLIF(external_account_id, ''), '')
		FROM provider_connections
		WHERE tenant_id = $1 AND location_id = $2 AND kind = $3
		  AND status = 'active'
		ORDER BY provider, external_account_id, id`, tenantID, locationID, kind,
	)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Option])
}

func agentNumbers(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	agentID string,
) (numbers []string, err error) {
	rows, err := tx.Query(ctx, `
		SELECT phone_e164 FROM phone_numbers
		WHERE tenant_id = $1 AND agent_id = $2 AND status = 'active'
		ORDER BY phone_e164`, tenantID, agentID,
	)
	if err != nil {
		return nil, err
	}
	numbers, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	for i, number := range numbers {
		numbers[i] = formatPhone(number)
	}
	return numbers, nil
}

func requireManager(ctx context.Context, tx pgx.Tx) error {
	var allowed bool
	if err := tx.QueryRow(
		ctx, `SELECT app_current_user_manages_tenant()`,
	).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func requireOne(result pgconn.CommandTag) error {
	if result.RowsAffected() != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func localeOptions() []Option {
	return []Option{
		{Value: "fr-FR", Label: "Français (France)"},
		{Value: "fr-BE", Label: "Français (Belgique)"},
		{Value: "en-GB", Label: "Anglais"},
		{Value: "es-ES", Label: "Espagnol"},
	}
}

func nullUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
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
