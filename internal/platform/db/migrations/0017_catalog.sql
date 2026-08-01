-- Published catalog items and human-approved, auditable file imports.
-- +goose Up
CREATE TABLE catalog_imports (
    id                 UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id          UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    location_id        UUID NOT NULL,
    uploaded_by_user_id UUID NOT NULL,
    source_filename    TEXT NOT NULL,
    source_format      TEXT NOT NULL,
    source_media_type  TEXT NOT NULL,
    source_size_bytes  INTEGER NOT NULL,
    source_sha256      BYTEA NOT NULL,
    source_content     BYTEA,
    status             TEXT NOT NULL DEFAULT 'analyzing',
    rejection_reason   TEXT,
    publication_mode   TEXT,
    published_at       TIMESTAMPTZ,
    published_by_user_id UUID,
    cancelled_at       TIMESTAMPTZ,
    cancelled_by_user_id UUID,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, uploaded_by_user_id)
        REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, published_by_user_id)
        REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, cancelled_by_user_id)
        REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE RESTRICT,
    CONSTRAINT catalog_imports_filename_present CHECK (
        btrim(source_filename) <> '' AND char_length(source_filename) <= 255
    ),
    CONSTRAINT catalog_imports_format_valid CHECK (
        source_format IN ('csv', 'xlsx', 'unknown')
    ),
    CONSTRAINT catalog_imports_media_type_present CHECK (
        btrim(source_media_type) <> '' AND char_length(source_media_type) <= 127
    ),
    CONSTRAINT catalog_imports_size_valid CHECK (
        source_size_bytes >= 0
        AND (
            (status = 'rejected' AND (
                source_content IS NULL
                OR source_size_bytes = octet_length(source_content)
            ))
            OR (status <> 'rejected'
                AND source_format IN ('csv', 'xlsx')
                AND source_size_bytes > 0
                AND source_size_bytes <= 5242880
                AND source_size_bytes = octet_length(source_content))
        )
    ),
    CONSTRAINT catalog_imports_sha256_valid CHECK (
        octet_length(source_sha256) = 32
    ),
    CONSTRAINT catalog_imports_status_valid CHECK (
        status IN ('analyzing', 'ready', 'published', 'rejected', 'cancelled')
    ),
    CONSTRAINT catalog_imports_rejection_consistent CHECK (
        (status = 'rejected' AND rejection_reason IN (
            'unsupported', 'too_large', 'empty', 'unreadable', 'no_columns'
        ))
        OR (status <> 'rejected' AND rejection_reason IS NULL)
    ),
    CONSTRAINT catalog_imports_publication_consistent CHECK (
        (status = 'published'
            AND publication_mode IN ('merge', 'replace')
            AND published_at IS NOT NULL
            AND published_by_user_id IS NOT NULL)
        OR (status <> 'published'
            AND publication_mode IS NULL
            AND published_at IS NULL
            AND published_by_user_id IS NULL)
    ),
    CONSTRAINT catalog_imports_cancellation_consistent CHECK (
        (status = 'cancelled'
            AND cancelled_at IS NOT NULL
            AND cancelled_by_user_id IS NOT NULL)
        OR (status <> 'cancelled'
            AND cancelled_at IS NULL
            AND cancelled_by_user_id IS NULL)
    ),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, location_id, source_sha256)
);

CREATE INDEX catalog_imports_tenant_created_idx
    ON catalog_imports (tenant_id, created_at DESC);

