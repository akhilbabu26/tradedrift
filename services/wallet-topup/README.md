# Wallet Top-Up Microservice (`wallet-topup`)

The **Wallet Top-Up Service** handles fiat-to-simulated-USDT deposits in the TradeDrift platform. It orchestrates per-user daily deposit limits, payment gateway interactions (Razorpay/Mock), cryptographic webhook verification, and distributed asynchronous balance crediting with zero double-spend or quota-leak risks.

---

## 1. System Architecture & Directory Layout

The service adheres strictly to **Clean Architecture** and **Domain-Driven Design (DDD)**:

```
services/wallet-topup/
├── cmd/
│   └── server/
│       ├── main.go                       # Application entrypoint & graceful shutdown
│       └── README.md                     # Server bootstrap & lifecycle documentation
├── migrations/
│   ├── 00001_create_daily_quota_table.sql # Daily quota tracking & check constraints
│   ├── 00002_create_topup_orders_table.sql# Top-up order ledger & state machine
│   ├── 00003_create_webhook_events_table.sql # Webhook audit trail & replay deduplication
│   ├── 00004_update_topup_expiry_index.sql # Fast partial index for expired orders
│   └── README.md                         # Database schema & table design documentation
├── internal/
│   ├── client/
│   │   ├── wallet_client.go              # Downstream Wallet gRPC client
│   │   └── README.md                     # gRPC communication & idempotency documentation
│   ├── config/
│   │   ├── config.go                     # 12-factor environment configuration loader
│   │   └── README.md                     # Configuration options & defaults
│   ├── domain/
│   │   ├── errors.go                     # Typed business sentinel errors
│   │   ├── models.go                     # Pure domain entities, states & calculations
│   │   └── README.md                     # Domain models & state machine documentation
│   ├── handler/
│   │   ├── dto.go                        # camelCase request/response DTOs
│   │   ├── middleware.go                 # X-User-ID auth & latency logging
│   │   ├── router.go                     # Go 1.22+ standard library HTTP router
│   │   ├── topup_handler.go              # Endpoints: POST /topups, GET /daily-usage
│   │   ├── webhook_handler.go            # Endpoint: POST /webhooks/payment/{provider}
│   │   └── README.md                     # HTTP transport & status mapping documentation
│   ├── payment/
│   │   ├── provider.go                   # PaymentProvider interface
│   │   ├── README.md                     # Gateway abstraction & timing attack defense
│   │   └── mock/
│   │       └── mock_provider.go          # Deterministic HMAC mock provider
│   ├── repository/
│   │   ├── interfaces.go                 # Persistence contracts & DBTX abstraction
│   │   ├── README.md                     # Repository layer & transaction documentation
│   │   └── postgres/
│   │       ├── daily_limit_repo.go       # Quota reservations & resets
│   │       ├── topup_order_repo.go       # Order queries & batch worker claim leases
│   │       ├── tx_manager.go             # Unified multi-table atomic transactions
│   │       └── webhook_event_repo.go     # Webhook audit log & replay deduplication
│   ├── service/
│   │   ├── expiry_worker.go              # Sweeper worker recovering abandoned quotas
│   │   ├── reconciler.go                 # Distributed worker crediting Core Wallet
│   │   ├── topup_service.go              # Order creation & quota orchestration
│   │   ├── webhook_service.go            # Webhook processing & atomic confirmation
│   │   └── README.md                     # Service orchestrators & worker documentation
│   └── webhook/
│       ├── verifier.go                   # HMAC-SHA256 signature & replay window verifier
│       └── README.md                     # Webhook security perimeter documentation
├── go.mod
├── go.sum
└── README.md                             # Master documentation (this file)
```

---

## 2. Core Financial & Architectural Invariants

### 1. Zero Over-Deposit Race Conditions (TOCTOU Defense)
- **Problem**: Concurrent requests could pass quota checks simultaneously and deposit beyond the daily ceiling.
- **Solution**: Enforced at the PostgreSQL engine level in `daily_topup_limits`:
  ```sql
  CONSTRAINT chk_quota_ceiling CHECK (reserved_inr + consumed_inr <= limit_inr)
  ```
  Quota is reserved atomically in `INITIATED` status before any call to external payment gateways.

