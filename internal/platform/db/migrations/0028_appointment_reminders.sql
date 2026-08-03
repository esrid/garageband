-- +goose Up
ALTER TABLE appointments
    ADD COLUMN reminder_sent_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE appointments
    DROP COLUMN reminder_sent_at;
