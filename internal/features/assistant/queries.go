package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/assistanttools"
	"github.com/esrid/garageband/internal/platform/db"
)

var (
	ErrForbidden       = errors.New("assistant access is forbidden")
	ErrExecutionClosed = errors.New("assistant tool execution is already closed")
)

type LocationRef struct {
	ID   string
	Name string
}

type Conversation struct {
	ID         string
	LocationID string
	Location   string
	Title      string
	Status     string
	UpdatedAt  time.Time
}

type Message struct {
	ID        string
	Sequence  int
	Role      string
	Content   string
	CreatedAt time.Time
}

type ToolExecution struct {
	ID             string
	ConversationID string
	LocationID     string
	ToolName       string
	Consequence    string
	Status         string
	Input          json.RawMessage
	PreviewSummary string
	Output         json.RawMessage
	ErrorMessage   string
	ProposedAt     time.Time
	ConfirmedAt    sql.NullTime
	CompletedAt    sql.NullTime
}

type Workspace struct {
	Organization  string
	Role          string
	Locations     []LocationRef
	Conversations []Conversation
	Current       Conversation
	Messages      []Message
	Executions    []ToolExecution
}

type Store struct{ db *db.DB }

func NewStore(database *db.DB) *Store { return &Store{db: database} }

func (s *Store) Workspace(
	ctx context.Context,
	tenantID string,
	userID string,
	conversationID string,
) (workspace Workspace, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT tenant.name, membership.role
			FROM tenant_memberships membership
			JOIN tenants tenant ON tenant.id = membership.tenant_id
			WHERE membership.tenant_id = $1 AND membership.user_id = $2`,
			tenantID, userID,
		).Scan(&workspace.Organization, &workspace.Role); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrForbidden
			}
			return err
		}
		locationRows, err := tx.Query(ctx, `
			SELECT id::text, name
			FROM locations
			WHERE tenant_id = $1 AND status = 'active'
			ORDER BY name, id`, tenantID)
		if err != nil {
			return err
		}
		workspace.Locations, err = pgx.CollectRows(locationRows, pgx.RowToStructByPos[LocationRef])
		if err != nil {
			return err
		}

		conversationRows, err := tx.Query(ctx, `
			SELECT conversation.id::text, conversation.location_id::text,
			       location.name, conversation.title, conversation.status,
			       conversation.updated_at
			FROM assistant_conversations conversation
			JOIN locations location
			  ON location.tenant_id = conversation.tenant_id
			 AND location.id = conversation.location_id
			WHERE conversation.tenant_id = $1
			  AND conversation.created_by_user_id = $2
			ORDER BY conversation.updated_at DESC, conversation.id DESC
			LIMIT 30`, tenantID, userID)
		if err != nil {
			return err
		}
		workspace.Conversations, err = pgx.CollectRows(conversationRows, func(row pgx.CollectableRow) (Conversation, error) {
			var conversation Conversation
			err := scanConversation(row, &conversation)
			return conversation, err
		})
		if err != nil {
			return err
		}
		if conversationID == "" {
			return nil
		}
		if err := tx.QueryRow(ctx, `
			SELECT conversation.id::text, conversation.location_id::text,
			       location.name, conversation.title, conversation.status,
			       conversation.updated_at
			FROM assistant_conversations conversation
			JOIN locations location
			  ON location.tenant_id = conversation.tenant_id
			 AND location.id = conversation.location_id
			WHERE conversation.tenant_id = $1 AND conversation.id = $2
			  AND conversation.created_by_user_id = $3`,
			tenantID, conversationID, userID,
		).Scan(
			&workspace.Current.ID, &workspace.Current.LocationID,
			&workspace.Current.Location, &workspace.Current.Title,
			&workspace.Current.Status, &workspace.Current.UpdatedAt,
		); err != nil {
			return err
		}
		if workspace.Messages, err = loadMessages(ctx, tx, tenantID, conversationID); err != nil {
			return err
		}
		workspace.Executions, err = loadExecutions(ctx, tx, tenantID, conversationID)
		return err
	})
	return workspace, err
}

func (s *Store) AppendUserMessage(
	ctx context.Context,
	tenantID string,
	userID string,
	conversationID string,
	locationID string,
	content string,
) (conversation Conversation, messages []Message, userSequence int, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if conversationID == "" {
			title := conversationTitle(content)
			if err := tx.QueryRow(ctx, `
				INSERT INTO assistant_conversations (
				    tenant_id, location_id, created_by_user_id, title
				)
				SELECT $1, location.id, $2, $4
				FROM locations location
				WHERE location.tenant_id = $1 AND location.id = $3
				  AND location.status = 'active'
				RETURNING id::text, location_id::text, title, status, updated_at`,
				tenantID, userID, locationID, title,
			).Scan(
				&conversation.ID, &conversation.LocationID, &conversation.Title,
				&conversation.Status, &conversation.UpdatedAt,
			); err != nil {
				return err
			}
		} else {
			if err := tx.QueryRow(ctx, `
				SELECT id::text, location_id::text, title, status, updated_at
				FROM assistant_conversations
				WHERE tenant_id = $1 AND id = $2 AND created_by_user_id = $3
				  AND status = 'active'
				FOR UPDATE`, tenantID, conversationID, userID,
			).Scan(
				&conversation.ID, &conversation.LocationID, &conversation.Title,
				&conversation.Status, &conversation.UpdatedAt,
			); err != nil {
				return err
			}
			if locationID != "" && locationID != conversation.LocationID {
				return ErrForbidden
			}
		}
		var err error
		if userSequence, err = appendMessage(
			ctx, tx, tenantID, conversation.LocationID, conversation.ID, "user", content,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE assistant_conversations SET updated_at = now()
			WHERE tenant_id = $1 AND id = $2`, tenantID, conversation.ID); err != nil {
			return err
		}
		messages, err = loadMessages(ctx, tx, tenantID, conversation.ID)
		return err
	})
	return conversation, messages, userSequence, err
}

