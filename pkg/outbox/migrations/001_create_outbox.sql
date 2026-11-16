-- The CREATE ... IF NOT EXISTS guards (indexes included) make this migration safe
-- after a service's own migration that creates the same table and indexes
-- (chat-service's 006_create_outbox.sql, auth-service's 00001): either order
-- works, on an empty database and on an existing one. Editing the file does not
-- touch a database that already recorded version 1.

-- +goose Up
CREATE TABLE IF NOT EXISTS outbox_messages (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type  VARCHAR(64)  NOT NULL,
    aggregate_id    VARCHAR(64)  NOT NULL,
    event_type      VARCHAR(128) NOT NULL,
    topic           VARCHAR(128) NOT NULL,
    key             VARCHAR(128) NOT NULL DEFAULT '',
    payload         JSONB        NOT NULL,
    headers         JSONB        NOT NULL DEFAULT '{}',
    status          VARCHAR(16)  NOT NULL DEFAULT 'pending',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    sent_at         TIMESTAMPTZ
);

-- Relay index: fetch pending messages in creation order (partial — only pending rows).
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON outbox_messages (created_at)
    WHERE status = 'pending';

-- Reaper index: find old sent messages for cleanup (partial — only sent rows).
CREATE INDEX IF NOT EXISTS idx_outbox_reaper ON outbox_messages (sent_at)
    WHERE status = 'sent';

-- +goose Down
DROP TABLE IF EXISTS outbox_messages;
