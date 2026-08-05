package voice

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Register wires the two endpoints a call needs. Neither sits behind
// RequireTenant: a caller is anonymous, and both prove their own origin —
// the webhook through Twilio's signature, the socket through a token we
// minted in the TwiML that opened it.
func Register(
	mux *http.ServeMux,
	store *Store,
	config Config,
	responder Responder,
	logger *slog.Logger,
) {
	// Without the carrier's auth token neither door can prove anything. The
	// webhook would reject every call, which is merely useless — but the relay
	// token would be an HMAC keyed on the empty string, which anyone can
	// compute, so a stranger could open a socket as any garage. Registering
	// nothing is the only safe reading of "telephony is not configured".
	if strings.TrimSpace(config.AuthToken) == "" {
		logger.Warn("telephony disabled: TWILIO_AUTH_TOKEN is not set")
		return
	}

	h := &handler{
		store:     store,
		config:    config,
		responder: responder,
		logger:    logger,
		now:       time.Now,
	}
	mux.Handle("POST /voice/incoming", http.HandlerFunc(h.incoming))
	mux.Handle("GET /voice/relay", http.HandlerFunc(h.relay))
}
