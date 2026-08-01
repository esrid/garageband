package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// DemonstrationProvider is an intentionally small local adapter used until a
// real model is selected. It recognizes only explicit contact-change requests;
// every resulting write still goes through preview and human confirmation.
type DemonstrationProvider struct{}

func NewDemonstrationProvider() *DemonstrationProvider { return &DemonstrationProvider{} }

var (
	demonstrationEmail = regexp.MustCompile(`(?i)[^\s@]+@[^\s@]+\.[^\s@]+`)
	demonstrationPhone = regexp.MustCompile(`\+[1-9][0-9]{7,14}`)
	demonstrationURL   = regexp.MustCompile(`https?://[^\s]+`)
)

func (p *DemonstrationProvider) Stream(_ context.Context, request Request) (Stream, error) {
	last := lastUserMessage(request.Messages)
	arguments := make(map[string]string)
	lower := strings.ToLower(last)
	if strings.Contains(lower, "mail") || strings.Contains(lower, "e-mail") {
		if value := demonstrationEmail.FindString(last); value != "" {
			arguments["email"] = strings.TrimRight(value, ".,;!?")
		}
	}
	if strings.Contains(lower, "téléphone") || strings.Contains(lower, "telephone") || strings.Contains(lower, "phone") {
		if value := demonstrationPhone.FindString(last); value != "" {
			arguments["phone_e164"] = value
		}
	}
	if strings.Contains(lower, "site web") || strings.Contains(lower, "website") || strings.Contains(lower, "url") {
		if value := demonstrationURL.FindString(last); value != "" {
			arguments["website_url"] = strings.TrimRight(value, ".,;!?")
		}
	}
	if len(arguments) != 0 && hasTool(request.Tools, "update_location_contact") {
		encoded, err := json.Marshal(arguments)
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256([]byte(last))
		return &sliceStream{chunks: []Chunk{{
			ToolCalls: []ToolCall{{
				ID:   "demo-" + hex.EncodeToString(hash[:8]),
				Name: "update_location_contact", Arguments: encoded,
			}},
			Done: true,
		}}}, nil
	}
	if (strings.Contains(lower, "prix") || strings.Contains(lower, "tarif") ||
		strings.Contains(lower, "combien coûte") || strings.Contains(lower, "combien coute")) &&
		hasTool(request.Tools, "search_catalog") {
		query := demonstrationCatalogQuery(last)
		encoded, err := json.Marshal(map[string]string{"query": query})
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256([]byte(last))
		return &sliceStream{chunks: []Chunk{{
			ToolCalls: []ToolCall{{
				ID:   "demo-" + hex.EncodeToString(hash[:8]),
				Name: "search_catalog", Arguments: encoded,
			}},
			Done: true,
		}}}, nil
	}
	return &sliceStream{chunks: []Chunk{{
		Text: "Le modèle de démonstration peut seulement préparer une modification explicite de l’e-mail, du téléphone international ou du site web de l’établissement sélectionné. Aucune action n’est effectuée sans votre confirmation.",
		Done: true,
	}}}, nil
}

func demonstrationCatalogQuery(message string) string {
	lower := strings.ToLower(message)
	markers := []string{
		"prix de la ", "prix du ", "prix de ",
		"tarif de la ", "tarif du ", "tarif de ",
		"combien coûte la ", "combien coûte le ", "combien coûte ",
		"combien coute la ", "combien coute le ", "combien coute ",
	}
	for _, marker := range markers {
		if index := strings.Index(lower, marker); index >= 0 {
			query := message[index+len(marker):]
			return strings.Trim(strings.TrimSpace(query), "?.!,;:\"'«»")
		}
	}
	return strings.Trim(strings.TrimSpace(message), "?.!,;:\"'«»")
}

func lastUserMessage(messages []Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "user" {
			return messages[index].Content
		}
	}
	return ""
}

func hasTool(tools []Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

type sliceStream struct {
	chunks []Chunk
	index  int
	closed bool
}

func (s *sliceStream) Recv() (Chunk, error) {
	if s.closed || s.index >= len(s.chunks) {
		return Chunk{}, EndOfStream
	}
	chunk := s.chunks[s.index]
	s.index++
	return chunk, nil
}

func (s *sliceStream) Close() error {
	if s.closed {
		return errors.New("language model stream already closed")
	}
	s.closed = true
	return nil
}
