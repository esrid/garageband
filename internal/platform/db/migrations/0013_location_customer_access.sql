-- Staff location assignments, canonical customer ownership, temporal sharing,
-- and durable identity snapshots for records retained after share revocation.
-- +goose Up
CREATE TABLE user_location_assignments (
    id                 UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id          UUID NOT NULL,
    user_id            UUID NOT NULL,
    location_id        UUID NOT NULL,
    assigned_by_user_id UUID NOT NULL,
    assigned_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_by_user_id UUID,
    revoked_at         TIMESTAMPTZ,
    FOREIGN KEY (tenant_id, user_id)
        REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, assigned_by_user_id)
        REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, revoked_by_user_id)
        REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT user_location_assignment_revocation_consistent CHECK (
        (revoked_at IS NULL AND revoked_by_user_id IS NULL)
        OR (revoked_at IS NOT NULL AND revoked_by_user_id IS NOT NULL
            AND revoked_at >= assigned_at)
    )
);

CREATE UNIQUE INDEX user_location_assignments_active_unique
    ON user_location_assignments (tenant_id, user_id, location_id)
    WHERE revoked_at IS NULL;

CREATE INDEX user_location_assignments_user_active_idx
    ON user_location_assignments (tenant_id, user_id, location_id)
    WHERE revoked_at IS NULL;

ALTER TABLE customers ADD COLUMN home_location_id UUID;

UPDATE customers customer
SET home_location_id = COALESCE(
    (
        SELECT appointment.location_id
        FROM appointments appointment
        WHERE appointment.tenant_id = customer.tenant_id
          AND appointment.customer_id = customer.id
        ORDER BY appointment.created_at, appointment.id
        LIMIT 1
    ),
    (
        SELECT repair.location_id
        FROM repair_orders repair
        WHERE repair.tenant_id = customer.tenant_id
          AND repair.customer_id = customer.id
        ORDER BY repair.created_at, repair.id
        LIMIT 1
    ),
    (
        SELECT location.id
        FROM locations location
        WHERE location.tenant_id = customer.tenant_id
        ORDER BY (location.status = 'active') DESC, location.created_at, location.id
        LIMIT 1
    )
);

-- Existing customers without any location cannot be assigned safely. Failing
-- the migration is deliberate: an operator must create/choose a real site.
ALTER TABLE customers
    ALTER COLUMN home_location_id SET NOT NULL,
    ADD CONSTRAINT customers_home_location_fkey
        FOREIGN KEY (tenant_id, home_location_id)
        REFERENCES locations (tenant_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT customers_tenant_id_id_home_location_id_key
        UNIQUE (tenant_id, id, home_location_id);

ALTER TABLE vehicles ADD COLUMN location_id UUID;

UPDATE vehicles vehicle
SET location_id = COALESCE(
    (
        SELECT customer.home_location_id
        FROM customers customer
        WHERE customer.tenant_id = vehicle.tenant_id
          AND customer.id = vehicle.customer_id
    ),
    (
        SELECT location.id
        FROM locations location
        WHERE location.tenant_id = vehicle.tenant_id
        ORDER BY (location.status = 'active') DESC, location.created_at, location.id
        LIMIT 1
    )
);

ALTER TABLE vehicles
    ALTER COLUMN location_id SET NOT NULL,
    ADD CONSTRAINT vehicles_location_fkey
        FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT vehicles_customer_home_location_fkey
        FOREIGN KEY (tenant_id, customer_id, location_id)
        REFERENCES customers (tenant_id, id, home_location_id)
        ON DELETE SET NULL (customer_id);

ALTER TABLE vehicle_lookup_runs ADD COLUMN location_id UUID;

UPDATE vehicle_lookup_runs lookup
SET location_id = COALESCE(
    (
        SELECT vehicle.location_id
        FROM vehicles vehicle
        WHERE vehicle.tenant_id = lookup.tenant_id
          AND vehicle.id = lookup.vehicle_id
    ),
    (
        SELECT location.id
        FROM locations location
        WHERE location.tenant_id = lookup.tenant_id
        ORDER BY (location.status = 'active') DESC, location.created_at, location.id
        LIMIT 1
    )
);

ALTER TABLE vehicle_lookup_runs
    ALTER COLUMN location_id SET NOT NULL,
    ADD CONSTRAINT vehicle_lookup_runs_location_fkey
        FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE RESTRICT;

CREATE TABLE customer_location_grants (
    id                    UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id             UUID NOT NULL,
    customer_id           UUID NOT NULL,
    source_location_id    UUID NOT NULL,
    receiving_location_id UUID NOT NULL,
    granted_by_user_id    UUID NOT NULL,
    granted_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_by_user_id    UUID,
    revoked_at            TIMESTAMPTZ,
    FOREIGN KEY (tenant_id, customer_id, source_location_id)
        REFERENCES customers (tenant_id, id, home_location_id)
        ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, receiving_location_id)
        REFERENCES locations (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, granted_by_user_id)
        REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, revoked_by_user_id)
        REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE RESTRICT,
    CONSTRAINT customer_location_grant_distinct_locations CHECK (
        source_location_id <> receiving_location_id
    ),
    CONSTRAINT customer_location_grant_revocation_consistent CHECK (
        (revoked_at IS NULL AND revoked_by_user_id IS NULL)
        OR (revoked_at IS NOT NULL AND revoked_by_user_id IS NOT NULL
            AND revoked_at >= granted_at)
    )
);

