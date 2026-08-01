// Package telephony defines call-control contracts without choosing a carrier.
package telephony

import (
	"context"
	"time"
)

type Webhook struct {
	Method  string
	URL     string
	Headers map[string][]string
	Body    []byte
}

type Event struct {
	ID             string
	Type           string
	ProviderCallID string
	FromE164       string
	ToE164         string
	OccurredAt     time.Time
	Metadata       map[string]string
}

type OutboundCall struct {
	FromE164    string
	ToE164      string
	CallbackURL string
}

type Call struct {
	ProviderCallID string
	Status         string
}

// ParseWebhook must authenticate the webhook before returning an event.
type Provider interface {
	ParseWebhook(ctx context.Context, webhook Webhook) (Event, error)
	StartOutbound(ctx context.Context, request OutboundCall) (Call, error)
	EndCall(ctx context.Context, providerCallID string) error
}
