-- +goose Up

CREATE TABLE IF NOT EXISTS notification_outbox (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type     VARCHAR(50) NOT NULL,
    payload        JSONB NOT NULL,
    target_channel VARCHAR(100) NOT NULL, -- Redis target channel (e.g. user:notifications:{user_id})
    status         VARCHAR(20) NOT NULL DEFAULT 'PENDING', -- 'PENDING', 'PROCESSING', 'PROCESSED', 'FAILED'
    retry_count    INT NOT NULL DEFAULT 0,
    last_error     TEXT,
    claimed_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at   TIMESTAMPTZ
);

-- Partial index for active outbox polling and lease recovery
CREATE INDEX IF NOT EXISTS idx_notification_outbox_pending 
    ON notification_outbox(created_at) 
    WHERE status IN ('PENDING', 'PROCESSING');

-- +goose Down

DROP INDEX IF EXISTS idx_notification_outbox_pending;
DROP TABLE IF EXISTS notification_outbox;