### 2. Client Request Idempotency
- **Problem**: Double-clicking or network retries creating duplicate orders and double-charging the user.
- **Solution**: Unique index on `(user_id, idempotency_key)`.
  - Same key + same amount $\to$ returns existing order immediately without re-calling the gateway.
  - Same key + different amount $\to$ rejects with `HTTP 409 Conflict`.

### 3. Distributed Reconciler Worker Fencing
- **Problem**: Parallel workers in multiple Kubernetes pods causing duplicate wallet credits or lock contention.
- **Solution**:
  - Workers claim batches with `SELECT ... FOR UPDATE SKIP LOCKED`.
  - Claims are tokenized: `claim_token UUID` and `claim_until TIMESTAMPTZ`.
  - Ledger completion is fenced: `UPDATE topup_orders SET status = 'COMPLETED' WHERE id = $1 AND claim_token = $2`. Stale workers whose lease expired are blocked.

### 4. Cryptographic Webhook Security & Replay Immunity
- **Problem**: Man-in-the-middle tampering, spoofed webhooks, and replay attacks.
- **Solution**:
  - Constant-time HMAC-SHA256 signature verification (`crypto/hmac.Equal`).
  - Replay window: $|T_{now} - T_{webhook}| \le 300\text{s}$.
  - Header vs Payload timestamp skew check: $|T_{header} - T_{payload}| \le 5\text{s}$.
  - Verified deduplication index: `UNIQUE (provider, event_id) WHERE signature_valid = TRUE`.

### 5. Cross-Midnight Payment Capture Attribution
- **Problem**: User reserves quota at 23:59 on Monday, but payment is captured at 00:01 on Tuesday.
- **Solution**: The payment date is strictly determined by the provider capture timestamp (`payload.PaidAt`) in **Indian Standard Time (IST)**.
  - Releases Day 1 reserved quota.
  - Attempts direct consumption against Day 2.
  - If Day 2 limit is exhausted, marks order `REFUND_REQUIRED` without consuming quota.

### 6. Downstream Ledger Idempotency
- When crediting Core Wallet via gRPC, `DepositFunds` passes:
  - `ReferenceID = order.ID`
  - `ReferenceType = "TOPUP"`
- Core Wallet enforces `UNIQUE (wallet_id, reference_id, reference_type)`, preventing ledger double-credits even if gRPC retries occur.

---

## 3. End-to-End Lifecycle State Machine & Flow

```text
                         USER
                          │
                          │ POST /topups (₹1–₹10, idempotency_key)
                          ▼
                ┌──────────────────────┐
                │   Top-Up API Handler  │
                └──────────┬───────────┘
                           │
                           ▼
                ┌──────────────────────┐
                │ PostgreSQL Tx:       │
                │ Lock daily quota     │
                │ Check & Reserve INR  │
                │ Check idempotency    │
                │ Create INITIATED ord │
                └──────────┬───────────┘
                           │
                           ▼
                ┌──────────────────────┐
                │ Payment Provider     │
                │ Create Order         │
                └──────────┬───────────┘
                           │
                    ┌──────┴──────┐
                    │             │
                  success       failure
                    │             │
                    ▼             ▼
          PAYMENT_PENDING   CancelInitiated (Release Quota)
                    │
                    ▼
           User pays at Gateway
                    │
                    ▼
          ┌──────────────────────┐
          │ Webhook: captured    │
          │ HMAC & Replay Verify │
          └──────────┬───────────┘
                     │
                     ▼
          ┌──────────────────────┐
          │ ProcessConfirmationTx│
          │ Lock order & Dedup   │
          │ Shift quota reserved │
          │   -> consumed        │
          └──────────┬───────────┘
                     │
                     ▼
              CREDIT_PENDING
                     │
                     ▼
          ┌──────────────────────┐
          │ Reconciler Worker    │
          │ Claim with lease +   │
          │   fencing token      │
          └──────────┬───────────┘
                     │
                     ▼
             CREDIT_PROCESSING
                     │
                     ▼
          ┌──────────────────────┐
          │ Wallet Service gRPC  │
          │ DepositFunds()       │
          │ (Idempotent Ledger)  │
          └──────────┬───────────┘
                     │
                     ▼
                 COMPLETED
```

