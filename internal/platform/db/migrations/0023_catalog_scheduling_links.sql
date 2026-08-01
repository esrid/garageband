-- Link the organization price catalog to each location's scheduling contract.
-- +goose Up
ALTER TABLE service_offerings
    ADD COLUMN catalog_item_id UUID,
    ADD COLUMN catalog_link_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD CONSTRAINT service_offerings_catalog_item_fk
        FOREIGN KEY (tenant_id, catalog_item_id)
        REFERENCES catalog_items (tenant_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT service_offerings_catalog_link_consistent CHECK (
        catalog_item_id IS NOT NULL OR NOT catalog_link_enabled
    );

CREATE UNIQUE INDEX service_offerings_catalog_location_unique
    ON service_offerings (tenant_id, location_id, catalog_item_id)
    WHERE catalog_item_id IS NOT NULL;

CREATE INDEX service_offerings_catalog_item_idx
    ON service_offerings (tenant_id, catalog_item_id)
    WHERE catalog_item_id IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION sync_catalog_service_offering()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    source_kind TEXT;
    source_name TEXT;
    source_description TEXT;
    source_price_kind TEXT;
    source_amount_cents INTEGER;
    source_currency TEXT;
    source_duration_minutes INTEGER;
    source_archived_at TIMESTAMPTZ;
    source_applies BOOLEAN;
BEGIN
    IF NEW.catalog_item_id IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT item.kind, item.name, item.description, item.price_kind,
           item.amount_cents, item.currency, item.duration_minutes,
           item.archived_at,
           item.location_scope = 'all' OR EXISTS (
               SELECT 1
               FROM catalog_item_locations item_location
               WHERE item_location.tenant_id = item.tenant_id
                 AND item_location.catalog_item_id = item.id
                 AND item_location.location_id = NEW.location_id
           )
    INTO source_kind, source_name, source_description, source_price_kind,
         source_amount_cents, source_currency, source_duration_minutes,
         source_archived_at, source_applies
    FROM catalog_items item
    WHERE item.tenant_id = NEW.tenant_id
      AND item.id = NEW.catalog_item_id;

    -- The foreign key reports a missing source with its stable constraint name.
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;

    NEW.name := source_name;
    NEW.description := source_description;
    IF source_duration_minutes IS NOT NULL THEN
        NEW.duration_minutes := source_duration_minutes;
    END IF;
    NEW.price_cents := CASE
        WHEN source_price_kind = 'fixed' THEN source_amount_cents
        ELSE NULL
    END;
    NEW.currency := source_currency;
    NEW.active := NEW.catalog_link_enabled
        AND source_archived_at IS NULL
        AND source_kind IN ('service', 'package')
        AND source_duration_minutes IS NOT NULL
        AND source_applies;
    NEW.updated_at := now();
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER service_offerings_sync_catalog
BEFORE INSERT OR UPDATE OF catalog_item_id, catalog_link_enabled, location_id,
    name, description, duration_minutes, price_cents, currency, active
ON service_offerings
FOR EACH ROW EXECUTE FUNCTION sync_catalog_service_offering();

-- +goose StatementBegin
CREATE FUNCTION refresh_catalog_service_offerings()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    old_tenant_id UUID;
    old_item_id UUID;
    new_tenant_id UUID;
    new_item_id UUID;
BEGIN
    IF TG_TABLE_NAME = 'catalog_items' THEN
        old_tenant_id := NEW.tenant_id;
        old_item_id := NEW.id;
        new_tenant_id := NEW.tenant_id;
        new_item_id := NEW.id;
    ELSE
        IF TG_OP <> 'INSERT' THEN
            old_tenant_id := OLD.tenant_id;
            old_item_id := OLD.catalog_item_id;
        END IF;
        IF TG_OP <> 'DELETE' THEN
            new_tenant_id := NEW.tenant_id;
            new_item_id := NEW.catalog_item_id;
        END IF;
    END IF;

    UPDATE service_offerings service
    SET catalog_link_enabled = service.catalog_link_enabled
    WHERE service.catalog_item_id IS NOT NULL
      AND (
          (service.tenant_id = old_tenant_id AND service.catalog_item_id = old_item_id)
          OR (service.tenant_id = new_tenant_id AND service.catalog_item_id = new_item_id)
      );
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER catalog_items_refresh_scheduling
AFTER UPDATE OF kind, name, description, price_kind, amount_cents, currency,
    duration_minutes, location_scope, archived_at
ON catalog_items
FOR EACH ROW EXECUTE FUNCTION refresh_catalog_service_offerings();

CREATE TRIGGER catalog_item_locations_refresh_scheduling
AFTER INSERT OR UPDATE OF catalog_item_id, location_id OR DELETE
ON catalog_item_locations
FOR EACH ROW EXECUTE FUNCTION refresh_catalog_service_offerings();

-- +goose Down
DROP TRIGGER catalog_item_locations_refresh_scheduling ON catalog_item_locations;
DROP TRIGGER catalog_items_refresh_scheduling ON catalog_items;
DROP FUNCTION refresh_catalog_service_offerings();
DROP TRIGGER service_offerings_sync_catalog ON service_offerings;
DROP FUNCTION sync_catalog_service_offering();
DROP INDEX service_offerings_catalog_item_idx;
DROP INDEX service_offerings_catalog_location_unique;
ALTER TABLE service_offerings
    DROP CONSTRAINT service_offerings_catalog_link_consistent,
    DROP CONSTRAINT service_offerings_catalog_item_fk,
    DROP COLUMN catalog_link_enabled,
    DROP COLUMN catalog_item_id;
