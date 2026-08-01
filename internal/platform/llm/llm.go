// Package llm defines the language-model port used by the conversation
// orchestrator.
package llm

import (
	"context"
	"encoding/json"
	"io"
)

type Message struct {
	Role       string
	Content    string
	ToolCallID string
}

type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type Request struct {
	SystemPrompt string
	Messages     []Message
	Tools        []Tool
	Temperature  float64
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type Chunk struct {
	Text      string
	ToolCalls []ToolCall
	Done      bool
}

type Stream interface {
	Recv() (Chunk, error)
	Close() error
}

type Provider interface {
	Stream(ctx context.Context, request Request) (Stream, error)
}

// EndOfStream lets adapters expose the standard streaming sentinel without
// leaking provider-specific errors.
var EndOfStream = io.EOF
