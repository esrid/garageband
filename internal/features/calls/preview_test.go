package calls

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/a-h/templ"
)

// TestWritePreview dumps the call screens as static HTML for visual and
// accessibility review. It is a tool, not an assertion: it only runs when
// GARAGEBAND_PREVIEW_DIR points somewhere. The sample calls exist for this
// preview and for the view tests — never for production code.
func TestWritePreview(t *testing.T) {
	dir := os.Getenv("GARAGEBAND_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set GARAGEBAND_PREVIEW_DIR to write preview pages")
	}
	moment := func(hour, minute, second int) time.Time {
		return time.Date(2026, 3, 12, hour, minute, second, 0, time.UTC)
	}

	booked := Call{
		ID: "k1", StartedAt: moment(9, 12, 0), EndedAt: moment(9, 14, 35),
		Direction: "inbound", Status: "completed", CallerNumber: "+33612345678",
		CustomerID: "c1", CustomerName: "Claire Dupont", LocationName: "Atelier Gerland",
		Summary: "Signale un bruit au freinage. Rendez-vous pris jeudi à 8 h 30 sur le Pont 1.",
		Outcome: "Rendez-vous pris", HasRecording: true,
	}
	unknown := Call{
		ID: "k2", StartedAt: moment(10, 3, 0), EndedAt: moment(10, 5, 12),
		Direction: "inbound", Status: "completed", CallerNumber: "+33698765432",
		LocationName: "Atelier Gerland",
		Summary:      "Demande le prix d'une distribution. L'agent n'a pas su rattacher le correspondant.",
	}
	missed := Call{
		ID: "k3", StartedAt: moment(12, 41, 0), EndedAt: moment(12, 41, 18),
		Direction: "inbound", Status: "no_answer", CallerNumber: "+33677889900",
		LocationName: "Atelier Gerland",
	}

	transcript := Transcript{
		Organization: "Garage Central", Call: booked,
		Messages: []Message{
			{Speaker: SpeakerSystem, Content: "Appel enregistré. Le correspondant en a été informé.", OccurredAt: moment(9, 12, 2)},
			{Speaker: SpeakerAgent, Content: "Garage Central, bonjour. Que puis-je faire pour vous ?", OccurredAt: moment(9, 12, 5)},
			{Speaker: SpeakerCaller, Content: "Bonjour, j'ai un bruit au freinage depuis deux semaines sur ma Clio.", OccurredAt: moment(9, 12, 14)},
			{Speaker: SpeakerTool, Content: "Recherche du client par numéro — Claire Dupont, AB-123-CD (Renault Clio)", OccurredAt: moment(9, 12, 18)},
			{Speaker: SpeakerAgent, Content: "Je vois votre Clio, madame Dupont. Je regarde nos disponibilités.", OccurredAt: moment(9, 12, 24)},
			{Speaker: SpeakerTool, Content: "Recherche de disponibilité — Pont 1, jeudi 12 mars 08:30", OccurredAt: moment(9, 12, 31)},
			{Speaker: SpeakerAgent, Content: "Jeudi matin à 8 h 30, cela vous convient ?", OccurredAt: moment(9, 12, 38)},
			{Speaker: SpeakerCaller, Content: "Oui, très bien.", OccurredAt: moment(9, 13, 2)},
			{Speaker: SpeakerTool, Content: "Rendez-vous créé — jeudi 12 mars 08:30, Pont 1", OccurredAt: moment(9, 13, 9)},
			{Speaker: SpeakerAgent, Content: "C'est noté. À jeudi, bonne journée.", OccurredAt: moment(9, 13, 15)},
		},
	}

	pages := map[string]templ.Component{
		"calls.html":           Index(Inbox{Organization: "Garage Central", Calls: []Call{booked, unknown, missed}}),
		"calls-attention.html": Index(Inbox{Organization: "Garage Central", Calls: []Call{unknown, missed}, Filter: FilterNeedsAttention}),
		"calls-empty.html":     Index(Inbox{Organization: "Garage Central"}),
		"calls-clear.html":     Index(Inbox{Organization: "Garage Central", Filter: FilterNeedsAttention}),
		"call-transcript.html": Show(transcript),
		"call-unknown.html":    Show(Transcript{Organization: "Garage Central", Call: missed}),
	}
	for name, page := range pages {
		file, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := page.Render(context.Background(), file); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
