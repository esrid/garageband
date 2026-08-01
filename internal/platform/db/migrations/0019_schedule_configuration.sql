-- Persist whether a location has deliberately configured its weekly schedule.
-- An enabled schedule with no row for a weekday means that weekday is closed;
-- this remains true even when the last opening window is removed.
-- +goose Up
ALTER TABLE locations
    ADD COLUMN availability_schedule_enabled BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE locations location
SET availability_schedule_enabled = TRUE
WHERE EXISTS (
    SELECT 1 FROM location_opening_hours opening
    WHERE opening.tenant_id = location.tenant_id
      AND opening.location_id = location.id
);

-- +goose StatementBegin
CREATE FUNCTION enable_location_schedule()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended(NEW.location_id::text, 0));
    UPDATE locations
    SET availability_schedule_enabled = TRUE
    WHERE tenant_id = NEW.tenant_id AND id = NEW.location_id;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER location_opening_hours_enable_schedule
BEFORE INSERT ON location_opening_hours
FOR EACH ROW EXECUTE FUNCTION enable_location_schedule();

-- +goose StatementBegin
CREATE FUNCTION lock_location_schedule_change()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended(OLD.location_id::text, 0));
    RETURN OLD;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER location_opening_hours_lock_delete
BEFORE DELETE ON location_opening_hours
FOR EACH ROW EXECUTE FUNCTION lock_location_schedule_change();

-- +goose StatementBegin
CREATE FUNCTION validate_location_schedule_after_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    timezone_name TEXT;
BEGIN
    SELECT timezone INTO timezone_name
    FROM locations
    WHERE tenant_id = OLD.tenant_id AND id = OLD.location_id;

    IF EXISTS (
        SELECT 1
        FROM appointments appointment
        WHERE appointment.tenant_id = OLD.tenant_id
          AND appointment.location_id = OLD.location_id
          AND appointment.status IN ('pending', 'confirmed', 'in_progress')
          AND appointment.ends_at > now()
          AND NOT EXISTS (
              SELECT 1
              FROM location_opening_hours opening
              WHERE opening.tenant_id = appointment.tenant_id
                AND opening.location_id = appointment.location_id
                AND opening.weekday = extract(
                    dow FROM appointment.starts_at AT TIME ZONE timezone_name
                )::SMALLINT
                AND (appointment.starts_at AT TIME ZONE timezone_name)::date =
                    (appointment.ends_at AT TIME ZONE timezone_name)::date
                AND opening.opens_at <=
                    (appointment.starts_at AT TIME ZONE timezone_name)::time
                AND opening.closes_at >=
                    (appointment.ends_at AT TIME ZONE timezone_name)::time
          )
    ) THEN
        RAISE EXCEPTION 'opening-hour removal would strand an active appointment'
            USING ERRCODE = '23514', CONSTRAINT = 'opening_hours_preserve_appointments';
    END IF;

    RETURN OLD;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER location_opening_hours_validate_delete
AFTER DELETE ON location_opening_hours
FOR EACH ROW EXECUTE FUNCTION validate_location_schedule_after_delete();

-- +goose StatementBegin
CREATE FUNCTION preserve_location_timezone_history()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.timezone IS DISTINCT FROM OLD.timezone AND (
        EXISTS (
            SELECT 1 FROM appointments appointment
            WHERE appointment.tenant_id = OLD.tenant_id
              AND appointment.location_id = OLD.id
        )
        OR EXISTS (
            SELECT 1 FROM location_closures closure
            WHERE closure.tenant_id = OLD.tenant_id
              AND closure.location_id = OLD.id
        )
    ) THEN
        RAISE EXCEPTION 'location timezone cannot change after scheduling activity'
            USING ERRCODE = '23514', CONSTRAINT = 'locations_timezone_preserves_schedule';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER locations_preserve_timezone_history
BEFORE UPDATE OF timezone ON locations
FOR EACH ROW EXECUTE FUNCTION preserve_location_timezone_history();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_appointment_working_time()
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

    PERFORM pg_advisory_xact_lock(hashtextextended(NEW.location_id::text, 0));

    SELECT timezone, availability_schedule_enabled
    INTO timezone_name, schedule_configured
    FROM locations
    WHERE tenant_id = NEW.tenant_id AND id = NEW.location_id;

    local_start := NEW.starts_at AT TIME ZONE timezone_name;
    local_end := NEW.ends_at AT TIME ZONE timezone_name;
    local_weekday := extract(dow FROM local_start)::SMALLINT;

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

-- +goose Down
DROP TRIGGER locations_preserve_timezone_history ON locations;
DROP FUNCTION preserve_location_timezone_history();
DROP TRIGGER location_opening_hours_validate_delete ON location_opening_hours;
DROP FUNCTION validate_location_schedule_after_delete();
DROP TRIGGER location_opening_hours_lock_delete ON location_opening_hours;
DROP FUNCTION lock_location_schedule_change();
DROP TRIGGER location_opening_hours_enable_schedule ON location_opening_hours;
DROP FUNCTION enable_location_schedule();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_appointment_working_time()
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

ALTER TABLE locations DROP COLUMN availability_schedule_enabled;
