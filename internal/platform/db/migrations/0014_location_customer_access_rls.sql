-- Replace organization-wide visibility with physical-location and temporal
-- customer-dossier authorization.
-- +goose Up
DROP POLICY location_select ON locations;
CREATE POLICY location_select ON locations
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(id)
    );

DROP POLICY tenant_isolation ON location_opening_hours;
CREATE POLICY location_opening_hours_select ON location_opening_hours
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );
CREATE POLICY location_opening_hours_write ON location_opening_hours
    FOR ALL USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );

DROP POLICY tenant_isolation ON customers;
CREATE POLICY customer_select ON customers
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_has_customer_access(id, home_location_id)
    );
CREATE POLICY customer_insert ON customers
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(home_location_id)
    );
CREATE POLICY customer_update ON customers
    FOR UPDATE USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(home_location_id)
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(home_location_id)
    );

DROP POLICY tenant_isolation ON customer_contacts;
CREATE POLICY customer_contact_select ON customer_contacts
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM customers customer
            WHERE customer.tenant_id = customer_contacts.tenant_id
              AND customer.id = customer_contacts.customer_id
        )
    );
CREATE POLICY customer_contact_write ON customer_contacts
    FOR ALL USING (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM customers customer
            WHERE customer.tenant_id = customer_contacts.tenant_id
              AND customer.id = customer_contacts.customer_id
              AND app_current_user_can_access_location(customer.home_location_id)
        )
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM customers customer
            WHERE customer.tenant_id = customer_contacts.tenant_id
              AND customer.id = customer_contacts.customer_id
              AND app_current_user_can_access_location(customer.home_location_id)
        )
    );

DROP POLICY tenant_isolation ON vehicles;
CREATE POLICY vehicle_select ON vehicles
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND (
            (customer_id IS NULL
                AND app_current_user_can_access_location(location_id))
            OR EXISTS (
                SELECT 1 FROM customers customer
                WHERE customer.tenant_id = vehicles.tenant_id
                  AND customer.id = vehicles.customer_id
            )
        )
    );
CREATE POLICY vehicle_write ON vehicles
    FOR ALL USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
        AND (
            customer_id IS NULL
            OR EXISTS (
                SELECT 1 FROM customers customer
                WHERE customer.tenant_id = vehicles.tenant_id
                  AND customer.id = vehicles.customer_id
                  AND customer.home_location_id = vehicles.location_id
            )
        )
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
        AND (
            customer_id IS NULL
            OR EXISTS (
                SELECT 1 FROM customers customer
                WHERE customer.tenant_id = vehicles.tenant_id
                  AND customer.id = vehicles.customer_id
                  AND customer.home_location_id = vehicles.location_id
            )
        )
    );

DROP POLICY tenant_isolation ON vehicle_lookup_runs;
CREATE POLICY vehicle_lookup_run_access ON vehicle_lookup_runs
    FOR ALL USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );

DROP POLICY tenant_isolation ON service_offerings;
CREATE POLICY service_offering_select ON service_offerings
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );
CREATE POLICY service_offering_write ON service_offerings
    FOR ALL USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );

DROP POLICY tenant_isolation ON bookable_resources;
CREATE POLICY bookable_resource_select ON bookable_resources
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );
CREATE POLICY bookable_resource_write ON bookable_resources
    FOR ALL USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );

DROP POLICY tenant_isolation ON appointments;
CREATE POLICY appointment_select ON appointments
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND (
            app_current_user_can_access_location(location_id)
            OR (
                customer_id IS NOT NULL
                AND EXISTS (
                    SELECT 1
                    FROM customers customer
                    WHERE customer.tenant_id = appointments.tenant_id
                      AND customer.id = appointments.customer_id
                      AND (
                          appointments.location_id = customer.home_location_id
                          OR EXISTS (
                              SELECT 1
                              FROM customer_location_grants access_grant
                              WHERE access_grant.tenant_id = appointments.tenant_id
                                AND access_grant.customer_id = appointments.customer_id
                                AND access_grant.receiving_location_id = appointments.location_id
                                AND appointments.created_at >= access_grant.granted_at
                                AND (
                                    access_grant.revoked_at IS NULL
                                    OR appointments.created_at < access_grant.revoked_at
                                )
                          )
                      )
                )
            )
        )
    );
CREATE POLICY appointment_insert ON appointments
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
        AND (
            customer_id IS NULL
            OR EXISTS (
                SELECT 1 FROM customers customer
                WHERE customer.tenant_id = appointments.tenant_id
                  AND customer.id = appointments.customer_id
            )
        )
    );
CREATE POLICY appointment_update ON appointments
    FOR UPDATE USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );

DROP POLICY tenant_isolation ON provider_connections;
CREATE POLICY provider_connection_select ON provider_connections
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND (
            (location_id IS NULL AND app_current_user_manages_tenant())
            OR app_current_user_can_access_location(location_id)
        )
    );
CREATE POLICY provider_connection_write ON provider_connections
    FOR ALL USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );

DROP POLICY tenant_isolation ON agents;
CREATE POLICY agent_select ON agents
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );
CREATE POLICY agent_write ON agents
    FOR ALL USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );

