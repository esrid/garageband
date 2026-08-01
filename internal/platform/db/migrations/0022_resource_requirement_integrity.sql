-- Keep automatic resource requirements true across direct SQL writes too.
-- +goose Up
ALTER TABLE bookable_resources
    ADD CONSTRAINT bookable_resources_name_length
    CHECK (char_length(name) <= 120) NOT VALID;

-- +goose StatementBegin
CREATE FUNCTION validate_appointment_resource_requirements()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    candidate_tenant_id UUID;
    candidate_appointment_id UUID;
    candidate_status TEXT;
    candidate_service_id UUID;
BEGIN
    IF TG_TABLE_NAME = 'appointments' THEN
        candidate_tenant_id := NEW.tenant_id;
        candidate_appointment_id := NEW.id;
        candidate_status := NEW.status;
        candidate_service_id := NEW.service_id;
    ELSE
        IF TG_OP = 'DELETE' THEN
            candidate_tenant_id := OLD.tenant_id;
            candidate_appointment_id := OLD.appointment_id;
        ELSE
            candidate_tenant_id := NEW.tenant_id;
            candidate_appointment_id := NEW.appointment_id;
        END IF;
        SELECT status, service_id
        INTO candidate_status, candidate_service_id
        FROM appointments
        WHERE tenant_id = candidate_tenant_id
          AND id = candidate_appointment_id;
        IF NOT FOUND THEN
            RETURN NULL;
        END IF;
    END IF;

    IF candidate_status IN ('pending', 'confirmed', 'in_progress')
       AND EXISTS (
           SELECT 1
           FROM service_resource_requirements requirement
           WHERE requirement.tenant_id = candidate_tenant_id
             AND requirement.service_id = candidate_service_id
             AND (
                 SELECT count(*)
                 FROM appointment_resource_reservations reservation
                 JOIN bookable_resources resource
                   ON resource.tenant_id = reservation.tenant_id
                  AND resource.id = reservation.resource_id
                 WHERE reservation.tenant_id = candidate_tenant_id
                   AND reservation.appointment_id = candidate_appointment_id
                   AND resource.kind = requirement.resource_kind
             ) < requirement.quantity
       ) THEN
        RAISE EXCEPTION 'appointment does not reserve every required resource'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'appointments_satisfy_resource_requirements';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER appointments_validate_resource_requirements
AFTER INSERT OR UPDATE OF service_id, status ON appointments
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_appointment_resource_requirements();

CREATE CONSTRAINT TRIGGER reservations_validate_resource_requirements
AFTER INSERT OR UPDATE OR DELETE ON appointment_resource_reservations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_appointment_resource_requirements();

-- +goose StatementBegin
CREATE FUNCTION validate_service_requirement_change()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM appointments appointment
        WHERE appointment.tenant_id = NEW.tenant_id
          AND appointment.service_id = NEW.service_id
          AND appointment.status IN ('pending', 'confirmed', 'in_progress')
          AND appointment.ends_at > now()
          AND (
              SELECT count(*)
              FROM appointment_resource_reservations reservation
              JOIN bookable_resources resource
                ON resource.tenant_id = reservation.tenant_id
               AND resource.id = reservation.resource_id
              WHERE reservation.tenant_id = appointment.tenant_id
                AND reservation.appointment_id = appointment.id
                AND resource.kind = NEW.resource_kind
          ) < NEW.quantity
    ) THEN
        RAISE EXCEPTION 'resource requirement would invalidate a future appointment'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'service_requirements_preserve_appointments';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER service_requirements_validate_change
AFTER INSERT OR UPDATE OF resource_kind, quantity
ON service_resource_requirements
FOR EACH ROW EXECUTE FUNCTION validate_service_requirement_change();

-- +goose StatementBegin
CREATE FUNCTION preserve_reserved_resource_activity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.active AND NOT NEW.active AND EXISTS (
        SELECT 1
        FROM appointment_resource_reservations reservation
        WHERE reservation.tenant_id = OLD.tenant_id
          AND reservation.resource_id = OLD.id
          AND reservation.status IN ('pending', 'confirmed', 'in_progress')
          AND reservation.ends_at > now()
    ) THEN
        RAISE EXCEPTION 'active resource has future reservations'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'bookable_resources_preserve_appointments';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER bookable_resources_preserve_activity
BEFORE UPDATE OF active ON bookable_resources
FOR EACH ROW EXECUTE FUNCTION preserve_reserved_resource_activity();

-- +goose Down
DROP TRIGGER bookable_resources_preserve_activity ON bookable_resources;
DROP FUNCTION preserve_reserved_resource_activity();
DROP TRIGGER service_requirements_validate_change ON service_resource_requirements;
DROP FUNCTION validate_service_requirement_change();
DROP TRIGGER reservations_validate_resource_requirements ON appointment_resource_reservations;
DROP TRIGGER appointments_validate_resource_requirements ON appointments;
DROP FUNCTION validate_appointment_resource_requirements();
ALTER TABLE bookable_resources DROP CONSTRAINT bookable_resources_name_length;
