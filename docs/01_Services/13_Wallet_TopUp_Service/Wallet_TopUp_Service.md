# TradeDrift — Wallet Top-Up Service Specification

> **Status:** ✅ Production Hardened (Design Complete)  
> **Document:** 13_Wallet_TopUp_Service.md  
> **Service:** Wallet Top-Up Service (`services/wallet-topup`)  
> **Version:** V1.3.1  
> **Last Updated:** September 2026  
> **Sources:** `08_Admin_API.md`, `07_Wallet_Service.md`, `18_PostgreSQL_Design.md`

---

## 1. Purpose

The **Wallet Top-Up Service** is the dedicated fiat-to-simulated-crypto onboarding orchestrator for the TradeDrift platform. It allows verified users to purchase simulated USDT at a fixed rate of **₹1 = 1,000 USDT** (in whole-rupee increments, ₹1 to ₹10) using real-world micro-payments, enforces a strict daily limit (default ₹10/day), orchestrates payment gateway interactions, ingests webhooks securely, and guarantees idempotent, verified balance deposits to the core ledger.

### Core Objectives:
1. **Ledger & Payment Boundary Separation:** Insulates the core `Wallet Service` (which manages double-entry balance consistency, reservations, and trade settlement) from external gateway latencies, provider outages, and public webhook traffic.
2. **Whole-Rupee Fixed Conversion:** Enforces deterministic conversion of **₹1 INR = 1,000 USDT** in whole-rupee increments (₹1 to ₹10).
3. **Decoupled Webhook Ingestion:** Acknowledges payment provider webhooks immediately upon committing payment confirmation and quota transitions (`CREDIT_PENDING`), eliminating gRPC network latency from webhook HTTP response times.
4. **Tokenized Lease-Based Reconciler:** Background reconcilers claim `CREDIT_PENDING` orders using an explicit lease token (`claim_token` UUID and `claim_until`), preventing stale workers from completing expired leases and avoiding PostgreSQL row locks during external gRPC calls.
5. **Atomic Cross-Midnight Quota Attribution:** Accurately accounts for payments initiated on Day $N$ but captured on Day $N+1$ inside a single atomic database transaction without consuming quota when exhausted.
6. **Strict Invariant Verification:** Avoids silent accounting corrections (`GREATEST()`) in favor of strict `RowsAffected == 1` assertions with anomaly alerting.
7. **Provider-Scoped Identity Boundaries:** Provider order IDs and payment IDs are strictly scoped per provider (`UNIQUE (provider, provider_order_id)`).
8. **Distributed Systems Guarantee:** Delivers **at-least-once delivery with idempotent processing resulting in an exactly-once financial effect** on user balances.

---

## 2. Out of Scope

| Concern | Owning Service |
|---|---|
| User registration, profile & JWT issuance | Authentication Service |
| Balance mutation, ledger reservations & trade settlement | Wallet Service (`WalletService.DepositFunds`) |
| Trade execution & order matching | Matching Engine |
| WebSocket & push notifications | Notification Service / Gateway WS Hub |
| Administrative account suspension & wallet freezing | Admin Service |

---

## 3. System Architecture & Context

