-- Opening-hour integrity, exceptional closures, and booking-time enforcement.
-- +goose Up
ALTER TABLE location_opening_hours
    ADD COLUMN open_during INT8RANGE GENERATED ALWAYS AS (
        int8range(
            extract(epoch FROM opens_at)::BIGINT,
            extract(epoch FROM closes_at)::BIGINT,
            '[)'
        )
    ) STORED;

ALTER TABLE location_opening_hours
    ADD CONSTRAINT location_opening_hours_no_overlap
    EXCLUDE USING gist (
        location_id WITH =,
        weekday WITH =,
        open_during WITH &&
    );

CREATE TABLE location_closures (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id      UUID NOT NULL,
    location_id    UUID NOT NULL,
    starts_at      TIMESTAMPTZ NOT NULL,
    ends_at        TIMESTAMPTZ NOT NULL,
    closed_during  TSTZRANGE GENERATED ALWAYS AS (
        tstzrange(starts_at, ends_at, '[)')
    ) STORED,
    reason         TEXT,
    created_by_user_id UUID NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, created_by_user_id)
        REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE RESTRICT,
    CONSTRAINT location_closures_time_ordered CHECK (starts_at < ends_at),
    CONSTRAINT location_closures_reason_valid CHECK (
        reason IS NULL OR (btrim(reason) <> '' AND char_length(reason) <= 300)
    ),
    UNIQUE (tenant_id, id),
    EXCLUDE USING gist (
        location_id WITH =,
        closed_during WITH &&
    )
);

CREATE INDEX location_closures_location_start_idx
    ON location_closures (tenant_id, location_id, starts_at);

ALTER TABLE location_closures ENABLE ROW LEVEL SECURITY;
ALTER TABLE location_closures FORCE ROW LEVEL SECURITY;
CREATE POLICY location_closure_select ON location_closures
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );
CREATE POLICY location_closure_insert ON location_closures
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
        AND created_by_user_id = app_current_user_id()
    );
CREATE POLICY location_closure_delete ON location_closures
    FOR DELETE USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );

-- +goose StatementBegin
CREATE FUNCTION validate_appointment_working_time()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    timezone_name TEXT;
    local_start TIMESTAMP;
    local_end TIMESTAMP;
    local_weekday SMALLINT;
    schedule_configured BOOLEAN;
BEGIN
    IF NEW.status NOT IN ('pending', 'confirmed', 'in_progress') THEN
        RETURN NEW;
    END IF;

    -- Serialize the cross-table closure/appointment checks per location.
    PERFORM pg_advisory_xact_lock(hashtextextended(NEW.location_id::text, 0));

    SELECT timezone INTO timezone_name
    FROM locations
    WHERE tenant_id = NEW.tenant_id AND id = NEW.location_id;

    local_start := NEW.starts_at AT TIME ZONE timezone_name;
    local_end := NEW.ends_at AT TIME ZONE timezone_name;
    local_weekday := extract(dow FROM local_start)::SMALLINT;

    SELECT EXISTS (
        SELECT 1 FROM location_opening_hours opening
        WHERE opening.tenant_id = NEW.tenant_id
          AND opening.location_id = NEW.location_id
    ) INTO schedule_configured;

    IF schedule_configured AND NOT EXISTS (
        SELECT 1 FROM location_opening_hours opening
        WHERE opening.tenant_id = NEW.tenant_id
          AND opening.location_id = NEW.location_id
          AND opening.weekday = local_weekday
          AND local_start::date = local_end::date
          AND opening.opens_at <= local_start::time
          AND opening.closes_at >= local_end::time
    ) THEN
        RAISE EXCEPTION 'appointment falls outside configured opening hours'
            USING ERRCODE = '23514', CONSTRAINT = 'appointments_within_opening_hours';
    END IF;

    IF EXISTS (
        SELECT 1 FROM location_closures closure
        WHERE closure.tenant_id = NEW.tenant_id
          AND closure.location_id = NEW.location_id
          AND closure.closed_during && tstzrange(NEW.starts_at, NEW.ends_at, '[)')
    ) THEN
        RAISE EXCEPTION 'appointment overlaps a workshop closure'
            USING ERRCODE = '23514', CONSTRAINT = 'appointments_avoid_closures';
    END IF;

    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION validate_location_closure()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended(NEW.location_id::text, 0));

    IF EXISTS (
        SELECT 1 FROM appointments appointment
        WHERE appointment.tenant_id = NEW.tenant_id
          AND appointment.location_id = NEW.location_id
          AND appointment.status IN ('pending', 'confirmed', 'in_progress')
          AND appointment.occupied_during && tstzrange(NEW.starts_at, NEW.ends_at, '[)')
    ) THEN
        RAISE EXCEPTION 'workshop closure overlaps an active appointment'
            USING ERRCODE = '23514', CONSTRAINT = 'closures_avoid_appointments';
    END IF;

    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER location_closures_validate
BEFORE INSERT OR UPDATE OF location_id, starts_at, ends_at ON location_closures
FOR EACH ROW EXECUTE FUNCTION validate_location_closure();

CREATE TRIGGER appointments_validate_working_time
BEFORE INSERT OR UPDATE OF location_id, starts_at, ends_at, status ON appointments
FOR EACH ROW EXECUTE FUNCTION validate_appointment_working_time();

-- +goose Down
DROP TRIGGER appointments_validate_working_time ON appointments;
DROP TRIGGER location_closures_validate ON location_closures;
DROP FUNCTION validate_location_closure();
DROP FUNCTION validate_appointment_working_time();
DROP POLICY location_closure_delete ON location_closures;
DROP POLICY location_closure_insert ON location_closures;
DROP POLICY location_closure_select ON location_closures;
ALTER TABLE location_closures NO FORCE ROW LEVEL SECURITY;
ALTER TABLE location_closures DISABLE ROW LEVEL SECURITY;
DROP TABLE location_closures;
ALTER TABLE location_opening_hours DROP CONSTRAINT location_opening_hours_no_overlap;
ALTER TABLE location_opening_hours DROP COLUMN open_during;