```mermaid
stateDiagram-v2
    [*] --> INITIATED: User calls POST /topups (Quota Reserved)
    INITIATED --> PAYMENT_PENDING: Gateway Order Created (provider_order_id assigned)
    INITIATED --> FAILED: Gateway Call Failed (Quota Released atomically)
    
    PAYMENT_PENDING --> CREDIT_PENDING: Webhook: payment.captured (Quota: Reserved -> Consumed)
    PAYMENT_PENDING --> EXPIRED: Expiry Worker catches unpaid order (Quota Released)
    PAYMENT_PENDING --> REFUND_REQUIRED: Paid after order expired / Day 2 quota exhausted

    CREDIT_PENDING --> CREDIT_PROCESSING: Reconciler claims lease (claim_token, claim_until)
    CREDIT_PROCESSING --> COMPLETED: Wallet gRPC DepositFunds() succeeded
    CREDIT_PROCESSING --> CREDIT_PENDING: Worker lease expired / RPC retry
    CREDIT_PROCESSING --> REFUND_REQUIRED: Reconciler unrecoverable failure
    
    COMPLETED --> [*]
    EXPIRED --> [*]
    FAILED --> [*]
    REFUND_REQUIRED --> [*]
```

---

## 4. API Reference

### 1. Create Top-Up Order
`POST /api/v1/topups`

**Headers:**
- `X-User-ID: <uuid>` (Required)
- `X-Idempotency-Key: <string>` (Required, max 100 chars)

**Request Body:**
```json
{
  "inrAmount": 10
}
```

**Response (`201 Created`):**
```json
{
  "topupId": "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
  "userId": "11111111-1111-1111-1111-111111111111",
  "inrAmount": 10,
  "usdtAmount": "10000.0000000000",
  "exchangeRate": 1000,
  "status": "PAYMENT_PENDING",
  "provider": "MOCK",
  "providerOrderId": "mock_order_9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
  "expiresAt": "2026-09-11T14:45:00Z",
  "createdAt": "2026-09-11T14:30:00Z"
}
```

---

### 2. Get Daily Usage
`GET /api/v1/topups/daily-usage`

**Headers:**
- `X-User-ID: <uuid>` (Required)

**Response (`200 OK`):**
```json
{
  "userId": "11111111-1111-1111-1111-111111111111",
  "usageDate": "2026-09-11",
  "limitInr": 10,
  "reservedInr": 0,
  "consumedInr": 10,
  "remainingInr": 0,
  "resetsAt": "2026-09-11T18:30:00Z"
}
```

---

### 3. Get Top-Up By ID
`GET /api/v1/topups/{id}`

**Headers:**
- `X-User-ID: <uuid>` (Required)

**Response (`200 OK`):**
```json
{
  "topupId": "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
  "userId": "11111111-1111-1111-1111-111111111111",
  "inrAmount": 10,
  "usdtAmount": "10000.0000000000",
  "exchangeRate": 1000,
  "status": "COMPLETED",
  "provider": "MOCK",
  "providerOrderId": "mock_order_9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
  "expiresAt": "2026-09-11T14:45:00Z",
  "createdAt": "2026-09-11T14:30:00Z"
}
```

---

### 4. Ingest Payment Gateway Webhook
`POST /api/v1/webhooks/payment/{provider}`

**Headers:**
- `X-Webhook-Signature: <hex_digest>` (Required)
- `X-Webhook-Timestamp: <unix_epoch_seconds>` (Required)

**Request Body:**
```json
{
  "event_id": "evt_capture_1001",
  "event_type": "payment.captured",
  "payment_id": "pay_test_9999",
  "provider_order_id": "mock_order_9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
  "inr_amount": 10,
  "currency": "INR",
  "paid_at": 1789139400,
  "timestamp": 1789139400
}
```

**Response (`200 OK`):**
```json
{
  "status": "SUCCESS"
}
```

---

### 5. Health Check
`GET /health`

**Response (`200 OK`):**
```json
{
  "ok": true
}
```

---

## 5. Configuration Reference

| Variable | Default | Purpose |
| :--- | :--- | :--- |
| `PORT` | `8084` | HTTP server port. |
| `POSTGRES_DSN` | `postgres://postgres:postgres@localhost:5432/tradedrift_topup?sslmode=disable` | PostgreSQL database connection string. |
| `WALLET_GRPC_ADDR` | `localhost:50052` | gRPC address of Core Wallet Service. |
| `DAILY_LIMIT_INR` | `10` | Daily fiat deposit cap per user. |
| `WEBHOOK_SECRET` | `topup_mock_secret_key_12345` | Shared HMAC secret for webhook signature verification. |