CREATE TABLE catalog_items (
    id                 UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id          UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    kind               TEXT NOT NULL,
    reference          TEXT,
    name               TEXT NOT NULL,
    description        TEXT,
    price_kind         TEXT NOT NULL,
    amount_cents       INTEGER,
    max_amount_cents   INTEGER,
    tax_basis          TEXT NOT NULL DEFAULT 'incl',
    vat_basis_points   INTEGER NOT NULL DEFAULT 2000,
    currency           TEXT NOT NULL DEFAULT 'EUR',
    per_hour           BOOLEAN NOT NULL DEFAULT FALSE,
    duration_minutes   INTEGER,
    effective_from     DATE,
    effective_to       DATE,
    location_scope     TEXT NOT NULL DEFAULT 'all',
    source_import_id   UUID,
    created_by_user_id UUID NOT NULL,
    updated_by_user_id UUID NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at        TIMESTAMPTZ,
    archived_by_user_id UUID,
    FOREIGN KEY (tenant_id, source_import_id)
        REFERENCES catalog_imports (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, created_by_user_id)
        REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, updated_by_user_id)
        REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, archived_by_user_id)
        REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE RESTRICT,
    CONSTRAINT catalog_items_kind_valid CHECK (
        kind IN ('service', 'product', 'package', 'supplement', 'labour_rate')
    ),
    CONSTRAINT catalog_items_reference_valid CHECK (
        reference IS NULL
        OR (btrim(reference) <> '' AND char_length(reference) <= 80)
    ),
    CONSTRAINT catalog_items_name_present CHECK (
        btrim(name) <> '' AND char_length(name) <= 160
    ),
    CONSTRAINT catalog_items_description_valid CHECK (
        description IS NULL OR char_length(description) <= 2000
    ),
    CONSTRAINT catalog_items_price_kind_valid CHECK (
        price_kind IN ('fixed', 'from', 'range', 'quote')
    ),
    CONSTRAINT catalog_items_price_consistent CHECK (
        (price_kind = 'quote' AND amount_cents IS NULL AND max_amount_cents IS NULL)
        OR (price_kind IN ('fixed', 'from')
            AND amount_cents >= 0 AND max_amount_cents IS NULL)
        OR (price_kind = 'range'
            AND amount_cents >= 0 AND max_amount_cents >= amount_cents)
    ),
    CONSTRAINT catalog_items_tax_basis_valid CHECK (tax_basis IN ('excl', 'incl')),
    CONSTRAINT catalog_items_vat_valid CHECK (vat_basis_points BETWEEN 0 AND 10000),
    CONSTRAINT catalog_items_currency_eur CHECK (currency = 'EUR'),
    CONSTRAINT catalog_items_per_hour_consistent CHECK (
        per_hour = (kind = 'labour_rate')
    ),
    CONSTRAINT catalog_items_duration_valid CHECK (
        duration_minutes IS NULL
        OR (kind IN ('service', 'package') AND duration_minutes BETWEEN 5 AND 1440)
    ),
    CONSTRAINT catalog_items_effective_dates_ordered CHECK (
        effective_from IS NULL OR effective_to IS NULL OR effective_from <= effective_to
    ),
    CONSTRAINT catalog_items_scope_valid CHECK (location_scope IN ('all', 'selected')),
    CONSTRAINT catalog_items_archive_consistent CHECK (
        (archived_at IS NULL AND archived_by_user_id IS NULL)
        OR (archived_at IS NOT NULL AND archived_by_user_id IS NOT NULL)
    ),
    UNIQUE (tenant_id, id)
);

CREATE UNIQUE INDEX catalog_items_active_reference_unique
    ON catalog_items (tenant_id, lower(reference))
    WHERE reference IS NOT NULL AND archived_at IS NULL;

CREATE INDEX catalog_items_tenant_name_idx
    ON catalog_items (tenant_id, lower(name))
    WHERE archived_at IS NULL;

CREATE INDEX catalog_items_quotable_idx
    ON catalog_items (tenant_id, effective_from, effective_to)
    WHERE archived_at IS NULL;

CREATE TABLE catalog_item_locations (
    tenant_id      UUID NOT NULL,
    catalog_item_id UUID NOT NULL,
    location_id    UUID NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, catalog_item_id, location_id),
    FOREIGN KEY (tenant_id, catalog_item_id)
        REFERENCES catalog_items (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, location_id)
        REFERENCES locations (tenant_id, id) ON DELETE RESTRICT
);

