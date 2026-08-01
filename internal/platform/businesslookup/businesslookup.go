// Package businesslookup defines the port for enriching garage onboarding from
// an official business identifier or public website.
package businesslookup

import (
	"context"
	"encoding/json"
)

type Request struct {
	SIRET      string
	WebsiteURL string
}

type Address struct {
	Line1       string
	Line2       string
	PostalCode  string
	City        string
	CountryCode string
}

type Profile struct {
	SIREN       string
	SIRET       string
	LegalName   string
	TradingName string
	WebsiteURL  string
	PhoneE164   string
	Email       string
	Address     Address
	Description string
	Raw         json.RawMessage
}

// Provider may use an official registry, website extraction, or both. The
// feature layer decides which returned fields the user must confirm.
type Provider interface {
	Name() string
	Enrich(ctx context.Context, request Request) (Profile, error)
}
