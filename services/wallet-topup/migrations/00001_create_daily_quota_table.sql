-- +goose Up

-- 1. Daily Topup Limits — per-user per-day quota enforcement
-- reserved_inr + consumed_inr must not exceed limit_inr (enforced at DB layer)
CREATE TABLE IF NOT EXISTS daily_topup_limits (
    user_id      UUID NOT NULL,
    usage_date   DATE NOT NULL,
    limit_inr    BIGINT NOT NULL DEFAULT 10,
    reserved_inr BIGINT NOT NULL DEFAULT 0,
    consumed_inr BIGINT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT pk_daily_topup_limits PRIMARY KEY (user_id, usage_date),
    CONSTRAINT chk_limit_positive   CHECK (limit_inr > 0),
    CONSTRAINT chk_reserved_nonneg  CHECK (reserved_inr >= 0),
    CONSTRAINT chk_consumed_nonneg  CHECK (consumed_inr >= 0),
    CONSTRAINT chk_quota_ceiling    CHECK (reserved_inr + consumed_inr <= limit_inr)
);

-- 2. Topup Orders — full payment lifecycle state machine
-- status covers every state from initiation through payment, credit, completion, failure and expiry
-- claim_token / claim_until / claimed_at implement distributed reconciler lease fencing
-- attempt_count + last_error enable retry auditing and circuit breaker logic
CREATE TABLE IF NOT EXISTS topup_orders (
    id                UUID PRIMARY KEY,
    user_id           UUID NOT NULL,
    idempotency_key   VARCHAR(100) NOT NULL,
    inr_amount        BIGINT NOT NULL,
    usdt_amount       DECIMAL(30,10) NOT NULL,
    reservation_date  DATE NOT NULL,
    provider          VARCHAR(50) NOT NULL,
    provider_order_id VARCHAR(100),
    payment_id        VARCHAR(100),
    status            VARCHAR(30) NOT NULL
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
    claim_token       UUID,
    claim_until       TIMESTAMPTZ,
    claimed_at        TIMESTAMPTZ,
    attempt_count     INT NOT NULL DEFAULT 0,
    last_error        TEXT,
    expires_at        TIMESTAMPTZ NOT NULL,
    completed_at      TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_topup_inr_amount  CHECK (inr_amount BETWEEN 1 AND 10),
    CONSTRAINT chk_topup_usdt_amount CHECK (usdt_amount = inr_amount * 1000),
    CONSTRAINT uq_topup_provider_order   UNIQUE (provider, provider_order_id),
    CONSTRAINT uq_topup_provider_payment UNIQUE (provider, payment_id)
);

-- Unique index: prevents duplicate topup submissions per user
CREATE UNIQUE INDEX IF NOT EXISTS idx_topup_user_idempotency ON topup_orders(user_id, idempotency_key);

-- Reconciler lease index: fast lookup for orders needing credit processing
CREATE INDEX IF NOT EXISTS idx_topup_reconciler_lease ON topup_orders(status, claim_until, created_at ASC)
    WHERE status IN ('CREDIT_PENDING', 'CREDIT_PROCESSING');

-- Expiry scanner index: covers both INITIATED and PAYMENT_PENDING (updated from 00004)
CREATE INDEX IF NOT EXISTS idx_topup_expiry ON topup_orders(status, expires_at)
    WHERE status IN ('PAYMENT_PENDING', 'INITIATED');

-- 3. Webhook Events — raw payment provider event audit log
-- signature_valid must be TRUE for deduplication uniqueness to apply (prevents poisoning)
-- status tracks processing outcome for each received event
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

-- Deduplication: only applies to cryptographically verified events
CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_verified_dedup ON webhook_events(provider, event_id)
    WHERE signature_valid = TRUE;

CREATE INDEX IF NOT EXISTS idx_webhook_received_at ON webhook_events(received_at DESC);
CREATE INDEX IF NOT EXISTS idx_webhook_payment_id  ON webhook_events(provider, payment_id);

-- +goose Down
DROP TABLE IF EXISTS webhook_events;
DROP TABLE IF EXISTS topup_orders;
DROP TABLE IF EXISTS daily_topup_limits;
