-- Tenant = garage business. A tenant can own one or more physical locations.
-- Users remain global identities and join tenants through memberships.
-- +goose Up
CREATE TABLE tenants (
    id           UUID PRIMARY KEY DEFAULT uuidv7(),
    slug         TEXT NOT NULL,
    name         TEXT NOT NULL,
    legal_name   TEXT,
    siren        TEXT,
    website_url  TEXT,
    default_locale TEXT NOT NULL DEFAULT 'fr-FR',
    status       TEXT NOT NULL DEFAULT 'active',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT tenants_slug_format CHECK (
        slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'
        AND char_length(slug) BETWEEN 2 AND 63
    ),
    CONSTRAINT tenants_name_present CHECK (btrim(name) <> ''),
    CONSTRAINT tenants_siren_format CHECK (
        siren IS NULL OR siren ~ '^[0-9]{9}$'
    ),
    CONSTRAINT tenants_website_url_format CHECK (
        website_url IS NULL OR website_url ~* '^https?://[^[:space:]]+$'
    ),
    CONSTRAINT tenants_locale_format CHECK (
        default_locale ~ '^[a-z]{2}(?:-[A-Z]{2})?$'
    ),
    CONSTRAINT tenants_status_valid CHECK (
        status IN ('active', 'suspended', 'closed')
    ),
    UNIQUE (slug)
);

CREATE TABLE tenant_memberships (
    tenant_id UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    user_id   UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id),
    CONSTRAINT tenant_memberships_role_valid CHECK (
        role IN ('owner', 'admin', 'manager', 'member')
    )
);

CREATE INDEX tenant_memberships_user_tenant_idx
    ON tenant_memberships (user_id, tenant_id);

CREATE TABLE locations (
    id           UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id    UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    slug         TEXT NOT NULL,
    name         TEXT NOT NULL,
    siret        TEXT,
    phone_e164   TEXT,
    email        TEXT,
    address_line1 TEXT,
    address_line2 TEXT,
    postal_code  TEXT,
    city         TEXT,
    country_code TEXT NOT NULL DEFAULT 'FR',
    timezone     TEXT NOT NULL DEFAULT 'Europe/Paris',
    status       TEXT NOT NULL DEFAULT 'active',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT locations_slug_format CHECK (
        slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'
        AND char_length(slug) BETWEEN 2 AND 63
    ),
    CONSTRAINT locations_name_present CHECK (btrim(name) <> ''),
    CONSTRAINT locations_siret_format CHECK (
        siret IS NULL OR siret ~ '^[0-9]{14}$'
    ),
    CONSTRAINT locations_phone_format CHECK (
        phone_e164 IS NULL OR phone_e164 ~ '^\+[1-9][0-9]{7,14}$'
    ),
    CONSTRAINT locations_email_normalized CHECK (
        email IS NULL OR (email = lower(email) AND email ~ '^[^[:space:]@]+@[^[:space:]@]+$')
    ),
    CONSTRAINT locations_country_code_format CHECK (
        country_code ~ '^[A-Z]{2}$'
    ),
    CONSTRAINT locations_status_valid CHECK (
        status IN ('active', 'inactive')
    ),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, slug)
);

CREATE UNIQUE INDEX locations_siret_unique
    ON locations (siret)
    WHERE siret IS NOT NULL;

CREATE TABLE location_opening_hours (
    tenant_id  UUID NOT NULL,
    location_id UUID NOT NULL,
    weekday    SMALLINT NOT NULL,
    opens_at   TIME NOT NULL,
    closes_at  TIME NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (location_id, weekday, opens_at),
    FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT location_opening_hours_weekday_valid CHECK (
        weekday BETWEEN 0 AND 6
    ),
    CONSTRAINT location_opening_hours_ordered CHECK (opens_at < closes_at)
);

CREATE TABLE business_enrichment_runs (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id   UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    source_kind TEXT NOT NULL,
    source_value TEXT NOT NULL,
    provider    TEXT,
    status      TEXT NOT NULL DEFAULT 'pending',
    result      JSONB,
    error_code  TEXT,
    error_message TEXT,
    started_at  TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT business_enrichment_source_valid CHECK (
        source_kind IN ('siret', 'website')
    ),
    CONSTRAINT business_enrichment_source_present CHECK (
        btrim(source_value) <> ''
    ),
    CONSTRAINT business_enrichment_status_valid CHECK (
        status IN ('pending', 'running', 'succeeded', 'failed')
    ),
    CONSTRAINT business_enrichment_result_object CHECK (
        result IS NULL OR jsonb_typeof(result) = 'object'
    ),
    CONSTRAINT business_enrichment_completion_consistent CHECK (
        (status IN ('pending', 'running') AND completed_at IS NULL)
        OR (status IN ('succeeded', 'failed') AND completed_at IS NOT NULL)
    )
);

CREATE INDEX business_enrichment_runs_tenant_created_idx
    ON business_enrichment_runs (tenant_id, created_at DESC);

ALTER TABLE sessions
    ADD COLUMN active_tenant_id UUID REFERENCES tenants (id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE sessions DROP COLUMN active_tenant_id;
DROP TABLE business_enrichment_runs;
DROP TABLE location_opening_hours;
DROP TABLE locations;
DROP TABLE tenant_memberships;
DROP TABLE tenants;
