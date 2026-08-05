package voice

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	twilioclient "github.com/twilio/twilio-go/client"
)

// Config is what the feature needs from the environment. It is read once in
// internal/app/config.go like every other setting.
type Config struct {
	// PublicBaseURL is the origin Twilio reaches us on, without a trailing
	// slash. The webhook signature is computed over the full public URL, so a
	// proxy that rewrites the host breaks validation unless this matches.
	PublicBaseURL string
	// AuthToken signs Twilio's webhooks; it is also what proves a socket
	// handshake came from a TwiML we issued.
	AuthToken string
	// Greeting is spoken before the socket carries anything.
	Greeting string
}

type handler struct {
	store     *Store
	config    Config
	responder Responder
	logger    *slog.Logger
	now       func() time.Time
}

const (
	relayTokenTTL   = 2 * time.Minute
	maxCallDuration = time.Hour
	rejectTwiML     = `<?xml version="1.0" encoding="UTF-8"?><Response><Reject/></Response>`
)

// incoming answers a call Twilio just received. It resolves the dialled number
// to a site and hands the audio to ConversationRelay; an unknown number is
// rejected rather than answered by an agent belonging to nobody.
func (h *handler) incoming(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !h.validSignature(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// The route comes from the number's own webhook URL, which Twilio signed in
	// full a few lines above. A number whose URL is missing any of it is one we
	// did not provision: reject rather than answer for a garage we cannot name.
	route := routeFromQuery(r.URL.Query())
	if !route.complete() {
		h.writeTwiML(w, []byte(rejectTwiML))
		return
	}

	document, err := ConnectTwiML(h.socketURL(route), route, FrenchWorkshopVoice(h.config.Greeting))
	if err != nil {
		h.logger.Error("rendering twiml", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.writeTwiML(w, document)
}

func (h *handler) writeTwiML(w http.ResponseWriter, document []byte) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	if _, err := w.Write(document); err != nil {
		h.logger.Error("writing twiml", "error", err)
	}
}

// validSignature checks Twilio signed this webhook. Verified against
// https://pkg.go.dev/github.com/twilio/twilio-go/client 2026-08-05.
func (h *handler) validSignature(r *http.Request) bool {
	params := make(map[string]string, len(r.PostForm))
	for key := range r.PostForm {
		params[key] = r.PostForm.Get(key)
	}
	validator := twilioclient.NewRequestValidator(h.config.AuthToken)
	return validator.Validate(
		h.config.PublicBaseURL+r.URL.RequestURI(),
		params,
		r.Header.Get("X-Twilio-Signature"),
	)
}

// socketURL points ConversationRelay at our loop, carrying a short-lived token
// so the socket only accepts handshakes for a TwiML we just issued. Twilio does
// not sign the WebSocket upgrade, so without this anyone who guessed the path
// could speak as any garage.
func (h *handler) socketURL(route Route) string {
	expiry := h.now().Add(relayTokenTTL).Unix()
	query := url.Values{}
	query.Set("tenant", route.TenantID)
	query.Set("location", route.LocationID)
	query.Set("agent", route.AgentID)
	query.Set("number", route.NumberID)
	query.Set("exp", strconv.FormatInt(expiry, 10))
	query.Set("token", h.relayToken(route, expiry))

	socket := strings.TrimPrefix(strings.TrimPrefix(h.config.PublicBaseURL, "http://"), "https://")
	scheme := "wss://"
	if strings.HasPrefix(h.config.PublicBaseURL, "http://") {
		scheme = "ws://"
	}
	return scheme + socket + "/voice/relay?" + query.Encode()
}

func routeFromQuery(query url.Values) Route {
	return Route{
		TenantID:   query.Get("tenant"),
		LocationID: query.Get("location"),
		AgentID:    query.Get("agent"),
		NumberID:   query.Get("number"),
	}
}

func (h *handler) relayToken(route Route, expiry int64) string {
	mac := hmac.New(sha256.New, []byte(h.config.AuthToken))
	// Every field is signed, separated: swapping the agent or the number of a
	// call is as much an impersonation as swapping its tenant.
	// Write on a hash.Hash never returns an error.
	mac.Write([]byte(strings.Join([]string{
		route.TenantID, route.LocationID, route.AgentID, route.NumberID,
		strconv.FormatInt(expiry, 10),
	}, "|")))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// authorizeRelay rebuilds the token and compares it in constant time.
func (h *handler) authorizeRelay(r *http.Request) (Route, bool) {
	query := r.URL.Query()
	route := routeFromQuery(query)
	if !route.complete() {
		return Route{}, false
	}
	expiry, err := strconv.ParseInt(query.Get("exp"), 10, 64)
	if err != nil || h.now().Unix() > expiry {
		return Route{}, false
	}
	expected := h.relayToken(route, expiry)
	if !hmac.Equal([]byte(expected), []byte(query.Get("token"))) {
		return Route{}, false
	}
	return route, true
}

// relay is the loop itself: one WebSocket, one call, one Session. The socket
// only moves JSON around — every decision lives in Session, which is why it can
// be tested without a telephone.
func (h *handler) relay(w http.ResponseWriter, r *http.Request) {
	route, ok := h.authorizeRelay(r)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		h.logger.Error("accepting relay socket", "error", err)
		return
	}
	defer func() {
		// CloseNow is the best-effort path: the normal close already happened
		// below, and a second attempt on an open socket is what frees it.
		if err := conn.CloseNow(); err != nil {
			h.logger.Debug("closing relay socket", "error", err)
		}
	}()

	session := NewSession(
		Call{TenantID: route.TenantID, LocationID: route.LocationID},
		h.config.Greeting,
		h.responder,
	)

	// A garage call that is still open after an hour is a socket nobody hung
	// up, not a conversation. The cap bounds the goroutine and the carrier
	// bill; it is far above any real call.
	ctx, cancel := context.WithTimeout(r.Context(), maxCallDuration)
	defer cancel()

	record := &callRecord{store: h.store, route: route, logger: h.logger, now: h.now}
	// The closing writes must survive the socket's own context: it is already
	// cancelled by the time a caller hangs up, which is exactly when the last
	// words and the end of the call have to be saved.
	defer record.close(context.WithoutCancel(ctx), session)

	for {
		var in Inbound
		if err := wsjson.Read(ctx, conn, &in); err != nil {
			if websocket.CloseStatus(err) != -1 || errors.Is(err, context.Canceled) {
				return
			}
			h.logger.Error("reading relay message", "error", err)
			return
		}

		replies, err := session.Handle(ctx, in)
		if err != nil {
			h.logger.Error("handling relay message",
				"error", err, "type", in.Type, "call", session.Call().CallSid)
			record.fail()
			if err := conn.Close(websocket.StatusInternalError, "agent failed"); err != nil {
				h.logger.Debug("closing after failure", "error", err)
			}
			return
		}
		if in.Type == messageSetup {
			record.start(ctx, session.Call())
		}
		record.flush(ctx, session)

		for _, reply := range replies {
			if err := wsjson.Write(ctx, conn, reply); err != nil {
				h.logger.Error("writing relay message", "error", err)
				return
			}
		}
	}
}

// callRecord persists a call as it happens. Every failure here is logged and
// swallowed on purpose: losing the transcript of a call is bad, dropping the
// call itself because the database hiccuped is worse. The caller is on the
// line either way.
type callRecord struct {
	store  *Store
	route  Route
	logger *slog.Logger
	now    func() time.Time

	callID  string
	flushed int
	status  string
}

func (c *callRecord) start(ctx context.Context, call Call) {
	if c.callID != "" || call.CallSid == "" {
		return
	}
	callID, err := c.store.StartCall(
		ctx, c.route, call.CallSid, call.FromE164, call.ToE164, c.now(),
	)
	if err != nil {
		c.logger.Error("opening call record", "error", err, "call", call.CallSid)
		return
	}
	c.callID = callID
	c.status = "completed"
}

// flush writes the turns that can no longer change. The agent's last sentence
// is held back because an interrupt truncates it to what the caller actually
// heard; writing it early would store words nobody received.
func (c *callRecord) flush(ctx context.Context, session *Session) {
	if c.callID == "" {
		return
	}
	c.write(ctx, session.Transcript(), false)
}

func (c *callRecord) fail() { c.status = "failed" }

func (c *callRecord) close(ctx context.Context, session *Session) {
	if c.callID == "" {
		return
	}
	c.write(ctx, session.Transcript(), true)
	if c.status == "" {
		c.status = "completed"
	}
	if err := c.store.EndCall(
		ctx, c.route.TenantID, c.callID, c.status, c.now(),
	); err != nil {
		c.logger.Error("closing call record", "error", err, "call", c.callID)
	}
}

func (c *callRecord) write(ctx context.Context, transcript []Turn, final bool) {
	end := len(transcript)
	if !final && end > 0 && transcript[end-1].Role == RoleAgent {
		end--
	}
	if end <= c.flushed {
		return
	}
	pending := transcript[c.flushed:end]
	if err := c.store.AppendTurns(
		ctx, c.route.TenantID, c.callID, pending, c.now(),
	); err != nil {
		c.logger.Error("saving call turns", "error", err, "call", c.callID)
		return
	}
	c.flushed = end
}