DROP POLICY tenant_isolation ON phone_numbers;
CREATE POLICY phone_number_select ON phone_numbers
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );
CREATE POLICY phone_number_write ON phone_numbers
    FOR ALL USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_manages_tenant()
    );

DROP POLICY tenant_isolation ON calls;
CREATE POLICY call_select ON calls
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND (
            app_current_user_can_access_location(location_id)
            OR (
                customer_id IS NOT NULL
                AND EXISTS (
                    SELECT 1
                    FROM customers customer
                    WHERE customer.tenant_id = calls.tenant_id
                      AND customer.id = calls.customer_id
                      AND (
                          calls.location_id = customer.home_location_id
                          OR EXISTS (
                              SELECT 1 FROM customer_location_grants access_grant
                              WHERE access_grant.tenant_id = calls.tenant_id
                                AND access_grant.customer_id = calls.customer_id
                                AND access_grant.receiving_location_id = calls.location_id
                                AND calls.created_at >= access_grant.granted_at
                                AND (
                                    access_grant.revoked_at IS NULL
                                    OR calls.created_at < access_grant.revoked_at
                                )
                          )
                      )
                )
            )
        )
    );
CREATE POLICY call_write ON calls
    FOR ALL USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );

DROP POLICY tenant_isolation ON call_messages;
CREATE POLICY call_message_select ON call_messages
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM calls call
            WHERE call.tenant_id = call_messages.tenant_id
              AND call.id = call_messages.call_id
        )
    );
CREATE POLICY call_message_write ON call_messages
    FOR ALL USING (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM calls call
            WHERE call.tenant_id = call_messages.tenant_id
              AND call.id = call_messages.call_id
              AND app_current_user_can_access_location(call.location_id)
        )
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM calls call
            WHERE call.tenant_id = call_messages.tenant_id
              AND call.id = call_messages.call_id
              AND app_current_user_can_access_location(call.location_id)
        )
    );

DROP POLICY tenant_isolation ON tool_executions;
CREATE POLICY tool_execution_select ON tool_executions
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM calls call
            WHERE call.tenant_id = tool_executions.tenant_id
              AND call.id = tool_executions.call_id
        )
    );
CREATE POLICY tool_execution_write ON tool_executions
    FOR ALL USING (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM calls call
            WHERE call.tenant_id = tool_executions.tenant_id
              AND call.id = tool_executions.call_id
              AND app_current_user_can_access_location(call.location_id)
        )
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM calls call
            WHERE call.tenant_id = tool_executions.tenant_id
              AND call.id = tool_executions.call_id
              AND app_current_user_can_access_location(call.location_id)
        )
    );

DROP POLICY tenant_isolation ON customer_memories;
CREATE POLICY customer_memory_select ON customer_memories
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND (
            app_current_user_can_access_location(location_id)
            OR EXISTS (
                SELECT 1
                FROM customers customer
                WHERE customer.tenant_id = customer_memories.tenant_id
                  AND customer.id = customer_memories.customer_id
                  AND (
                      customer_memories.location_id = customer.home_location_id
                      OR EXISTS (
                          SELECT 1 FROM customer_location_grants access_grant
                          WHERE access_grant.tenant_id = customer_memories.tenant_id
                            AND access_grant.customer_id = customer_memories.customer_id
                            AND access_grant.receiving_location_id = customer_memories.location_id
                            AND customer_memories.created_at >= access_grant.granted_at
                            AND (
                                access_grant.revoked_at IS NULL
                                OR customer_memories.created_at < access_grant.revoked_at
                            )
                      )
                  )
            )
        )
    );
CREATE POLICY customer_memory_insert ON customer_memories
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
        AND EXISTS (
            SELECT 1 FROM customers customer
            WHERE customer.tenant_id = customer_memories.tenant_id
              AND customer.id = customer_memories.customer_id
        )
    );
CREATE POLICY customer_memory_update ON customer_memories
    FOR UPDATE USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );

DROP POLICY tenant_isolation ON appointment_calendar_events;
CREATE POLICY appointment_calendar_event_select ON appointment_calendar_events
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM appointments appointment
            WHERE appointment.tenant_id = appointment_calendar_events.tenant_id
              AND appointment.id = appointment_calendar_events.appointment_id
        )
    );
CREATE POLICY appointment_calendar_event_write ON appointment_calendar_events
    FOR ALL USING (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM appointments appointment
            WHERE appointment.tenant_id = appointment_calendar_events.tenant_id
              AND appointment.id = appointment_calendar_events.appointment_id
              AND app_current_user_can_access_location(appointment.location_id)
        )
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM appointments appointment
            WHERE appointment.tenant_id = appointment_calendar_events.tenant_id
              AND appointment.id = appointment_calendar_events.appointment_id
              AND app_current_user_can_access_location(appointment.location_id)
        )
    );

