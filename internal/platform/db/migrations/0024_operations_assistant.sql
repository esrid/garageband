-- Location-scoped employee conversations and confirmed, immutable action audit.
-- +goose Up
CREATE TABLE assistant_conversations (
    id                 UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id          UUID NOT NULL,
    location_id        UUID NOT NULL,
    created_by_user_id UUID NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    title              TEXT NOT NULL DEFAULT 'Nouvelle conversation',
    status             TEXT NOT NULL DEFAULT 'active',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT assistant_conversations_title_present CHECK (
        btrim(title) <> '' AND char_length(title) <= 160
    ),
    CONSTRAINT assistant_conversations_status_valid CHECK (
        status IN ('active', 'archived')
    ),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, location_id, id)
);

CREATE INDEX assistant_conversations_user_recent_idx
    ON assistant_conversations (tenant_id, created_by_user_id, updated_at DESC);

CREATE TABLE assistant_messages (
    id              UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id       UUID NOT NULL,
    location_id     UUID NOT NULL,
    conversation_id UUID NOT NULL,
    sequence        INTEGER NOT NULL,
    role            TEXT NOT NULL,
    content         TEXT NOT NULL,
    metadata        JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, location_id, conversation_id)
        REFERENCES assistant_conversations (tenant_id, location_id, id) ON DELETE CASCADE,
    CONSTRAINT assistant_messages_sequence_valid CHECK (sequence >= 0),
    CONSTRAINT assistant_messages_role_valid CHECK (
        role IN ('user', 'assistant', 'system', 'tool')
    ),
    CONSTRAINT assistant_messages_content_present CHECK (
        btrim(content) <> '' AND char_length(content) <= 20000
    ),
    CONSTRAINT assistant_messages_metadata_object CHECK (
        jsonb_typeof(metadata) = 'object'
    ),
    UNIQUE (conversation_id, sequence),
    UNIQUE (tenant_id, id)
);

CREATE INDEX assistant_messages_conversation_idx
    ON assistant_messages (tenant_id, conversation_id, sequence);

CREATE TABLE assistant_tool_executions (
    id                   UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id            UUID NOT NULL,
    location_id          UUID NOT NULL,
    conversation_id      UUID NOT NULL,
    requested_by_user_id UUID NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    idempotency_key      TEXT NOT NULL,
    tool_name            TEXT NOT NULL,
    consequence          TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'proposed',
    input                JSONB NOT NULL,
    preview              JSONB NOT NULL,
    output               JSONB,
    affected_records     JSONB NOT NULL DEFAULT '[]'::JSONB,
    error_code           TEXT,
    error_message        TEXT,
    proposed_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_at         TIMESTAMPTZ,
    completed_at         TIMESTAMPTZ,
    FOREIGN KEY (tenant_id, location_id, conversation_id)
        REFERENCES assistant_conversations (tenant_id, location_id, id) ON DELETE CASCADE,
    CONSTRAINT assistant_tool_executions_key_present CHECK (
        btrim(idempotency_key) <> '' AND char_length(idempotency_key) <= 200
    ),
    CONSTRAINT assistant_tool_executions_name_present CHECK (
        btrim(tool_name) <> '' AND char_length(tool_name) <= 120
    ),
    CONSTRAINT assistant_tool_executions_consequence_valid CHECK (
        consequence IN ('read', 'write', 'destructive')
    ),
    CONSTRAINT assistant_tool_executions_status_valid CHECK (
        status IN ('proposed', 'running', 'succeeded', 'failed', 'rejected')
    ),
    CONSTRAINT assistant_tool_executions_input_object CHECK (
        jsonb_typeof(input) = 'object'
    ),
    CONSTRAINT assistant_tool_executions_preview_object CHECK (
        jsonb_typeof(preview) = 'object'
    ),
    CONSTRAINT assistant_tool_executions_output_object CHECK (
        output IS NULL OR jsonb_typeof(output) = 'object'
    ),
    CONSTRAINT assistant_tool_executions_affected_array CHECK (
        jsonb_typeof(affected_records) = 'array'
    ),
    CONSTRAINT assistant_tool_executions_lifecycle_consistent CHECK (
        (status = 'proposed' AND confirmed_at IS NULL AND completed_at IS NULL)
        OR (status = 'running' AND confirmed_at IS NOT NULL AND completed_at IS NULL)
        OR (status IN ('succeeded', 'failed')
            AND confirmed_at IS NOT NULL AND completed_at IS NOT NULL)
        OR (status = 'rejected' AND completed_at IS NOT NULL)
    ),
    UNIQUE (tenant_id, conversation_id, idempotency_key),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, location_id, id)
);

CREATE INDEX assistant_tool_executions_conversation_idx
    ON assistant_tool_executions (tenant_id, conversation_id, proposed_at, id);

