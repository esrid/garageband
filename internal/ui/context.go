package ui

import "context"

// activeLocationNameKey carries the session's current site into the shared
// shell without making the layout depend on the auth feature. The auth
// middleware is the producer; the shared UI is the consumer.
type activeLocationNameKey struct{}

// WithActiveLocationName adds the current site label to a request context.
func WithActiveLocationName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, activeLocationNameKey{}, name)
}

// ActiveLocationName returns the current site label carried by the request.
func ActiveLocationName(ctx context.Context) string {
	name, _ := ctx.Value(activeLocationNameKey{}).(string)
	return name
}