CREATE INDEX catalog_item_locations_location_idx
    ON catalog_item_locations (tenant_id, location_id, catalog_item_id);

-- +goose StatementBegin
CREATE FUNCTION validate_catalog_item_location_scope()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    candidate_tenant_id UUID;
    candidate_item_id UUID;
    candidate_scope TEXT;
    location_count INTEGER;
BEGIN
    IF TG_TABLE_NAME = 'catalog_items' THEN
        candidate_tenant_id := NEW.tenant_id;
        candidate_item_id := NEW.id;
    ELSIF TG_OP = 'DELETE' THEN
        candidate_tenant_id := OLD.tenant_id;
        candidate_item_id := OLD.catalog_item_id;
    ELSE
        candidate_tenant_id := NEW.tenant_id;
        candidate_item_id := NEW.catalog_item_id;
    END IF;

    SELECT location_scope INTO candidate_scope
    FROM catalog_items
    WHERE tenant_id = candidate_tenant_id AND id = candidate_item_id;

    IF candidate_scope IS NULL THEN
        RETURN NULL;
    END IF;

    SELECT count(*) INTO location_count
    FROM catalog_item_locations
    WHERE tenant_id = candidate_tenant_id AND catalog_item_id = candidate_item_id;

    IF candidate_scope = 'all' AND location_count <> 0 THEN
        RAISE EXCEPTION 'an all-location catalog item cannot name locations'
            USING ERRCODE = '23514', CONSTRAINT = 'catalog_item_location_scope_consistent';
    END IF;
    IF candidate_scope = 'selected' AND location_count = 0 THEN
        RAISE EXCEPTION 'a selected-location catalog item requires a location'
            USING ERRCODE = '23514', CONSTRAINT = 'catalog_item_location_scope_consistent';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER catalog_items_validate_location_scope
AFTER INSERT OR UPDATE OF location_scope ON catalog_items
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_catalog_item_location_scope();

CREATE CONSTRAINT TRIGGER catalog_item_locations_validate_scope
AFTER INSERT OR DELETE ON catalog_item_locations
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION validate_catalog_item_location_scope();

CREATE TABLE catalog_import_rows (
    id                 UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id          UUID NOT NULL,
    import_id          UUID NOT NULL,
    row_number         INTEGER NOT NULL,
    classification     TEXT NOT NULL,
    raw_data           JSONB NOT NULL,
    kind               TEXT,
    reference          TEXT,
    name               TEXT,
    description        TEXT,
    price_kind         TEXT,
    amount_cents       INTEGER,
    max_amount_cents   INTEGER,
    tax_basis          TEXT,
    vat_basis_points   INTEGER,
    duration_minutes   INTEGER,
    effective_from     DATE,
    effective_to       DATE,
    issue              TEXT,
    matching_item_id   UUID,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, import_id)
        REFERENCES catalog_imports (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, matching_item_id)
        REFERENCES catalog_items (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT catalog_import_rows_number_valid CHECK (row_number >= 2),
    CONSTRAINT catalog_import_rows_classification_valid CHECK (
        classification IN ('valid', 'ambiguous', 'rejected')
    ),
    CONSTRAINT catalog_import_rows_raw_object CHECK (jsonb_typeof(raw_data) = 'object'),
    CONSTRAINT catalog_import_rows_issue_consistent CHECK (
        (classification = 'valid' AND issue IS NULL AND matching_item_id IS NULL)
        OR (classification = 'ambiguous' AND btrim(issue) <> '' AND matching_item_id IS NOT NULL)
        OR (classification = 'rejected' AND btrim(issue) <> '' AND matching_item_id IS NULL)
    ),
    CONSTRAINT catalog_import_rows_kind_valid CHECK (
        kind IS NULL OR kind IN ('service', 'product', 'package', 'supplement', 'labour_rate')
    ),
    CONSTRAINT catalog_import_rows_price_kind_valid CHECK (
        price_kind IS NULL OR price_kind IN ('fixed', 'from', 'range', 'quote')
    ),
    CONSTRAINT catalog_import_rows_normalized_valid CHECK (
        classification = 'rejected'
        OR (
            kind IS NOT NULL
            AND name IS NOT NULL AND btrim(name) <> '' AND char_length(name) <= 160
            AND price_kind IS NOT NULL
            AND tax_basis IN ('excl', 'incl')
            AND vat_basis_points BETWEEN 0 AND 10000
            AND (
                (price_kind = 'quote' AND amount_cents IS NULL AND max_amount_cents IS NULL)
                OR (price_kind IN ('fixed', 'from')
                    AND amount_cents >= 0 AND max_amount_cents IS NULL)
                OR (price_kind = 'range'
                    AND amount_cents >= 0 AND max_amount_cents >= amount_cents)
            )
            AND (duration_minutes IS NULL OR (
                kind IN ('service', 'package') AND duration_minutes BETWEEN 5 AND 1440
            ))
            AND (effective_from IS NULL OR effective_to IS NULL OR effective_from <= effective_to)
        )
    ),
    UNIQUE (tenant_id, import_id, row_number)
);

