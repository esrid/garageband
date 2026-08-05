package app

import (
	"encoding/base64"
	"fmt"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"

	"github.com/esrid/garageband/internal/platform/oauth"
)

// Config holds all runtime configuration, read from environment variables
// (12-factor). Add fields here, never scatter os.Getenv calls in features.
type Config struct {
	Addr        string
	DatabaseURL string
	// BaseURL is the public origin, used to build OAuth callback URLs.
	BaseURL string
	// CookieSecure sets the Secure flag on cookies; set COOKIE_SECURE=false
	// only for local http development.
	CookieSecure       bool
	GoogleClientID     string
	GoogleClientSecret string
	// EncryptionKey is the base64-encoded 32-byte AES-256 key protecting
	// encrypted_secrets rows (OAuth refresh tokens today). Calendar
	// connections stay disabled without it, the same way GoogleClientID
	// gates login.
	EncryptionKey []byte
	// BusinessLookupURL can target a compatible stub in development. Empty
	// means the official Recherche d'entreprises API.
	BusinessLookupURL string
	// TwilioAccountSID and TwilioAuthToken belong to the parent account. The
	// token also validates inbound webhooks and signs the relay socket's
	// short-lived token, so an empty one leaves both endpoints refusing
	// everything rather than trusting anything.
	TwilioAccountSID string
	TwilioAuthToken  string
	// VoiceGreeting is what the agent says before the socket carries
	// anything; ConversationRelay speaks it itself.
	VoiceGreeting string
}

func ConfigFromEnv() Config {
	key, err := decodeEncryptionKey(os.Getenv("ENCRYPTION_KEY"))
	if err != nil {
		// A malformed key is a configuration mistake, not a runtime
		// condition to limp past: fail at boot, not on the first calendar
		// connection attempt someone makes in production.
		panic(err)
	}
	return Config{
		Addr:               envOr("ADDR", ":8080"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		BaseURL:            envOr("BASE_URL", "http://localhost:8080"),
		CookieSecure:       os.Getenv("COOKIE_SECURE") != "false",
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		EncryptionKey:      key,
		BusinessLookupURL:  os.Getenv("BUSINESS_LOOKUP_URL"),
		TwilioAccountSID:   os.Getenv("TWILIO_ACCOUNT_SID"),
		TwilioAuthToken:    os.Getenv("TWILIO_AUTH_TOKEN"),
		VoiceGreeting: envOr("VOICE_GREETING",
			"Bonjour, vous êtes en relation avec l'assistant du garage. Que puis-je faire pour vous ?"),
	}
}

func decodeEncryptionKey(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("ENCRYPTION_KEY must be base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("ENCRYPTION_KEY must decode to 32 bytes, got %d", len(key))
	}
	return key, nil
}

// OAuthProviders builds the configured provider adapters. Add a provider:
// write its adapter in internal/platform/oauth, append it here.
func (c Config) OAuthProviders() []oauth.Provider {
	var ps []oauth.Provider
	if c.GoogleClientID != "" {
		ps = append(ps, oauth.NewGoogle(c.GoogleClientID, c.GoogleClientSecret,
			c.BaseURL+"/auth/google/callback"))
	}
	return ps
}

// GoogleCalendarOAuthConfig reuses the same Google Cloud OAuth client as
// login, with the Calendar-specific scope and a distinct callback: one
// client, two consent flows. Register this redirect URL as a second
// Authorized redirect URI on that client — it does not replace the login one.
func (c Config) GoogleCalendarOAuthConfig() oauth2.Config {
	return oauth2.Config{
		ClientID:     c.GoogleClientID,
		ClientSecret: c.GoogleClientSecret,
		Endpoint:     endpoints.Google,
		RedirectURL:  c.BaseURL + "/oauth/google-calendar/callback",
		Scopes:       []string{"https://www.googleapis.com/auth/calendar.events"},
	}
}

// CalendarEnabled says whether a location may offer to connect a calendar:
// both the OAuth client and the secret-encryption key must be configured.
func (c Config) CalendarEnabled() bool {
	return c.GoogleClientID != "" && len(c.EncryptionKey) == 32
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
