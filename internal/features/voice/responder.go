package voice

import "context"

// StaticResponder says one sentence, whatever it is asked. It is what the loop
// runs with until the permitted tools, their authorization, their confirmation
// rules and their audits are wired to a real model — the product's own rule,
// and the reason the socket ships before the intelligence behind it.
//
// It is not a stub in the throwaway sense: a garage whose agent picks up and
// says "I cannot help yet, someone will call you back" is better than a line
// that rings into nothing, and this is also what an unreachable model must
// fall back to later.
type StaticResponder struct{ Sentence string }

func (r StaticResponder) Respond(context.Context, Call, []Turn) (string, error) {
	return r.Sentence, nil
}
