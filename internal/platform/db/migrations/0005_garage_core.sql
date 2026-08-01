-- Customers, vehicles, services, resources, and collision-safe appointments.
-- +goose Up
CREATE EXTENSION IF NOT EXISTS btree_gist WITH SCHEMA public;

CREATE TABLE customers (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id      UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    first_name     TEXT,
    last_name      TEXT,
    company_name   TEXT,
    preferred_locale TEXT NOT NULL DEFAULT 'fr-FR',
    notes          TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ,
    CONSTRAINT customers_identity_present CHECK (
        NULLIF(btrim(first_name), '') IS NOT NULL
        OR NULLIF(btrim(last_name), '') IS NOT NULL
        OR NULLIF(btrim(company_name), '') IS NOT NULL
    ),
    CONSTRAINT customers_locale_format CHECK (
        preferred_locale ~ '^[a-z]{2}(?:-[A-Z]{2})?$'
    ),
    UNIQUE (tenant_id, id)
);

CREATE INDEX customers_tenant_name_idx
    ON customers (tenant_id, last_name, first_name)
    WHERE deleted_at IS NULL;

CREATE TABLE customer_contacts (
    id               UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id        UUID NOT NULL,
    customer_id      UUID NOT NULL,
    kind             TEXT NOT NULL,
    value            TEXT NOT NULL,
    normalized_value TEXT NOT NULL,
    label            TEXT,
    is_primary       BOOLEAN NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ,
    FOREIGN KEY (tenant_id, customer_id)
        REFERENCES customers (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT customer_contacts_kind_valid CHECK (
        kind IN ('phone', 'email')
    ),
    CONSTRAINT customer_contacts_value_present CHECK (
        btrim(value) <> '' AND btrim(normalized_value) <> ''
    ),
    CONSTRAINT customer_contacts_normalized CHECK (
        (kind = 'phone' AND normalized_value ~ '^\+[1-9][0-9]{7,14}$')
        OR
        (kind = 'email' AND normalized_value = lower(normalized_value)
            AND normalized_value ~ '^[^[:space:]@]+@[^[:space:]@]+$')
    )
);

CREATE UNIQUE INDEX customer_contacts_active_value_unique
    ON customer_contacts (tenant_id, kind, normalized_value)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX customer_contacts_one_primary_per_kind
    ON customer_contacts (tenant_id, customer_id, kind)
    WHERE is_primary AND deleted_at IS NULL;

CREATE TABLE vehicles (
    id                   UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id            UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    customer_id          UUID,
    registration_country TEXT NOT NULL DEFAULT 'FR',
    registration_plate   TEXT,
    vin                  TEXT,
    make                 TEXT,
    model                TEXT,
    trim                 TEXT,
    fuel_type            TEXT,
    first_registration_on DATE,
    metadata             JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at           TIMESTAMPTZ,
    FOREIGN KEY (tenant_id, customer_id)
        REFERENCES customers (tenant_id, id) ON DELETE SET NULL (customer_id),
    CONSTRAINT vehicles_country_code_format CHECK (
        registration_country ~ '^[A-Z]{2}$'
    ),
    CONSTRAINT vehicles_plate_normalized CHECK (
        registration_plate IS NULL
        OR (
            registration_plate = upper(registration_plate)
            AND registration_plate !~ '[[:space:]]'
            AND char_length(registration_plate) BETWEEN 2 AND 16
        )
    ),
    CONSTRAINT vehicles_vin_format CHECK (
        vin IS NULL OR vin ~ '^[A-HJ-NPR-Z0-9]{17}$'
    ),
    CONSTRAINT vehicles_identity_present CHECK (
        registration_plate IS NOT NULL OR vin IS NOT NULL
    ),
    CONSTRAINT vehicles_metadata_object CHECK (
        jsonb_typeof(metadata) = 'object'
    ),
    UNIQUE (tenant_id, id)
);

CREATE UNIQUE INDEX vehicles_active_plate_unique
    ON vehicles (tenant_id, registration_country, registration_plate)
    WHERE registration_plate IS NOT NULL AND deleted_at IS NULL;

CREATE UNIQUE INDEX vehicles_active_vin_unique
    ON vehicles (tenant_id, vin)
    WHERE vin IS NOT NULL AND deleted_at IS NULL;

CREATE TABLE vehicle_lookup_runs (
    id            UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id     UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    vehicle_id    UUID,
    country_code  TEXT NOT NULL DEFAULT 'FR',
    registration_plate TEXT NOT NULL,
    provider      TEXT,
    status        TEXT NOT NULL DEFAULT 'pending',
    result        JSONB,
    error_code    TEXT,
    error_message TEXT,
    completed_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, vehicle_id)
        REFERENCES vehicles (tenant_id, id) ON DELETE SET NULL (vehicle_id),
    CONSTRAINT vehicle_lookup_country_format CHECK (
        country_code ~ '^[A-Z]{2}$'
    ),
    CONSTRAINT vehicle_lookup_plate_normalized CHECK (
        registration_plate = upper(registration_plate)
        AND registration_plate !~ '[[:space:]]'
        AND char_length(registration_plate) BETWEEN 2 AND 16
    ),
    CONSTRAINT vehicle_lookup_status_valid CHECK (
        status IN ('pending', 'running', 'succeeded', 'not_found', 'failed')
    ),
    CONSTRAINT vehicle_lookup_result_object CHECK (
        result IS NULL OR jsonb_typeof(result) = 'object'
    )
);

