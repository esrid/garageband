-- Storage for provider_connections.secret_ref: an application-encrypted
-- secret (an OAuth refresh token, to start), never a plaintext credential in
-- any other table. AES-256-GCM at the application layer, key from
-- ENCRYPTION_KEY, not pgcrypto: no new extension, key never touches the
-- database process.
-- +goose Up
CREATE TABLE encrypted_secrets (
    id         UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id  UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    ciphertext BYTEA NOT NULL,
    nonce      BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT encrypted_secrets_ciphertext_present CHECK (octet_length(ciphertext) > 0),
    CONSTRAINT encrypted_secrets_nonce_length CHECK (octet_length(nonce) = 12)
);

ALTER TABLE encrypted_secrets ENABLE ROW LEVEL SECURITY;
ALTER TABLE encrypted_secrets FORCE ROW LEVEL SECURITY;

-- Any tenant member may resolve a secret: resolution happens server-side as
-- a side effect of an action (booking an appointment) already authorized on
-- its own terms, not through any endpoint that lists or exposes secrets.
CREATE POLICY encrypted_secret_select ON encrypted_secrets
    FOR SELECT USING (tenant_id = app_current_tenant_id());
-- Writing a secret only ever happens from a provider-connection flow, which
-- is owner/admin-gated the same way provider_connections itself is.
CREATE POLICY encrypted_secret_write ON encrypted_secrets
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    );
CREATE POLICY encrypted_secret_delete ON encrypted_secrets
    FOR DELETE USING (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    );

-- +goose Down
DROP POLICY encrypted_secret_delete ON encrypted_secrets;
DROP POLICY encrypted_secret_write ON encrypted_secrets;
DROP POLICY encrypted_secret_select ON encrypted_secrets;
DROP TABLE encrypted_secrets;
