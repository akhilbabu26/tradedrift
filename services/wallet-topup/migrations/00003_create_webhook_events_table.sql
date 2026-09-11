-- +goose Up
-- Table: webhook_events
-- Webhook event audit and processing log (raw payload immutable, status tracks processing lifecycle)

CREATE TABLE IF NOT EXISTS webhook_events (
    id              UUID PRIMARY KEY,
    provider        VARCHAR(50) NOT NULL,
    event_id        VARCHAR(100) NOT NULL,
    payment_id      VARCHAR(100),
    signature_valid BOOLEAN NOT NULL,
    payload         JSONB NOT NULL,
    status          VARCHAR(20) NOT NULL DEFAULT 'RECEIVED'
                    CHECK (status IN ('RECEIVED', 'PROCESSED', 'IGNORED', 'FAILED')),
    error_message   TEXT,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMPTZ
);

-- Deduplication index only applies to cryptographically verified events (prevents cache poisoning)
CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_verified_dedup 
    ON webhook_events(provider, event_id) 
    WHERE signature_valid = TRUE;

CREATE INDEX IF NOT EXISTS idx_webhook_received_at 
    ON webhook_events(received_at DESC);

CREATE INDEX IF NOT EXISTS idx_webhook_payment_id 
    ON webhook_events(provider, payment_id);

-- +goose Down
DROP TABLE IF EXISTS webhook_events;

