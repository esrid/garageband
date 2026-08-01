-- Durable customer and vehicle service history. Appointments describe planned
-- work; repair orders describe what was diagnosed, approved, and performed.
-- +goose Up
CREATE TABLE repair_orders (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id      UUID NOT NULL,
    location_id    UUID NOT NULL,
    customer_id    UUID NOT NULL,
    vehicle_id     UUID NOT NULL,
    appointment_id UUID,
    status         TEXT NOT NULL DEFAULT 'estimate',
    odometer_km    INTEGER,
    customer_complaint TEXT,
    diagnosis      TEXT,
    work_performed TEXT,
    internal_note  TEXT,
    currency       TEXT NOT NULL DEFAULT 'EUR',
    subtotal_cents INTEGER NOT NULL DEFAULT 0,
    tax_cents      INTEGER NOT NULL DEFAULT 0,
    total_cents    INTEGER NOT NULL DEFAULT 0,
    opened_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    approved_at    TIMESTAMPTZ,
    completed_at   TIMESTAMPTZ,
    cancelled_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, customer_id)
        REFERENCES customers (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, vehicle_id)
        REFERENCES vehicles (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, appointment_id)
        REFERENCES appointments (tenant_id, id) ON DELETE SET NULL (appointment_id),
    CONSTRAINT repair_orders_status_valid CHECK (
        status IN (
            'estimate', 'awaiting_approval', 'approved', 'in_progress',
            'completed', 'cancelled'
        )
    ),
    CONSTRAINT repair_orders_odometer_valid CHECK (
        odometer_km IS NULL OR odometer_km BETWEEN 0 AND 9999999
    ),
    CONSTRAINT repair_orders_currency_format CHECK (
        currency ~ '^[A-Z]{3}$'
    ),
    CONSTRAINT repair_orders_amounts_valid CHECK (
        subtotal_cents >= 0
        AND tax_cents >= 0
        AND total_cents >= 0
        AND total_cents = subtotal_cents + tax_cents
    ),
    CONSTRAINT repair_orders_lifecycle_consistent CHECK (
        (status = 'completed' AND completed_at IS NOT NULL AND cancelled_at IS NULL)
        OR (status = 'cancelled' AND cancelled_at IS NOT NULL AND completed_at IS NULL)
        OR (status NOT IN ('completed', 'cancelled')
            AND completed_at IS NULL AND cancelled_at IS NULL)
    ),
    CONSTRAINT repair_orders_approval_consistent CHECK (
        status IN ('estimate', 'awaiting_approval', 'cancelled')
        OR approved_at IS NOT NULL
    ),
    UNIQUE (tenant_id, id)
);

CREATE INDEX repair_orders_customer_opened_idx
    ON repair_orders (tenant_id, customer_id, opened_at DESC);

CREATE INDEX repair_orders_vehicle_opened_idx
    ON repair_orders (tenant_id, vehicle_id, opened_at DESC);

CREATE TABLE repair_order_items (
    id              UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id       UUID NOT NULL,
    repair_order_id UUID NOT NULL,
    service_id      UUID,
    kind            TEXT NOT NULL,
    description     TEXT NOT NULL,
    quantity        NUMERIC(10,3) NOT NULL DEFAULT 1,
    unit_price_cents INTEGER NOT NULL DEFAULT 0,
    tax_rate        NUMERIC(5,4) NOT NULL DEFAULT 0,
    line_subtotal_cents INTEGER NOT NULL DEFAULT 0,
    line_tax_cents  INTEGER NOT NULL DEFAULT 0,
    line_total_cents INTEGER NOT NULL DEFAULT 0,
    position        INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, repair_order_id)
        REFERENCES repair_orders (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, service_id)
        REFERENCES service_offerings (tenant_id, id) ON DELETE SET NULL (service_id),
    CONSTRAINT repair_order_items_kind_valid CHECK (
        kind IN ('labor', 'part', 'fee', 'discount')
    ),
    CONSTRAINT repair_order_items_description_present CHECK (
        btrim(description) <> ''
    ),
    CONSTRAINT repair_order_items_quantity_valid CHECK (quantity > 0),
    CONSTRAINT repair_order_items_unit_price_valid CHECK (
        unit_price_cents >= 0
    ),
    CONSTRAINT repair_order_items_tax_rate_valid CHECK (
        tax_rate BETWEEN 0 AND 1
    ),
    CONSTRAINT repair_order_items_amounts_valid CHECK (
        line_subtotal_cents >= 0
        AND line_tax_cents >= 0
        AND line_total_cents >= 0
        AND line_total_cents = line_subtotal_cents + line_tax_cents
    ),
    CONSTRAINT repair_order_items_position_valid CHECK (position >= 0),
    UNIQUE (repair_order_id, position),
    UNIQUE (tenant_id, id)
);

-- +goose Down
DROP TABLE repair_order_items;
DROP TABLE repair_orders;
