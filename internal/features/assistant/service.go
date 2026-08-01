package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/esrid/garageband/internal/platform/assistanttools"
	"github.com/esrid/garageband/internal/platform/llm"
)

type Service struct {
	store *Store
	model llm.Provider
	tools *assistanttools.Registry
}

func NewService(store *Store, model llm.Provider, tools *assistanttools.Registry) *Service {
	return &Service{store: store, model: model, tools: tools}
}

func (s *Service) Send(
	ctx context.Context,
	tenantID string,
	userID string,
	conversationID string,
	locationID string,
	content string,
) (string, error) {
	conversation, messages, userSequence, err := s.store.AppendUserMessage(
		ctx, tenantID, userID, conversationID, locationID, content,
	)
	if err != nil {
		return "", err
	}
	request := llm.Request{
		SystemPrompt: "You are Garageband's employee operations assistant. Use only the explicit tools provided. Never invent data, tenant ids, location ids, permissions, prices, or completed changes. A write tool only creates a preview; the employee confirms it separately.",
		Messages:     modelMessages(messages),
		Tools:        modelTools(s.tools.Definitions()),
		Temperature:  0,
	}
	stream, err := s.model.Stream(ctx, request)
	if err != nil {
		return conversation.ID, err
	}
	text, calls, receiveErr := receiveModel(stream)
	if receiveErr != nil {
		return conversation.ID, receiveErr
	}
	if strings.TrimSpace(text) != "" {
		if err := s.store.AppendAssistantMessage(ctx, tenantID, userID, conversation.ID, text); err != nil {
			return conversation.ID, err
		}
	}
	if len(calls) == 0 {
		return conversation.ID, nil
	}
	// One proposal at a time keeps confirmation unambiguous. A future model
	// adapter may continue after the employee resolves this proposal.
	call := calls[0]
	definition, ok := s.tools.Definition(call.Name)
	if !ok {
		if err := s.store.AppendAssistantMessage(ctx, tenantID, userID, conversation.ID, "Je ne peux pas utiliser l’action demandée : elle ne fait pas partie des outils autorisés."); err != nil {
			return conversation.ID, err
		}
		return conversation.ID, nil
	}
	preview, err := s.tools.Preview(ctx, assistanttools.Scope{
		TenantID: tenantID, UserID: userID, LocationID: conversation.LocationID,
	}, call.Name, call.Arguments)
	if err != nil {
		if appendErr := s.store.AppendAssistantMessage(
			ctx, tenantID, userID, conversation.ID,
			"Je ne peux pas préparer cette action : "+safeToolError(err),
		); appendErr != nil {
			return conversation.ID, errors.Join(err, appendErr)
		}
		return conversation.ID, nil
	}
	if !definition.ConfirmationRequired {
		return conversation.ID, errors.New("assistant tool without confirmation is not supported yet")
	}
	callID := call.ID
	if callID == "" {
		callID = fmt.Sprintf("tool-%d", userSequence)
	}
	_, err = s.store.ProposeTool(
		ctx, tenantID, userID, conversation.ID, userSequence,
		callID, definition, preview,
	)
	return conversation.ID, err
}

func (s *Service) Confirm(
	ctx context.Context,
	tenantID string,
	userID string,
	conversationID string,
	executionID string,
) error {
	execution, shouldExecute, err := s.store.BeginExecution(
		ctx, tenantID, userID, conversationID, executionID,
	)
	if err != nil || !shouldExecute {
		return err
	}
	result, executionErr := s.tools.Execute(ctx, assistanttools.Scope{
		TenantID: tenantID, UserID: userID, LocationID: execution.LocationID,
		IdempotencyKey: execution.ID,
	}, execution.ToolName, execution.Input)
	return s.store.FinishExecution(
		ctx, tenantID, userID, execution, result, executionErr,
	)
}

func (s *Service) Reject(
	ctx context.Context,
	tenantID string,
	userID string,
	conversationID string,
	executionID string,
) error {
	return s.store.RejectExecution(ctx, tenantID, userID, conversationID, executionID)
}

func modelMessages(messages []Message) []llm.Message {
	result := make([]llm.Message, 0, len(messages))
	for _, message := range messages {
		if message.Role == "user" || message.Role == "assistant" {
			result = append(result, llm.Message{Role: message.Role, Content: message.Content})
		}
	}
	return result
}

func modelTools(definitions []assistanttools.Definition) []llm.Tool {
	tools := make([]llm.Tool, 0, len(definitions))
	for _, definition := range definitions {
		tools = append(tools, llm.Tool{
			Name: definition.Name, Description: definition.Description,
			InputSchema: definition.InputSchema,
		})
	}
	return tools
}

func receiveModel(stream llm.Stream) (text string, calls []llm.ToolCall, err error) {
	defer func() { err = errors.Join(err, stream.Close()) }()
	var builder strings.Builder
	for {
		chunk, receiveErr := stream.Recv()
		if errors.Is(receiveErr, llm.EndOfStream) {
			break
		}
		if receiveErr != nil {
			return "", nil, receiveErr
		}
		builder.WriteString(chunk.Text)
		calls = append(calls, chunk.ToolCalls...)
		if chunk.Done {
			break
		}
	}
	return builder.String(), calls, nil
}
