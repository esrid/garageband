// Package vehiclelookup defines the provider-neutral vehicle registration
// lookup port.
package vehiclelookup

import (
	"context"
	"encoding/json"
	"time"
)

type Request struct {
	CountryCode       string
	RegistrationPlate string
}

type Vehicle struct {
	RegistrationPlate   string
	VIN                 string
	Make                string
	Model               string
	Trim                string
	FuelType            string
	FirstRegistrationOn time.Time
	Raw                 json.RawMessage
}

type Provider interface {
	Lookup(ctx context.Context, request Request) (Vehicle, error)
}
