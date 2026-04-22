-- Rename processed_outbox_messages.outbox_id -> event_id to match the
-- events-to-proto refactor (see design spec §6.2). The column is renamed,
-- not duplicated, because every service runs this migration atomically
-- at startup and no active consumers read the old name.

-- +goose Up
ALTER TABLE processed_outbox_messages
    RENAME COLUMN outbox_id TO event_id;

-- +goose Down
ALTER TABLE processed_outbox_messages
    RENAME COLUMN event_id TO outbox_id;
