# TradeDrift — Wallet Top-Up Database Design

> **Status:** ✅ Designed — Production Hardened  
> **Document:** 12_Wallet_TopUp_Database.md  
> **Directory:** docs/05_Database/  
> **Database:** `tradedrift_topup`  
> **Last Updated:** September 2026  

---

## 1. Purpose

The `tradedrift_topup` database stores all user fiat top-up orders, atomic daily quota reservations, payment gateway state mappings, and immutable webhook receipt logs. It guarantees strict idempotency when handling external payment webhooks and prevents concurrent daily-limit overspending at the database layer.

---

## 2. Table Schemas

### 2.1 Table: `daily_topup_limits`
Maintains atomic daily reservation and consumption counters per user per calendar day (IST). Replaces expensive aggregation scans with single-row atomic `UPDATE` operations.

```sql
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
```

---

### 2.2 Table: `topup_orders`
Tracks the lifecycle of individual top-up orders from client initiation through gateway payment capture, tokenized lease claiming, and core ledger deposit.

```sql
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
```

---

### 2.3 Table: `webhook_events`
Stores raw webhook payloads received from external payment providers to ensure an immutable audit trail and prevent duplicate processing across rebalances and restarts.

```sql
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
```

---

## 3. Query Design & Atomic Concurrency Patterns

### 3.1 Atomic Daily Limit Reservation
When handling `POST /api/v1/topups`, quota is reserved atomically without manual application-level locks:

```sql
-- Step 1: Ensure date record exists
INSERT INTO daily_topup_limits (user_id, usage_date, limit_inr, reserved_inr, consumed_inr)
VALUES ($user_id, $today_ist, 10, 0, 0)
ON CONFLICT (user_id, usage_date) DO NOTHING;

-- Step 2: Atomic update checking remaining capacity
UPDATE daily_topup_limits
SET reserved_inr = reserved_inr + $amount,
    updated_at   = NOW()
WHERE user_id    = $user_id
  AND usage_date = $today_ist
  AND (reserved_inr + consumed_inr + $amount) <= limit_inr;
```
* If `RowsAffected() == 0`: Daily limit exceeded. Reject immediately.

### 3.2 State-Bound Expiration & Quota Release (Strict Invariant Check)
To ensure a reservation cannot be released twice, and to catch accounting inconsistencies without using `GREATEST()`:

```sql
-- Step 1: Transition order to EXPIRED only if currently PAYMENT_PENDING
UPDATE topup_orders
SET status     = 'EXPIRED',
    updated_at = NOW()
WHERE id       = $id 
  AND status   = 'PAYMENT_PENDING';

-- Step 2: Only release quota if the order status transition succeeded (RowsAffected == 1)
UPDATE daily_topup_limits
SET reserved_inr = reserved_inr - $amount,
    updated_at   = NOW()
WHERE user_id    = $user_id
  AND usage_date = $reservation_date
  AND reserved_inr >= $amount;
```
* If Step 2 returns `RowsAffected() == 0`, an invariant violation alert is raised.

### 3.3 Atomic Webhook Quota & State Transaction (Tx 1)
When payment capture is confirmed, all mutations execute in a **single atomic database transaction**:

