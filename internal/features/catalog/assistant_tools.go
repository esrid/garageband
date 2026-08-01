package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/esrid/garageband/internal/platform/assistanttools"
)

const ToolSearchCatalog = "search_catalog"

var searchCatalogSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {"type": "string", "minLength": 1, "maxLength": 160, "description": "Service, product, package, reference, or price topic to find"}
  },
  "required": ["query"],
  "additionalProperties": false
}`)

type searchCatalogInput struct {
	Query string `json:"query"`
}

func (s *Store) Definitions() []assistanttools.Definition {
	return []assistanttools.Definition{{
		Name:                 ToolSearchCatalog,
		Description:          "Find currently effective catalog prices that may be quoted at the scoped location.",
		InputSchema:          searchCatalogSchema,
		Consequence:          assistanttools.ConsequenceRead,
		ConfirmationRequired: false,
	}}
}

func (s *Store) Preview(
	ctx context.Context,
	scope assistanttools.Scope,
	name string,
	input json.RawMessage,
) (assistanttools.Preview, error) {
	if name != ToolSearchCatalog {
		return assistanttools.Preview{}, assistanttools.ErrUnknownTool
	}
	parsed, canonical, err := parseSearchCatalogInput(input)
	if err != nil {
		return assistanttools.Preview{}, err
	}
	if _, err := s.Quotable(
		ctx, scope.TenantID, scope.UserID, scope.LocationID, parsed.Query, time.Now().UTC(),
	); err != nil {
		return assistanttools.Preview{}, mapCatalogToolError(err)
	}
	return assistanttools.Preview{
		Summary: "Rechercher dans le catalogue actif : « " + parsed.Query + " ».",
		Input:   canonical,
	}, nil
}

func (s *Store) Execute(
	ctx context.Context,
	scope assistanttools.Scope,
	name string,
	input json.RawMessage,
) (assistanttools.Result, error) {
	if name != ToolSearchCatalog {
		return assistanttools.Result{}, assistanttools.ErrUnknownTool
	}
	parsed, _, err := parseSearchCatalogInput(input)
	if err != nil {
		return assistanttools.Result{}, err
	}
	items, err := s.Quotable(
		ctx, scope.TenantID, scope.UserID, scope.LocationID, parsed.Query, time.Now().UTC(),
	)
	if err != nil {
		return assistanttools.Result{}, mapCatalogToolError(err)
	}
	output, err := json.Marshal(map[string]any{"query": parsed.Query, "items": items})
	if err != nil {
		return assistanttools.Result{}, err
	}
	affected := make([]assistanttools.AffectedRecord, 0, len(items))
	for _, item := range items {
		affected = append(affected, assistanttools.AffectedRecord{Kind: "catalog_item", ID: item.ID})
	}
	return assistanttools.Result{
		Summary: formatCatalogToolAnswer(parsed.Query, items),
		Output:  output, AffectedRecords: affected,
	}, nil
}

func parseSearchCatalogInput(input json.RawMessage) (searchCatalogInput, json.RawMessage, error) {
	var parsed searchCatalogInput
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return parsed, nil, catalogToolArgumentsError(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return parsed, nil, catalogToolArgumentsError(err)
	}
	parsed.Query = strings.TrimSpace(parsed.Query)
	if parsed.Query == "" || utf8.RuneCountInString(parsed.Query) > 160 {
		return parsed, nil, catalogToolArgumentsError(nil)
	}
	canonical, err := json.Marshal(parsed)
	if err != nil {
		return parsed, nil, err
	}
	return parsed, canonical, nil
}

func catalogToolArgumentsError(cause error) error {
	toolErr := &assistanttools.ToolError{
		Code: "invalid_arguments", Field: "query",
		Message: "Précisez une prestation, un produit ou une référence à rechercher.",
	}
	if cause == nil {
		return toolErr
	}
	return errors.Join(toolErr, cause)
}

func mapCatalogToolError(err error) error {
	if errors.Is(err, ErrForbidden) {
		return &assistanttools.ToolError{
			Code: "forbidden", Message: "Vous n’avez pas accès au catalogue de cet établissement.",
		}
	}
	return err
}

func formatCatalogToolAnswer(query string, items []CatalogItemRecord) string {
	if len(items) == 0 {
		return "Je n’ai trouvé aucun prix actuellement applicable pour « " + query + " » dans cet établissement."
	}
	lines := make([]string, 0, len(items)+1)
	lines = append(lines, "Prix actuellement applicables :")
	for _, item := range items {
		line := "• " + item.Name + " — " + catalogToolPrice(item)
		if item.DurationMinutes != nil {
			line += fmt.Sprintf(" · %d min", *item.DurationMinutes)
		}
		if item.Reference != "" {
			line += " · réf. " + item.Reference
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func catalogToolPrice(item CatalogItemRecord) string {
	amount := func(value *int64) string {
		if value == nil {
			return ""
		}
		return fmt.Sprintf("%d,%02d €", *value/100, *value%100)
	}
	var label string
	switch item.PriceKind {
	case PriceFixed:
		label = amount(item.AmountCents)
	case PriceFrom:
		label = "à partir de " + amount(item.AmountCents)
	case PriceRange:
		label = amount(item.AmountCents) + " à " + amount(item.MaxAmountCents)
	default:
		return "sur devis"
	}
	if item.TaxBasis == TaxExclusive {
		label += " HT"
	} else {
		label += " TTC"
	}
	if item.PerHour {
		label += " / h"
	}
	return label
}
