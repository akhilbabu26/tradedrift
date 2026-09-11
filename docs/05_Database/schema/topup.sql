-- TradeDrift Wallet Top-Up Database Schema (Production Hardened)

-- 1. Daily Quota Limits Table
CREATE TABLE daily_topup_limits (
    user_id       UUID NOT NULL,
    usage_date    DATE NOT NULL,                  -- Date in Asia/Kolkata (IST)
    limit_inr     INT NOT NULL DEFAULT 10,        -- Maximum whole-rupee daily limit (default ₹10)
    reserved_inr  INT NOT NULL DEFAULT 0,         -- Amount held by active PAYMENT_PENDING orders
    consumed_inr  INT NOT NULL DEFAULT 0,         -- Amount actually paid & credited
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, usage_date),
    CONSTRAINT check_quota_non_negative CHECK (reserved_inr >= 0 AND consumed_inr >= 0)
);

CREATE INDEX idx_daily_quota_user_date 
    ON daily_topup_limits(user_id, usage_date DESC);

-- 2. Top-Up Orders Table
CREATE TABLE topup_orders (
    id                UUID PRIMARY KEY,               -- UUIDv7 internal order ID
    user_id           UUID NOT NULL,                  -- User identifier from JWT
    idempotency_key   VARCHAR(64) NOT NULL,           -- Client-supplied idempotency key
    inr_amount        INT NOT NULL CHECK (inr_amount >= 1 AND inr_amount <= 10), -- Whole rupees (1 to 10)
    usdt_amount       DECIMAL(30, 10) NOT NULL CHECK (usdt_amount > 0),          -- Calculated USDT (1 INR = 1000 USDT)
    exchange_rate     DECIMAL(10, 2) NOT NULL DEFAULT 1000.00,
    status            VARCHAR(20) NOT NULL DEFAULT 'PAYMENT_PENDING'
                      CHECK (status IN ('INITIATED', 'PAYMENT_PENDING', 'PAYMENT_CONFIRMED', 'CREDIT_PENDING', 'CREDIT_PROCESSING', 'COMPLETED', 'FAILED', 'EXPIRED', 'REFUND_REQUIRED')),
    provider          VARCHAR(50) NOT NULL,           -- 'MOCK', 'RAZORPAY', 'CASHFREE'
    provider_order_id VARCHAR(100),                   -- Order ID assigned by gateway
    payment_id        VARCHAR(100),                   -- Gateway payment / transaction ID
    failure_reason    TEXT,                           -- Detailed reason if status is FAILED or REFUND_REQUIRED
    reservation_date  DATE NOT NULL,                  -- Date when reservation was created (Asia/Kolkata IST)
    expires_at        TIMESTAMPTZ NOT NULL,           -- Reservation expiration (NOW() + 15m)
    claim_token       UUID,                           -- Worker instance token holding current lease
    claimed_at        TIMESTAMPTZ,                    -- Timestamp when worker claimed lease
    claim_until       TIMESTAMPTZ,                    -- Lease expiry timestamp (claimed_at + 60s)
    attempt_count     INT NOT NULL DEFAULT 0,         -- Number of credit attempts (observability)
    last_error        TEXT,                           -- Last gRPC or reconciler error message
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    paid_at           TIMESTAMPTZ,                    -- Timestamp when gateway confirmed capture
    completed_at      TIMESTAMPTZ,                    -- Timestamp when Wallet Service credited USDT
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_topup_user_idempotency UNIQUE (user_id, idempotency_key),
    CONSTRAINT uq_topup_provider_order UNIQUE (provider, provider_order_id),
    CONSTRAINT uq_topup_provider_payment UNIQUE (provider, payment_id)
);

CREATE INDEX idx_topup_user_status_created 
    ON topup_orders(user_id, status, created_at DESC);

CREATE INDEX idx_topup_provider_order 
    ON topup_orders(provider, provider_order_id);

CREATE INDEX idx_topup_reconciler_lease 
    ON topup_orders(created_at ASC) 
    WHERE status IN ('CREDIT_PENDING', 'CREDIT_PROCESSING');

-- 3. Webhook Events Table (Raw Ingestion & Deduplication)
CREATE TABLE webhook_events (
    id              UUID PRIMARY KEY,               -- UUIDv7 internal event ID
    provider        VARCHAR(50) NOT NULL,           -- 'MOCK', 'RAZORPAY', 'CASHFREE'
    event_id        VARCHAR(100) NOT NULL,          -- Provider's unique event identifier
    payment_id      VARCHAR(100),                   -- Gateway payment identifier
    signature_valid BOOLEAN NOT NULL,               -- Explicit verification flag
    payload         JSONB NOT NULL,                 -- Raw unmodified JSON body from provider
    status          VARCHAR(20) NOT NULL DEFAULT 'RECEIVED'
                    CHECK (status IN ('RECEIVED', 'PROCESSED', 'IGNORED', 'FAILED')),
    error_message   TEXT,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMPTZ
);

-- Deduplication index only applies to cryptographically verified events (prevents cache poisoning)
CREATE UNIQUE INDEX idx_webhook_verified_dedup 
    ON webhook_events(provider, event_id) 
    WHERE signature_valid = TRUE;

CREATE INDEX idx_webhook_received_at 
    ON webhook_events(received_at DESC);

CREATE INDEX idx_webhook_payment_id 
    ON webhook_events(provider, payment_id);