```
                         ┌──────────────────────────┐
                         │       User (Client)      │
                         └────────────┬─────────────┘
                                      │
          1. POST /api/v1/topups { "inrAmount": 5 }
             Header: Idempotency-Key: <UUID>
                                      ▼
                         ┌──────────────────────────┐
                         │       API Gateway        │
                         └────────────┬─────────────┘
                                      │ forwards (Bearer JWT)
                                      ▼
┌────────────────────────────────────────────────────────────────────────┐
│                      Wallet Top-Up Service                             │
│                                                                        │
│  ┌──────────────────────────┐         ┌─────────────────────────────┐  │
│  │   Daily Quota Engine     │         │     Payment Orchestrator    │  │
│  │  (Atomic UPDATE on DB)   │         │ (Creates order on Provider) │  │
│  └─────────────┬────────────┘         └──────────────┬──────────────┘  │
│                │                                     │                 │
│  ┌─────────────▼─────────────────────────────────────▼──────────────┐  │
│  │                    PostgreSQL (tradedrift_topup)                 │  │
│  │  - topup_orders        (Order lifecycle, claim_token, lease)     │  │
│  │  - daily_topup_limits  (Atomic reserved/consumed counters)       │  │
│  │  - webhook_events      (Immutable webhook audit & deduplication) │  │
│  └─────────────┬────────────────────────────────────────────────────┘  │
│                │                                     ▲                 │
│                │ 4. Reconciler Worker                │ 3. Webhook      │
│                │    Claims lease & gRPC call         │ (HMAC Verify)   │
└────────────────┼─────────────────────────────────────┼─────────────────┘
                 │                                     │
                 │ 2. Create ₹ payment                 │
                 ▼                                     │
   ┌──────────────────────────┐                        │
   │ Payment Gateway Provider │────────────────────────┘
   │ (Razorpay / Cashfree /   │  Webhook response (200 OK) returned
   │  Mock Provider for Dev)  │  immediately after CREDIT_PENDING committed!
   └──────────────────────────┘
                 │
                 │ 5. Asynchronous gRPC DepositFunds() (outside DB transaction)
                 ▼
   ┌──────────────────────────┐
   │      Wallet Service      │
   │ (DepositFunds gRPC Call) │ ──► Credits +5,000 USDT to user wallet
   └──────────────────────────┘     Inserts into `wallet_transactions`
```

---

## 4. Business Rules & Financial Invariants

### 4.1 Initial Seed vs. Top-Up Distinction
* **Initial Registration Seed:** Every newly verified user receives a one-time free **10,000 USDT** seed. This is handled by `WalletService.InitializeWallet` upon registration and is completely independent of the Top-Up Service.
* **Additional Top-Up:** Available at any time regardless of current wallet balance (balance-agnostic).

### 4.2 Economic & Ledger Invariants
- **TI-1 (Whole-Rupee Fixed Conversion):** The conversion rate is strictly **₹1 INR = 1,000 USDT**. Only positive whole-rupee integers are permitted ($\text{inr\_amount} \in \{1, 2, \dots, 10\}$).
- **TI-2 (Explicit Quota Accounting):** Available allowance is defined as:
  $$\text{AvailableQuota} = \text{DailyLimit} - \text{ReservedINR} - \text{ConsumedINR}$$
- **TI-3 (Timezone Date Boundary):** The daily limit quota is keyed by calendar date in **Indian Standard Time (IST, UTC+05:30)** (`DATE(timezone('Asia/Kolkata', NOW()))`). Daily limits reset at **00:00:00 IST**.
- **TI-4 (Attribution by `paid_at`):** Consumed quota is attributed to the business day the payment was **captured (`paid_at`)**, not when the order was initiated (`created_at`).
- **TI-5 (Atomic Cross-Midnight Transition):** Quota commit and order state transition occur inside the **same atomic database transaction**:
  - **Same Day (`reservation_date == paid_date`):** `reserved -= amount; consumed += amount`.
  - **Cross-Midnight (`reservation_date != paid_date`):** Day 1 reservation is released. Day 2 quota is attempted. If Day 2 quota is available, `consumed += amount` on Day 2 and order transitions to `CREDIT_PENDING`. If Day 2 quota is exhausted, consumed quota is **never incremented** on Day 2, and the order transitions directly to `REFUND_REQUIRED`.