DROP POLICY tenant_isolation ON repair_orders;
CREATE POLICY repair_order_select ON repair_orders
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND (
            app_current_user_can_access_location(location_id)
            OR EXISTS (
                SELECT 1
                FROM customers customer
                WHERE customer.tenant_id = repair_orders.tenant_id
                  AND customer.id = repair_orders.customer_id
                  AND (
                      repair_orders.location_id = customer.home_location_id
                      OR EXISTS (
                          SELECT 1 FROM customer_location_grants access_grant
                          WHERE access_grant.tenant_id = repair_orders.tenant_id
                            AND access_grant.customer_id = repair_orders.customer_id
                            AND access_grant.receiving_location_id = repair_orders.location_id
                            AND repair_orders.created_at >= access_grant.granted_at
                            AND (
                                access_grant.revoked_at IS NULL
                                OR repair_orders.created_at < access_grant.revoked_at
                            )
                      )
                  )
            )
        )
    );
CREATE POLICY repair_order_insert ON repair_orders
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
        AND EXISTS (
            SELECT 1 FROM customers customer
            WHERE customer.tenant_id = repair_orders.tenant_id
              AND customer.id = repair_orders.customer_id
        )
    );
CREATE POLICY repair_order_update ON repair_orders
    FOR UPDATE USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND app_current_user_can_access_location(location_id)
    );

DROP POLICY tenant_isolation ON repair_order_items;
CREATE POLICY repair_order_item_select ON repair_order_items
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM repair_orders repair
            WHERE repair.tenant_id = repair_order_items.tenant_id
              AND repair.id = repair_order_items.repair_order_id
        )
    );
CREATE POLICY repair_order_item_write ON repair_order_items
    FOR ALL USING (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM repair_orders repair
            WHERE repair.tenant_id = repair_order_items.tenant_id
              AND repair.id = repair_order_items.repair_order_id
              AND app_current_user_can_access_location(repair.location_id)
        )
    ) WITH CHECK (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM repair_orders repair
            WHERE repair.tenant_id = repair_order_items.tenant_id
              AND repair.id = repair_order_items.repair_order_id
              AND app_current_user_can_access_location(repair.location_id)
        )
    );

-- +goose Down
DROP POLICY repair_order_item_write ON repair_order_items;
DROP POLICY repair_order_item_select ON repair_order_items;
CREATE POLICY tenant_isolation ON repair_order_items FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY repair_order_update ON repair_orders;
DROP POLICY repair_order_insert ON repair_orders;
DROP POLICY repair_order_select ON repair_orders;
CREATE POLICY tenant_isolation ON repair_orders FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY appointment_calendar_event_write ON appointment_calendar_events;
DROP POLICY appointment_calendar_event_select ON appointment_calendar_events;
CREATE POLICY tenant_isolation ON appointment_calendar_events FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY customer_memory_update ON customer_memories;
DROP POLICY customer_memory_insert ON customer_memories;
DROP POLICY customer_memory_select ON customer_memories;
CREATE POLICY tenant_isolation ON customer_memories FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY tool_execution_write ON tool_executions;
DROP POLICY tool_execution_select ON tool_executions;
CREATE POLICY tenant_isolation ON tool_executions FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY call_message_write ON call_messages;
DROP POLICY call_message_select ON call_messages;
CREATE POLICY tenant_isolation ON call_messages FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY call_write ON calls;
DROP POLICY call_select ON calls;
CREATE POLICY tenant_isolation ON calls FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY phone_number_write ON phone_numbers;
DROP POLICY phone_number_select ON phone_numbers;
CREATE POLICY tenant_isolation ON phone_numbers FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY agent_write ON agents;
DROP POLICY agent_select ON agents;
CREATE POLICY tenant_isolation ON agents FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY provider_connection_write ON provider_connections;
DROP POLICY provider_connection_select ON provider_connections;
CREATE POLICY tenant_isolation ON provider_connections FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY appointment_update ON appointments;
DROP POLICY appointment_insert ON appointments;
DROP POLICY appointment_select ON appointments;
CREATE POLICY tenant_isolation ON appointments FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY bookable_resource_write ON bookable_resources;
DROP POLICY bookable_resource_select ON bookable_resources;
CREATE POLICY tenant_isolation ON bookable_resources FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY service_offering_write ON service_offerings;
DROP POLICY service_offering_select ON service_offerings;
CREATE POLICY tenant_isolation ON service_offerings FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY vehicle_lookup_run_access ON vehicle_lookup_runs;
CREATE POLICY tenant_isolation ON vehicle_lookup_runs FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY vehicle_write ON vehicles;
DROP POLICY vehicle_select ON vehicles;
CREATE POLICY tenant_isolation ON vehicles FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY customer_contact_write ON customer_contacts;
DROP POLICY customer_contact_select ON customer_contacts;
CREATE POLICY tenant_isolation ON customer_contacts FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY customer_update ON customers;
DROP POLICY customer_insert ON customers;
DROP POLICY customer_select ON customers;
CREATE POLICY tenant_isolation ON customers FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY location_opening_hours_write ON location_opening_hours;
DROP POLICY location_opening_hours_select ON location_opening_hours;
CREATE POLICY tenant_isolation ON location_opening_hours FOR ALL
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
DROP POLICY location_select ON locations;
CREATE POLICY location_select ON locations FOR SELECT
    USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_belongs_to_tenant()
    );
