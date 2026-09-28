-- +goose Up

-- 1. Notifications — user notification inbox
-- type must be one of the known notification categories (enforced at DB layer)
-- reference_id/reference_type link back to the source domain object (trade, order, deposit)
CREATE TABLE IF NOT EXISTS notifications (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL,
    title          VARCHAR(255) NOT NULL,
    message        TEXT NOT NULL,
    type           VARCHAR(30) NOT NULL
                   CONSTRAINT chk_notification_type CHECK (type IN ('INFO', 'TRADE_FILL', 'SYSTEM', 'ACCOUNT')),
    reference_id   UUID,
    reference_type VARCHAR(30)
                   CONSTRAINT chk_notification_reference_type CHECK (reference_type IS NULL OR reference_type IN ('TRADE', 'ORDER', 'DEPOSIT')),
    is_read        BOOLEAN NOT NULL DEFAULT FALSE,
    read_at        TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Fast user inbox retrieval: newest first, with id as deterministic tiebreaker
CREATE INDEX IF NOT EXISTS idx_notifications_user_inbox ON notifications(user_id, created_at DESC, id DESC);

-- 2. Processed Events — Kafka event deduplication log
-- notification_id enables idempotent gRPC CreateNotification recovery:
-- if the worker crashes after inserting the notification but before marking the event processed,
-- on retry it can recover the already-created notification_id rather than creating a duplicate
CREATE TABLE IF NOT EXISTS processed_events (
    event_id        UUID PRIMARY KEY,
    user_id         UUID,
    notification_id UUID,
    processed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 3. Notification Outbox — reliable Redis pub/sub delivery via outbox pattern
-- claim_token: per-claim UUID for lease fencing (prevents double-publish on worker crash)
-- status lifecycle: PENDING → PROCESSING → PROCESSED | FAILED
CREATE TABLE IF NOT EXISTS notification_outbox (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type     VARCHAR(50) NOT NULL,
    payload        JSONB NOT NULL,
    target_channel VARCHAR(100) NOT NULL,
    status         VARCHAR(20) NOT NULL DEFAULT 'PENDING'
                   CONSTRAINT chk_outbox_status CHECK (status IN ('PENDING', 'PROCESSING', 'PROCESSED', 'FAILED')),
    retry_count    INT NOT NULL DEFAULT 0,
    last_error     TEXT,
    claim_token    UUID,
    claimed_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at   TIMESTAMPTZ
);

-- Active polling index: only covers PENDING/PROCESSING rows (stays tiny)
CREATE INDEX IF NOT EXISTS idx_notification_outbox_pending ON notification_outbox(created_at)
    WHERE status IN ('PENDING', 'PROCESSING');

-- Retention cleanup index: fast scan for processed rows eligible for pruning
CREATE INDEX IF NOT EXISTS idx_notification_outbox_processed ON notification_outbox(published_at, target_channel)
    WHERE status = 'PROCESSED';

-- +goose Down
DROP INDEX IF EXISTS idx_notification_outbox_processed;
DROP INDEX IF EXISTS idx_notification_outbox_pending;
DROP TABLE IF EXISTS notification_outbox;
DROP TABLE IF EXISTS processed_events;
DROP INDEX IF EXISTS idx_notifications_user_inbox;
DROP TABLE IF EXISTS notifications;
