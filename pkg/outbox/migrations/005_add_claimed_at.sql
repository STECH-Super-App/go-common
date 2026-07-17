-- Relay claim-by-update: the relay now claims a batch by flipping rows to
-- status='processing' (with claimed_at set) inside a single UPDATE, instead of
-- the previous autocommit SELECT ... FOR UPDATE SKIP LOCKED whose row locks
-- were released the moment the statement returned (a no-op — two relays could
-- fetch and forward the same rows).
--
-- Allowed values of outbox_messages.status (documented here — the column has
-- no CHECK constraint):
--   'pending'    — written, not yet claimed by a relay
--   'processing' — claimed by a relay; claimed_at records when. Returned to
--                  'pending' by the relay on a failed Kafka write, or by the
--                  Reaper's ReleaseStuck backstop after ClaimTimeout when the
--                  claiming relay crashed mid-batch
--   'sent'       — successfully published to Kafka; reaped after retention

-- +goose Up
ALTER TABLE outbox_messages ADD COLUMN claimed_at TIMESTAMPTZ;

-- ReleaseStuck index: find stale claims from crashed relays (partial — only processing rows).
CREATE INDEX idx_outbox_processing ON outbox_messages (claimed_at)
    WHERE status = 'processing';

-- +goose Down
DROP INDEX IF EXISTS idx_outbox_processing;
ALTER TABLE outbox_messages DROP COLUMN IF EXISTS claimed_at;