- **TI-6 (Strict Invariant Updates):** Quota deductions must explicitly assert `reserved_inr >= $amount`. The service **never** uses `GREATEST(0, ...)` to mask accounting inconsistencies.
- **TI-7 (Payload Validation Invariant):** The webhook payload amount, currency, and provider must strictly match the persisted `topup_orders` values. USDT is always calculated from stored order data.
- **TI-8 (Tokenized Lease Reconciler):** Background workers acquire work with a random `claim_token` UUID. A worker can only complete an order if its token still matches `claim_token`, preventing stale workers from overwriting a reassigned lease.
- **TI-9 (Idempotency Key Strictness):** Same idempotency key with identical parameters returns the existing order. Same idempotency key with modified parameters returns `409 Conflict` (`IDEMPOTENCY_KEY_REUSED`).
- **TI-10 (Exactly-Once Financial Effect):** Balance credit is applied **exactly once in the ledger**, even if the Top-Up Service retries the `WalletService.DepositFunds` call. Idempotency is enforced by the tuple `(reference_id = topup_id, reference_type = 'TOPUP')` in the Wallet Service.

---

## 5. State Machine & Resilient Lifecycle

```
                  ┌──────────────┐
                  │  INITIATED   │  (Idempotency verified, reservation attempted)
                  └──────┬───────┘
                         │
                         ▼
               ┌───────────────────┐
               │  PAYMENT_PENDING  │  (Quota reserved; gateway order created)
               └──────┬───────┬────┘
                      │       │
      Payment Success │       │ 15-Minute Expiry (No payment captured)
      Webhook / Verify│       ▼
                      │ ┌───────────┐
                      │ │  EXPIRED  │  (Atomic check -> release reservation)
                      │ └───────────┘
                      ▼
            ┌───────────────────┐
            │ PAYMENT_CONFIRMED │  (Transient in-transaction intermediate state)
            └─────────┬─────────┘
                      │
         ┌────────────┴────────────┐
         │ (Quota Available)       │ (Cross-Midnight Quota Exhausted)
         ▼                         ▼
┌─────────────────┐       ┌─────────────────┐
│ CREDIT_PENDING  │       │ REFUND_REQUIRED │
└────────┬────────┘       └─────────────────┘
         │
         ▼
┌───────────────────┐
│ CREDIT_PROCESSING │  (Claimed via lease: claim_token + claim_until)
└────────┬──────────┘
         │
         │ gRPC DepositFunds() (outside DB transaction)
         ▼
  ┌─────────────┐
  │  COMPLETED  │  (Balance credited in Wallet Service; matches claim_token)
  └─────────────┘
```

> **Note on `PAYMENT_CONFIRMED`:** This state is a transient in-transaction stage during webhook processing. Within that same database transaction, quota is committed: if capacity exists, the order immediately advances to `CREDIT_PENDING`; if Day 2 quota is exhausted, it transitions directly to `REFUND_REQUIRED`. An order never rests permanently in `PAYMENT_CONFIRMED`.

---

## 6. Daily Quota Architecture & Atomic Transactions

### 6.1 Table: `daily_topup_limits`
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
```

### 6.2 The Complete Atomic Webhook Quota Transaction (Tx 1)
Payment confirmation and quota accounting execute inside a **single database transaction**:

```sql
BEGIN;

-- 1. Lock the order row and verify it is still in PAYMENT_PENDING
SELECT id, user_id, inr_amount, usdt_amount, reservation_date, status
FROM topup_orders
WHERE provider = $provider AND provider_order_id = $provider_order_id
FOR UPDATE;

-- 2. Validate payload: provider matches, currency == 'INR', amount == stored inr_amount

-- 3. Case A: reservation_date == paid_date (Same Day)
IF reservation_date = paid_date THEN
    UPDATE daily_topup_limits
    SET reserved_inr = reserved_inr - $amount,
        consumed_inr = consumed_inr + $amount,
        updated_at   = NOW()
    WHERE user_id    = $user_id
      AND usage_date = $paid_date
      AND reserved_inr >= $amount;
    
    -- Assert RowsAffected == 1
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

    -- b) Ensure Day 2 record exists
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

    -- d) If Day 2 update succeeded (RowsAffected == 1), advance to CREDIT_PENDING
    --    If Day 2 was exhausted (RowsAffected == 0), set REFUND_REQUIRED without incrementing consumed!
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

---

## 7. Security-First Webhook Processing Pipeline