CREATE UNIQUE INDEX customer_location_grants_active_unique
    ON customer_location_grants (tenant_id, customer_id, receiving_location_id)
    WHERE revoked_at IS NULL;

CREATE INDEX customer_location_grants_customer_history_idx
    ON customer_location_grants (
        tenant_id, customer_id, receiving_location_id, granted_at, revoked_at
    );

ALTER TABLE customer_memories
    ADD COLUMN location_id UUID,
    ADD COLUMN customer_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB,
    ADD CONSTRAINT customer_memories_customer_snapshot_object CHECK (
        jsonb_typeof(customer_snapshot) = 'object'
    );

UPDATE customer_memories memory
SET location_id = COALESCE(
    (
        SELECT call.location_id
        FROM calls call
        WHERE call.tenant_id = memory.tenant_id
          AND call.id = memory.source_call_id
    ),
    (
        SELECT customer.home_location_id
        FROM customers customer
        WHERE customer.tenant_id = memory.tenant_id
          AND customer.id = memory.customer_id
    )
),
customer_snapshot = COALESCE((
    SELECT jsonb_strip_nulls(jsonb_build_object(
        'customer_id', customer.id,
        'first_name', customer.first_name,
        'last_name', customer.last_name,
        'company_name', customer.company_name
    ))
    FROM customers customer
    WHERE customer.tenant_id = memory.tenant_id
      AND customer.id = memory.customer_id
), '{}'::JSONB);

ALTER TABLE customer_memories
    ALTER COLUMN location_id SET NOT NULL,
    ADD CONSTRAINT customer_memories_location_fkey
        FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE RESTRICT,
    DROP CONSTRAINT customer_memories_tenant_id_customer_id_key_key,
    ADD CONSTRAINT customer_memories_location_key_unique
        UNIQUE (tenant_id, customer_id, location_id, key);

ALTER TABLE appointments
    ADD COLUMN customer_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB,
    ADD COLUMN vehicle_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB,
    ADD CONSTRAINT appointments_customer_snapshot_object CHECK (
        jsonb_typeof(customer_snapshot) = 'object'
    ),
    ADD CONSTRAINT appointments_vehicle_snapshot_object CHECK (
        jsonb_typeof(vehicle_snapshot) = 'object'
    );

ALTER TABLE repair_orders
    ADD COLUMN customer_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB,
    ADD COLUMN vehicle_snapshot JSONB NOT NULL DEFAULT '{}'::JSONB,
    ADD CONSTRAINT repair_orders_customer_snapshot_object CHECK (
        jsonb_typeof(customer_snapshot) = 'object'
    ),
    ADD CONSTRAINT repair_orders_vehicle_snapshot_object CHECK (
        jsonb_typeof(vehicle_snapshot) = 'object'
    );

UPDATE appointments appointment
SET customer_snapshot = COALESCE((
        SELECT jsonb_strip_nulls(jsonb_build_object(
            'customer_id', customer.id,
            'first_name', customer.first_name,
            'last_name', customer.last_name,
            'company_name', customer.company_name
        ))
        FROM customers customer
        WHERE customer.tenant_id = appointment.tenant_id
          AND customer.id = appointment.customer_id
    ), '{}'::JSONB),
    vehicle_snapshot = COALESCE((
        SELECT jsonb_strip_nulls(jsonb_build_object(
            'vehicle_id', vehicle.id,
            'registration_country', vehicle.registration_country,
            'registration_plate', vehicle.registration_plate,
            'vin', vehicle.vin,
            'make', vehicle.make,
            'model', vehicle.model,
            'trim', vehicle.trim
        ))
        FROM vehicles vehicle
        WHERE vehicle.tenant_id = appointment.tenant_id
          AND vehicle.id = appointment.vehicle_id
    ), '{}'::JSONB);

