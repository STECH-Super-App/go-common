-- +goose Up
CREATE TABLE IF NOT EXISTS processed_outbox_messages (
    outbox_id    UUID        PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Reaper index: find old processed records for cleanup.
CREATE INDEX idx_processed_outbox_reaper ON processed_outbox_messages (processed_at);

-- +goose Down
DROP TABLE IF EXISTS processed_outbox_messages;