CREATE INDEX vehicle_lookup_runs_tenant_plate_idx
    ON vehicle_lookup_runs (
        tenant_id, country_code, registration_plate, created_at DESC
    );

CREATE TABLE service_offerings (
    id               UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id        UUID NOT NULL,
    location_id      UUID NOT NULL,
    code             TEXT NOT NULL,
    name             TEXT NOT NULL,
    description      TEXT,
    duration_minutes INTEGER NOT NULL,
    buffer_before_minutes INTEGER NOT NULL DEFAULT 0,
    buffer_after_minutes INTEGER NOT NULL DEFAULT 0,
    price_cents      INTEGER,
    currency         TEXT NOT NULL DEFAULT 'EUR',
    active           BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT service_offerings_code_format CHECK (
        code ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'
    ),
    CONSTRAINT service_offerings_name_present CHECK (btrim(name) <> ''),
    CONSTRAINT service_offerings_duration_valid CHECK (
        duration_minutes BETWEEN 5 AND 1440
    ),
    CONSTRAINT service_offerings_buffers_valid CHECK (
        buffer_before_minutes BETWEEN 0 AND 1440
        AND buffer_after_minutes BETWEEN 0 AND 1440
    ),
    CONSTRAINT service_offerings_price_valid CHECK (
        price_cents IS NULL OR price_cents >= 0
    ),
    CONSTRAINT service_offerings_currency_format CHECK (
        currency ~ '^[A-Z]{3}$'
    ),
    UNIQUE (tenant_id, id),
    UNIQUE (location_id, code)
);

CREATE TABLE bookable_resources (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id   UUID NOT NULL,
    location_id UUID NOT NULL,
    kind        TEXT NOT NULL,
    name        TEXT NOT NULL,
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    metadata    JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT bookable_resources_kind_valid CHECK (
        kind IN ('technician', 'bay', 'equipment', 'calendar')
    ),
    CONSTRAINT bookable_resources_name_present CHECK (btrim(name) <> ''),
    CONSTRAINT bookable_resources_metadata_object CHECK (
        jsonb_typeof(metadata) = 'object'
    ),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, location_id, id)
);

CREATE TABLE appointments (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id      UUID NOT NULL,
    location_id    UUID NOT NULL,
    customer_id    UUID,
    vehicle_id     UUID,
    service_id     UUID,
    resource_id    UUID,
    status         TEXT NOT NULL DEFAULT 'pending',
    starts_at      TIMESTAMPTZ NOT NULL,
    ends_at        TIMESTAMPTZ NOT NULL,
    occupied_during TSTZRANGE GENERATED ALWAYS AS (
        tstzrange(starts_at, ends_at, '[)')
    ) STORED,
    source         TEXT NOT NULL DEFAULT 'dashboard',
    customer_note  TEXT,
    internal_note  TEXT,
    cancellation_reason TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    cancelled_at   TIMESTAMPTZ,
    FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, customer_id)
        REFERENCES customers (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, vehicle_id)
        REFERENCES vehicles (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, service_id)
        REFERENCES service_offerings (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, location_id, resource_id)
        REFERENCES bookable_resources (tenant_id, location_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT appointments_status_valid CHECK (
        status IN (
            'draft', 'pending', 'confirmed', 'in_progress',
            'completed', 'cancelled', 'no_show'
        )
    ),
    CONSTRAINT appointments_time_ordered CHECK (starts_at < ends_at),
    CONSTRAINT appointments_source_valid CHECK (
        source IN ('dashboard', 'agent', 'calendar', 'import')
    ),
    CONSTRAINT appointments_resource_when_booked CHECK (
        status IN ('draft', 'cancelled') OR resource_id IS NOT NULL
    ),
    CONSTRAINT appointments_cancellation_consistent CHECK (
        (status = 'cancelled' AND cancelled_at IS NOT NULL)
        OR (status <> 'cancelled' AND cancelled_at IS NULL)
    ),
    UNIQUE (tenant_id, id),
    EXCLUDE USING gist (
        resource_id WITH =,
        occupied_during WITH &&
    ) WHERE (
        status IN ('pending', 'confirmed', 'in_progress')
    )
);

CREATE INDEX appointments_tenant_location_start_idx
    ON appointments (tenant_id, location_id, starts_at);

CREATE INDEX appointments_tenant_customer_start_idx
    ON appointments (tenant_id, customer_id, starts_at DESC)
    WHERE customer_id IS NOT NULL;

-- +goose Down
DROP TABLE appointments;
DROP TABLE bookable_resources;
DROP TABLE service_offerings;
DROP TABLE vehicle_lookup_runs;
DROP TABLE vehicles;
DROP TABLE customer_contacts;
DROP TABLE customers;