UPDATE repair_orders repair
SET customer_snapshot = COALESCE((
        SELECT jsonb_strip_nulls(jsonb_build_object(
            'customer_id', customer.id,
            'first_name', customer.first_name,
            'last_name', customer.last_name,
            'company_name', customer.company_name
        ))
        FROM customers customer
        WHERE customer.tenant_id = repair.tenant_id
          AND customer.id = repair.customer_id
    ), '{}'::JSONB),
    vehicle_snapshot = COALESCE((
        SELECT jsonb_strip_nulls(jsonb_build_object(
            'vehicle_id', vehicle.id,
            'registration_country', vehicle.registration_country,
            'registration_plate', vehicle.registration_plate,
            'vin', vehicle.vin,
            'make', vehicle.make,
            'model', vehicle.model,
            'trim', vehicle.trim
        ))
        FROM vehicles vehicle
        WHERE vehicle.tenant_id = repair.tenant_id
          AND vehicle.id = repair.vehicle_id
    ), '{}'::JSONB);

CREATE FUNCTION app_current_user_can_access_location(candidate_location_id UUID)
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
PARALLEL SAFE
AS $$
    SELECT candidate_location_id IS NOT NULL AND (
        app_current_user_manages_tenant()
        OR EXISTS (
            SELECT 1
            FROM user_location_assignments assignment
            WHERE assignment.tenant_id = app_current_tenant_id()
              AND assignment.user_id = app_current_user_id()
              AND assignment.location_id = candidate_location_id
              AND assignment.revoked_at IS NULL
        )
    )
$$;

CREATE FUNCTION app_current_user_has_customer_access(
    candidate_customer_id UUID,
    customer_home_location_id UUID
)
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
PARALLEL SAFE
AS $$
    SELECT app_current_user_can_access_location(customer_home_location_id)
        OR EXISTS (
            SELECT 1
            FROM customer_location_grants access_grant
            WHERE access_grant.tenant_id = app_current_tenant_id()
              AND access_grant.customer_id = candidate_customer_id
              AND access_grant.receiving_location_id <> customer_home_location_id
              AND access_grant.revoked_at IS NULL
              AND app_current_user_can_access_location(
                  access_grant.receiving_location_id
              )
        )
$$;

ALTER TABLE user_location_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_location_assignments FORCE ROW LEVEL SECURITY;
CREATE POLICY user_location_assignment_select ON user_location_assignments
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND (
            app_current_user_manages_tenant()
            OR user_id = app_current_user_id()
        )
    );
CREATE POLICY user_location_assignment_insert ON user_location_assignments
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );
CREATE POLICY user_location_assignment_update ON user_location_assignments
    FOR UPDATE USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );

ALTER TABLE customer_location_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_location_grants FORCE ROW LEVEL SECURITY;
CREATE POLICY customer_location_grant_select ON customer_location_grants
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND (
            app_current_user_manages_tenant()
            OR app_current_user_can_access_location(source_location_id)
            OR app_current_user_can_access_location(receiving_location_id)
        )
    );
CREATE POLICY customer_location_grant_insert ON customer_location_grants
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );
CREATE POLICY customer_location_grant_update ON customer_location_grants
    FOR UPDATE USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );

-- +goose StatementBegin
CREATE FUNCTION protect_location_assignment_audit()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.user_id IS DISTINCT FROM OLD.user_id
        OR NEW.location_id IS DISTINCT FROM OLD.location_id
        OR NEW.assigned_by_user_id IS DISTINCT FROM OLD.assigned_by_user_id
        OR NEW.assigned_at IS DISTINCT FROM OLD.assigned_at
        OR OLD.revoked_at IS NOT NULL
        OR OLD.revoked_by_user_id IS NOT NULL
        OR NEW.revoked_at IS NULL
        OR NEW.revoked_by_user_id IS NULL THEN
        RAISE EXCEPTION 'location assignment audit fields are immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER user_location_assignments_protect_audit
BEFORE UPDATE ON user_location_assignments
FOR EACH ROW EXECUTE FUNCTION protect_location_assignment_audit();

