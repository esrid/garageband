// Package assistanttools defines the narrow application-tool port shared by
// employee chat and, later, telephone and messaging agents. Tools receive a
// trusted scope from the application; model-provided JSON cannot choose a
// tenant, user, or location.
package assistanttools

import (
	"context"
	"encoding/json"
	"errors"
)

const (
	ConsequenceRead        = "read"
	ConsequenceWrite       = "write"
	ConsequenceDestructive = "destructive"
)

var ErrUnknownTool = errors.New("unknown assistant tool")

type Scope struct {
	TenantID       string
	UserID         string
	LocationID     string
	IdempotencyKey string
}

type Definition struct {
	Name                 string
	Description          string
	InputSchema          json.RawMessage
	Consequence          string
	ConfirmationRequired bool
}

type AffectedRecord struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type Preview struct {
	Summary         string
	Input           json.RawMessage
	AffectedRecords []AffectedRecord
}

type Result struct {
	Summary         string
	Output          json.RawMessage
	AffectedRecords []AffectedRecord
}

type ToolError struct {
	Code    string
	Field   string
	Message string
}

func (e *ToolError) Error() string { return e.Message }

type Executor interface {
	Definitions() []Definition
	Preview(context.Context, Scope, string, json.RawMessage) (Preview, error)
	Execute(context.Context, Scope, string, json.RawMessage) (Result, error)
}

// Registry combines feature-owned executors without moving their business
// rules into the composition root or letting features import one another. It
// is the single answer to "what is this tool, and who runs it": both come from
// the same entry, so what the model is shown can never describe one tool while
// another one runs.
type Registry struct {
	tools map[string]registered
	names []string // registration order, so listings stay stable
}

type registered struct {
	definition Definition
	executor   Executor
}

// NewRegistry indexes every executor's tools by name. Two executors claiming
// the same name is a wiring mistake with no sane resolution - the description
// shown and the code run would come from different features - so it stops the
// process at boot rather than surfacing as a wrong answer later. Same call
// that http.ServeMux makes for a duplicate route pattern.
func NewRegistry(executors ...Executor) *Registry {
	registry := &Registry{tools: make(map[string]registered)}
	for _, executor := range executors {
		for _, definition := range executor.Definitions() {
			if definition.Name == "" {
				continue
			}
			if _, duplicate := registry.tools[definition.Name]; duplicate {
				panic("assistanttools: two executors registered the tool " + definition.Name)
			}
			registry.tools[definition.Name] = registered{
				definition: definition, executor: executor,
			}
			registry.names = append(registry.names, definition.Name)
		}
	}
	return registry
}

func (r *Registry) Definitions() []Definition {
	definitions := make([]Definition, 0, len(r.names))
	for _, name := range r.names {
		definitions = append(definitions, r.tools[name].definition)
	}
	return definitions
}

func (r *Registry) Definition(name string) (Definition, bool) {
	tool, ok := r.tools[name]
	return tool.definition, ok
}

func (r *Registry) Preview(
	ctx context.Context,
	scope Scope,
	name string,
	input json.RawMessage,
) (Preview, error) {
	tool, ok := r.tools[name]
	if !ok {
		return Preview{}, ErrUnknownTool
	}
	return tool.executor.Preview(ctx, scope, name, input)
}

func (r *Registry) Execute(
	ctx context.Context,
	scope Scope,
	name string,
	input json.RawMessage,
) (Result, error) {
	tool, ok := r.tools[name]
	if !ok {
		return Result{}, ErrUnknownTool
	}
	return tool.executor.Execute(ctx, scope, name, input)
}