CREATE INDEX catalog_import_rows_import_classification_idx
    ON catalog_import_rows (tenant_id, import_id, classification, row_number);

CREATE TABLE catalog_publications (
    id                   UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id            UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    import_id            UUID NOT NULL,
    version              INTEGER NOT NULL,
    mode                 TEXT NOT NULL,
    published_by_user_id UUID NOT NULL,
    before_state         JSONB NOT NULL,
    after_state          JSONB NOT NULL,
    published_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    rolled_back_at       TIMESTAMPTZ,
    rolled_back_by_user_id UUID,
    FOREIGN KEY (tenant_id, import_id)
        REFERENCES catalog_imports (tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, published_by_user_id)
        REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, rolled_back_by_user_id)
        REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE RESTRICT,
    CONSTRAINT catalog_publications_version_valid CHECK (version > 0),
    CONSTRAINT catalog_publications_mode_valid CHECK (mode IN ('merge', 'replace')),
    CONSTRAINT catalog_publications_before_array CHECK (jsonb_typeof(before_state) = 'array'),
    CONSTRAINT catalog_publications_after_array CHECK (jsonb_typeof(after_state) = 'array'),
    CONSTRAINT catalog_publications_rollback_consistent CHECK (
        (rolled_back_at IS NULL AND rolled_back_by_user_id IS NULL)
        OR (rolled_back_at IS NOT NULL AND rolled_back_by_user_id IS NOT NULL)
    ),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, import_id),
    UNIQUE (tenant_id, version)
);

-- +goose StatementBegin
CREATE FUNCTION touch_catalog_updated_at()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER catalog_items_touch_updated_at
BEFORE UPDATE ON catalog_items
FOR EACH ROW EXECUTE FUNCTION touch_catalog_updated_at();

CREATE TRIGGER catalog_imports_touch_updated_at
BEFORE UPDATE ON catalog_imports
FOR EACH ROW EXECUTE FUNCTION touch_catalog_updated_at();

-- Import source and actor data are audit evidence. Only the explicit lifecycle
-- transitions below may change after insertion.
-- +goose StatementBegin
CREATE FUNCTION protect_catalog_import_audit()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.id, NEW.tenant_id, NEW.location_id, NEW.uploaded_by_user_id,
        NEW.source_filename, NEW.source_format, NEW.source_media_type,
        NEW.source_size_bytes, NEW.source_sha256, NEW.source_content, NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.tenant_id, OLD.location_id, OLD.uploaded_by_user_id,
        OLD.source_filename, OLD.source_format, OLD.source_media_type,
        OLD.source_size_bytes, OLD.source_sha256, OLD.source_content, OLD.created_at
    ) THEN
        RAISE EXCEPTION 'catalog import audit fields are immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'catalog_import_audit_immutable';
    END IF;
    IF NOT (
        (OLD.status = 'analyzing' AND NEW.status IN ('ready', 'rejected'))
        OR (OLD.status = 'ready' AND NEW.status IN ('published', 'cancelled'))
    ) THEN
        RAISE EXCEPTION 'invalid catalog import status transition'
            USING ERRCODE = '23514', CONSTRAINT = 'catalog_import_status_transition_valid';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER catalog_imports_protect_audit