-- A domain tool writes this receipt in the same transaction as its mutation.
-- It closes the crash window between applying the change and finalizing the
-- conversational audit: a retry returns the receipt instead of replaying it.
CREATE TABLE application_tool_receipts (
    tenant_id       UUID NOT NULL,
    location_id     UUID NOT NULL,
    idempotency_key TEXT NOT NULL,
    tool_name       TEXT NOT NULL,
    output          JSONB NOT NULL,
    affected_records JSONB NOT NULL,
    applied_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT application_tool_receipts_key_present CHECK (
        btrim(idempotency_key) <> '' AND char_length(idempotency_key) <= 200
    ),
    CONSTRAINT application_tool_receipts_name_present CHECK (btrim(tool_name) <> ''),
    CONSTRAINT application_tool_receipts_output_object CHECK (jsonb_typeof(output) = 'object'),
    CONSTRAINT application_tool_receipts_affected_array CHECK (jsonb_typeof(affected_records) = 'array'),
    PRIMARY KEY (tenant_id, idempotency_key)
);

-- Messages are append-only; a correction is another message, not a rewrite.
-- +goose StatementBegin
CREATE FUNCTION protect_assistant_message_history()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'assistant message history is immutable'
        USING ERRCODE = '23514', CONSTRAINT = 'assistant_message_history_immutable';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER assistant_messages_protect_history
BEFORE UPDATE OR DELETE ON assistant_messages
FOR EACH ROW EXECUTE FUNCTION protect_assistant_message_history();

-- Only the state-machine fields may change, and terminal audit rows are frozen.
-- +goose StatementBegin
CREATE FUNCTION protect_assistant_tool_audit()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'assistant tool audit is immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'assistant_tool_audit_immutable';
    END IF;
    IF OLD.tenant_id IS DISTINCT FROM NEW.tenant_id
       OR OLD.location_id IS DISTINCT FROM NEW.location_id
       OR OLD.conversation_id IS DISTINCT FROM NEW.conversation_id
       OR OLD.requested_by_user_id IS DISTINCT FROM NEW.requested_by_user_id
       OR OLD.idempotency_key IS DISTINCT FROM NEW.idempotency_key
       OR OLD.tool_name IS DISTINCT FROM NEW.tool_name
       OR OLD.consequence IS DISTINCT FROM NEW.consequence
       OR OLD.input IS DISTINCT FROM NEW.input
       OR OLD.preview IS DISTINCT FROM NEW.preview
       OR OLD.proposed_at IS DISTINCT FROM NEW.proposed_at
       OR OLD.status IN ('succeeded', 'failed', 'rejected')
       OR (OLD.status = 'proposed' AND NEW.status NOT IN ('running', 'rejected'))
       OR (OLD.status = 'running' AND NEW.status NOT IN ('succeeded', 'failed'))
       OR (OLD.status = 'proposed' AND (
           OLD.output IS DISTINCT FROM NEW.output
           OR OLD.affected_records IS DISTINCT FROM NEW.affected_records
           OR OLD.error_code IS DISTINCT FROM NEW.error_code
           OR OLD.error_message IS DISTINCT FROM NEW.error_message
       ))
       OR (OLD.status = 'proposed' AND NEW.status = 'running'
           AND OLD.completed_at IS DISTINCT FROM NEW.completed_at)
       OR (OLD.status = 'proposed' AND NEW.status = 'rejected'
           AND OLD.confirmed_at IS DISTINCT FROM NEW.confirmed_at)
       OR (OLD.status = 'running'
           AND OLD.confirmed_at IS DISTINCT FROM NEW.confirmed_at) THEN
        RAISE EXCEPTION 'assistant tool audit is immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'assistant_tool_audit_immutable';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER assistant_tool_executions_protect_audit
BEFORE UPDATE OR DELETE ON assistant_tool_executions
FOR EACH ROW EXECUTE FUNCTION protect_assistant_tool_audit();

-- +goose StatementBegin
CREATE FUNCTION protect_application_tool_receipt()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'application tool receipt is immutable'
        USING ERRCODE = '23514', CONSTRAINT = 'application_tool_receipt_immutable';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER application_tool_receipts_protect_history
BEFORE UPDATE OR DELETE ON application_tool_receipts
FOR EACH ROW EXECUTE FUNCTION protect_application_tool_receipt();

ALTER TABLE assistant_conversations ENABLE ROW LEVEL SECURITY;
ALTER TABLE assistant_conversations FORCE ROW LEVEL SECURITY;
CREATE POLICY assistant_conversation_select ON assistant_conversations
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND created_by_user_id = app_current_user_id()
        AND app_current_user_can_access_location(location_id)
    );
CREATE POLICY assistant_conversation_insert ON assistant_conversations
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND created_by_user_id = app_current_user_id()
        AND app_current_user_can_access_location(location_id)
    );
