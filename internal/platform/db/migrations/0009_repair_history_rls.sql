-- +goose Up
ALTER TABLE repair_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE repair_orders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON repair_orders
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE repair_order_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE repair_order_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON repair_order_items
    FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

-- +goose Down
DROP POLICY tenant_isolation ON repair_order_items;
ALTER TABLE repair_order_items NO FORCE ROW LEVEL SECURITY;
ALTER TABLE repair_order_items DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON repair_orders;
ALTER TABLE repair_orders NO FORCE ROW LEVEL SECURITY;
ALTER TABLE repair_orders DISABLE ROW LEVEL SECURITY;
