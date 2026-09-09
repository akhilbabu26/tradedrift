-- +goose Up

-- 1. Add claim_token column for per-claim ownership tracking.
-- Stamped by FetchPendingOutbox and verified by MarkOutboxPublished, ReleaseOutboxClaims,
-- and IncrementOutboxRetry to prevent race conditions on lease expiry.
ALTER TABLE notification_outbox
    ADD COLUMN IF NOT EXISTS claim_token UUID;

-- 2. Add notification_id to processed_events so that gRPC CreateNotification calls
-- can be made idempotent by storing and recovering the created notification on retry.
ALTER TABLE processed_events
    ADD COLUMN IF NOT EXISTS notification_id UUID;

-- 3. Closed-enum CHECK constraints to enforce data integrity at the database layer.
ALTER TABLE notification_outbox
    ADD CONSTRAINT chk_outbox_status
    CHECK (status IN ('PENDING', 'PROCESSING', 'PROCESSED', 'FAILED'));

ALTER TABLE notifications
    ADD CONSTRAINT chk_notification_type
    CHECK (type IN ('INFO', 'TRADE_FILL', 'SYSTEM', 'ACCOUNT'));

ALTER TABLE notifications
    ADD CONSTRAINT chk_notification_reference_type
    CHECK (reference_type IS NULL OR reference_type IN ('TRADE', 'ORDER', 'DEPOSIT'));

-- +goose Down

ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notification_reference_type;
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notification_type;
ALTER TABLE notification_outbox DROP CONSTRAINT IF EXISTS chk_outbox_status;
ALTER TABLE processed_events DROP COLUMN IF EXISTS notification_id;
ALTER TABLE notification_outbox DROP COLUMN IF EXISTS claim_token;