```sql
BEGIN;

-- 1. Lock the order row and verify it is still in PAYMENT_PENDING
SELECT id, user_id, inr_amount, usdt_amount, reservation_date, status
FROM topup_orders
WHERE provider = $provider AND provider_order_id = $provider_order_id
FOR UPDATE;

-- 2. Validate payload: provider, currency == 'INR', amount == stored inr_amount

-- 3. Case A: reservation_date == paid_date (Same Day)
IF reservation_date = paid_date THEN
    UPDATE daily_topup_limits
    SET reserved_inr = reserved_inr - $amount,
        consumed_inr = consumed_inr + $amount,
        updated_at   = NOW()
    WHERE user_id    = $user_id
      AND usage_date = $paid_date
      AND reserved_inr >= $amount;
    
    -- Transition directly to CREDIT_PENDING
    UPDATE topup_orders
    SET status     = 'CREDIT_PENDING',
        paid_at    = $paid_at,
        payment_id = $payment_id,
        updated_at = NOW()
    WHERE id       = $id;

-- Case B: reservation_date != paid_date (Cross-Midnight)
ELSE
    -- a) Release Day 1 reservation
    UPDATE daily_topup_limits
    SET reserved_inr = reserved_inr - $amount,
        updated_at   = NOW()
    WHERE user_id    = $user_id
      AND usage_date = $reservation_date
      AND reserved_inr >= $amount;

    -- b) Ensure Day 2 row exists
    INSERT INTO daily_topup_limits (user_id, usage_date, limit_inr, reserved_inr, consumed_inr)
    VALUES ($user_id, $paid_date, 10, 0, 0)
    ON CONFLICT (user_id, usage_date) DO NOTHING;

    -- c) Attempt to consume Day 2 quota atomically
    UPDATE daily_topup_limits
    SET consumed_inr = consumed_inr + $amount,
        updated_at   = NOW()
    WHERE user_id    = $user_id
      AND usage_date = $paid_date
      AND (reserved_inr + consumed_inr + $amount) <= limit_inr;

    -- d) Strict conditional branching:
    -- If Day 2 update succeeded (RowsAffected == 1): advance to CREDIT_PENDING
    -- If Day 2 quota was exhausted (RowsAffected == 0): set REFUND_REQUIRED without consuming quota!
    IF rows_affected = 1 THEN
        UPDATE topup_orders
        SET status     = 'CREDIT_PENDING',
            paid_at    = $paid_at,
            payment_id = $payment_id,
            updated_at = NOW()
        WHERE id       = $id;
    ELSE
        UPDATE topup_orders
        SET status         = 'REFUND_REQUIRED',
            paid_at        = $paid_at,
            payment_id     = $payment_id,
            failure_reason = 'Day 2 quota exhausted during cross-midnight settlement',
            updated_at     = NOW()
        WHERE id           = $id;
    END IF;
END IF;

COMMIT;
```

### 3.4 Tokenized Lease Reconciler Pattern (No Row Locks During Network Calls)

```sql
-- Step 1: Claim batch inside short transaction
BEGIN;

SELECT id, user_id, usdt_amount, payment_id
FROM topup_orders
WHERE status = 'CREDIT_PENDING'
   OR (status = 'CREDIT_PROCESSING' AND claim_until < NOW())
ORDER BY created_at ASC
LIMIT 50
FOR UPDATE SKIP LOCKED;

UPDATE topup_orders
SET status        = 'CREDIT_PROCESSING',
    claim_token   = $worker_token,
    claimed_at    = NOW(),
    claim_until   = NOW() + INTERVAL '60 seconds',
    attempt_count = attempt_count + 1,
    updated_at    = NOW()
WHERE id = ANY($claimed_ids);

COMMIT;

-- Step 2: gRPC WalletService.DepositFunds() executed with zero database locks held!

-- Step 3: On gRPC success, complete only if this worker still holds the valid claim_token!
UPDATE topup_orders
SET status       = 'COMPLETED',
    completed_at = NOW(),
    updated_at   = NOW()
WHERE id          = $id
  AND status      = 'CREDIT_PROCESSING'
  AND claim_token = $worker_token;
```

---

### 3.5 Webhook Audit & Processing State Transitions

To ensure a tamper-proof and debuggable webhook audit trail, `webhook_events` is updated upon processing completion or validation failure:

```sql
-- When webhook is successfully processed and credit order queued:
UPDATE webhook_events
SET status       = 'PROCESSED',
    processed_at = NOW()
WHERE id = $webhook_event_id;

-- When webhook fails payload, currency, or order validation:
UPDATE webhook_events
SET status        = 'FAILED',
    error_message = $reason,
    processed_at  = NOW()
WHERE id = $webhook_event_id;
```

