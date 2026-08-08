package assistanttools_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/esrid/garageband/internal/platform/assistanttools"
)

// stubExecutor is one feature's worth of tools: it answers with its own name,
// so a dispatch that reaches the wrong owner is visible rather than plausible.
type stubExecutor struct {
	owner string
	names []string
}

func (e stubExecutor) Definitions() []assistanttools.Definition {
	definitions := make([]assistanttools.Definition, 0, len(e.names))
	for _, name := range e.names {
		definitions = append(definitions, assistanttools.Definition{
			Name:        name,
			Description: "owned by " + e.owner,
			Consequence: assistanttools.ConsequenceRead,
		})
	}
	return definitions
}

func (e stubExecutor) Preview(
	_ context.Context, _ assistanttools.Scope, name string, _ json.RawMessage,
) (assistanttools.Preview, error) {
	return assistanttools.Preview{Summary: e.owner + ":" + name}, nil
}

func (e stubExecutor) Execute(
	_ context.Context, _ assistanttools.Scope, name string, _ json.RawMessage,
) (assistanttools.Result, error) {
	return assistanttools.Result{Summary: e.owner + ":" + name}, nil
}

func TestRegistryDispatchesEachToolToItsOwner(t *testing.T) {
	registry := assistanttools.NewRegistry(
		stubExecutor{owner: "agenda", names: []string{"book", "cancel"}},
		stubExecutor{owner: "customers", names: []string{"correct"}},
	)

	names := make([]string, 0, 3)
	for _, definition := range registry.Definitions() {
		names = append(names, definition.Name)
	}
	if !slices.Equal(names, []string{"book", "cancel", "correct"}) {
		t.Fatalf("definitions = %v, want them once each in registration order", names)
	}

	for name, owner := range map[string]string{
		"book": "agenda", "cancel": "agenda", "correct": "customers",
	} {
		result, err := registry.Execute(t.Context(), assistanttools.Scope{}, name, nil)
		if err != nil || result.Summary != owner+":"+name {
			t.Fatalf("execute %q = %q %v, want %q", name, result.Summary, err, owner+":"+name)
		}
		preview, err := registry.Preview(t.Context(), assistanttools.Scope{}, name, nil)
		if err != nil || preview.Summary != owner+":"+name {
			t.Fatalf("preview %q = %q %v, want %q", name, preview.Summary, err, owner+":"+name)
		}
		// What the model is shown must come from the same entry that runs.
		definition, ok := registry.Definition(name)
		if !ok || definition.Description != "owned by "+owner {
			t.Fatalf("definition %q = %+v ok %v", name, definition, ok)
		}
	}
}

func TestRegistryRefusesAnUnknownTool(t *testing.T) {
	registry := assistanttools.NewRegistry(stubExecutor{owner: "agenda", names: []string{"book"}})
	if _, err := registry.Execute(
		t.Context(), assistanttools.Scope{}, "invented", nil,
	); !errors.Is(err, assistanttools.ErrUnknownTool) {
		t.Fatalf("execute unknown = %v, want ErrUnknownTool", err)
	}
	if _, err := registry.Preview(
		t.Context(), assistanttools.Scope{}, "invented", nil,
	); !errors.Is(err, assistanttools.ErrUnknownTool) {
		t.Fatalf("preview unknown = %v, want ErrUnknownTool", err)
	}
	if _, ok := registry.Definition("invented"); ok {
		t.Fatal("an unknown tool has a definition")
	}
}

// TestRegistryStopsAtBootOnADuplicateName pins the failure that used to be
// silent: the name resolved to one feature's description and another
// feature's code, so the model was told one thing and something else ran.
func TestRegistryStopsAtBootOnADuplicateName(t *testing.T) {
	defer func() {
		recovered := recover()
		message, ok := recovered.(string)
		if !ok || message == "" {
			t.Fatalf("recovered %v, want a panic naming the duplicate tool", recovered)
		}
	}()
	assistanttools.NewRegistry(
		stubExecutor{owner: "agenda", names: []string{"book"}},
		stubExecutor{owner: "customers", names: []string{"book"}},
	)
	t.Fatal("two executors registered the same tool name without stopping")
}