BEFORE UPDATE ON catalog_imports
FOR EACH ROW EXECUTE FUNCTION protect_catalog_import_audit();

-- +goose StatementBegin
CREATE FUNCTION protect_catalog_item_identity()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(NEW.id, NEW.tenant_id, NEW.created_by_user_id, NEW.created_at)
       IS DISTINCT FROM
       ROW(OLD.id, OLD.tenant_id, OLD.created_by_user_id, OLD.created_at) THEN
        RAISE EXCEPTION 'catalog item identity is immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'catalog_item_identity_immutable';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER catalog_items_protect_identity
BEFORE UPDATE ON catalog_items
FOR EACH ROW EXECUTE FUNCTION protect_catalog_item_identity();

-- +goose StatementBegin
CREATE FUNCTION protect_catalog_publication_audit()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.id, NEW.tenant_id, NEW.import_id, NEW.version, NEW.mode,
        NEW.published_by_user_id, NEW.before_state, NEW.after_state, NEW.published_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.tenant_id, OLD.import_id, OLD.version, OLD.mode,
        OLD.published_by_user_id, OLD.before_state, OLD.after_state, OLD.published_at
    ) OR OLD.rolled_back_at IS NOT NULL OR NEW.rolled_back_at IS NULL THEN
        RAISE EXCEPTION 'catalog publication audit is immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'catalog_publication_audit_immutable';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER catalog_publications_protect_audit
BEFORE UPDATE ON catalog_publications
FOR EACH ROW EXECUTE FUNCTION protect_catalog_publication_audit();

ALTER TABLE catalog_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalog_items FORCE ROW LEVEL SECURITY;
CREATE POLICY catalog_item_select ON catalog_items
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_belongs_to_tenant()
        AND (
            app_current_user_manages_tenant()
            OR (
                location_scope = 'all'
                AND EXISTS (
                    SELECT 1 FROM locations accessible_location
                    WHERE accessible_location.tenant_id = catalog_items.tenant_id
                      AND app_current_user_can_access_location(accessible_location.id)
                )
            )
            OR EXISTS (
                SELECT 1 FROM catalog_item_locations item_location
                WHERE item_location.tenant_id = catalog_items.tenant_id
                  AND item_location.catalog_item_id = catalog_items.id
                  AND app_current_user_can_access_location(item_location.location_id)
            )
        )
    );
CREATE POLICY catalog_item_insert ON catalog_items
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    );
CREATE POLICY catalog_item_update ON catalog_items
    FOR UPDATE USING (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    ) WITH CHECK (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    );

ALTER TABLE catalog_item_locations ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalog_item_locations FORCE ROW LEVEL SECURITY;
CREATE POLICY catalog_item_location_select ON catalog_item_locations
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_belongs_to_tenant()
        AND (
            app_current_user_manages_tenant()
            OR app_current_user_can_access_location(location_id)
        )
    );
CREATE POLICY catalog_item_location_write ON catalog_item_locations
    FOR ALL USING (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    ) WITH CHECK (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    );

ALTER TABLE catalog_imports ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalog_imports FORCE ROW LEVEL SECURITY;
CREATE POLICY catalog_import_select ON catalog_imports
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND app_current_user_belongs_to_tenant()
        AND (
            app_current_user_manages_tenant()
            OR app_current_user_can_access_location(location_id)
        )
    );
CREATE POLICY catalog_import_insert ON catalog_imports
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    );
CREATE POLICY catalog_import_update ON catalog_imports
    FOR UPDATE USING (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    ) WITH CHECK (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    );

