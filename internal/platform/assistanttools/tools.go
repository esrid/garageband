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
// rules into the composition root or letting features import one another.
type Registry struct {
	executors   []Executor
	definitions map[string]Definition
}

func NewRegistry(executors ...Executor) *Registry {
	registry := &Registry{
		executors: executors, definitions: make(map[string]Definition),
	}
	for _, executor := range executors {
		for _, definition := range executor.Definitions() {
			if definition.Name != "" {
				registry.definitions[definition.Name] = definition
			}
		}
	}
	return registry
}

func (r *Registry) Definitions() []Definition {
	definitions := make([]Definition, 0, len(r.definitions))
	for _, executor := range r.executors {
		for _, definition := range executor.Definitions() {
			if _, exists := r.definitions[definition.Name]; exists {
				definitions = append(definitions, definition)
			}
		}
	}
	return definitions
}

func (r *Registry) Definition(name string) (Definition, bool) {
	definition, ok := r.definitions[name]
	return definition, ok
}

func (r *Registry) Preview(
	ctx context.Context,
	scope Scope,
	name string,
	input json.RawMessage,
) (Preview, error) {
	for _, executor := range r.executors {
		if executorOwns(executor, name) {
			return executor.Preview(ctx, scope, name, input)
		}
	}
	return Preview{}, ErrUnknownTool
}

func (r *Registry) Execute(
	ctx context.Context,
	scope Scope,
	name string,
	input json.RawMessage,
) (Result, error) {
	for _, executor := range r.executors {
		if executorOwns(executor, name) {
			return executor.Execute(ctx, scope, name, input)
		}
	}
	return Result{}, ErrUnknownTool
}

func executorOwns(executor Executor, name string) bool {
	for _, definition := range executor.Definitions() {
		if definition.Name == name {
			return true
		}
	}
	return false
}
