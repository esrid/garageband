package voice

import (
	"log/slog"
	"net/http"
	"time"
)

// Register wires the two endpoints a call needs. Neither sits behind
// RequireTenant: a caller is anonymous, and both prove their own origin —
// the webhook through Twilio's signature, the socket through a token we
// minted in the TwiML that opened it.
func Register(
	mux *http.ServeMux,
	config Config,
	responder Responder,
	logger *slog.Logger,
) {
	h := &handler{
		config:    config,
		responder: responder,
		logger:    logger,
		now:       time.Now,
	}
	mux.Handle("POST /voice/incoming", http.HandlerFunc(h.incoming))
	mux.Handle("GET /voice/relay", http.HandlerFunc(h.relay))
}
