-- Employees (reception, mechanic) are plain members: they run the day-to-day
-- work — customers, appointments, calls — but must not reshape the business.
-- 0012/0014 already put locations, catalog, agents, phone_numbers and
-- provider_connections behind app_current_user_manages_tenant(). Two tables
-- were left on a tenant-only check by 0011: a member could rename or delete the
-- organization, add staff, or promote themselves to owner. Nothing exposes
-- those writes over HTTP yet, but tenant_memberships is exactly the table the
-- staff-invitation feature will write, so the rule belongs here first.
--
-- Reads are untouched. Two bootstrap facts constrain the rest:
--   * onboarding inserts a tenant BEFORE any membership exists, so INSERT on
--     tenants stays role-free (0011 already scopes it to the transaction's own
--     server-generated tenant id).
--   * tenant_membership_select must keep no membership predicate of its own,
--     or the write policies below would recurse through it.

-- +goose Up
DROP POLICY tenant_update ON tenants;

CREATE POLICY tenant_update ON tenants
    FOR UPDATE
    USING (
        id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    )
    WITH CHECK (
        id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );

-- No DELETE policy at all: a garage is a durable business record, like a
-- location, and closing one is a status change. No code path deletes a tenant.
DROP POLICY tenant_delete ON tenants;

-- The second branch is the onboarding bootstrap: the very first membership of a
-- brand-new tenant has no owner available to authorize it. It is deliberately
-- narrower than "the tenant is empty" — the creator may only make *themselves*
-- its owner, never enrol a third party — so that a future handler taking a
-- tenant id from a request cannot be turned into a way to join someone else's
-- ownerless tenant.
DROP POLICY tenant_membership_insert ON tenant_memberships;

CREATE POLICY tenant_membership_insert ON tenant_memberships
    FOR INSERT
    WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND (
            app_current_user_manages_tenant()
            OR (
                user_id = app_current_user_id()
                AND role = 'owner'
                AND NOT EXISTS (
                    SELECT 1
                    FROM tenant_memberships existing
                    WHERE existing.tenant_id = app_current_tenant_id()
                )
            )
        )
    );

DROP POLICY tenant_membership_update ON tenant_memberships;

CREATE POLICY tenant_membership_update ON tenant_memberships
    FOR UPDATE
    USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    )
    WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );

-- user_id <> app_current_user_id() stops the last owner from removing their own
-- access and locking the business out of its own account. Whether an admin may
-- remove an owner is left open until a UI exists to ask the question.
DROP POLICY tenant_membership_delete ON tenant_memberships;

CREATE POLICY tenant_membership_delete ON tenant_memberships
    FOR DELETE
    USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
        AND user_id <> app_current_user_id()
    );

-- One human = one row, whatever they signed in with. Without this, inviting the
-- mechanic through a future non-OAuth provider and letting him later sign in
-- with Google under the same address would silently create a second user and a
-- second membership, because users is unique on (provider, provider_id) only.
-- The constraint forces that merge to be written deliberately instead of being
-- discovered as duplicated staff. Invited employees with no work address keep
-- email = '', which the partial index leaves unconstrained.
CREATE UNIQUE INDEX users_email_unique
    ON users (lower(email))
    WHERE email <> '';

-- +goose Down
DROP INDEX users_email_unique;

DROP POLICY tenant_membership_delete ON tenant_memberships;
CREATE POLICY tenant_membership_delete ON tenant_memberships
    FOR DELETE
    USING (tenant_id = app_current_tenant_id());

DROP POLICY tenant_membership_update ON tenant_memberships;
CREATE POLICY tenant_membership_update ON tenant_memberships
    FOR UPDATE
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

DROP POLICY tenant_membership_insert ON tenant_memberships;
CREATE POLICY tenant_membership_insert ON tenant_memberships
    FOR INSERT
    WITH CHECK (tenant_id = app_current_tenant_id());

CREATE POLICY tenant_delete ON tenants
    FOR DELETE
    USING (id = app_current_tenant_id());

DROP POLICY tenant_update ON tenants;
CREATE POLICY tenant_update ON tenants
    FOR UPDATE
    USING (id = app_current_tenant_id())
    WITH CHECK (id = app_current_tenant_id());
