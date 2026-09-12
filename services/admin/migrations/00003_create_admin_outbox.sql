-- +goose Up
-- Table: admin_outbox
-- Transactional outbox for at-least-once Kafka event publishing.
-- Equipped with durable leases and backoff retry tracking.

CREATE TABLE IF NOT EXISTS admin_outbox (
    id              UUID         PRIMARY KEY,              -- event_id (UUIDv7)
    operation_id    UUID         NOT NULL REFERENCES admin_operations(id),
    topic           VARCHAR(128) NOT NULL,
    payload         JSONB        NOT NULL,
    published       BOOLEAN      NOT NULL DEFAULT FALSE,
    published_at    TIMESTAMPTZ,
    status          VARCHAR(20)  NOT NULL DEFAULT 'PENDING'
                    CHECK (status IN ('PENDING', 'PROCESSING', 'PUBLISHED', 'FAILED')),
    attempt_count   INT          NOT NULL DEFAULT 0,
    max_attempts    INT          NOT NULL DEFAULT 10,
    next_attempt_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_error      TEXT,
    locked_at       TIMESTAMPTZ,
    locked_by       UUID,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Partial index: outbox publisher polls only actionable, unpublished events due for delivery.
CREATE INDEX IF NOT EXISTS idx_admin_outbox_due
    ON admin_outbox(next_attempt_at ASC)
    WHERE published = FALSE;

-- +goose Down
DROP TABLE IF EXISTS admin_outbox;