ALTER TABLE catalog_import_rows ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalog_import_rows FORCE ROW LEVEL SECURITY;
CREATE POLICY catalog_import_row_select ON catalog_import_rows
    FOR SELECT USING (
        tenant_id = app_current_tenant_id()
        AND EXISTS (
            SELECT 1 FROM catalog_imports catalog_import
            WHERE catalog_import.tenant_id = catalog_import_rows.tenant_id
              AND catalog_import.id = catalog_import_rows.import_id
        )
    );
CREATE POLICY catalog_import_row_insert ON catalog_import_rows
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    );

ALTER TABLE catalog_publications ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalog_publications FORCE ROW LEVEL SECURITY;
CREATE POLICY catalog_publication_select ON catalog_publications
    FOR SELECT USING (
        tenant_id = app_current_tenant_id() AND app_current_user_belongs_to_tenant()
    );
CREATE POLICY catalog_publication_insert ON catalog_publications
    FOR INSERT WITH CHECK (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
    );
CREATE POLICY catalog_publication_rollback ON catalog_publications
    FOR UPDATE USING (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
        AND rolled_back_at IS NULL
    ) WITH CHECK (
        tenant_id = app_current_tenant_id() AND app_current_user_manages_tenant()
        AND rolled_back_at IS NOT NULL AND rolled_back_by_user_id IS NOT NULL
    );

-- +goose Down
DROP POLICY catalog_publication_rollback ON catalog_publications;
DROP POLICY catalog_publication_insert ON catalog_publications;
DROP POLICY catalog_publication_select ON catalog_publications;
ALTER TABLE catalog_publications NO FORCE ROW LEVEL SECURITY;
ALTER TABLE catalog_publications DISABLE ROW LEVEL SECURITY;

DROP POLICY catalog_import_row_insert ON catalog_import_rows;
DROP POLICY catalog_import_row_select ON catalog_import_rows;
ALTER TABLE catalog_import_rows NO FORCE ROW LEVEL SECURITY;
ALTER TABLE catalog_import_rows DISABLE ROW LEVEL SECURITY;

DROP POLICY catalog_import_update ON catalog_imports;
DROP POLICY catalog_import_insert ON catalog_imports;
DROP POLICY catalog_import_select ON catalog_imports;
ALTER TABLE catalog_imports NO FORCE ROW LEVEL SECURITY;
ALTER TABLE catalog_imports DISABLE ROW LEVEL SECURITY;

DROP POLICY catalog_item_location_write ON catalog_item_locations;
DROP POLICY catalog_item_location_select ON catalog_item_locations;
ALTER TABLE catalog_item_locations NO FORCE ROW LEVEL SECURITY;
ALTER TABLE catalog_item_locations DISABLE ROW LEVEL SECURITY;

DROP POLICY catalog_item_update ON catalog_items;
DROP POLICY catalog_item_insert ON catalog_items;
DROP POLICY catalog_item_select ON catalog_items;
ALTER TABLE catalog_items NO FORCE ROW LEVEL SECURITY;
ALTER TABLE catalog_items DISABLE ROW LEVEL SECURITY;

DROP TRIGGER catalog_publications_protect_audit ON catalog_publications;
DROP FUNCTION protect_catalog_publication_audit();
DROP TRIGGER catalog_items_protect_identity ON catalog_items;
DROP FUNCTION protect_catalog_item_identity();
DROP TRIGGER catalog_imports_protect_audit ON catalog_imports;
DROP FUNCTION protect_catalog_import_audit();
DROP TRIGGER catalog_imports_touch_updated_at ON catalog_imports;
DROP TRIGGER catalog_items_touch_updated_at ON catalog_items;
DROP FUNCTION touch_catalog_updated_at();
DROP TABLE catalog_publications;
DROP TABLE catalog_import_rows;
DROP TRIGGER catalog_item_locations_validate_scope ON catalog_item_locations;
DROP TRIGGER catalog_items_validate_location_scope ON catalog_items;
DROP FUNCTION validate_catalog_item_location_scope();
DROP TABLE catalog_item_locations;
DROP TABLE catalog_items;
DROP TABLE catalog_imports;