-- +goose StatementBegin
CREATE FUNCTION protect_customer_grant_audit()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.customer_id IS DISTINCT FROM OLD.customer_id
        OR NEW.source_location_id IS DISTINCT FROM OLD.source_location_id
        OR NEW.receiving_location_id IS DISTINCT FROM OLD.receiving_location_id
        OR NEW.granted_by_user_id IS DISTINCT FROM OLD.granted_by_user_id
        OR NEW.granted_at IS DISTINCT FROM OLD.granted_at
        OR OLD.revoked_at IS NOT NULL
        OR OLD.revoked_by_user_id IS NOT NULL
        OR NEW.revoked_at IS NULL
        OR NEW.revoked_by_user_id IS NULL THEN
        RAISE EXCEPTION 'customer grant audit fields are immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER customer_location_grants_protect_audit
BEFORE UPDATE ON customer_location_grants
FOR EACH ROW EXECUTE FUNCTION protect_customer_grant_audit();

-- +goose StatementBegin
CREATE FUNCTION fill_customer_identity_snapshots()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.customer_snapshot IS DISTINCT FROM OLD.customer_snapshot
            OR NEW.vehicle_snapshot IS DISTINCT FROM OLD.vehicle_snapshot THEN
            RAISE EXCEPTION 'identity snapshots are immutable'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    NEW.customer_snapshot = '{}'::JSONB;
    IF NEW.customer_id IS NOT NULL THEN
        SELECT jsonb_strip_nulls(jsonb_build_object(
            'customer_id', customer.id,
            'first_name', customer.first_name,
            'last_name', customer.last_name,
            'company_name', customer.company_name
        ))
        INTO NEW.customer_snapshot
        FROM customers customer
        WHERE customer.tenant_id = NEW.tenant_id
          AND customer.id = NEW.customer_id;
    END IF;

    NEW.vehicle_snapshot = '{}'::JSONB;
    IF NEW.vehicle_id IS NOT NULL THEN
        SELECT jsonb_strip_nulls(jsonb_build_object(
            'vehicle_id', vehicle.id,
            'registration_country', vehicle.registration_country,
            'registration_plate', vehicle.registration_plate,
            'vin', vehicle.vin,
            'make', vehicle.make,
            'model', vehicle.model,
            'trim', vehicle.trim
        ))
        INTO NEW.vehicle_snapshot
        FROM vehicles vehicle
        WHERE vehicle.tenant_id = NEW.tenant_id
          AND vehicle.id = NEW.vehicle_id;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER appointments_fill_identity_snapshots
BEFORE INSERT OR UPDATE OF customer_snapshot, vehicle_snapshot ON appointments
FOR EACH ROW EXECUTE FUNCTION fill_customer_identity_snapshots();

CREATE TRIGGER repair_orders_fill_identity_snapshots
BEFORE INSERT OR UPDATE OF customer_snapshot, vehicle_snapshot ON repair_orders
FOR EACH ROW EXECUTE FUNCTION fill_customer_identity_snapshots();

-- +goose StatementBegin
CREATE FUNCTION fill_customer_memory_snapshot()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.customer_snapshot IS DISTINCT FROM OLD.customer_snapshot THEN
            RAISE EXCEPTION 'customer memory identity snapshot is immutable'
                USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;

    SELECT jsonb_strip_nulls(jsonb_build_object(
        'customer_id', customer.id,
        'first_name', customer.first_name,
        'last_name', customer.last_name,
        'company_name', customer.company_name
    ))
    INTO NEW.customer_snapshot
    FROM customers customer
    WHERE customer.tenant_id = NEW.tenant_id
      AND customer.id = NEW.customer_id;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER customer_memories_fill_identity_snapshot
BEFORE INSERT OR UPDATE OF customer_snapshot ON customer_memories
FOR EACH ROW EXECUTE FUNCTION fill_customer_memory_snapshot();

-- +goose StatementBegin
CREATE FUNCTION prevent_customer_memory_reassignment()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.location_id IS DISTINCT FROM OLD.location_id
        OR NEW.customer_id IS DISTINCT FROM OLD.customer_id
        OR NEW.source_call_id IS DISTINCT FROM OLD.source_call_id THEN
        RAISE EXCEPTION 'customer memory ownership is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER customer_memories_prevent_reassignment
BEFORE UPDATE OF tenant_id, location_id, customer_id, source_call_id
ON customer_memories
FOR EACH ROW EXECUTE FUNCTION prevent_customer_memory_reassignment();

-- +goose StatementBegin
CREATE FUNCTION protect_customer_home_location()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.home_location_id IS DISTINCT FROM OLD.home_location_id THEN
        RAISE EXCEPTION 'customer home location is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER customers_protect_home_location
BEFORE UPDATE OF home_location_id ON customers
FOR EACH ROW EXECUTE FUNCTION protect_customer_home_location();

