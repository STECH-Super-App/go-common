-- The consumer-side deduplicator of events.NewPostgresDeduplicator: one row per
-- (consumer, event) that a handler's transaction has processed.
--
-- It replaces processed_outbox_messages, whose key was the bare event id (SL-31):
-- two consumers of one topic in one database would have swallowed each other's
-- events. consumer is the Kafka group id the dispatcher was built with
-- (events.WithGroup). event_id is text, not uuid, so a producer that sends a
-- non-UUID id is deduplicated instead of failing every delivery.
--
-- Old rows stay in processed_outbox_messages; the new deduplicator reads that
-- table too for as long as it exists, so a redelivery across the upgrade is
-- still recognised.

-- +goose Up
CREATE TABLE IF NOT EXISTS processed_events (
    consumer     VARCHAR(128) NOT NULL,
    event_id     VARCHAR(128) NOT NULL,
    processed_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (consumer, event_id)
);

-- Reaper index: delete claims older than the retention in bounded batches.
CREATE INDEX IF NOT EXISTS idx_processed_events_reaper ON processed_events (processed_at);

-- +goose Down
DROP TABLE IF EXISTS processed_events;
