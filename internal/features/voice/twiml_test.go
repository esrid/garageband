package voice

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestConnectTwiMLCarriesVoiceAndRoute(t *testing.T) {
	document, err := ConnectTwiML(
		"wss://example.test/voice/relay?token=abc",
		Route{TenantID: "tenant-1", LocationID: "location-1"},
		FrenchWorkshopVoice("Bonjour !"),
	)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(document)

	for _, want := range []string{
		`<ConversationRelay`,
		`url="wss://example.test/voice/relay?token=abc"`,
		`language="fr-FR"`,
		`ttsProvider="ElevenLabs"`,
		// Twilio's own fr-FR voice on the telephony model.
		`voice="a5n9pJUnAhX4fn7lx3uo"`,
		`welcomeGreeting="Bonjour !"`,
		`interruptSensitivity="medium"`,
		`<Parameter name="tenantId" value="tenant-1">`,
		`<Parameter name="locationId" value="location-1">`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %s in:\n%s", want, rendered)
		}
	}
}

// The socket is the one door Twilio does not sign. Its token is what stops a
// stranger from opening a call as any garage.
func TestRelayTokenRejectsTamperingAndExpiry(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	h := &handler{
		config: Config{PublicBaseURL: "https://app.test", AuthToken: "secret"},
		now:    func() time.Time { return now },
	}
	route := Route{
		TenantID:   "tenant-1",
		LocationID: "location-1",
		AgentID:    "agent-1",
		NumberID:   "number-1",
	}

	socketURL := h.socketURL(route)
	if !strings.HasPrefix(socketURL, "wss://app.test/voice/relay?") {
		t.Fatalf("unexpected socket url: %s", socketURL)
	}
	query := mustQuery(t, socketURL)

	got, ok := h.authorizeRelay(httptest.NewRequest("GET", "/voice/relay?"+query.Encode(), nil))
	if !ok {
		t.Fatal("refused a token it had just minted")
	}
	if got.TenantID != route.TenantID || got.LocationID != route.LocationID {
		t.Fatalf("route lost in the round trip: %+v", got)
	}

	// Answering for another garage with a token minted for this one.
	tampered := cloneValues(query)
	tampered.Set("tenant", "tenant-2")
	if _, ok := h.authorizeRelay(
		httptest.NewRequest("GET", "/voice/relay?"+tampered.Encode(), nil),
	); ok {
		t.Fatal("accepted a token minted for another tenant")
	}

	// The same token, once the call it was issued for is long over.
	late := &handler{config: h.config, now: func() time.Time { return now.Add(time.Hour) }}
	if _, ok := late.authorizeRelay(
		httptest.NewRequest("GET", "/voice/relay?"+query.Encode(), nil),
	); ok {
		t.Fatal("accepted an expired token")
	}

	// Swapping the agent is as much an impersonation as swapping the tenant.
	swapped := cloneValues(query)
	swapped.Set("agent", "agent-2")
	if _, ok := h.authorizeRelay(
		httptest.NewRequest("GET", "/voice/relay?"+swapped.Encode(), nil),
	); ok {
		t.Fatal("accepted a token minted for another agent")
	}

	// A route missing a field describes a number we never provisioned.
	incomplete := cloneValues(query)
	incomplete.Del("number")
	if _, ok := h.authorizeRelay(
		httptest.NewRequest("GET", "/voice/relay?"+incomplete.Encode(), nil),
	); ok {
		t.Fatal("accepted a route with no number")
	}

	// A different auth token must not validate our signature.
	other := &handler{
		config: Config{PublicBaseURL: h.config.PublicBaseURL, AuthToken: "other"},
		now:    func() time.Time { return now },
	}
	if _, ok := other.authorizeRelay(
		httptest.NewRequest("GET", "/voice/relay?"+query.Encode(), nil),
	); ok {
		t.Fatal("accepted a token signed with another secret")
	}
}

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Query()
}

func cloneValues(values url.Values) url.Values {
	clone := url.Values{}
	for key, list := range values {
		for _, value := range list {
			clone.Add(key, value)
		}
	}
	return clone
}