-- +goose StatementBegin
CREATE FUNCTION prevent_customer_event_reassignment()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.location_id IS DISTINCT FROM OLD.location_id
        OR NEW.customer_id IS DISTINCT FROM OLD.customer_id
        OR NEW.vehicle_id IS DISTINCT FROM OLD.vehicle_id THEN
        RAISE EXCEPTION 'customer event ownership is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER appointments_prevent_reassignment
BEFORE UPDATE OF tenant_id, location_id, customer_id, vehicle_id ON appointments
FOR EACH ROW EXECUTE FUNCTION prevent_customer_event_reassignment();

CREATE TRIGGER repair_orders_prevent_reassignment
BEFORE UPDATE OF tenant_id, location_id, customer_id, vehicle_id ON repair_orders
FOR EACH ROW EXECUTE FUNCTION prevent_customer_event_reassignment();

-- +goose Down
DROP TRIGGER customers_protect_home_location ON customers;
DROP FUNCTION protect_customer_home_location();
DROP TRIGGER customer_memories_prevent_reassignment ON customer_memories;
DROP FUNCTION prevent_customer_memory_reassignment();
DROP TRIGGER customer_memories_fill_identity_snapshot ON customer_memories;
DROP FUNCTION fill_customer_memory_snapshot();
DROP TRIGGER repair_orders_prevent_reassignment ON repair_orders;
DROP TRIGGER appointments_prevent_reassignment ON appointments;
DROP FUNCTION prevent_customer_event_reassignment();
DROP TRIGGER repair_orders_fill_identity_snapshots ON repair_orders;
DROP TRIGGER appointments_fill_identity_snapshots ON appointments;
DROP FUNCTION fill_customer_identity_snapshots();

DROP TRIGGER customer_location_grants_protect_audit ON customer_location_grants;
DROP FUNCTION protect_customer_grant_audit();
DROP TRIGGER user_location_assignments_protect_audit ON user_location_assignments;
DROP FUNCTION protect_location_assignment_audit();

DROP POLICY customer_location_grant_update ON customer_location_grants;
DROP POLICY customer_location_grant_insert ON customer_location_grants;
DROP POLICY customer_location_grant_select ON customer_location_grants;
ALTER TABLE customer_location_grants NO FORCE ROW LEVEL SECURITY;
ALTER TABLE customer_location_grants DISABLE ROW LEVEL SECURITY;

DROP POLICY user_location_assignment_update ON user_location_assignments;
DROP POLICY user_location_assignment_insert ON user_location_assignments;
DROP POLICY user_location_assignment_select ON user_location_assignments;
ALTER TABLE user_location_assignments NO FORCE ROW LEVEL SECURITY;
ALTER TABLE user_location_assignments DISABLE ROW LEVEL SECURITY;

DROP FUNCTION app_current_user_has_customer_access(UUID, UUID);
DROP FUNCTION app_current_user_can_access_location(UUID);

ALTER TABLE repair_orders
    DROP CONSTRAINT repair_orders_vehicle_snapshot_object,
    DROP CONSTRAINT repair_orders_customer_snapshot_object,
    DROP COLUMN vehicle_snapshot,
    DROP COLUMN customer_snapshot;

ALTER TABLE appointments
    DROP CONSTRAINT appointments_vehicle_snapshot_object,
    DROP CONSTRAINT appointments_customer_snapshot_object,
    DROP COLUMN vehicle_snapshot,
    DROP COLUMN customer_snapshot;

ALTER TABLE customer_memories
    DROP CONSTRAINT customer_memories_location_key_unique,
    ADD CONSTRAINT customer_memories_tenant_id_customer_id_key_key
        UNIQUE (tenant_id, customer_id, key),
    DROP CONSTRAINT customer_memories_customer_snapshot_object,
    DROP CONSTRAINT customer_memories_location_fkey,
    DROP COLUMN customer_snapshot,
    DROP COLUMN location_id;

DROP TABLE customer_location_grants;

ALTER TABLE vehicle_lookup_runs
    DROP CONSTRAINT vehicle_lookup_runs_location_fkey,
    DROP COLUMN location_id;

ALTER TABLE vehicles
    DROP CONSTRAINT vehicles_customer_home_location_fkey,
    DROP CONSTRAINT vehicles_location_fkey,
    DROP COLUMN location_id;

ALTER TABLE customers
    DROP CONSTRAINT customers_tenant_id_id_home_location_id_key,
    DROP CONSTRAINT customers_home_location_fkey,
    DROP COLUMN home_location_id;

DROP TABLE user_location_assignments;
