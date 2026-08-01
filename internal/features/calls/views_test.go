package calls

import (
	"bytes"
	"context"
	"html"
	"strings"
	"testing"
	"time"

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

func at(hour, minute, second int) time.Time {
	return time.Date(2026, 3, 12, hour, minute, second, 0, time.UTC)
}

var answered = Call{
	ID: "k1", StartedAt: at(9, 12, 0), EndedAt: at(9, 14, 35),
	Direction: "inbound", Status: "completed",
	CallerNumber: "+33612345678", CustomerID: "c1", CustomerName: "Claire Dupont",
	LocationName: "Atelier Gerland",
	Summary:      "Demande un rendez-vous pour un bruit au freinage.",
	Outcome:      "Rendez-vous pris", HasRecording: true,
}

var missed = Call{
	ID: "k2", StartedAt: at(10, 3, 0), EndedAt: at(10, 3, 20),
	Direction: "inbound", Status: "no_answer", CallerNumber: "+33698765432",
	LocationName: "Atelier Gerland",
}

func TestInboxShowsWhatHappened(t *testing.T) {
	page := render(t, Index(Inbox{Organization: "Garage Central", Calls: []Call{answered, missed}}))
	mustContain(t, page,
		"Appels", "2 appels", "Claire Dupont", "Entrant", "12 mars 2026", "09:12",
		"2 min 35 s", "Terminé", "Demande un rendez-vous", "Enregistrement disponible",
		`href="/customers/c1"`, `href="/calls/k1"`,
		// The unidentified missed call is what a human has to pick up.
		"+33698765432", "Sans réponse", "Correspondant non reconnu",
		"1 appel demande votre attention",
	)
	// A call nobody answered has no conversation to open.
	mustContain(t, page, "Pas de conversation à lire")
	mustNotContain(t, page, `href="/calls/k2"`)
}

func TestAttentionCountsMissedAndUnrecognised(t *testing.T) {
	unknownButAnswered := answered
	unknownButAnswered.ID, unknownButAnswered.CustomerID, unknownButAnswered.CustomerName = "k3", "", ""
	inbox := Inbox{Calls: []Call{answered, missed, unknownButAnswered}}
	if got := inbox.AttentionCount(); got != 2 {
		t.Errorf("AttentionCount() = %d, want 2", got)
	}
	// An answered call with an unknown caller still wants a human.
	if !unknownButAnswered.NeedsAttention() {
		t.Error("an unrecognised caller needs attention even when answered")
	}
	if answered.NeedsAttention() {
		t.Error("an answered, identified call needs nothing")
	}
}

func TestInboxFilterMarksTheCurrentView(t *testing.T) {
	all := render(t, Index(Inbox{Calls: []Call{answered}}))
	mustContain(t, all, `href="/calls" aria-current="page"`)

	filtered := render(t, Index(Inbox{Calls: []Call{missed}, Filter: FilterNeedsAttention}))
	mustContain(t, filtered, `aria-current="page"`, "À traiter")
	// The banner would be noise on a screen that already shows only those calls.
	mustNotContain(t, filtered, "demande votre attention")
}

func TestEmptyStatesDifferByFilter(t *testing.T) {
	empty := render(t, Index(Inbox{Organization: "Garage Central"}))
	mustContain(t, empty, "Aucun appel pour l'instant", "apparaîtront ici")

	nothingToDo := render(t, Index(Inbox{Organization: "Garage Central", Filter: FilterNeedsAttention}))
	mustContain(t, nothingToDo, "Rien à traiter", "rattachés à un client", `href="/calls"`)
}

func TestTranscriptSeparatesSpeakersAndActions(t *testing.T) {
	page := render(t, Show(Transcript{
		Organization: "Garage Central", Call: answered,
		Messages: []Message{
			{Speaker: SpeakerCaller, Content: "Bonjour, j'ai un bruit au freinage.", OccurredAt: at(9, 12, 10)},
			{Speaker: SpeakerAgent, Content: "Je peux vous proposer jeudi à 9 h.", OccurredAt: at(9, 12, 40)},
			{Speaker: SpeakerTool, Content: "Recherche de disponibilité — Pont 1, jeudi 09:00", OccurredAt: at(9, 12, 35)},
			{Speaker: SpeakerSystem, Content: "Appel enregistré, information donnée au correspondant.", OccurredAt: at(9, 12, 2)},
		},
	}))
	mustContain(t, page,
		"Transcription", "Client", "Agent",
		// A tool call is not speech, and saying so keeps the transcript honest.
		"Action de l'agent", "Système",
		"Bonjour, j'ai un bruit au freinage.", "09:12",
		"Résumé", "Issue : Rendez-vous pris",
		`href="/calls"`, `href="/customers/c1"`,
	)
}

func TestTranscriptOfAnUnrecognisedCallerSaysWhatToDo(t *testing.T) {
	page := render(t, Show(Transcript{Organization: "Garage Central", Call: missed}))
	mustContain(t, page,
		"+33698765432", "Correspondant non reconnu",
		"Rattachez cet appel depuis la fiche du client",
		"Aucune transcription pour cet appel.",
	)
	mustNotContain(t, page, "Résumé")
}

func TestCallLabelFallsBackAllTheWayToAWithheldNumber(t *testing.T) {
	if got := answered.Label(); got != "Claire Dupont" {
		t.Errorf("identified = %q", got)
	}
	if got := missed.Label(); got != "+33698765432" {
		t.Errorf("number = %q", got)
	}
	if got := (Call{}).Label(); got != "Numéro masqué" {
		t.Errorf("withheld = %q", got)
	}
}

func TestDurationReadsAsAHumanWouldSayIt(t *testing.T) {
	cases := []struct {
		from, to time.Time
		want     string
	}{
		{at(9, 0, 0), at(9, 0, 45), "45 s"},
		{at(9, 0, 0), at(9, 2, 0), "2 min"},
		{at(9, 0, 0), at(9, 2, 35), "2 min 35 s"},
		// Still ringing: no end, so no duration rather than a wrong one.
		{at(9, 0, 0), time.Time{}, ""},
		// Clock skew from a provider must not print a negative duration.
		{at(9, 5, 0), at(9, 0, 0), ""},
	}
	for _, testCase := range cases {
		call := Call{StartedAt: testCase.from, EndedAt: testCase.to}
		if got := call.Duration(); got != testCase.want {
			t.Errorf("Duration(%v→%v) = %q, want %q", testCase.from, testCase.to, got, testCase.want)
		}
	}
}

func TestLabelsCoverTheDatabaseVocabulary(t *testing.T) {
	for status, want := range map[string]string{
		"ringing": "Sonne", "in_progress": "En cours", "completed": "Terminé",
		"failed": "Échec", "busy": "Occupé", "no_answer": "Sans réponse",
		"cancelled": "Annulé",
	} {
		if got := statusLabel(status); got != want {
			t.Errorf("statusLabel(%q) = %q, want %q", status, got, want)
		}
	}
	// An unknown value shows through rather than vanishing.
	if got := statusLabel("surprise"); got != "surprise" {
		t.Errorf("unknown status = %q", got)
	}
	if got := directionLabel("outbound"); got != "Sortant" {
		t.Errorf("outbound = %q", got)
	}
}

func TestServerError(t *testing.T) {
	page := render(t, Index(Inbox{
		Organization: "Garage Central",
		Notice:       Notice{Kind: NoticeError, Message: "Les appels n'ont pas pu être chargés."},
	}))
	mustContain(t, page, "Action impossible pour le moment", "Les appels n'ont pas pu être chargés.", "alert-warning")
}
