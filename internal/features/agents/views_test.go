package agents

import (
	"bytes"
	"context"
	"html"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func render(t *testing.T, page templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	if err := page.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return html.UnescapeString(buf.String())
}

func mustContain(t *testing.T, page string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}

func mustNotContain(t *testing.T, page string, unwanted ...string) {
	t.Helper()
	for _, bad := range unwanted {
		if strings.Contains(page, bad) {
			t.Errorf("page should not contain %q", bad)
		}
	}
}

var live = Agent{
	ID: "ag1", Name: "Accueil Gerland", LocationName: "Atelier Gerland",
	Status: StatusActive, Numbers: []string{"+33472000000"},
}

func TestListSaysWhetherCustomersActuallyReachTheAgent(t *testing.T) {
	page := render(t, List(Index{
		Organization: "Garage Central", CanManage: true,
		Agents: []Agent{live},
	}))
	mustContain(t, page,
		"Agents téléphoniques", "1 agent", "Accueil Gerland", "Atelier Gerland",
		"En ligne", "+33472000000", "Vos clients joignent cet agent.",
		`href="/agents/ag1"`,
	)
}

// Active is not the same as reachable, and the three ways it can fail must not
// look alike: no providers, no number, or simply switched off.
func TestListDistinguishesTheWaysAnAgentIsUnreachable(t *testing.T) {
	notReady := live
	notReady.Missing = []string{KindLLM, KindTTS}
	page := render(t, List(Index{CanManage: true, Agents: []Agent{notReady}}))
	mustContain(t, page, "Pas encore opérationnel.", "le modèle de langage, la synthèse vocale")

	noNumber := live
	noNumber.Numbers = nil
	page = render(t, List(Index{CanManage: true, Agents: []Agent{noNumber}}))
	mustContain(t, page, "Aucune ligne ne le joint.", "Aucun numéro ne pointe vers cet agent")

	paused := live
	paused.Status = StatusPaused
	page = render(t, List(Index{CanManage: true, Agents: []Agent{paused}}))
	mustContain(t, page, "En pause", "Hors ligne.", "ne sont pas pris par l'agent")
}

func TestReachableNeedsAllThree(t *testing.T) {
	if !live.Reachable() {
		t.Error("an active, ready, reachable agent should be reachable")
	}
	for name, agent := range map[string]Agent{
		"paused":      {Status: StatusPaused, Numbers: []string{"+33"}},
		"no provider": {Status: StatusActive, Missing: []string{KindLLM}, Numbers: []string{"+33"}},
		"no number":   {Status: StatusActive},
	} {
		if agent.Reachable() {
			t.Errorf("%s should not be reachable", name)
		}
	}
}

func TestListEmptySendsYouToCreateASite(t *testing.T) {
	page := render(t, List(Index{Organization: "Garage Central", CanManage: true}))
	mustContain(t, page, "Aucun agent configuré", `href="/locations"`)
}

func TestListReadOnlyHidesConfiguration(t *testing.T) {
	page := render(t, List(Index{CanManage: false, Agents: []Agent{live}}))
	mustContain(t, page, "Accueil Gerland")
	mustNotContain(t, page, `href="/agents/ag1"`, "Configurer")
}

func formFixture() FormPage {
	return FormPage{
		ID: "ag1", Organization: "Garage Central", LocationName: "Atelier Gerland",
		Status: StatusPaused, CanManage: true, Numbers: []string{"+33472000000"},
		Values: FormValues{
			Name: "Accueil Gerland", Greeting: "Garage Central, bonjour.",
			Fallback: "Je vous passe un collègue.", Locale: "fr-FR",
			LLM: "c1", STT: "c2", TTS: "c3",
		},
		Locales:        []Option{{Value: "fr-FR", Label: "Français"}},
		LLMConnections: []Option{{Value: "c1", Label: "Fournisseur A"}},
		STTConnections: []Option{{Value: "c2", Label: "Fournisseur B"}},
		TTSConnections: []Option{{Value: "c3", Label: "Fournisseur C"}},
	}
}

func TestFormPostsEveryFieldTheBackendParses(t *testing.T) {
	page := render(t, Form(formFixture()))
	for _, field := range []string{
		FieldName, FieldGreeting, FieldPrompt, FieldFallback,
		FieldLocale, FieldLLM, FieldSTT, FieldTTS,
	} {
		if !strings.Contains(page, `name="`+field+`"`) {
			t.Errorf("form is missing the %q control", field)
		}
	}
	mustContain(t, page,
		`action="/agents/ag1"`, "Ce que vos clients entendent",
		"Garage Central, bonjour.", "Je vous passe un collègue.",
		// Ready and paused: the screen offers to put it on the line.
		"Mettre l'agent en ligne", `action="/agents/ag1/activate"`,
	)
	mustNotContain(t, page, "Mettre en pause")
}

// An agent with no providers connected cannot answer, whatever it is told to
// say. The screen must not offer to put it on the line.
func TestFormWithoutProvidersExplainsAndRefusesActivation(t *testing.T) {
	form := formFixture()
	form.LLMConnections = nil
	form.TTSConnections = nil
	page := render(t, Form(form))
	mustContain(t, page,
		"Cet agent ne peut pas encore répondre",
		"le modèle de langage, la synthèse vocale",
		// Preparing the wording is still useful, and the screen says so.
		"elles seront prêtes le jour où les fournisseurs seront connectés",
		"Aucun fournisseur connecté pour ce rôle",
		"décrocherait sans pouvoir parler",
	)
	mustNotContain(t, page, `action="/agents/ag1/activate"`, `name="llm_connection_id"`)
}

func TestFormOfALiveAgentOffersThePause(t *testing.T) {
	form := formFixture()
	form.Status = StatusActive
	page := render(t, Form(form))
	mustContain(t, page,
		"Mettre l'agent en pause", `action="/agents/ag1/pause"`,
		"btn-error", "restent intacts",
	)
	mustNotContain(t, page, `action="/agents/ag1/activate"`)
	// The lifecycle control sits outside the save form.
	save := page[strings.Index(page, `action="/agents/ag1"`):]
	if end := strings.Index(save, "</form>"); end >= 0 && strings.Contains(save[:end], "/pause") {
		t.Error("the pause control must not sit inside the save form")
	}
}

func TestFormValidationErrorsAreTiedToTheirFields(t *testing.T) {
	form := formFixture()
	form.Notice = Notice{Kind: NoticeInvalid, Message: "Deux champs demandent une correction."}
	form.FieldErrors = map[string]string{
		FieldGreeting: "Écrivez la phrase d'accueil.",
		FieldFallback: "Dites ce qu'il fait quand il ne sait pas.",
	}
	page := render(t, Form(form))
	mustContain(t, page,
		"Vérifiez les informations ci-dessous",
		"Écrivez la phrase d'accueil.", "Dites ce qu'il fait quand il ne sait pas.",
		`id="greeting-error"`, `id="fallback_message-error"`,
		"textarea-error",
	)
	if got := strings.Count(page, `aria-invalid="true"`); got != 2 {
		t.Errorf("aria-invalid=true count = %d, want 2", got)
	}
}

func TestFormReadOnly(t *testing.T) {
	form := formFixture()
	form.CanManage = false
	page := render(t, Form(form))
	mustContain(t, page, "lecture seule", "Accueil Gerland", "+33472000000")
	mustNotContain(t, page, `name="greeting"`, "Enregistrer", "Mettre en ligne")
}

func TestServerError(t *testing.T) {
	page := render(t, List(Index{
		Organization: "Garage Central",
		Notice:       Notice{Kind: NoticeError, Message: "Les agents n'ont pas pu être chargés."},
	}))
	mustContain(t, page, "Action impossible pour le moment", "alert-warning")
}

func TestLabelsUseTheOwnersVocabularyNotTheSchemas(t *testing.T) {
	if got := kindLabel(KindSTT); got != "la transcription de la voix" {
		t.Errorf("stt = %q", got)
	}
	if got := statusLabel(StatusActive); got != "En ligne" {
		t.Errorf("active = %q", got)
	}
	// An unknown value shows through rather than vanishing.
	if got := statusLabel("surprise"); got != "surprise" {
		t.Errorf("unknown = %q", got)
	}
	if got := numbersSummary(nil); got != "Aucun numéro ne pointe vers cet agent" {
		t.Errorf("no numbers = %q", got)
	}
	if got := agentSummary(3); got != "3 agents" {
		t.Errorf("plural = %q", got)
	}
}
