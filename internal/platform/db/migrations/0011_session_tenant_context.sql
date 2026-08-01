-- A session may activate only a tenant its user belongs to. User-scoped RLS
-- permits workspace discovery before a tenant has been selected; all other
-- tenant tables remain accessible only through app.current_tenant_id.
-- +goose Up
CREATE FUNCTION app_current_user_id()
RETURNS UUID
LANGUAGE SQL
STABLE
PARALLEL SAFE
AS $$
    SELECT NULLIF(current_setting('app.current_user_id', true), '')::UUID
$$;

DROP POLICY tenant_isolation ON tenant_memberships;

CREATE POLICY tenant_membership_select ON tenant_memberships
    FOR SELECT
    USING (
        tenant_id = app_current_tenant_id()
        OR user_id = app_current_user_id()
    );

CREATE POLICY tenant_membership_insert ON tenant_memberships
    FOR INSERT
    WITH CHECK (tenant_id = app_current_tenant_id());

CREATE POLICY tenant_membership_update ON tenant_memberships
    FOR UPDATE
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

CREATE POLICY tenant_membership_delete ON tenant_memberships
    FOR DELETE
    USING (tenant_id = app_current_tenant_id());

DROP POLICY tenant_isolation ON tenants;

CREATE POLICY tenant_select ON tenants
    FOR SELECT
    USING (
        id = app_current_tenant_id()
        OR EXISTS (
            SELECT 1
            FROM tenant_memberships membership
            WHERE membership.tenant_id = tenants.id
              AND membership.user_id = app_current_user_id()
        )
    );

CREATE POLICY tenant_insert ON tenants
    FOR INSERT
    WITH CHECK (id = app_current_tenant_id());

CREATE POLICY tenant_update ON tenants
    FOR UPDATE
    USING (id = app_current_tenant_id())
    WITH CHECK (id = app_current_tenant_id());

CREATE POLICY tenant_delete ON tenants
    FOR DELETE
    USING (id = app_current_tenant_id());

ALTER TABLE sessions
    DROP CONSTRAINT sessions_active_tenant_id_fkey;

ALTER TABLE sessions
    ADD CONSTRAINT sessions_active_membership_fkey
    FOREIGN KEY (active_tenant_id, user_id)
    REFERENCES tenant_memberships (tenant_id, user_id)
    ON DELETE SET NULL (active_tenant_id);

-- +goose Down
ALTER TABLE sessions
    DROP CONSTRAINT sessions_active_membership_fkey;

ALTER TABLE sessions
    ADD CONSTRAINT sessions_active_tenant_id_fkey
    FOREIGN KEY (active_tenant_id)
    REFERENCES tenants (id)
    ON DELETE SET NULL;

DROP POLICY tenant_delete ON tenants;
DROP POLICY tenant_update ON tenants;
DROP POLICY tenant_insert ON tenants;
DROP POLICY tenant_select ON tenants;
CREATE POLICY tenant_isolation ON tenants
    FOR ALL
    USING (id = app_current_tenant_id())
    WITH CHECK (id = app_current_tenant_id());

DROP POLICY tenant_membership_delete ON tenant_memberships;
DROP POLICY tenant_membership_update ON tenant_memberships;
DROP POLICY tenant_membership_insert ON tenant_memberships;
DROP POLICY tenant_membership_select ON tenant_memberships;
CREATE POLICY tenant_isolation ON tenant_memberships
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

DROP FUNCTION app_current_user_id();