```
               POST /api/v1/webhooks/payment/:provider
                                  │
                                  ▼
                   Step 1: Read Raw Request Body
                                  │
                                  ▼
             Step 2: Provider-Specific Signature Verification
                                  │
                       ┌──────────┴──────────┐
                  Invalid Signature      Valid Signature
                       │                     │
                       ▼                     ▼
             Record Audit Row in     Step 3: Verify Timestamp Replay Window
             `webhook_events`               (|T_now - T_event| <= 300s)
             (signature_valid=FALSE,         │
              status='FAILED')               ├──────────────┐
                       │                Expired / Future  Valid
                       ▼                     │              │
             Return 401 Unauthorized         ▼              ▼
                                     Record Audit Row  Step 4: Idempotent Event Insert
                                     status='FAILED'   `webhook_events`
                                     Return 401        (signature_valid=TRUE)
                                                            │
                                                  ┌─────────┴─────────┐
                                             Duplicate Event      New Event
                                                  │                   │
                                                  ▼                   ▼
                                            Return 200 OK      Step 5: Validate Payload
                                            (ALREADY_PROCESSED) (order_id, amount, INR)
                                                                      │
                                                                      ▼
                                                               Step 6: Atomic Quota Tx
                                                               (PAYMENT_PENDING ->
                                                                CREDIT_PENDING)
                                                                      │
                                                                      ▼
                                                               Step 7: Update webhook_events
                                                               (status='PROCESSED',
                                                                processed_at=NOW())
                                                                      │
                                                                      ▼
                                                               Return 200 OK (Fast Ack!)
```

### 7.1 Webhook Storage (`webhook_events`)
```sql
CREATE TABLE webhook_events (
    id              UUID PRIMARY KEY,
    provider        VARCHAR(50) NOT NULL,           -- 'MOCK', 'RAZORPAY', 'CASHFREE'
    event_id        VARCHAR(100) NOT NULL,          -- Provider event ID
    payment_id      VARCHAR(100),                   -- Gateway payment ID
    signature_valid BOOLEAN NOT NULL,               -- Explicit verification flag
    payload         JSONB NOT NULL,                 -- Raw unmodified payload
    status          VARCHAR(20) NOT NULL DEFAULT 'RECEIVED'
                    CHECK (status IN ('RECEIVED', 'PROCESSED', 'IGNORED', 'FAILED')),
    error_message   TEXT,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_webhook_verified_dedup 
    ON webhook_events(provider, event_id) 
    WHERE signature_valid = TRUE;

CREATE INDEX idx_webhook_received_at 
    ON webhook_events(received_at DESC);

CREATE INDEX idx_webhook_payment_id 
    ON webhook_events(provider, payment_id);
```

---

## 8. Tokenized Lease Reconciler (No Network Locks)

```go
// Step 1: Claim batch inside short transaction using unique worker token
workerToken := uuid.New()
tx, _ := db.Begin(ctx)

rows, _ := tx.Query(ctx, `
    SELECT id, user_id, usdt_amount, payment_id
    FROM topup_orders
    WHERE status = 'CREDIT_PENDING'
       OR (status = 'CREDIT_PROCESSING' AND claim_until < NOW())
    ORDER BY created_at ASC
    LIMIT 50
    FOR UPDATE SKIP LOCKED
`)

tx.Exec(ctx, `
    UPDATE topup_orders 
    SET status        = 'CREDIT_PROCESSING',
        claim_token   = $1,
        claimed_at    = NOW(),
        claim_until   = NOW() + INTERVAL '60 seconds',
        attempt_count = attempt_count + 1,
        updated_at    = NOW() 
    WHERE id = ANY($2)
