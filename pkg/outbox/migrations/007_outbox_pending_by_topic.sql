-- The relay claims pending rows one topic at a time, oldest first (SL-03): this
-- keeps that claim, the pending-topic list and the by-topic backlog gauges
-- index-only. Partial, so it stays as small as idx_outbox_pending.

-- +goose Up
CREATE INDEX IF NOT EXISTS idx_outbox_pending_topic ON outbox_messages (topic, created_at)
    WHERE status = 'pending';

-- +goose Down
DROP INDEX IF EXISTS idx_outbox_pending_topic;