CREATE POLICY assistant_conversation_update ON assistant_conversations
    FOR UPDATE USING (
        tenant_id = app_current_tenant_id()
        AND created_by_user_id = app_current_user_id()
        AND app_current_user_can_access_location(location_id)
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND created_by_user_id = app_current_user_id()
        AND app_current_user_can_access_location(location_id)
    );

ALTER TABLE assistant_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE assistant_messages FORCE ROW LEVEL SECURITY;
CREATE POLICY assistant_message_select ON assistant_messages
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM assistant_conversations conversation
            WHERE conversation.tenant_id = assistant_messages.tenant_id
              AND conversation.id = assistant_messages.conversation_id
        )
    );
CREATE POLICY assistant_message_insert ON assistant_messages
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM assistant_conversations conversation
            WHERE conversation.tenant_id = assistant_messages.tenant_id
              AND conversation.location_id = assistant_messages.location_id
              AND conversation.id = assistant_messages.conversation_id
        )
    );

ALTER TABLE assistant_tool_executions ENABLE ROW LEVEL SECURITY;
ALTER TABLE assistant_tool_executions FORCE ROW LEVEL SECURITY;
CREATE POLICY assistant_tool_execution_select ON assistant_tool_executions
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND requested_by_user_id = app_current_user_id()
        AND EXISTS (
            SELECT 1 FROM assistant_conversations conversation
            WHERE conversation.tenant_id = assistant_tool_executions.tenant_id
              AND conversation.id = assistant_tool_executions.conversation_id
        )
    );
CREATE POLICY assistant_tool_execution_insert ON assistant_tool_executions
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND requested_by_user_id = app_current_user_id()
        AND EXISTS (
            SELECT 1 FROM assistant_conversations conversation
            WHERE conversation.tenant_id = assistant_tool_executions.tenant_id
              AND conversation.location_id = assistant_tool_executions.location_id
              AND conversation.id = assistant_tool_executions.conversation_id
        )
    );
CREATE POLICY assistant_tool_execution_update ON assistant_tool_executions
    FOR UPDATE USING (
        tenant_id = app_current_tenant_id()
        AND requested_by_user_id = app_current_user_id()
        AND EXISTS (
            SELECT 1 FROM assistant_conversations conversation
            WHERE conversation.tenant_id = assistant_tool_executions.tenant_id
              AND conversation.id = assistant_tool_executions.conversation_id
        )
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND requested_by_user_id = app_current_user_id()
    );

ALTER TABLE application_tool_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE application_tool_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY application_tool_receipt_select ON application_tool_receipts
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );
CREATE POLICY application_tool_receipt_insert ON application_tool_receipts
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );

-- +goose Down
DROP POLICY application_tool_receipt_insert ON application_tool_receipts;
DROP POLICY application_tool_receipt_select ON application_tool_receipts;
ALTER TABLE application_tool_receipts NO FORCE ROW LEVEL SECURITY;
ALTER TABLE application_tool_receipts DISABLE ROW LEVEL SECURITY;
DROP POLICY assistant_tool_execution_update ON assistant_tool_executions;
DROP POLICY assistant_tool_execution_insert ON assistant_tool_executions;
DROP POLICY assistant_tool_execution_select ON assistant_tool_executions;
ALTER TABLE assistant_tool_executions NO FORCE ROW LEVEL SECURITY;
ALTER TABLE assistant_tool_executions DISABLE ROW LEVEL SECURITY;
DROP POLICY assistant_message_insert ON assistant_messages;
DROP POLICY assistant_message_select ON assistant_messages;
ALTER TABLE assistant_messages NO FORCE ROW LEVEL SECURITY;
ALTER TABLE assistant_messages DISABLE ROW LEVEL SECURITY;
DROP POLICY assistant_conversation_update ON assistant_conversations;
DROP POLICY assistant_conversation_insert ON assistant_conversations;
DROP POLICY assistant_conversation_select ON assistant_conversations;
ALTER TABLE assistant_conversations NO FORCE ROW LEVEL SECURITY;
ALTER TABLE assistant_conversations DISABLE ROW LEVEL SECURITY;
DROP TRIGGER application_tool_receipts_protect_history ON application_tool_receipts;
DROP FUNCTION protect_application_tool_receipt();
DROP TRIGGER assistant_tool_executions_protect_audit ON assistant_tool_executions;
DROP FUNCTION protect_assistant_tool_audit();
DROP TRIGGER assistant_messages_protect_history ON assistant_messages;
DROP FUNCTION protect_assistant_message_history();
DROP TABLE application_tool_receipts;
DROP TABLE assistant_tool_executions;
DROP TABLE assistant_messages;
DROP TABLE assistant_conversations;
