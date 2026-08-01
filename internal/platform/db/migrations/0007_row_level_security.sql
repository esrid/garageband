-- RLS is defense-in-depth. The application must still authorize membership and
-- include tenant_id in its queries. app.current_tenant_id must be set locally
-- inside the same transaction as tenant data access.
-- +goose Up
CREATE FUNCTION app_current_tenant_id()
RETURNS UUID
LANGUAGE SQL
STABLE
PARALLEL SAFE
AS $$
    SELECT NULLIF(current_setting('app.current_tenant_id', true), '')::UUID
$$;

ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenants
    FOR ALL
    USING (id = app_current_tenant_id())
    WITH CHECK (id = app_current_tenant_id());

ALTER TABLE tenant_memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_memberships FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_memberships
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE locations ENABLE ROW LEVEL SECURITY;
ALTER TABLE locations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON locations
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE location_opening_hours ENABLE ROW LEVEL SECURITY;
ALTER TABLE location_opening_hours FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON location_opening_hours
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE business_enrichment_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE business_enrichment_runs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON business_enrichment_runs
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE customers ENABLE ROW LEVEL SECURITY;
ALTER TABLE customers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customers
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE customer_contacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_contacts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_contacts
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE vehicles ENABLE ROW LEVEL SECURITY;
ALTER TABLE vehicles FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON vehicles
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE vehicle_lookup_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE vehicle_lookup_runs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON vehicle_lookup_runs
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE service_offerings ENABLE ROW LEVEL SECURITY;
ALTER TABLE service_offerings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON service_offerings
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE bookable_resources ENABLE ROW LEVEL SECURITY;
ALTER TABLE bookable_resources FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON bookable_resources
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE appointments ENABLE ROW LEVEL SECURITY;
ALTER TABLE appointments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON appointments
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE provider_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE provider_connections FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON provider_connections
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE agents ENABLE ROW LEVEL SECURITY;
ALTER TABLE agents FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON agents
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE phone_numbers ENABLE ROW LEVEL SECURITY;
ALTER TABLE phone_numbers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON phone_numbers
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE calls ENABLE ROW LEVEL SECURITY;
ALTER TABLE calls FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON calls
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE call_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE call_messages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON call_messages
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE tool_executions ENABLE ROW LEVEL SECURITY;
ALTER TABLE tool_executions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tool_executions
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE customer_memories ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_memories FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_memories
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE appointment_calendar_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE appointment_calendar_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON appointment_calendar_events
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

-- +goose Down
DROP POLICY tenant_isolation ON appointment_calendar_events;
ALTER TABLE appointment_calendar_events NO FORCE ROW LEVEL SECURITY;
ALTER TABLE appointment_calendar_events DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON customer_memories;
ALTER TABLE customer_memories NO FORCE ROW LEVEL SECURITY;
ALTER TABLE customer_memories DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON tool_executions;
ALTER TABLE tool_executions NO FORCE ROW LEVEL SECURITY;
ALTER TABLE tool_executions DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON call_messages;
ALTER TABLE call_messages NO FORCE ROW LEVEL SECURITY;
ALTER TABLE call_messages DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON calls;
ALTER TABLE calls NO FORCE ROW LEVEL SECURITY;
ALTER TABLE calls DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON phone_numbers;
ALTER TABLE phone_numbers NO FORCE ROW LEVEL SECURITY;
ALTER TABLE phone_numbers DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON agents;
ALTER TABLE agents NO FORCE ROW LEVEL SECURITY;
ALTER TABLE agents DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON provider_connections;
ALTER TABLE provider_connections NO FORCE ROW LEVEL SECURITY;
ALTER TABLE provider_connections DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON appointments;
ALTER TABLE appointments NO FORCE ROW LEVEL SECURITY;
ALTER TABLE appointments DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON bookable_resources;
ALTER TABLE bookable_resources NO FORCE ROW LEVEL SECURITY;
ALTER TABLE bookable_resources DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON service_offerings;
ALTER TABLE service_offerings NO FORCE ROW LEVEL SECURITY;
ALTER TABLE service_offerings DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON vehicle_lookup_runs;
ALTER TABLE vehicle_lookup_runs NO FORCE ROW LEVEL SECURITY;
ALTER TABLE vehicle_lookup_runs DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON vehicles;
ALTER TABLE vehicles NO FORCE ROW LEVEL SECURITY;
ALTER TABLE vehicles DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON customer_contacts;
ALTER TABLE customer_contacts NO FORCE ROW LEVEL SECURITY;
ALTER TABLE customer_contacts DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON customers;
ALTER TABLE customers NO FORCE ROW LEVEL SECURITY;
ALTER TABLE customers DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON business_enrichment_runs;
ALTER TABLE business_enrichment_runs NO FORCE ROW LEVEL SECURITY;
ALTER TABLE business_enrichment_runs DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON location_opening_hours;
ALTER TABLE location_opening_hours NO FORCE ROW LEVEL SECURITY;
ALTER TABLE location_opening_hours DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON locations;
ALTER TABLE locations NO FORCE ROW LEVEL SECURITY;
ALTER TABLE locations DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON tenant_memberships;
ALTER TABLE tenant_memberships NO FORCE ROW LEVEL SECURITY;
ALTER TABLE tenant_memberships DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON tenants;
ALTER TABLE tenants NO FORCE ROW LEVEL SECURITY;
ALTER TABLE tenants DISABLE ROW LEVEL SECURITY;
DROP FUNCTION app_current_tenant_id();
