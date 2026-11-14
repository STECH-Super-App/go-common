-- +goose NO TRANSACTION
-- The relay claims pending rows one topic at a time, oldest first (SL-03): this
-- index serves that claim ((topic, created_at) order, LIMIT n), the loose index
-- scan that lists the topics with pending rows, and the per-topic age of the
-- oldest row. Partial, so it stays as small as idx_outbox_pending.
--
-- Built CONCURRENTLY, outside a transaction: a plain CREATE INDEX holds a lock
-- that blocks every INSERT into outbox_messages — every business write of the
-- service — for as long as the build takes on a large table. A concurrent build
-- that was interrupted leaves an INVALID index of this name; IF NOT EXISTS would
-- then skip it for ever, so outbox.Migrate drops an invalid leftover first (under
-- its advisory lock) and this statement rebuilds it.

-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_outbox_pending_topic ON outbox_messages (topic, created_at)
    WHERE status = 'pending';

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_outbox_pending_topic;
