package agents

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/a-h/templ"
)

// TestWritePreview dumps the agent screens as static HTML for visual and
// accessibility review. It is a tool, not an assertion: it only runs when
// GARAGEBAND_PREVIEW_DIR points somewhere. The sample agents exist for this
// preview and for the view tests — never for production code.
func TestWritePreview(t *testing.T) {
	dir := os.Getenv("GARAGEBAND_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set GARAGEBAND_PREVIEW_DIR to write preview pages")
	}

	gerland := Agent{
		ID: "ag1", Name: "Accueil Gerland", LocationName: "Atelier Gerland",
		Status: StatusActive, Numbers: []string{"+33472000000"},
	}
	villeurbanne := Agent{
		ID: "ag2", Name: "Accueil Villeurbanne", LocationName: "Atelier Villeurbanne",
		Status: StatusDraft, Missing: []string{KindLLM, KindSTT, KindTTS},
	}
	vaise := Agent{
		ID: "ag3", Name: "Accueil Vaise", LocationName: "Atelier Vaise",
		Status: StatusPaused, Numbers: []string{"+33478000000"},
	}

	configured := FormPage{
		ID: "ag1", Organization: "Garage Central", LocationName: "Atelier Gerland",
		Status: StatusActive, CanManage: true, Numbers: []string{"+33472000000"},
		Values: FormValues{
			Name:     "Accueil Gerland",
			Greeting: "Garage Central, bonjour. Que puis-je faire pour vous ?",
			Fallback: "Je préfère vous passer un collègue, il vous rappellera dans la journée.",
			Prompt: "Tu réponds au téléphone d'un garage à Lyon.\n" +
				"Tu peux prendre, déplacer et annuler un rendez-vous.\n" +
				"Tu ne donnes jamais un prix qui n'est pas au catalogue : tu proposes un rappel.\n" +
				"Si le client insiste ou s'énerve, tu passes la main à un humain.",
			Locale: "fr-FR", LLM: "c1", STT: "c2", TTS: "c3",
		},
		Locales:        []Option{{Value: "fr-FR", Label: "Français"}, {Value: "en-GB", Label: "Anglais"}},
		LLMConnections: []Option{{Value: "c1", Label: "Modèle principal"}},
		STTConnections: []Option{{Value: "c2", Label: "Transcription temps réel"}},
		TTSConnections: []Option{{Value: "c3", Label: "Voix française"}},
	}
	notReady := configured
	notReady.ID, notReady.Status = "ag2", StatusDraft
	notReady.Values.Name = "Accueil Villeurbanne"
	notReady.LocationName = "Atelier Villeurbanne"
	notReady.Numbers = nil
	notReady.LLMConnections, notReady.TTSConnections = nil, nil

	paused := configured
	paused.Status = StatusPaused

	invalid := configured
	invalid.Notice = Notice{Kind: NoticeInvalid, Message: "Deux champs demandent une correction."}
	invalid.Values.Greeting, invalid.Values.Fallback = "", ""
	invalid.FieldErrors = map[string]string{
		FieldGreeting: "Écrivez ce que l'agent dit en décrochant.",
		FieldFallback: "Dites ce qu'il fait quand il ne sait pas répondre.",
	}

	readOnly := configured
	readOnly.CanManage = false

	pages := map[string]templ.Component{
		"agents.html":          List(Index{Organization: "Garage Central", CanManage: true, Agents: []Agent{gerland, villeurbanne, vaise}}),
		"agents-empty.html":    List(Index{Organization: "Garage Central", CanManage: true}),
		"agent-live.html":      Form(configured),
		"agent-paused.html":    Form(paused),
		"agent-not-ready.html": Form(notReady),
		"agent-invalid.html":   Form(invalid),
		"agent-readonly.html":  Form(readOnly),
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
