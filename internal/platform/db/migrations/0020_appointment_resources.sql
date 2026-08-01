-- Reserve every technician, bay, or piece of equipment used by one appointment.
-- +goose Up
ALTER TABLE appointments
    ADD CONSTRAINT appointments_tenant_location_id_unique
    UNIQUE (tenant_id, location_id, id);

CREATE TABLE appointment_resource_reservations (
    tenant_id      UUID NOT NULL,
    location_id    UUID NOT NULL,
    appointment_id UUID NOT NULL,
    resource_id    UUID NOT NULL,
    starts_at      TIMESTAMPTZ NOT NULL,
    ends_at        TIMESTAMPTZ NOT NULL,
    status         TEXT NOT NULL,
    occupied_during TSTZRANGE GENERATED ALWAYS AS (
        tstzrange(starts_at, ends_at, '[)')
    ) STORED,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (appointment_id, resource_id),
    FOREIGN KEY (tenant_id, location_id, appointment_id)
        REFERENCES appointments (tenant_id, location_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, location_id, resource_id)
        REFERENCES bookable_resources (tenant_id, location_id, id) ON DELETE RESTRICT,
    CONSTRAINT appointment_resource_reservations_status_valid CHECK (
        status IN (
            'draft', 'pending', 'confirmed', 'in_progress',
            'completed', 'cancelled', 'no_show'
        )
    ),
    CONSTRAINT appointment_resource_reservations_time_ordered CHECK (
        starts_at < ends_at
    ),
    UNIQUE (tenant_id, appointment_id, resource_id),
    CONSTRAINT appointment_resource_reservations_no_overlap
    EXCLUDE USING gist (
        resource_id WITH =,
        occupied_during WITH &&
    ) WHERE (status IN ('pending', 'confirmed', 'in_progress'))
);

CREATE INDEX appointment_resource_reservations_appointment_idx
    ON appointment_resource_reservations (tenant_id, appointment_id);

INSERT INTO appointment_resource_reservations (
    tenant_id, location_id, appointment_id, resource_id,
    starts_at, ends_at, status, created_at
)
SELECT tenant_id, location_id, id, resource_id,
       starts_at, ends_at, status, created_at
FROM appointments
WHERE resource_id IS NOT NULL;

ALTER TABLE appointment_resource_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE appointment_resource_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY appointment_resource_reservation_select
    ON appointment_resource_reservations
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );
CREATE POLICY appointment_resource_reservation_insert
    ON appointment_resource_reservations
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );
CREATE POLICY appointment_resource_reservation_update
    ON appointment_resource_reservations
    FOR UPDATE USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );
CREATE POLICY appointment_resource_reservation_delete
    ON appointment_resource_reservations
    FOR DELETE USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );

-- +goose StatementBegin
CREATE FUNCTION validate_appointment_resource_reservation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    appointment_location_id UUID;
    appointment_starts_at TIMESTAMPTZ;
    appointment_ends_at TIMESTAMPTZ;
    appointment_status TEXT;
BEGIN
    SELECT location_id, starts_at, ends_at, status
    INTO appointment_location_id, appointment_starts_at,
         appointment_ends_at, appointment_status
    FROM appointments
    WHERE tenant_id = NEW.tenant_id AND id = NEW.appointment_id;

    IF NOT FOUND OR ROW(
        NEW.location_id, NEW.starts_at, NEW.ends_at, NEW.status
    ) IS DISTINCT FROM ROW(
        appointment_location_id, appointment_starts_at,
        appointment_ends_at, appointment_status
    ) THEN
        RAISE EXCEPTION 'resource reservation must mirror its appointment'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'appointment_resource_reservation_matches_appointment';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER appointment_resource_reservations_validate
BEFORE INSERT OR UPDATE OF location_id, appointment_id, starts_at, ends_at, status
ON appointment_resource_reservations
FOR EACH ROW EXECUTE FUNCTION validate_appointment_resource_reservation();

-- +goose StatementBegin
CREATE FUNCTION sync_appointment_resource_reservations()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.resource_id IS DISTINCT FROM NEW.resource_id
       AND OLD.resource_id IS NOT NULL THEN
        DELETE FROM appointment_resource_reservations
        WHERE tenant_id = OLD.tenant_id
          AND appointment_id = OLD.id
          AND resource_id = OLD.resource_id;
    END IF;

    UPDATE appointment_resource_reservations
    SET location_id = NEW.location_id,
        starts_at = NEW.starts_at,
        ends_at = NEW.ends_at,
        status = NEW.status
    WHERE tenant_id = NEW.tenant_id AND appointment_id = NEW.id;

    IF NEW.resource_id IS NOT NULL THEN
        INSERT INTO appointment_resource_reservations (
            tenant_id, location_id, appointment_id, resource_id,
            starts_at, ends_at, status
        ) VALUES (
            NEW.tenant_id, NEW.location_id, NEW.id, NEW.resource_id,
            NEW.starts_at, NEW.ends_at, NEW.status
        )
        ON CONFLICT (appointment_id, resource_id) DO UPDATE
        SET location_id = EXCLUDED.location_id,
            starts_at = EXCLUDED.starts_at,
            ends_at = EXCLUDED.ends_at,
            status = EXCLUDED.status;
    END IF;

    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER appointments_sync_resource_reservations
AFTER INSERT OR UPDATE OF location_id, resource_id, starts_at, ends_at, status
ON appointments
FOR EACH ROW EXECUTE FUNCTION sync_appointment_resource_reservations();

-- +goose Down
DROP TRIGGER appointments_sync_resource_reservations ON appointments;
DROP FUNCTION sync_appointment_resource_reservations();
DROP TRIGGER appointment_resource_reservations_validate ON appointment_resource_reservations;
DROP FUNCTION validate_appointment_resource_reservation();
DROP POLICY appointment_resource_reservation_delete ON appointment_resource_reservations;
DROP POLICY appointment_resource_reservation_update ON appointment_resource_reservations;
DROP POLICY appointment_resource_reservation_insert ON appointment_resource_reservations;
DROP POLICY appointment_resource_reservation_select ON appointment_resource_reservations;
ALTER TABLE appointment_resource_reservations NO FORCE ROW LEVEL SECURITY;
ALTER TABLE appointment_resource_reservations DISABLE ROW LEVEL SECURITY;
DROP TABLE appointment_resource_reservations;
ALTER TABLE appointments DROP CONSTRAINT appointments_tenant_location_id_unique;
