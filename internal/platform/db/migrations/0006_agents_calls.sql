-- AI agent configuration and an auditable record of calls and tool activity.
-- Provider secrets are never stored here; secret_ref points to the configured
-- secret store.
-- +goose Up
CREATE TABLE provider_connections (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id   UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    location_id UUID,
    kind        TEXT NOT NULL,
    provider    TEXT NOT NULL,
    external_account_id TEXT,
    secret_ref  TEXT NOT NULL,
    config      JSONB NOT NULL DEFAULT '{}'::JSONB,
    status      TEXT NOT NULL DEFAULT 'active',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT provider_connections_kind_valid CHECK (
        kind IN (
            'telephony', 'speech_to_text', 'text_to_speech', 'llm',
            'calendar', 'vehicle_lookup', 'business_lookup', 'web_search'
        )
    ),
    CONSTRAINT provider_connections_provider_present CHECK (
        btrim(provider) <> ''
    ),
    CONSTRAINT provider_connections_secret_ref_present CHECK (
        btrim(secret_ref) <> ''
    ),
    CONSTRAINT provider_connections_config_object CHECK (
        jsonb_typeof(config) = 'object'
    ),
    CONSTRAINT provider_connections_status_valid CHECK (
        status IN ('active', 'disabled', 'error')
    ),
    UNIQUE (tenant_id, id)
);

CREATE TABLE agents (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id   UUID NOT NULL,
    location_id UUID NOT NULL,
    name        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'draft',
    locale      TEXT NOT NULL DEFAULT 'fr-FR',
    timezone    TEXT NOT NULL DEFAULT 'Europe/Paris',
    system_prompt TEXT NOT NULL DEFAULT '',
    greeting    TEXT NOT NULL DEFAULT '',
    fallback_message TEXT NOT NULL DEFAULT '',
    llm_connection_id UUID,
    speech_to_text_connection_id UUID,
    text_to_speech_connection_id UUID,
    settings    JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, llm_connection_id)
        REFERENCES provider_connections (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, speech_to_text_connection_id)
        REFERENCES provider_connections (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, text_to_speech_connection_id)
        REFERENCES provider_connections (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT agents_name_present CHECK (btrim(name) <> ''),
    CONSTRAINT agents_status_valid CHECK (
        status IN ('draft', 'active', 'paused', 'archived')
    ),
    CONSTRAINT agents_locale_format CHECK (
        locale ~ '^[a-z]{2}(?:-[A-Z]{2})?$'
    ),
    CONSTRAINT agents_settings_object CHECK (
        jsonb_typeof(settings) = 'object'
    ),
    UNIQUE (tenant_id, id)
);

CREATE TABLE phone_numbers (
    id                    UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id             UUID NOT NULL,
    location_id           UUID NOT NULL,
    agent_id              UUID,
    telephony_connection_id UUID NOT NULL,
    phone_e164            TEXT NOT NULL,
    external_number_id    TEXT NOT NULL,
    status                TEXT NOT NULL DEFAULT 'active',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, agent_id)
        REFERENCES agents (tenant_id, id) ON DELETE SET NULL (agent_id),
    FOREIGN KEY (tenant_id, telephony_connection_id)
        REFERENCES provider_connections (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT phone_numbers_e164_format CHECK (
        phone_e164 ~ '^\+[1-9][0-9]{7,14}$'
    ),
    CONSTRAINT phone_numbers_external_id_present CHECK (
        btrim(external_number_id) <> ''
    ),
    CONSTRAINT phone_numbers_status_valid CHECK (
        status IN ('active', 'disabled', 'porting')
    ),
    UNIQUE (phone_e164),
    UNIQUE (tenant_id, id)
);

CREATE TABLE calls (
    id              UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id       UUID NOT NULL,
    location_id     UUID NOT NULL,
    agent_id        UUID NOT NULL,
    phone_number_id UUID,
    customer_id     UUID,
    provider_call_id TEXT NOT NULL,
    direction       TEXT NOT NULL,
    status          TEXT NOT NULL,
    from_e164       TEXT NOT NULL,
    to_e164         TEXT NOT NULL,
    started_at      TIMESTAMPTZ NOT NULL,
    answered_at     TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    summary         TEXT,
    outcome         TEXT,
    recording_uri   TEXT,
    recording_purpose TEXT,
    recording_notice_at TIMESTAMPTZ,
    recording_retention_until TIMESTAMPTZ,
    metadata        JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, agent_id)
        REFERENCES agents (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, phone_number_id)
        REFERENCES phone_numbers (tenant_id, id) ON DELETE SET NULL (phone_number_id),
    FOREIGN KEY (tenant_id, customer_id)
        REFERENCES customers (tenant_id, id) ON DELETE SET NULL (customer_id),
    CONSTRAINT calls_direction_valid CHECK (
        direction IN ('inbound', 'outbound')
    ),
    CONSTRAINT calls_status_valid CHECK (
        status IN (
            'ringing', 'in_progress', 'completed', 'failed',
            'busy', 'no_answer', 'cancelled'
        )
    ),
    CONSTRAINT calls_phone_formats CHECK (
        from_e164 ~ '^\+[1-9][0-9]{7,14}$'
        AND to_e164 ~ '^\+[1-9][0-9]{7,14}$'
    ),
    CONSTRAINT calls_time_ordered CHECK (
        (answered_at IS NULL OR answered_at >= started_at)
        AND (ended_at IS NULL OR ended_at >= COALESCE(answered_at, started_at))
    ),
    CONSTRAINT calls_recording_consistent CHECK (
        (
            recording_uri IS NULL
            AND recording_purpose IS NULL
            AND recording_notice_at IS NULL
            AND recording_retention_until IS NULL
        )
        OR
        (
            recording_uri IS NOT NULL
            AND btrim(recording_uri) <> ''
            AND recording_purpose IN ('contract', 'quality', 'training')
            AND recording_notice_at IS NOT NULL
            AND recording_retention_until > recording_notice_at
        )
    ),
    CONSTRAINT calls_metadata_object CHECK (
        jsonb_typeof(metadata) = 'object'
    ),
    UNIQUE (tenant_id, provider_call_id),
    UNIQUE (tenant_id, id)
);

