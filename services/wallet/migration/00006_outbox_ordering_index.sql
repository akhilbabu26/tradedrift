-- +goose Up
-- SQL in this section is executed when the migration is applied.

-- Update partial claiming index to include 'id' for deterministic FIFO ordering:
DROP INDEX IF EXISTS idx_outbox_claiming;
CREATE INDEX idx_outbox_claiming
    ON outbox(created_at ASC, id ASC)
    WHERE status IN ('PENDING', 'PROCESSING');

-- +goose Down
-- SQL in this section is executed when the migration is rolled back.
DROP INDEX IF EXISTS idx_outbox_claiming;
CREATE INDEX idx_outbox_claiming
    ON outbox(created_at)
    WHERE status IN ('PENDING', 'PROCESSING');
