-- +goose Up

CREATE TABLE IF NOT EXISTS notifications (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL,
    title          VARCHAR(255) NOT NULL,
    message        TEXT NOT NULL,
    type           VARCHAR(30) NOT NULL, -- 'INFO', 'TRADE_FILL', 'SYSTEM', 'ACCOUNT'
    reference_id   UUID,
    reference_type VARCHAR(30),          -- 'TRADE', 'ORDER', 'DEPOSIT'
    is_read        BOOLEAN NOT NULL DEFAULT FALSE,
    read_at        TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for fast user inbox retrieval sorted by newest first, with id as deterministic tiebreaker
CREATE INDEX IF NOT EXISTS idx_notifications_user_inbox 
    ON notifications(user_id, created_at DESC, id DESC);

-- Deduplication log for incoming Kafka domain events
CREATE TABLE IF NOT EXISTS processed_events (
    event_id      UUID PRIMARY KEY,     -- Source domain event ID
    user_id       UUID,                 -- Optional user reference (nullable for multi-user events)
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down

DROP TABLE IF EXISTS processed_events;
DROP INDEX IF EXISTS idx_notifications_user_inbox;
DROP TABLE IF EXISTS notifications;
