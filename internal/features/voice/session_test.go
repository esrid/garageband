package voice

import (
	"context"
	"errors"
	"testing"
)

type scriptedResponder struct {
	answer     string
	err        error
	sawCall    Call
	sawHistory []Turn
	calls      int
}

func (r *scriptedResponder) Respond(
	_ context.Context, call Call, transcript []Turn,
) (string, error) {
	r.calls++
	r.sawCall = call
	r.sawHistory = transcript
	return r.answer, r.err
}

func TestSessionAnswersOnlyTheFinalTranscription(t *testing.T) {
	responder := &scriptedResponder{answer: "Nous sommes ouverts jusqu'à 18 heures."}
	session := NewSession(Call{TenantID: "t", LocationID: "l"}, "Bonjour !", responder)
	ctx := context.Background()

	partial, err := session.Handle(ctx, Inbound{
		Type: messagePrompt, VoicePrompt: "vous fermez", Last: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(partial) != 0 {
		t.Fatalf("answered a partial transcription: %v", partial)
	}
	if responder.calls != 0 {
		t.Fatalf("responder called on a partial transcription")
	}

	replies, err := session.Handle(ctx, Inbound{
		Type: messagePrompt, VoicePrompt: "vous fermez à quelle heure ?", Last: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 {
		t.Fatalf("want one reply, got %d", len(replies))
	}
	if replies[0].Type != messageText || !replies[0].Last {
		t.Fatalf("want a final text message, got %+v", replies[0])
	}
	if replies[0].Token != responder.answer {
		t.Fatalf("want %q, got %q", responder.answer, replies[0].Token)
	}
}

func TestSessionKeepsTheGreetingInTheTranscript(t *testing.T) {
	responder := &scriptedResponder{answer: "Bien sûr."}
	session := NewSession(Call{}, "Bonjour, garage Martin.", responder)

	if _, err := session.Handle(context.Background(), Inbound{
		Type: messagePrompt, VoicePrompt: "bonjour", Last: true,
	}); err != nil {
		t.Fatal(err)
	}

	if len(responder.sawHistory) != 2 {
		t.Fatalf("want greeting + caller turn, got %+v", responder.sawHistory)
	}
	if responder.sawHistory[0].Role != RoleAgent ||
		responder.sawHistory[0].Text != "Bonjour, garage Martin." {
		t.Fatalf("greeting missing from the transcript: %+v", responder.sawHistory[0])
	}
}

// An interruption is the one message that rewrites history: what the agent
// meant to say is not what the caller heard, and the next answer must build on
// the second.
func TestSessionTruncatesAnInterruptedSentence(t *testing.T) {
	responder := &scriptedResponder{
		answer: "Nous proposons la vidange, le freinage et la climatisation.",
	}
	session := NewSession(Call{}, "", responder)
	ctx := context.Background()

	if _, err := session.Handle(ctx, Inbound{
		Type: messagePrompt, VoicePrompt: "que faites-vous ?", Last: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Handle(ctx, Inbound{
		Type:                     messageInterrupt,
		UtteranceUntilInterrupt:  "Nous proposons la vidange,",
		DurationUntilInterruptMs: 460,
	}); err != nil {
		t.Fatal(err)
	}

	transcript := session.Transcript()
	last := transcript[len(transcript)-1]
	if last.Role != RoleAgent || last.Text != "Nous proposons la vidange," {
		t.Fatalf("transcript still claims the whole sentence landed: %+v", last)
	}
}

func TestSessionDropsAnAgentTurnInterruptedBeforeAnyWord(t *testing.T) {
	responder := &scriptedResponder{answer: "Un instant je vous prie."}
	session := NewSession(Call{}, "", responder)
	ctx := context.Background()

	if _, err := session.Handle(ctx, Inbound{
		Type: messagePrompt, VoicePrompt: "allô ?", Last: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Handle(ctx, Inbound{
		Type: messageInterrupt, UtteranceUntilInterrupt: "",
	}); err != nil {
		t.Fatal(err)
	}

	for _, turn := range session.Transcript() {
		if turn.Role == RoleAgent {
			t.Fatalf("kept an agent turn nobody heard: %+v", turn)
		}
	}
}

func TestSessionTakesTheCallIdentityFromSetup(t *testing.T) {
	session := NewSession(Call{TenantID: "t", LocationID: "l"}, "", &scriptedResponder{})

	if _, err := session.Handle(context.Background(), Inbound{
		Type:          messageSetup,
		CallSid:       "CA123",
		From:          "+596696000000",
		To:            "+33123456789",
		ForwardedFrom: "+596596000000",
	}); err != nil {
		t.Fatal(err)
	}

	call := session.Call()
	if call.CallSid != "CA123" || call.FromE164 != "+596696000000" {
		t.Fatalf("setup not applied: %+v", call)
	}
	// The forward is the normal path: the customer dialled the garage's own
	// line, not ours.
	if call.ForwardedFrom != "+596596000000" {
		t.Fatalf("lost the forwarding number: %+v", call)
	}
	if call.TenantID != "t" || call.LocationID != "l" {
		t.Fatalf("setup overwrote the routed tenant: %+v", call)
	}
}

func TestSessionSaysNothingWhenTheResponderIsSilent(t *testing.T) {
	session := NewSession(Call{}, "", &scriptedResponder{answer: "   "})

	replies, err := session.Handle(context.Background(), Inbound{
		Type: messagePrompt, VoicePrompt: "…", Last: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 0 {
		t.Fatalf("spoke an empty answer: %+v", replies)
	}
}

func TestSessionPropagatesAResponderFailure(t *testing.T) {
	failure := errors.New("model unavailable")
	session := NewSession(Call{}, "", &scriptedResponder{err: failure})

	if _, err := session.Handle(context.Background(), Inbound{
		Type: messagePrompt, VoicePrompt: "bonjour", Last: true,
	}); !errors.Is(err, failure) {
		t.Fatalf("want %v, got %v", failure, err)
	}
}
