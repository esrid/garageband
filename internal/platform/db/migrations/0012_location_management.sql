-- Location management is tenant-visible but only owners and admins may change
-- it. The authenticated user is supplied through a transaction-local setting.
-- +goose Up
ALTER TABLE locations
    ADD COLUMN website_url TEXT,
    ADD CONSTRAINT locations_website_url_format CHECK (
        website_url IS NULL OR website_url ~* '^https?://[^[:space:]]+$'
    );

CREATE FUNCTION app_current_user_belongs_to_tenant()
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
PARALLEL SAFE
AS $$
    SELECT EXISTS (
        SELECT 1
        FROM tenant_memberships membership
        WHERE membership.tenant_id = app_current_tenant_id()
          AND membership.user_id = app_current_user_id()
    )
$$;

CREATE FUNCTION app_current_user_manages_tenant()
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
PARALLEL SAFE
AS $$
    SELECT EXISTS (
        SELECT 1
        FROM tenant_memberships membership
        WHERE membership.tenant_id = app_current_tenant_id()
          AND membership.user_id = app_current_user_id()
          AND membership.role IN ('owner', 'admin')
    )
$$;

DROP POLICY tenant_isolation ON locations;

CREATE POLICY location_select ON locations
    FOR SELECT
    USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_belongs_to_tenant()
    );

CREATE POLICY location_insert ON locations
    FOR INSERT
    WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );

CREATE POLICY location_update ON locations
    FOR UPDATE
    USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    )
    WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );

-- There is intentionally no DELETE policy. Locations are durable business
-- records and transition between active and inactive states instead.

-- +goose StatementBegin
CREATE FUNCTION validate_location_timezone()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_timezone_names WHERE name = NEW.timezone
    ) THEN
        RAISE EXCEPTION 'unknown PostgreSQL timezone: %', NEW.timezone
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER locations_validate_timezone
BEFORE INSERT OR UPDATE OF timezone ON locations
FOR EACH ROW EXECUTE FUNCTION validate_location_timezone();

-- +goose StatementBegin
CREATE FUNCTION touch_location_updated_at()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER locations_touch_updated_at
BEFORE UPDATE ON locations
FOR EACH ROW EXECUTE FUNCTION touch_location_updated_at();

-- +goose Down
DROP TRIGGER locations_touch_updated_at ON locations;
DROP FUNCTION touch_location_updated_at();
DROP TRIGGER locations_validate_timezone ON locations;
DROP FUNCTION validate_location_timezone();

DROP POLICY location_update ON locations;
DROP POLICY location_insert ON locations;
DROP POLICY location_select ON locations;
CREATE POLICY tenant_isolation ON locations
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

DROP FUNCTION app_current_user_manages_tenant();
DROP FUNCTION app_current_user_belongs_to_tenant();

ALTER TABLE locations
    DROP CONSTRAINT locations_website_url_format,
    DROP COLUMN website_url;
