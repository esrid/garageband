-- Resource kinds and quantities required to perform a scheduling service.
-- +goose Up
ALTER TABLE service_offerings
    ADD CONSTRAINT service_offerings_tenant_location_id_unique
    UNIQUE (tenant_id, location_id, id);

CREATE TABLE service_resource_requirements (
    tenant_id    UUID NOT NULL,
    location_id  UUID NOT NULL,
    service_id   UUID NOT NULL,
    resource_kind TEXT NOT NULL,
    quantity     SMALLINT NOT NULL DEFAULT 1,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (service_id, resource_kind),
    FOREIGN KEY (tenant_id, location_id, service_id)
        REFERENCES service_offerings (tenant_id, location_id, id) ON DELETE CASCADE,
    CONSTRAINT service_resource_requirements_kind_valid CHECK (
        resource_kind IN ('technician', 'bay', 'equipment', 'calendar')
    ),
    CONSTRAINT service_resource_requirements_quantity_valid CHECK (
        quantity BETWEEN 1 AND 10
    ),
    UNIQUE (tenant_id, service_id, resource_kind)
);

CREATE INDEX service_resource_requirements_location_idx
    ON service_resource_requirements (tenant_id, location_id, service_id);

ALTER TABLE service_resource_requirements ENABLE ROW LEVEL SECURITY;
ALTER TABLE service_resource_requirements FORCE ROW LEVEL SECURITY;
CREATE POLICY service_resource_requirement_select
    ON service_resource_requirements
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );
CREATE POLICY service_resource_requirement_write
    ON service_resource_requirements
    FOR ALL USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );

-- +goose StatementBegin
CREATE FUNCTION touch_service_resource_requirement()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER service_resource_requirements_touch_updated_at
BEFORE UPDATE ON service_resource_requirements
FOR EACH ROW EXECUTE FUNCTION touch_service_resource_requirement();

-- +goose Down
DROP TRIGGER service_resource_requirements_touch_updated_at ON service_resource_requirements;
DROP FUNCTION touch_service_resource_requirement();
DROP POLICY service_resource_requirement_write ON service_resource_requirements;
DROP POLICY service_resource_requirement_select ON service_resource_requirements;
ALTER TABLE service_resource_requirements NO FORCE ROW LEVEL SECURITY;
ALTER TABLE service_resource_requirements DISABLE ROW LEVEL SECURITY;
DROP TABLE service_resource_requirements;
ALTER TABLE service_offerings
    DROP CONSTRAINT service_offerings_tenant_location_id_unique;
