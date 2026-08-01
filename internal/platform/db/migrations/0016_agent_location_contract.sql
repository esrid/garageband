-- Make the "one telephone agent per physical location" product rule true in
-- PostgreSQL and prevent cross-location/provider-kind configuration mistakes.
-- +goose Up
INSERT INTO agents (tenant_id, location_id, name)
SELECT location.tenant_id, location.id,
       left('Agent — ' || location.name, 120)
FROM locations location
WHERE NOT EXISTS (
    SELECT 1
    FROM agents agent
    WHERE agent.tenant_id = location.tenant_id
      AND agent.location_id = location.id
);

ALTER TABLE agents
    ADD CONSTRAINT agents_one_per_location
        UNIQUE (tenant_id, location_id),
    ADD CONSTRAINT agents_name_length CHECK (
        char_length(name) BETWEEN 1 AND 120
    ),
    ADD CONSTRAINT agents_greeting_length CHECK (
        char_length(greeting) <= 1000
    ),
    ADD CONSTRAINT agents_system_prompt_length CHECK (
        char_length(system_prompt) <= 20000
    ),
    ADD CONSTRAINT agents_fallback_message_length CHECK (
        char_length(fallback_message) <= 2000
    );

ALTER TABLE provider_connections
    ADD CONSTRAINT provider_connections_location_kind_key
        UNIQUE (tenant_id, location_id, id, kind);

ALTER TABLE agents
    ADD COLUMN llm_connection_kind TEXT
        GENERATED ALWAYS AS ('llm') STORED,
    ADD COLUMN speech_to_text_connection_kind TEXT
        GENERATED ALWAYS AS ('speech_to_text') STORED,
    ADD COLUMN text_to_speech_connection_kind TEXT
        GENERATED ALWAYS AS ('text_to_speech') STORED,
    ADD CONSTRAINT agents_llm_connection_location_kind_fkey
        FOREIGN KEY (
            tenant_id, location_id, llm_connection_id, llm_connection_kind
        ) REFERENCES provider_connections (tenant_id, location_id, id, kind)
        ON DELETE RESTRICT,
    ADD CONSTRAINT agents_stt_connection_location_kind_fkey
        FOREIGN KEY (
            tenant_id, location_id, speech_to_text_connection_id,
            speech_to_text_connection_kind
        ) REFERENCES provider_connections (tenant_id, location_id, id, kind)
        ON DELETE RESTRICT,
    ADD CONSTRAINT agents_tts_connection_location_kind_fkey
        FOREIGN KEY (
            tenant_id, location_id, text_to_speech_connection_id,
            text_to_speech_connection_kind
        ) REFERENCES provider_connections (tenant_id, location_id, id, kind)
        ON DELETE RESTRICT;

-- +goose StatementBegin
CREATE FUNCTION create_location_agent()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO agents (tenant_id, location_id, name, timezone)
        VALUES (
            NEW.tenant_id,
            NEW.id,
            left('Agent — ' || NEW.name, 120),
            NEW.timezone
        );
    ELSE
        UPDATE agents
        SET timezone = NEW.timezone, updated_at = now()
        WHERE tenant_id = NEW.tenant_id AND location_id = NEW.id;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER locations_create_agent
AFTER INSERT OR UPDATE OF timezone ON locations
FOR EACH ROW EXECUTE FUNCTION create_location_agent();

-- +goose Down
DROP TRIGGER locations_create_agent ON locations;
DROP FUNCTION create_location_agent();

ALTER TABLE agents
    DROP CONSTRAINT agents_tts_connection_location_kind_fkey,
    DROP CONSTRAINT agents_stt_connection_location_kind_fkey,
    DROP CONSTRAINT agents_llm_connection_location_kind_fkey,
    DROP COLUMN text_to_speech_connection_kind,
    DROP COLUMN speech_to_text_connection_kind,
    DROP COLUMN llm_connection_kind;

ALTER TABLE provider_connections
    DROP CONSTRAINT provider_connections_location_kind_key;

ALTER TABLE agents
    DROP CONSTRAINT agents_fallback_message_length,
    DROP CONSTRAINT agents_system_prompt_length,
    DROP CONSTRAINT agents_greeting_length,
    DROP CONSTRAINT agents_name_length,
    DROP CONSTRAINT agents_one_per_location;