func (s *Store) AppendAssistantMessage(
	ctx context.Context,
	tenantID string,
	userID string,
	conversationID string,
	content string,
) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		locationID, err := lockConversation(ctx, tx, tenantID, userID, conversationID)
		if err != nil {
			return err
		}
		if _, err := appendMessage(ctx, tx, tenantID, locationID, conversationID, "assistant", content); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE assistant_conversations SET updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenantID, conversationID)
		return err
	})
}

func (s *Store) ProposeTool(
	ctx context.Context,
	tenantID string,
	userID string,
	conversationID string,
	userSequence int,
	callID string,
	definition assistanttools.Definition,
	preview assistanttools.Preview,
) (executionID string, err error) {
	previewJSON, err := json.Marshal(map[string]string{"summary": preview.Summary})
	if err != nil {
		return "", err
	}
	affectedJSON, err := json.Marshal(preview.AffectedRecords)
	if err != nil {
		return "", err
	}
	idempotencyKey := fmt.Sprintf("message-%d:%s", userSequence, callID)
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		locationID, err := lockConversation(ctx, tx, tenantID, userID, conversationID)
		if err != nil {
			return err
		}
		inserted := true
		err = tx.QueryRow(ctx, `
			INSERT INTO assistant_tool_executions (
			    tenant_id, location_id, conversation_id, requested_by_user_id,
			    idempotency_key, tool_name, consequence, input, preview,
			    affected_records
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (tenant_id, conversation_id, idempotency_key) DO NOTHING
			RETURNING id::text`, tenantID, locationID, conversationID, userID,
			idempotencyKey, definition.Name, definition.Consequence,
			preview.Input, previewJSON, affectedJSON,
		).Scan(&executionID)
		if errors.Is(err, sql.ErrNoRows) {
			inserted = false
			err = tx.QueryRow(ctx, `
				SELECT id::text FROM assistant_tool_executions
				WHERE tenant_id = $1 AND conversation_id = $2
				  AND idempotency_key = $3`, tenantID, conversationID, idempotencyKey,
			).Scan(&executionID)
		}
		if err != nil {
			return err
		}
		if inserted {
			if _, err := appendMessage(ctx, tx, tenantID, locationID, conversationID, "assistant", "J’ai préparé cette action. Vérifiez-la avant de confirmer : "+preview.Summary); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE assistant_conversations SET updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenantID, conversationID)
		return err
	})
	return executionID, err
}

