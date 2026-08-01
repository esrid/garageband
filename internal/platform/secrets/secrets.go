// Package secrets defines indirection for provider credentials. Database rows
// contain only secret references, never raw OAuth tokens or API keys.
package secrets

import "context"

type Store interface {
	Resolve(ctx context.Context, reference string) ([]byte, error)
}
