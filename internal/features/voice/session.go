package voice

import (
	"context"
	"errors"
	"slices"
	"strings"
)

// Message names and field names come from Twilio's ConversationRelay WebSocket
// reference, verified against
// https://www.twilio.com/docs/voice/conversationrelay/websocket-messages
// 2026-08-05.
const (
	messageSetup     = "setup"
	messagePrompt    = "prompt"
	messageInterrupt = "interrupt"
	messageDTMF      = "dtmf"
	messageError     = "error"
	messageText      = "text"
)

// Inbound is one message Twilio sends over the socket. The five types share a
// single struct because they share a discriminator and never overlap: reading
// them into one shape keeps the loop free of a decode-twice dance.
type Inbound struct {
	Type string `json:"type"`

	// setup
	SessionID        string            `json:"sessionId,omitempty"`
	CallSid          string            `json:"callSid,omitempty"`
	From             string            `json:"from,omitempty"`
	To               string            `json:"to,omitempty"`
	ForwardedFrom    string            `json:"forwardedFrom,omitempty"`
	Direction        string            `json:"direction,omitempty"`
	CustomParameters map[string]string `json:"customParameters,omitempty"`

	// prompt
	VoicePrompt string `json:"voicePrompt,omitempty"`
	Lang        string `json:"lang,omitempty"`
	Last        bool   `json:"last,omitempty"`

	// interrupt
	UtteranceUntilInterrupt  string `json:"utteranceUntilInterrupt,omitempty"`
	DurationUntilInterruptMs int    `json:"durationUntilInterruptMs,omitempty"`

	// dtmf
	Digit string `json:"digit,omitempty"`

	// error
	Description string `json:"description,omitempty"`
}

// Outbound is what the loop sends back for Twilio to speak.
type Outbound struct {
	Type  string `json:"type"`
	Token string `json:"token"`
	Last  bool   `json:"last"`
}

// Role labels who spoke a turn. The strings reach the model, so they are the
// words a prompt would use rather than protocol names.
const (
	RoleCaller = "caller"
	RoleAgent  = "agent"
)

type Turn struct {
	Role string
	Text string
}

// Call is what the loop knows about the call it is serving. It is resolved
// before the socket opens, from the dialled number, and never from anything
// the caller says.
type Call struct {
	TenantID   string
	LocationID string
	CallSid    string
	FromE164   string
	ToE164     string
	// ForwardedFrom carries the garage's own line when the call reached us
	// through its no-answer forward, which is the normal path: the number the
	// customer dialled is the garage's, not ours.
	ForwardedFrom string
}

// Responder turns the conversation so far into what the agent should say next.
// It is the seam where the model and the product's own tools live; the loop
// itself decides nothing about content.
type Responder interface {
	Respond(ctx context.Context, call Call, transcript []Turn) (string, error)
}

// Session is one call's loop. It owns the transcript and decides what to send
// back for each message; given the same responder it is fully deterministic,
// which is what makes it testable without a socket or a telephone.
type Session struct {
	call       Call
	responder  Responder
	transcript []Turn
}

// NewSession starts a loop for an already-routed call. greeting is what
// ConversationRelay speaks on its own through welcomeGreeting, recorded here so
// the model knows what the caller already heard.
func NewSession(call Call, greeting string, responder Responder) *Session {
	session := &Session{call: call, responder: responder}
	if strings.TrimSpace(greeting) != "" {
		session.transcript = append(session.transcript, Turn{Role: RoleAgent, Text: greeting})
	}
	return session
}

// Transcript returns the conversation as the loop understands it, which is not
// always what the agent said: an interrupted sentence is truncated to the part
// the caller actually heard.
func (s *Session) Transcript() []Turn {
	return slices.Clone(s.transcript)
}

// Call reports the routed call this session serves.
func (s *Session) Call() Call { return s.call }

var errCallerError = errors.New("conversationrelay reported an error")

// Handle advances the loop by one message and returns what to send back.
// A nil slice means "say nothing", which is the right answer more often than
// not: partial transcriptions and interruptions change state without speaking.
func (s *Session) Handle(ctx context.Context, in Inbound) ([]Outbound, error) {
	switch in.Type {
	case messageSetup:
		s.applySetup(in)
		return nil, nil

	case messagePrompt:
		// Twilio streams partial transcriptions and marks the final one with
		// last. Answering a partial would talk over the caller mid-sentence.
		if !in.Last {
			return nil, nil
		}
		spoken := strings.TrimSpace(in.VoicePrompt)
		if spoken == "" {
			return nil, nil
		}
		s.transcript = append(s.transcript, Turn{Role: RoleCaller, Text: spoken})
		answer, err := s.responder.Respond(ctx, s.call, s.Transcript())
		if err != nil {
			return nil, err
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			return nil, nil
		}
		s.transcript = append(s.transcript, Turn{Role: RoleAgent, Text: answer})
		// ponytail: one token per answer. Streaming the model's tokens as they
		// arrive with last:false is what Twilio recommends for latency; do it
		// when Responder can stream, not before.
		return []Outbound{{Type: messageText, Token: answer, Last: true}}, nil

	case messageInterrupt:
		s.truncateLastAgentTurn(in.UtteranceUntilInterrupt)
		return nil, nil

	case messageDTMF:
		// ponytail: digits are recorded as caller turns so a model can react to
		// "press 1"; no IVR tree until a garage asks for one.
		if in.Digit != "" {
			s.transcript = append(s.transcript, Turn{Role: RoleCaller, Text: "[" + in.Digit + "]"})
		}
		return nil, nil

	case messageError:
		return nil, errors.Join(errCallerError, errors.New(in.Description))

	default:
		return nil, nil
	}
}

func (s *Session) applySetup(in Inbound) {
	if in.CallSid != "" {
		s.call.CallSid = in.CallSid
	}
	if in.From != "" {
		s.call.FromE164 = in.From
	}
	if in.To != "" {
		s.call.ToE164 = in.To
	}
	if in.ForwardedFrom != "" {
		s.call.ForwardedFrom = in.ForwardedFrom
	}
}

// truncateLastAgentTurn rewrites the agent's last sentence down to the part the
// caller heard before cutting in. Without it the transcript claims a sentence
// landed whole and the next answer builds on something nobody heard.
func (s *Session) truncateLastAgentTurn(heard string) {
	for i := len(s.transcript) - 1; i >= 0; i-- {
		if s.transcript[i].Role != RoleAgent {
			continue
		}
		heard = strings.TrimSpace(heard)
		if heard == "" {
			s.transcript = s.transcript[:i]
			return
		}
		s.transcript[i].Text = heard
		return
	}
}
