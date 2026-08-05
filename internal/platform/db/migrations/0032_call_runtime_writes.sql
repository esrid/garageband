-- The telephone agent writes a call while it is happening, and there is no
-- user then: the caller is anonymous and no employee is signed in. Every
-- existing write policy on calls and call_messages asks whether the current
-- user may reach the location, which is unanswerable in that moment, so the
-- runtime's inserts are refused and a real call leaves no trace.
--
-- These policies name that writer explicitly: a tenant is set, no user is.
-- The condition is deliberately the absence of a user rather than a role,
-- because it is exactly what distinguishes the machine from a person. It is
-- reachable only from the two anonymous endpoints in internal/features/voice,
-- both of which prove their origin before touching the database — Twilio's
-- signature on the webhook, an HMAC token on the socket.
--
-- Reading is granted too, and only because writing needs it: PostgreSQL
-- applies SELECT policies to a RETURNING clause, and the runtime reads back
-- the row it just inserted and the sequence its next turn continues from.
-- The reach stops there. Customers, appointments, catalogue prices and every
-- other table keep the policies they had, so a request that forgot to set its
-- user still cannot read a dossier — only the conversation it is itself
-- holding, in the tenant already in context.

-- +goose Up
CREATE POLICY call_runtime_insert ON calls
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id() AND app_current_user_id() IS NULL
    );

-- Ending a call, and only a call the same anonymous runtime is already in the
-- middle of: the row must belong to the tenant in context.
CREATE POLICY call_runtime_update ON calls
    FOR UPDATE USING (
        tenant_id = app_current_tenant_id() AND app_current_user_id() IS NULL
    ) WITH CHECK (
        tenant_id = app_current_tenant_id() AND app_current_user_id() IS NULL
    );

CREATE POLICY call_runtime_select ON calls
    FOR SELECT USING (
        tenant_id = app_current_tenant_id() AND app_current_user_id() IS NULL
    );

CREATE POLICY call_message_runtime_insert ON call_messages
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id() AND app_current_user_id() IS NULL
    );

CREATE POLICY call_message_runtime_select ON call_messages
    FOR SELECT USING (
        tenant_id = app_current_tenant_id() AND app_current_user_id() IS NULL
    );

-- +goose Down
DROP POLICY call_message_runtime_select ON call_messages;
DROP POLICY call_message_runtime_insert ON call_messages;
DROP POLICY call_runtime_select ON calls;
DROP POLICY call_runtime_update ON calls;
DROP POLICY call_runtime_insert ON calls;
