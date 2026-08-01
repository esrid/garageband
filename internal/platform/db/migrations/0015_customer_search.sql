-- Indexed customer search and the minimum source-site visibility required to
-- label an explicitly shared dossier.
-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

ALTER TABLE customers
    ADD COLUMN search_name TEXT GENERATED ALWAYS AS (
        lower(
            COALESCE(first_name, '') || ' ' ||
            COALESCE(last_name, '') || ' ' ||
            COALESCE(company_name, '')
        )
    ) STORED;

CREATE INDEX customers_search_name_trgm_idx
    ON customers USING GIN (search_name public.gin_trgm_ops)
    WHERE deleted_at IS NULL;

ALTER TABLE vehicles
    ADD COLUMN registration_plate_compact TEXT GENERATED ALWAYS AS (
        replace(registration_plate, '-', '')
    ) STORED;

CREATE INDEX vehicles_registration_plate_compact_idx
    ON vehicles (tenant_id, registration_plate_compact)
    WHERE registration_plate IS NOT NULL AND deleted_at IS NULL;

DROP POLICY location_select ON locations;
CREATE POLICY location_select ON locations
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND (
            app_current_user_can_access_location(id)
            OR EXISTS (
                SELECT 1
                FROM customers customer
                WHERE customer.tenant_id = locations.tenant_id
                  AND customer.home_location_id = locations.id
            )
            OR EXISTS (
                SELECT 1
                FROM appointments appointment
                WHERE appointment.tenant_id = locations.tenant_id
                  AND appointment.location_id = locations.id
            )
            OR EXISTS (
                SELECT 1
                FROM repair_orders repair
                WHERE repair.tenant_id = locations.tenant_id
                  AND repair.location_id = locations.id
            )
        )
    );

DROP POLICY service_offering_select ON service_offerings;
CREATE POLICY service_offering_select ON service_offerings
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND (
            app_current_user_can_access_location(location_id)
            OR EXISTS (
                SELECT 1
                FROM appointments appointment
                WHERE appointment.tenant_id = service_offerings.tenant_id
                  AND appointment.service_id = service_offerings.id
            )
        )
    );

-- +goose Down
DROP POLICY service_offering_select ON service_offerings;
CREATE POLICY service_offering_select ON service_offerings
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );

DROP POLICY location_select ON locations;
CREATE POLICY location_select ON locations
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(id)
    );

DROP INDEX vehicles_registration_plate_compact_idx;
ALTER TABLE vehicles DROP COLUMN registration_plate_compact;

DROP INDEX customers_search_name_trgm_idx;
ALTER TABLE customers DROP COLUMN search_name;