CREATE INDEX calls_tenant_started_idx
    ON calls (tenant_id, started_at DESC);

CREATE INDEX calls_tenant_customer_started_idx
    ON calls (tenant_id, customer_id, started_at DESC)
    WHERE customer_id IS NOT NULL;

CREATE TABLE call_messages (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id   UUID NOT NULL,
    call_id     UUID NOT NULL,
    sequence    INTEGER NOT NULL,
    speaker     TEXT NOT NULL,
    content     TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    metadata    JSONB NOT NULL DEFAULT '{}'::JSONB,
    FOREIGN KEY (tenant_id, call_id)
        REFERENCES calls (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT call_messages_sequence_valid CHECK (sequence >= 0),
    CONSTRAINT call_messages_speaker_valid CHECK (
        speaker IN ('caller', 'agent', 'system', 'tool')
    ),
    CONSTRAINT call_messages_content_present CHECK (btrim(content) <> ''),
    CONSTRAINT call_messages_metadata_object CHECK (
        jsonb_typeof(metadata) = 'object'
    ),
    UNIQUE (call_id, sequence)
);

CREATE TABLE tool_executions (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id      UUID NOT NULL,
    call_id        UUID NOT NULL,
    idempotency_key TEXT NOT NULL,
    tool_name      TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'started',
    input          JSONB NOT NULL,
    output         JSONB,
    error_code     TEXT,
    error_message  TEXT,
    started_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at   TIMESTAMPTZ,
    FOREIGN KEY (tenant_id, call_id)
        REFERENCES calls (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT tool_executions_name_present CHECK (btrim(tool_name) <> ''),
    CONSTRAINT tool_executions_status_valid CHECK (
        status IN ('started', 'succeeded', 'failed')
    ),
    CONSTRAINT tool_executions_input_object CHECK (
        jsonb_typeof(input) = 'object'
    ),
    CONSTRAINT tool_executions_output_object CHECK (
        output IS NULL OR jsonb_typeof(output) = 'object'
    ),
    CONSTRAINT tool_executions_completion_consistent CHECK (
        (status = 'started' AND completed_at IS NULL)
        OR (status IN ('succeeded', 'failed') AND completed_at IS NOT NULL)
    ),
    UNIQUE (tenant_id, idempotency_key)
);

CREATE TABLE customer_memories (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id   UUID NOT NULL,
    customer_id UUID NOT NULL,
    source_call_id UUID,
    key         TEXT NOT NULL,
    value       JSONB NOT NULL,
    confidence  NUMERIC(4,3),
    status      TEXT NOT NULL DEFAULT 'active',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, customer_id)
        REFERENCES customers (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, source_call_id)
        REFERENCES calls (tenant_id, id) ON DELETE SET NULL (source_call_id),
    CONSTRAINT customer_memories_key_format CHECK (
        key ~ '^[a-z][a-z0-9_.-]{0,99}$'
    ),
    CONSTRAINT customer_memories_confidence_valid CHECK (
        confidence IS NULL OR confidence BETWEEN 0 AND 1
    ),
    CONSTRAINT customer_memories_status_valid CHECK (
        status IN ('active', 'superseded', 'rejected')
    ),
    UNIQUE (tenant_id, customer_id, key)
);

CREATE TABLE appointment_calendar_events (
    tenant_id       UUID NOT NULL,
    appointment_id  UUID NOT NULL,
    connection_id   UUID NOT NULL,
    external_calendar_id TEXT NOT NULL,
    external_event_id TEXT NOT NULL,
    sync_status     TEXT NOT NULL DEFAULT 'pending',
    last_synced_at  TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (appointment_id, connection_id),
    FOREIGN KEY (tenant_id, appointment_id)
        REFERENCES appointments (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, connection_id)
        REFERENCES provider_connections (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT appointment_calendar_sync_status_valid CHECK (
        sync_status IN ('pending', 'synced', 'error', 'deleted')
    ),
    UNIQUE (connection_id, external_calendar_id, external_event_id)
);

-- +goose Down
DROP TABLE appointment_calendar_events;
DROP TABLE customer_memories;
DROP TABLE tool_executions;
DROP TABLE call_messages;
DROP TABLE calls;
DROP TABLE phone_numbers;
DROP TABLE agents;
DROP TABLE provider_connections;
