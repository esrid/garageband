// Package secrets defines indirection for provider credentials. Database rows
// (provider_connections.secret_ref) contain only a reference, never a raw
// OAuth token or API key.
package secrets

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Store resolves, creates, and removes secrets, scoped to the caller's own
// RLS-scoped transaction: a secret always lives behind the same tenant
// isolation as everything else in the database, not a separate trust
// boundary an application bug could bypass.
type Store interface {
	Resolve(ctx context.Context, tx pgx.Tx, reference string) ([]byte, error)
	Store(ctx context.Context, tx pgx.Tx, tenantID string, plaintext []byte) (reference string, err error)
	Delete(ctx context.Context, tx pgx.Tx, reference string) error
}
