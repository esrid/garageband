-- Onboarding drafts exist before a tenant does. They are therefore authorized
-- by (id, user_id) in every application query instead of tenant RLS. Finalizing
-- a draft creates all tenant-owned rows atomically and records tenant_id here
-- to make confirmation retries idempotent.
-- +goose Up
CREATE TABLE onboarding_drafts (
    id            UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    source_kind   TEXT NOT NULL,
    source_value  TEXT NOT NULL,
    provider      TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'ready',
    profile       JSONB NOT NULL,
    tenant_id     UUID REFERENCES tenants (id) ON DELETE SET NULL,
    error_code    TEXT,
    error_message TEXT,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT onboarding_drafts_source_valid CHECK (
        source_kind IN ('siret', 'website')
    ),
    CONSTRAINT onboarding_drafts_source_present CHECK (
        btrim(source_value) <> ''
    ),
    CONSTRAINT onboarding_drafts_provider_present CHECK (
        btrim(provider) <> ''
    ),
    CONSTRAINT onboarding_drafts_status_valid CHECK (
        status IN ('ready', 'failed', 'completed')
    ),
    CONSTRAINT onboarding_drafts_profile_object CHECK (
        jsonb_typeof(profile) = 'object'
    ),
    CONSTRAINT onboarding_drafts_completion_consistent CHECK (
        (status = 'completed' AND tenant_id IS NOT NULL)
        OR (status <> 'completed' AND tenant_id IS NULL)
    )
);

CREATE INDEX onboarding_drafts_user_created_idx
    ON onboarding_drafts (user_id, created_at DESC);

-- +goose Down
DROP TABLE onboarding_drafts;
