-- +goose Up
-- +goose StatementBegin
ALTER TABLE outbox_messages
    ALTER COLUMN aggregate_type TYPE VARCHAR(128),
    ALTER COLUMN aggregate_id   TYPE VARCHAR(255);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Not safely reversible for rows that already exceed 64 chars.
SELECT 1;
-- +goose StatementEnd
