package app

import (
	"os"

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
}

func ConfigFromEnv() Config {
	return Config{
		Addr:               envOr("ADDR", ":8080"),
		DatabaseURL:        envOr("DATABASE_URL", "file:app.db?_fk=on&_journal=WAL"),
		BaseURL:            envOr("BASE_URL", "http://localhost:8080"),
		CookieSecure:       os.Getenv("COOKIE_SECURE") != "false",
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
	}
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
