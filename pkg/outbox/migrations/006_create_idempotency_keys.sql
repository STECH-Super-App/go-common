-- REST idempotency of pkg/idempotency (audit-fix spec §3.2): one row per
-- (caller, endpoint, key), written in the SAME transaction as the work it
-- protects, so there is no "in progress" state: a request that has not
-- committed has left no row.
--
-- A concurrent second request with the same key blocks on the primary key until
-- the first one commits (it then replays the first result) or rolls back (it
-- then runs itself).
--
-- The row names the record the request created (resource_type + resource_id),
-- never the response text: a replay loads that record and passes the normal
-- permission check.

-- +goose Up
CREATE TABLE IF NOT EXISTS idempotency_keys (
    caller        VARCHAR(128) NOT NULL,
    endpoint      VARCHAR(256) NOT NULL,
    key           VARCHAR(128) NOT NULL,
    request_hash  BYTEA        NOT NULL,
    resource_type VARCHAR(64),
    resource_id   VARCHAR(128),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (caller, endpoint, key),
    -- Both set once the work is done, both empty before. Inside the transaction
    -- only; a committed row always has both.
    CONSTRAINT idempotency_keys_resource_both_or_none
        CHECK ((resource_type IS NULL) = (resource_id IS NULL))
);

-- Reaper index: delete keys older than the retention (24 h).
CREATE INDEX IF NOT EXISTS idx_idempotency_keys_reaper ON idempotency_keys (created_at);

-- +goose Down
DROP TABLE IF EXISTS idempotency_keys;
