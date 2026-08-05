-- Telephony provisioning: what a garage owns at the carrier.
--
-- One organization is one Twilio subaccount. That is the arrangement Twilio
-- prescribes for software vendors, and three product rules fall out of it for
-- free: closing a subaccount releases every number it holds, suspending one
-- stops calls while number charges continue (the paused-site policy), and a
-- number cannot move to another subaccount unless that subaccount carries its
-- own approved regulatory bundle.
--
-- The numbers themselves already live in phone_numbers since 0006. This
-- migration gives that table the two things provisioning needs and it lacks —
-- the compliance file a regulated number cannot be bought without, and a
-- release that frees the number for the next customer — rather than opening a
-- second table for the same thing.

-- +goose Up
CREATE TABLE telephony_accounts (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id      UUID NOT NULL UNIQUE REFERENCES tenants (id) ON DELETE CASCADE,
    provider       TEXT NOT NULL DEFAULT 'twilio',
    subaccount_sid TEXT NOT NULL UNIQUE,
    status         TEXT NOT NULL DEFAULT 'active',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT telephony_accounts_provider_present CHECK (btrim(provider) <> ''),
    CONSTRAINT telephony_accounts_subaccount_present CHECK (btrim(subaccount_sid) <> ''),
    CONSTRAINT telephony_accounts_status_valid CHECK (
        status IN ('active', 'suspended', 'closed')
    ),
    UNIQUE (tenant_id, id)
);

ALTER TABLE telephony_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE telephony_accounts FORCE ROW LEVEL SECURITY;

CREATE POLICY telephony_account_select ON telephony_accounts
    FOR SELECT USING (tenant_id = app_current_tenant_id());
CREATE POLICY telephony_account_insert ON telephony_accounts
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    );
CREATE POLICY telephony_account_update ON telephony_accounts
    FOR UPDATE USING (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    );

-- A bundle is the carrier's compliance file for one country and number type:
-- the end customer's identity, its registration number and its address. Twilio
-- reviews it, and that review is what sets the time to activation the order
-- form promises, so the wait is recorded rather than assumed.
CREATE TABLE regulatory_bundles (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id      UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    bundle_sid     TEXT NOT NULL UNIQUE,
    iso_country    TEXT NOT NULL,
    number_type    TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'draft',
    failure_reason TEXT,
    submitted_at   TIMESTAMPTZ,
    reviewed_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT regulatory_bundles_sid_present CHECK (btrim(bundle_sid) <> ''),
    CONSTRAINT regulatory_bundles_country_valid CHECK (iso_country ~ '^[A-Z]{2}$'),
    CONSTRAINT regulatory_bundles_number_type_valid CHECK (
        number_type IN ('local', 'mobile', 'national', 'toll-free')
    ),
    -- Twilio's own vocabulary, kept verbatim so a status never needs
    -- translating on its way in from the API.
    CONSTRAINT regulatory_bundles_status_valid CHECK (
        status IN (
            'draft', 'pending-review', 'in-review',
            'twilio-approved', 'twilio-rejected'
        )
    ),
    CONSTRAINT regulatory_bundles_reviewed_after_submission CHECK (
        reviewed_at IS NULL OR (submitted_at IS NOT NULL AND reviewed_at >= submitted_at)
    ),
    CONSTRAINT regulatory_bundles_reason_only_when_rejected CHECK (
        failure_reason IS NULL OR status = 'twilio-rejected'
    ),
    UNIQUE (tenant_id, id)
);

-- One live file per country and number type. A rejected one stays for its
-- reason and does not block the replacement.
CREATE UNIQUE INDEX regulatory_bundles_live_unique
    ON regulatory_bundles (tenant_id, iso_country, number_type)
    WHERE status <> 'twilio-rejected';

ALTER TABLE regulatory_bundles ENABLE ROW LEVEL SECURITY;
ALTER TABLE regulatory_bundles FORCE ROW LEVEL SECURITY;

CREATE POLICY regulatory_bundle_select ON regulatory_bundles
    FOR SELECT USING (tenant_id = app_current_tenant_id());
CREATE POLICY regulatory_bundle_insert ON regulatory_bundles
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    );
CREATE POLICY regulatory_bundle_update ON regulatory_bundles
    FOR UPDATE USING (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    );

-- What phone_numbers was missing to be provisioned rather than seeded.
ALTER TABLE phone_numbers
    ADD COLUMN bundle_id UUID,
    ADD COLUMN whatsapp_sender BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN released_at TIMESTAMPTZ,
    ADD CONSTRAINT phone_numbers_bundle_same_tenant
        FOREIGN KEY (tenant_id, bundle_id)
        REFERENCES regulatory_bundles (tenant_id, id) ON DELETE RESTRICT;

-- A number exists at the carrier before its webhooks are wired: 'provisioning'
-- is that gap, and 'released' is the number given back.
ALTER TABLE phone_numbers DROP CONSTRAINT phone_numbers_status_valid;
ALTER TABLE phone_numbers ADD CONSTRAINT phone_numbers_status_valid CHECK (
    status IN ('provisioning', 'active', 'disabled', 'porting', 'released')
);
ALTER TABLE phone_numbers ADD CONSTRAINT phone_numbers_released_consistent CHECK (
    (status = 'released') = (released_at IS NOT NULL)
);

-- The E.164 was unique for all time, which meant a number given back could
-- never be bought again, by this customer or by the next. The partial index
-- frees it the moment it is released, and the released row stays for its
-- history — the same shape as a customer's freed contact details.
ALTER TABLE phone_numbers DROP CONSTRAINT phone_numbers_phone_e164_key;
CREATE UNIQUE INDEX phone_numbers_held_unique
    ON phone_numbers (phone_e164)
    WHERE released_at IS NULL;

-- +goose Down
DROP INDEX phone_numbers_held_unique;
ALTER TABLE phone_numbers ADD CONSTRAINT phone_numbers_phone_e164_key UNIQUE (phone_e164);
ALTER TABLE phone_numbers DROP CONSTRAINT phone_numbers_released_consistent;
ALTER TABLE phone_numbers DROP CONSTRAINT phone_numbers_status_valid;
ALTER TABLE phone_numbers ADD CONSTRAINT phone_numbers_status_valid CHECK (
    status IN ('active', 'disabled', 'porting')
);
ALTER TABLE phone_numbers
    DROP CONSTRAINT phone_numbers_bundle_same_tenant,
    DROP COLUMN released_at,
    DROP COLUMN whatsapp_sender,
    DROP COLUMN bundle_id;
DROP POLICY regulatory_bundle_update ON regulatory_bundles;
DROP POLICY regulatory_bundle_insert ON regulatory_bundles;
DROP POLICY regulatory_bundle_select ON regulatory_bundles;
DROP TABLE regulatory_bundles;
DROP POLICY telephony_account_update ON telephony_accounts;
DROP POLICY telephony_account_insert ON telephony_accounts;
DROP POLICY telephony_account_select ON telephony_accounts;
DROP TABLE telephony_accounts;