func (s *Store) StartReadTool(
	ctx context.Context,
	tenantID string,
	userID string,
	conversationID string,
	userSequence int,
	callID string,
	definition assistanttools.Definition,
	preview assistanttools.Preview,
) (execution ToolExecution, err error) {
	previewJSON, err := json.Marshal(map[string]string{"summary": preview.Summary})
	if err != nil {
		return execution, err
	}
	idempotencyKey := fmt.Sprintf("message-%d:%s", userSequence, callID)
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		locationID, err := lockConversation(ctx, tx, tenantID, userID, conversationID)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO assistant_tool_executions (
			    tenant_id, location_id, conversation_id, requested_by_user_id,
			    idempotency_key, tool_name, consequence, status, input, preview
			) VALUES ($1, $2, $3, $4, $5, $6, 'read', 'running', $7, $8)
			ON CONFLICT (tenant_id, conversation_id, idempotency_key) DO NOTHING
			RETURNING id::text`, tenantID, locationID, conversationID, userID,
			idempotencyKey, definition.Name, preview.Input, previewJSON,
		).Scan(&execution.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return tx.QueryRow(ctx, `
				SELECT id::text, conversation_id::text, location_id::text, tool_name,
				       consequence, status, input, preview->>'summary',
				       COALESCE(output, '{}'::jsonb), COALESCE(error_message, ''),
				       proposed_at, confirmed_at, completed_at
				FROM assistant_tool_executions
				WHERE tenant_id = $1 AND conversation_id = $2
				  AND idempotency_key = $3`, tenantID, conversationID, idempotencyKey,
			).Scan(
				&execution.ID, &execution.ConversationID, &execution.LocationID,
				&execution.ToolName, &execution.Consequence, &execution.Status,
				&execution.Input, &execution.PreviewSummary, &execution.Output,
				&execution.ErrorMessage, &execution.ProposedAt,
				&execution.ConfirmedAt, &execution.CompletedAt,
			)
		}
		if err != nil {
			return err
		}
		execution.ConversationID = conversationID
		execution.LocationID = locationID
		execution.ToolName = definition.Name
		execution.Consequence = assistanttools.ConsequenceRead
		execution.Status = "running"
		execution.Input = preview.Input
		execution.PreviewSummary = preview.Summary
		return nil
	})
	return execution, err
}

func (s *Store) BeginExecution(
	ctx context.Context,
	tenantID string,
	userID string,
	conversationID string,
	executionID string,
) (execution ToolExecution, shouldExecute bool, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if _, err := lockConversation(ctx, tx, tenantID, userID, conversationID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT id::text, conversation_id::text, location_id::text, tool_name,
			       consequence, status, input, preview->>'summary',
			       COALESCE(output, '{}'::jsonb), COALESCE(error_message, ''),
			       proposed_at, confirmed_at, completed_at
			FROM assistant_tool_executions
			WHERE tenant_id = $1 AND conversation_id = $2 AND id = $3
			  AND requested_by_user_id = $4
			FOR UPDATE`, tenantID, conversationID, executionID, userID,
		).Scan(
			&execution.ID, &execution.ConversationID, &execution.LocationID,
			&execution.ToolName, &execution.Consequence, &execution.Status,
			&execution.Input, &execution.PreviewSummary, &execution.Output,
			&execution.ErrorMessage, &execution.ProposedAt,
			&execution.ConfirmedAt, &execution.CompletedAt,
		); err != nil {
			return err
		}
		switch execution.Status {
		case "proposed":
			if _, err := tx.Exec(ctx, `
				UPDATE assistant_tool_executions
				SET status = 'running', confirmed_at = now()
				WHERE tenant_id = $1 AND id = $2`, tenantID, executionID); err != nil {
				return err
			}
			execution.Status = "running"
			shouldExecute = true
		case "running":
			// Retrying an interrupted confirmation is safe because application
			// tools receive the durable execution id and must be idempotent.
			shouldExecute = true
		default:
			shouldExecute = false
		}
		return nil
	})
	return execution, shouldExecute, err
}

func (s *Store) FinishExecution(
	ctx context.Context,
	tenantID string,
	userID string,
	execution ToolExecution,
	result assistanttools.Result,
	executionErr error,
) error {
	output := result.Output
	if len(output) == 0 {
		output = json.RawMessage(`{}`)
	}
	affected, err := json.Marshal(result.AffectedRecords)
	if err != nil {
		return err
	}
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		locationID, err := lockConversation(ctx, tx, tenantID, userID, execution.ConversationID)
		if err != nil {
			return err
		}
		status := "succeeded"
		message := result.Summary
		errorCode, errorMessage := "", ""
		if executionErr != nil {
			status = "failed"
			message = "L’action n’a pas été effectuée : " + safeToolError(executionErr)
			errorCode, errorMessage = toolErrorDetails(executionErr)
		}
		result, err := tx.Exec(ctx, `
			UPDATE assistant_tool_executions SET
			    status = $4, output = $5,
			    affected_records = CASE WHEN $4 = 'failed' THEN affected_records ELSE $6 END,
			    error_code = NULLIF($7, ''), error_message = NULLIF($8, ''),
			    completed_at = now()
			WHERE tenant_id = $1 AND conversation_id = $2 AND id = $3
			  AND status = 'running'`, tenantID, execution.ConversationID,
			execution.ID, status, output, affected, errorCode, errorMessage)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return ErrExecutionClosed
		}
		if _, err := appendMessage(ctx, tx, tenantID, locationID, execution.ConversationID, "assistant", message); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE assistant_conversations SET updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenantID, execution.ConversationID)
		return err
	})
}

