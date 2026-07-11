-- TradeDrift Notification Database Schema

CREATE TABLE notifications (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL,
    title       VARCHAR(255) NOT NULL,
    message     TEXT NOT NULL,
    type        VARCHAR(30) NOT NULL CHECK (type IN ('TRADE_FILL', 'DEPOSIT_CONFIRMED', 'SYSTEM')),
    is_read     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    read_at     TIMESTAMPTZ
);

CREATE TABLE processed_events (
    event_id      UUID PRIMARY KEY,
    user_id       UUID NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for fetching user notifications
CREATE INDEX idx_notifications_user_created ON notifications (user_id, created_at DESC);
