-- +goose Up
-- Table: admin_operations
-- Records every administrative mutation with caller-scoped idempotency.
-- State machine: PENDING → PROCESSING → COMPLETED │ FAILED (non-retryable only)

CREATE TABLE IF NOT EXISTS admin_operations (
    id              UUID         PRIMARY KEY,              -- operation_id (UUIDv7)
    admin_id        UUID         NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    request_id      VARCHAR(128) NOT NULL,
    operation_type  VARCHAR(50)  NOT NULL
                    CHECK (operation_type IN (
                        'SUSPEND_USER',
                        'UNSUSPEND_USER',
                        'FREEZE_WALLET',
                        'UNFREEZE_WALLET',
                        'HALT_MARKET',
                        'RESUME_MARKET'
                    )),
    target_id       VARCHAR(128) NOT NULL,
    reason          TEXT         NOT NULL,
    status          VARCHAR(20)  NOT NULL DEFAULT 'PENDING'
                    CHECK (status IN ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED')),
    response_body   JSONB,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_admin_operation_idempotency UNIQUE (admin_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_admin_ops_admin_id
    ON admin_operations(admin_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS admin_operations;