`, workerToken, claimedIDs)

tx.Commit(ctx)

// Step 2: Perform external gRPC call WITHOUT holding any database locks
for _, order := range claimedOrders {
    resp, err := walletClient.DepositFunds(ctx, &walletv1.DepositFundsRequest{
        UserId:        order.UserID,
        Asset:         "USDT",
        Amount:        order.USDTAmount,
        ReferenceId:   order.ID,
        ReferenceType: "TOPUP",
    })

    if err == nil && resp.Success {
        // Step 3: Complete only if this worker still holds the valid claim_token!
        db.Exec(ctx, `
            UPDATE topup_orders 
            SET status       = 'COMPLETED',
                completed_at = NOW(),
                updated_at   = NOW() 
            WHERE id = $1 
              AND status = 'CREDIT_PROCESSING' 
              AND claim_token = $2
        `, order.ID, workerToken)
    } else {
        errMsg := "unknown error"
        if err != nil {
            errMsg = err.Error()
        }
        db.Exec(ctx, `
            UPDATE topup_orders 
            SET last_error  = $2,
                claim_until = NOW(),
                updated_at  = NOW()
            WHERE id = $1 AND claim_token = $3
        `, order.ID, errMsg, workerToken)
    }
}
```

---

## 9. Client Idempotency & Downstream Ledger Integration

### 9.1 Client Idempotency Key (`POST /api/v1/topups`)
Clients supply an `Idempotency-Key` header (UUID string).
- **Same key + same `inrAmount`:** Returns the existing top-up order.
- **Same key + different `inrAmount`:** Returns `409 Conflict` (`IDEMPOTENCY_KEY_REUSED`).

### 9.2 Wallet Service gRPC Contract
```protobuf
message DepositFundsRequest {
    string user_id        = 1; // Target User UUID
    string asset          = 2; // "USDT"
    string amount         = 3; // e.g. "5000.0000000000"
    string reference_id   = 4; // Top-up Order UUID (ensures idempotency)
    string reference_type = 5; // "TOPUP"
}

message DepositFundsResponse {
    bool   success        = 1;
    string transaction_id = 2; // Ledger transaction UUID
    string new_balance    = 3; // Available USDT balance after credit
}

service WalletService {
    rpc DepositFunds (DepositFundsRequest) returns (DepositFundsResponse);
}
```

---

## 10. Internal Package Structure

```
services/wallet-topup/
├── go.mod
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── config/
│   │   └── config.go             # Daily limit (10.00), rate (1000.00), secrets, ports
│   ├── domain/
│   │   ├── topup.go              # TopUpOrder, DailyQuota, Status constants
│   │   └── errors.go             # Canonical domain errors
│   ├── handler/
│   │   ├── http_handler.go       # POST /topups, GET /topups/daily-usage, GET /topups/:id
│   │   ├── webhook_handler.go    # POST /webhooks/payment/:provider
│   │   └── health.go             # /live, /ready, /health
│   ├── service/
│   │   ├── topup_service.go      # Orchestrator for order creation and verification
│   │   ├── quota_service.go      # Daily quota reservation/release/commit
│   │   └── reconciler.go         # Tokenized lease reconciler (CREDIT_PROCESSING)
│   ├── payment/
│   │   ├── provider.go           # PaymentProvider interface: CreatePaymentOrder(...)
│   │   ├── mock_provider.go      # Simulated provider for local development
│   │   └── razorpay_provider.go  # Razorpay API adapter
│   ├── webhook/
│   │   ├── verifier.go           # WebhookVerifier interface
│   │   ├── mock_verifier.go      # Mock HMAC-SHA256 verifier with timestamp replay check
│   │   └── razorpay_verifier.go  # Razorpay webhook signature verifier
│   ├── repository/
│   │   ├── repository.go         # Storage interfaces (TopUp, Quota, WebhookEvent)
│   │   └── postgres/
│   │       ├── topup_repo.go
│   │       ├── quota_repo.go
│   │       └── webhook_repo.go
│   └── client/
│       └── wallet_client.go      # gRPC client for WalletService.DepositFunds
├── migrations/
│   ├── 00001_create_daily_quota_table.sql
│   ├── 00002_create_topup_orders_table.sql
│   └── 00003_create_webhook_events_table.sql
└── Dockerfile
```