func (s *Store) RejectExecution(
	ctx context.Context,
	tenantID string,
	userID string,
	conversationID string,
	executionID string,
) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		locationID, err := lockConversation(ctx, tx, tenantID, userID, conversationID)
		if err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `
			UPDATE assistant_tool_executions
			SET status = 'rejected', completed_at = now()
			WHERE tenant_id = $1 AND conversation_id = $2 AND id = $3
			  AND status = 'proposed'`, tenantID, conversationID, executionID)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return ErrExecutionClosed
		}
		if _, err := appendMessage(ctx, tx, tenantID, locationID, conversationID, "assistant", "L’action proposée a été abandonnée. Aucune donnée n’a été modifiée."); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE assistant_conversations SET updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenantID, conversationID)
		return err
	})
}

func scanConversation(scanner interface{ Scan(...any) error }, conversation *Conversation) error {
	return scanner.Scan(
		&conversation.ID, &conversation.LocationID, &conversation.Location,
		&conversation.Title, &conversation.Status, &conversation.UpdatedAt,
	)
}

func lockConversation(ctx context.Context, tx pgx.Tx, tenantID, userID, conversationID string) (string, error) {
	var locationID string
	err := tx.QueryRow(ctx, `
		SELECT location_id::text
		FROM assistant_conversations
		WHERE tenant_id = $1 AND id = $2 AND created_by_user_id = $3
		  AND status = 'active'
		FOR UPDATE`, tenantID, conversationID, userID).Scan(&locationID)
	return locationID, err
}

func appendMessage(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	locationID string,
	conversationID string,
	role string,
	content string,
) (int, error) {
	var sequence int
	err := tx.QueryRow(ctx, `
		INSERT INTO assistant_messages (
		    tenant_id, location_id, conversation_id, sequence, role, content
		)
		SELECT $1, $2, $3, COALESCE(max(sequence), -1) + 1, $4, $5
		FROM assistant_messages
		WHERE tenant_id = $1 AND conversation_id = $3
		RETURNING sequence`, tenantID, locationID, conversationID, role, strings.TrimSpace(content)).Scan(&sequence)
	return sequence, err
}

func loadMessages(ctx context.Context, tx pgx.Tx, tenantID, conversationID string) ([]Message, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, sequence, role, content, created_at
		FROM assistant_messages
		WHERE tenant_id = $1 AND conversation_id = $2
		ORDER BY sequence`, tenantID, conversationID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Message])
}

func loadExecutions(ctx context.Context, tx pgx.Tx, tenantID, conversationID string) ([]ToolExecution, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, conversation_id::text, location_id::text, tool_name,
		       consequence, status, input, preview->>'summary',
		       COALESCE(output, '{}'::jsonb), COALESCE(error_message, ''),
		       proposed_at, confirmed_at, completed_at
		FROM assistant_tool_executions
		WHERE tenant_id = $1 AND conversation_id = $2
		ORDER BY proposed_at, id`, tenantID, conversationID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[ToolExecution])
}

func conversationTitle(content string) string {
	content = strings.Join(strings.Fields(content), " ")
	const maximum = 80
	if utf8.RuneCountInString(content) <= maximum {
		return content
	}
	runes := []rune(content)
	return strings.TrimSpace(string(runes[:maximum-1])) + "…"
}

func safeToolError(err error) string {
	var toolErr *assistanttools.ToolError
	if errors.As(err, &toolErr) && toolErr.Message != "" {
		return toolErr.Message
	}
	return "une erreur technique empêche cette modification. Vous pouvez réessayer."
}

func toolErrorDetails(err error) (string, string) {
	var toolErr *assistanttools.ToolError
	if errors.As(err, &toolErr) {
		return toolErr.Code, toolErr.Message
	}
	return "internal", "tool execution failed"
}
