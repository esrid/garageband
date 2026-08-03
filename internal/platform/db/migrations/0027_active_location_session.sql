-- +goose Up
ALTER TABLE sessions
    ADD COLUMN active_location_id UUID REFERENCES locations (id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE sessions
    DROP COLUMN active_location_id;
