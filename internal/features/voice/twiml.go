package voice

import (
	"encoding/xml"
	"strings"
)

// AgentVoice is what a site's agent sounds like. Twilio freezes voice and
// language when the call is answered — "you can't modify voice and language
// configurations set in TwiML through WebSocket messages" — so these are
// decided here, once, and never mid-call.
//
// Attribute names and defaults verified against
// https://www.twilio.com/docs/voice/twiml/connect/conversationrelay
// 2026-08-05.
type AgentVoice struct {
	// Language is the BCP-47 tag ConversationRelay transcribes and speaks in.
	Language string
	// TTSProvider is ElevenLabs by default at Twilio, which is also where the
	// French voices live and costs nothing on top of the per-minute price.
	TTSProvider string
	// Voice is the provider's voice id. ElevenLabs fr-FR is
	// a5n9pJUnAhX4fn7lx3uo on the telephony model.
	Voice string
	// Greeting is spoken by Twilio itself before the socket carries anything.
	Greeting string
	// InterruptSensitivity is high, medium or low. A workshop is loud; low
	// keeps a compressor from cutting the agent off mid-sentence.
	InterruptSensitivity string
}

// Defaults for a French workshop. Every one of them is overridable per site;
// none of them is invented — the voice id is Twilio's own for fr-FR.
func FrenchWorkshopVoice(greeting string) AgentVoice {
	return AgentVoice{
		Language:             "fr-FR",
		TTSProvider:          "ElevenLabs",
		Voice:                "a5n9pJUnAhX4fn7lx3uo",
		Greeting:             greeting,
		InterruptSensitivity: "medium",
	}
}

type twiMLResponse struct {
	XMLName xml.Name    `xml:"Response"`
	Connect twiMLConnec `xml:"Connect"`
}

type twiMLConnec struct {
	Relay twiMLRelay `xml:"ConversationRelay"`
}

type twiMLRelay struct {
	URL                  string           `xml:"url,attr"`
	Language             string           `xml:"language,attr,omitempty"`
	TTSProvider          string           `xml:"ttsProvider,attr,omitempty"`
	Voice                string           `xml:"voice,attr,omitempty"`
	WelcomeGreeting      string           `xml:"welcomeGreeting,attr,omitempty"`
	InterruptSensitivity string           `xml:"interruptSensitivity,attr,omitempty"`
	Parameters           []twiMLParameter `xml:"Parameter"`
}

type twiMLParameter struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// ParamTenantID and ParamLocationID travel as custom parameters so the socket
// knows which garage it is answering for without resolving the number twice.
// They are set by us on a webhook Twilio signed, never read from the caller.
const (
	ParamTenantID   = "tenantId"
	ParamLocationID = "locationId"
)

// ConnectTwiML renders the answer to an inbound call: hand the audio to
// ConversationRelay and tell it which socket to talk to.
func ConnectTwiML(socketURL string, route Route, voice AgentVoice) ([]byte, error) {
	document := twiMLResponse{
		Connect: twiMLConnec{
			Relay: twiMLRelay{
				URL:                  socketURL,
				Language:             voice.Language,
				TTSProvider:          voice.TTSProvider,
				Voice:                voice.Voice,
				WelcomeGreeting:      strings.TrimSpace(voice.Greeting),
				InterruptSensitivity: voice.InterruptSensitivity,
				Parameters: []twiMLParameter{
					{Name: ParamTenantID, Value: route.TenantID},
					{Name: ParamLocationID, Value: route.LocationID},
				},
			},
		},
	}
	body, err := xml.Marshal(document)
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
}
