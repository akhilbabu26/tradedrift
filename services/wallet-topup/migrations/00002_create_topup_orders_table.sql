-- +goose Up
-- Table: topup_orders
-- State machine, provider-scoped uniqueness, and tokenized reconciler lease fields

CREATE TABLE IF NOT EXISTS topup_orders (
    id                 UUID PRIMARY KEY,
    user_id            UUID NOT NULL,
    idempotency_key    VARCHAR(100) NOT NULL,
    inr_amount         BIGINT NOT NULL,
    usdt_amount        DECIMAL(30, 10) NOT NULL,
    reservation_date   DATE NOT NULL,
    provider           VARCHAR(50) NOT NULL,
    provider_order_id  VARCHAR(100),
    payment_id         VARCHAR(100),
    status             VARCHAR(30) NOT NULL
                       CHECK (status IN (
                           'INITIATED',
                           'PAYMENT_PENDING',
                           'CREDIT_PENDING',
                           'CREDIT_PROCESSING',
                           'COMPLETED',
                           'FAILED',
                           'EXPIRED',
                           'REFUND_REQUIRED'
                       )),
    claim_token        UUID,
    claim_until        TIMESTAMPTZ,
    claimed_at         TIMESTAMPTZ,
    attempt_count      INT NOT NULL DEFAULT 0,
    last_error         TEXT,
    expires_at         TIMESTAMPTZ NOT NULL,
    completed_at       TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_topup_inr_amount CHECK (inr_amount BETWEEN 1 AND 10),
    CONSTRAINT chk_topup_usdt_amount CHECK (usdt_amount = inr_amount * 1000),
    CONSTRAINT uq_topup_provider_order UNIQUE (provider, provider_order_id),
    CONSTRAINT uq_topup_provider_payment UNIQUE (provider, payment_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_topup_user_idempotency
    ON topup_orders(user_id, idempotency_key);

CREATE INDEX IF NOT EXISTS idx_topup_reconciler_lease
    ON topup_orders(status, claim_until, created_at ASC)
    WHERE status IN ('CREDIT_PENDING', 'CREDIT_PROCESSING');

CREATE INDEX IF NOT EXISTS idx_topup_expiry
    ON topup_orders(status, expires_at)
    WHERE status = 'PAYMENT_PENDING';

-- +goose Down
DROP TABLE IF EXISTS topup_orders;
