-- +goose Up
-- SQL in this section is executed when the migration is applied.

-- Add claim_token UUID to outbox for publisher worker lease fencing
ALTER TABLE outbox ADD COLUMN IF NOT EXISTS claim_token UUID;

-- +goose Down
-- SQL in this section is executed when the migration is rolled back.
ALTER TABLE outbox DROP COLUMN IF EXISTS claim_token;
