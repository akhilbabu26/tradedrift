# Complete End-to-End Operational Flow of Wallet Top-Up Service

This document provides a comprehensive, step-by-step technical breakdown of every flow, decision branch, state transition, and failure recovery path across the **Wallet Top-Up Service**, mapped directly to our codebase.

---

## Master Architecture Flowchart

```text
                         USER
                          │
                          │ POST /topups
                          │ amount = ₹1–₹10
                          │ idempotency_key
                          ▼
                ┌──────────────────────┐
                │   Top-Up API Handler  │
                └──────────┬───────────┘
                           │
                           ▼
                ┌──────────────────────┐
                │ Request Validation   │
                │                      │
                │ • user authenticated │
                │ • amount ₹1–₹10      │
                │ • whole INR          │
                │ • idempotency key    │
                └──────────┬───────────┘
                           │
                           ▼
                ┌──────────────────────┐
                │  CreateTopUp()       │
                └──────────┬───────────┘
                           │
                           ▼
             ┌─────────────────────────────┐
             │ PostgreSQL Transaction      │
             │                             │
             │ Lock daily quota            │
             │ Check available quota       │
             │ Reserve INR                 │
             │ Check idempotency           │
             │ Create INITIATED order      │
             └──────────────┬──────────────┘
                            │
                            ▼
                  INITIATED TOP-UP ORDER
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
          provider_order_id    CancelInitiated
                    │             │
                    │             ▼
                    │       ┌─────────────┐
                    │       │ DB Tx       │
                    │       │             │
                    │       │ FAILED      │
                    │       │ Release     │
                    │       │ quota       │
                    │       └─────────────┘
                    │
                    ▼
             PAYMENT_PENDING
                    │
                    ▼
             User completes
                payment
                    │
                    ▼
              PAYMENT PROVIDER
                    │
                    │ payment.captured
                    ▼
          ┌──────────────────────┐
          │ Webhook Endpoint     │
          └──────────┬───────────┘
                     │
                     ▼
          ┌────────────────────────┐
          │ Webhook Verification   │
          │                        │
          │ HMAC / signature       │
          │ timestamp / replay     │
          └───────────┬────────────┘
                      │
                      ▼
          ┌────────────────────────┐
          │ Payload Validation     │
          │                        │
          │ event_id               │
          │ payment_id             │
          │ provider_order_id      │
          │ event_type             │
          │ currency               │
          │ amount                 │
          │ PaidAt                 │
          └───────────┬────────────┘
                      │
                      ▼
        ┌──────────────────────────────┐
        │ ProcessPaymentConfirmationTx │
        └───────────────┬──────────────┘
                        │
                        ▼
             ┌─────────────────────────┐
             │ PostgreSQL Transaction  │
             │                         │
             │ Deduplicate webhook     │
             │ Lock top-up order       │
             │ Determine capture date  │
             │ Lock quota              │
             │ Consume/release quota   │
             │ Update order state      │
             │ Mark webhook processed  │
             └────────────┬────────────┘
                          │
                          ▼
                   CREDIT_PENDING
                          │
                          ▼
               ┌─────────────────────┐
               │ Reconciliation      │
               │ Worker              │
               └─────────┬───────────┘
                         │
                         ▼
                Claim with lease +
                  fencing token
                         │
                         ▼
                 CREDIT_PROCESSING
                         │
                         ▼
                Wallet Service gRPC
                         │
                         │ DepositFunds
                         │ reference_id = topup_id
                         │ reference_type = TOPUP
                         ▼
                 ┌────────────────┐
                 │ Wallet Service │
                 └───────┬────────┘
                         │
                         ▼
                Idempotency check
                         │
                         ▼
                 Credit wallet
                         │
                         ▼
                 Wallet ledger
                         │
                         ▼
                    COMMIT
                         │
                         ▼
              Reconciler completes
                         │
                         ▼
                    COMPLETED
```

---

## Phase 1: Reservation & Order Creation

```text
                         USER
                          │
                          │ POST /api/v1/topups
                          │ X-User-ID: <user_uuid>
                          │ X-Idempotency-Key: <key>
                          │ Body: {"inrAmount": 5}
                          ▼
                ┌──────────────────────┐
                │   RequireAuth MW     │
                │ Extract UserID to ctx│
                └──────────┬───────────┘
                           │
                           ▼
                ┌──────────────────────┐
                │ Payload Validation   │
                │                      │
                │ • 1 <= inrAmount <= 10
                │ • whole INR only     │
                │ • key <= 100 chars   │
                └──────────┬───────────┘
                           │
                           ▼
                ┌──────────────────────┐
                │  InitiateTopUpTx()   │
                │  Begin PostgreSQL Tx │
                └──────────┬───────────┘
                           │
                           ▼
                ┌──────────────────────┐
                │ Idempotency Check    │
                │                      │
                │ SELECT FOR UPDATE    │
                │ WHERE user_id & key  │
                └──────────┬───────────┘
                           │
            ┌──────────────┴──────────────┐
            │                             │
       Key Exists                   Key Is New
            │                             │
     ┌──────┴──────┐                      ▼
     │             │            ┌──────────────────────┐
Same Amount   Diff Amount       │ Ensure Daily Limit   │
     │             │            │ Row (ON CONFLICT)    │
     ▼             ▼            └──────────┬───────────┘
Return 200    Return 409                  │
Existing      Conflict                    ▼
Order         Error             ┌──────────────────────┐
                                │ Atomic Reservation   │
                                │                      │
                                │ UPDATE reserved_inr  │
                                │ WHERE total <= limit │
                                └──────────┬───────────┘
                                           │
                            ┌──────────────┴──────────────┐
                            │                             │
                      Quota Exceeded                 Quota Reserved
                            │                             │
                            ▼                             ▼
                      Rollback Tx               ┌──────────────────────┐
                      Return 429                │ Insert Order Row     │
                      Limit Exceeded            │                      │
                                                │ status = INITIATED   │
                                                │ expires_at = +15m    │
                                                └──────────┬───────────┘
                                                           │
                                                           ▼
                                                COMMIT TRANSACTION
                                                           │
                                                           ▼
                                                ┌──────────────────────┐
                                                │ Provider.CreateOrder │
                                                │ Handshake Gateway    │
                                                └──────────┬───────────┘
                                                           │
                                            ┌──────────────┴──────────────┐
                                            │                             │
                                         SUCCESS                       FAILURE
                                            │                             │
                                            ▼                             ▼
                                ┌──────────────────────┐      ┌──────────────────────┐
                                │ ActivatePayment      │      │ CancelInitiatedTx    │
                                │                      │      │                      │
                                │ status =             │      │ status = FAILED      │
                                │   PAYMENT_PENDING    │      │ release reserved_inr │
                                │ provider_order_id    │      └──────────┬───────────┘
                                └──────────┬───────────┘                 │
                                           │                             ▼
                                           ▼                      Return 502/500
                                  Return 201 Created              Gateway Error
                                  Provider Checkout URL
```

### Flow 1: Top-Up Request Ingestion & Validation
1. **Client Action**: Client sends `POST /api/v1/topups` with `X-User-ID`, `X-Idempotency-Key`, and JSON body `{"inrAmount": 5}`.
2. **Authentication Middleware**: In [internal/handler/middleware.go:L33-L46](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/handler/middleware.go#L33-L46), `RequireAuth` extracts `X-User-ID`, validates it is non-empty, and injects it into `r.Context()`.
3. **Payload & Bounds Validation**: In [internal/service/topup_service.go:L60-L71](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/service/topup_service.go#L60-L71):
   - Validates `idempotencyKey` is non-empty and $\le 100$ characters (`domain.ErrInvalidIdempotencyKey`).
   - Validates `inrAmount` is a whole rupee integer between ₹1 and ₹10 (`domain.ErrInvalidAmount`).
4. **Deterministic Conversion**: Computes high-precision simulated USDT string:
   ```go
   usdtAmount := fmt.Sprintf("%d.0000000000", inrAmount*1000)
   ```
   ₹5 yields strictly `"5000.0000000000"`.

---

### Flow 2: Atomic Quota Reservation (`InitiateTopUpTx`)
The service creates the order candidate and begins a PostgreSQL transaction in [internal/repository/postgres/tx_manager.go:L312-L390](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/repository/postgres/tx_manager.go#L312-L390):

1. **Daily Limit Row Guarantee**: Ensures `daily_topup_limits` has a row for `(user_id, usage_date)`:
   ```sql
   INSERT INTO daily_topup_limits (user_id, usage_date, limit_inr, reserved_inr, consumed_inr)
   VALUES ($1, $2, $3, 0, 0)
   ON CONFLICT (user_id, usage_date) DO NOTHING;
   ```
2. **Atomic Quota Reservation**:
   ```sql
   UPDATE daily_topup_limits
   SET reserved_inr = reserved_inr + $1,
       updated_at   = NOW()
   WHERE user_id    = $2
     AND usage_date = $3
     AND (reserved_inr + consumed_inr + $1) <= limit_inr;
   ```
   - If `RowsAffected == 0`, transaction rolls back and returns `domain.ErrDailyLimitExceeded` (HTTP 429/422).
3. **Insert Order in `INITIATED` Status**:
   Inserts the row into `topup_orders` with `status = 'INITIATED'` and `expires_at = now + 15 minutes`.
4. **Commit**: Quota is officially held; subsequent concurrent requests cannot claim it.

---

### Flow 3: Client Idempotency Check
Before quota is reserved, [tx_manager.go:L325-L339](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/repository/postgres/tx_manager.go#L325-L339) queries the candidate key under row lock:
```sql
SELECT id, inr_amount, status
FROM topup_orders
WHERE user_id = $1 AND idempotency_key = $2
FOR UPDATE;
```
- **Same Key + Same Amount**: Transaction rolls back immediately and returns the existing order object (`existing != nil`). The client gets an idempotent HTTP 200/201 response.
- **Same Key + Different Amount**: Rejects with `domain.ErrIdempotencyConflict` (`HTTP 409 Conflict`).

---

### Flow 4: Payment Provider Order Creation
With the database transaction committed and order in `INITIATED` state:
1. Top-Up service calls `s.paymentProvider.CreateOrder(ctx, orderID, inrAmount, "INR")` in [topup_service.go:L113](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/service/topup_service.go#L113).
2. The internal `orderID` is passed as the merchant reference.
3. Upon provider success, `ActivatePaymentPending` ([tx_manager.go:L393-L409](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/repository/postgres/tx_manager.go#L393-L409)) records `provider_order_id` and transitions the order:
   $$\text{INITIATED} \longrightarrow \text{PAYMENT\_PENDING}$$
4. Returns HTTP 201 Created with the provider checkout identifier.

---

### Flow 5: Provider Failure & Quota Rollback
If the payment gateway call times out or returns a 5xx error:
1. `s.paymentProvider.CreateOrder` returns an error in [topup_service.go:L114](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/service/topup_service.go#L114).
2. `TopUpService` immediately executes `CancelInitiatedOrderTx` ([tx_manager.go:L412-L456](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/repository/postgres/tx_manager.go#L412-L456)):
   ```sql
   -- 1. Mark order FAILED
   UPDATE topup_orders SET status = 'FAILED', last_error = $2, updated_at = NOW()
   WHERE id = $3 AND status = 'INITIATED';

   -- 2. Release reserved quota
   UPDATE daily_topup_limits SET reserved_inr = reserved_inr - $1, updated_at = NOW()
   WHERE user_id = $2 AND usage_date = $3 AND reserved_inr >= $1;
   ```
3. The order is terminal (`FAILED`) and the user's reserved quota is immediately restored.

---

## Phase 2: Payment Confirmation & Webhook Ingestion

```text
                    PAYMENT GATEWAY
                          │
                          │ POST /api/v1/webhooks/payment/MOCK
                          │ X-Signature, X-Timestamp
                          │ Event: payment.captured
                          ▼
                ┌──────────────────────┐
                │   Webhook Handler    │
                │ MaxBytesReader (64KB)│
                └──────────┬───────────┘
                           │
                           ▼
                ┌──────────────────────┐
                │ Security Verifier    │
                │                      │
                │ • |now - ts| <= 300s │
                │ • HMAC-SHA256 Match  │
                └──────────┬───────────┘
                           │
            ┌──────────────┴──────────────┐
            │                             │
       Valid HMAC                   Invalid / Replay
            │                             │
            ▼                             ▼
  ┌──────────────────────┐      ┌──────────────────────┐
  │ Payload Validation   │      │ Audit Webhook Row    │
  │                      │      │ SignatureValid=false │
  │ • Event == captured  │      │ Return 401 / 400     │
  │ • Currency == INR    │      └──────────────────────┘
  │ • Order & Amt Match  │
  └──────────┬───────────┘
             │
             ▼
  ┌──────────────────────────────┐
  │ ProcessPaymentConfirmationTx │
  │ Begin PostgreSQL Transaction │
  └──────────┬───────────────────┘
             │
             ▼
  ┌──────────────────────────────┐
  │ Deduplication Check          │
  │ SELECT event_id FOR UPDATE   │
  └──────────┬───────────────────┘
             │
      ┌──────┴──────┐
      │             │
  Processed      New Event
      │             │
      ▼             ▼
  Return 200  ┌──────────────────────────────┐
  AlreadyDone │ Lock TopUp Order Row         │
              │ SELECT ... FOR UPDATE        │
              └─────────────┬────────────────┘
                            │
              ┌─────────────┴─────────────┐
              │                           │
          EXPIRED                   PAYMENT_PENDING
              │                           │
              ▼                           ▼
  ┌──────────────────────┐    ┌──────────────────────────────┐
  │ Mark Order:          │    │ Calculate Capture Date (IST) │
  │ REFUND_REQUIRED      │    │ compare with ReservationDate │
  └──────────┬───────────┘    └──────────┬───────────────────┘
             │                           │
             │            ┌──────────────┴──────────────┐
             │            │                             │
             │        Same Day                     Cross-Midnight
             │            │                             │
             │            ▼                             ▼
             │  ┌──────────────────────┐   ┌──────────────────────────┐
             │  │ Shift Quota in DB    │   │ 1. Release Day 1 Quota   │
             │  │ reserved -= amount   │   │ 2. Ensure Day 2 Limit Row│
             │  │ consumed += amount   │   │ 3. Check Day 2 Remaining │
             │  └─────────┬────────────┘   └────────────┬─────────────┘
             │            │                             │
             │            │              ┌──────────────┴──────────────┐
             │            │              │                             │
             │            │         Day 2 Has Room              Day 2 Exhausted
             │            │              │                             │
             │            │              ▼                             ▼
             │            │     consumed_inr += amt           Mark Order:
             │            │     status = CREDIT_PENDING       REFUND_REQUIRED
             │            │              │                             │
             │            └──────────────┼─────────────────────────────┘
             │                           │
             ▼                           ▼
  ┌─────────────────────────────────────────────────────────────┐
  │ Webhook Audit Row -> PROCESSED & COMMIT TRANSACTION         │
  └──────────────────────────────┬──────────────────────────────┘
                                 │
                                 ▼
                     Return HTTP 200 {"status": "SUCCESS"}
```

### Flow 6: User Checkout & Webhook Receipt
1. The user pays via UPI/Card on the gateway checkout page.
2. Gateway captures the payment (`pay_123`) and POSTs a webhook to `/api/v1/webhooks/payment/MOCK`.
3. In [internal/handler/webhook_handler.go:L58](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/handler/webhook_handler.go#L58), the body is wrapped in `http.MaxBytesReader(w, r.Body, 64*1024)` to reject memory exhaustion DOS payloads.

---

### Flow 7: Perimeter Webhook Security Verification
In [internal/webhook/verifier.go:L24-L42](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/webhook/verifier.go#L24-L42):
1. **Replay Window Check**:
   $$|T_{now} - T_{header}| \le 300\text{ seconds}$$
   Rejects outdated or future-skewed requests with `domain.ErrWebhookReplay`.
2. **Constant-Time HMAC-SHA256 Verification**:
   Computes HMAC digest over `timestamp.payload` and compares using `crypto/hmac.Equal`.
   Rejects forged signatures with `domain.ErrInvalidSignature`.

---

### Flow 8: Strict Webhook Payload Validation
In [internal/service/webhook_service.go:L114-L203](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/service/webhook_service.go#L114-L203):
- **Mandatory Fields**: Rejects missing `event_id`, `payment_id`, `provider_order_id`, or `paid_at`.
- **Timestamp Coherence**: Verifies header timestamp vs payload inner timestamp:
  $$|T_{header} - T_{payload}| \le 5\text{ seconds}$$
- **Event Filter**: Strictly checks `payload.EventType == "payment.captured"`. Events like `order.paid` or `payment.failed` return HTTP 200 without moving financial state.
- **Currency Check**: Strictly requires `payload.Currency == "INR"`.
- **Order Identity & Amount Match**: Looks up order by `provider_order_id` and verifies `order.Provider == provider` and `order.INRAmount == payload.INRAmount`.

---

### Flow 9: Capture Date Determination (Cross-Midnight Protection)
In [webhook_service.go:L205-L206](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/service/webhook_service.go#L205-L206):
```go
captureTimeIST := time.Unix(payload.PaidAt, 0).In(s.locIST)
paidDate := captureTimeIST.Format("2006-01-02")
```
Payments captured at 23:59:59 are attributed to that calendar day in IST, even if the webhook arrives minutes later after midnight.

---

### Flow 10: Webhook Deduplication Check
Inside `ProcessPaymentConfirmationTx` ([tx_manager.go:L48-L80](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/repository/postgres/tx_manager.go#L48-L80)):
```sql
SELECT id, status FROM webhook_events
WHERE provider = $1 AND event_id = $2
FOR UPDATE;
```
- If the event was already `PROCESSED`, the transaction immediately returns `AlreadyProcessed: true` and an HTTP 200 OK.
- If it's a new event, it inserts an audit record with `status = 'RECEIVED'`.

---

### Flow 11: Payment Confirmation Transaction (`ProcessPaymentConfirmationTx`)
Inside the same database transaction:
1. **Locks Order Row**:
   ```sql
   SELECT ... FROM topup_orders WHERE id = $1 FOR UPDATE;
   ```
2. **Terminal State Verification**:
   - If `order.Status` is already `CREDIT_PENDING`, `CREDIT_PROCESSING`, or `COMPLETED` $\to$ marks webhook `PROCESSED` and returns 200 OK.
   - If `order.Status == 'EXPIRED'` $\to$ updates order to `REFUND_REQUIRED` ([tx_manager.go:L117-L135](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/repository/postgres/tx_manager.go#L117-L135)).

---

### Flow 12: Same-Day Quota Accounting
If `order.ReservationDate == paidDate`:
In [tx_manager.go:L144-L171](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/repository/postgres/tx_manager.go#L144-L171):
```sql
-- Atomically shift reserved -> consumed
UPDATE daily_topup_limits
SET reserved_inr = reserved_inr - $1,
    consumed_inr = consumed_inr + $1,
    updated_at   = NOW()
WHERE user_id = $2 AND usage_date = $3 AND reserved_inr >= $1;

-- Move order to CREDIT_PENDING
UPDATE topup_orders
SET status = 'CREDIT_PENDING', payment_id = $2, updated_at = NOW()
WHERE id = $3;
```

---

### Flow 13: Cross-Midnight Quota Accounting
If `paidDate != order.ReservationDate`:
In [tx_manager.go:L172-L241](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/repository/postgres/tx_manager.go#L172-L241):
1. **Release Day 1**: `UPDATE daily_topup_limits SET reserved_inr = reserved_inr - $1 WHERE usage_date = Day1`.
2. **Ensure Day 2**: `INSERT INTO daily_topup_limits ... ON CONFLICT DO NOTHING`.
3. **Attempt Day 2 Consumption**:
   ```sql
   UPDATE daily_topup_limits
   SET consumed_inr = consumed_inr + $1, updated_at = NOW()
   WHERE user_id = $2 AND usage_date = $3 AND (reserved_inr + consumed_inr + $1) <= limit_inr;
   ```
   - If `RowsAffected == 1` $\to$ order set to `CREDIT_PENDING`.
   - If `RowsAffected == 0` (Day 2 quota exhausted) $\to$ order set to `REFUND_REQUIRED`. Day 2 consumed is not incremented.

---

### Flow 14: Transition to `CREDIT_PENDING`
Once quota accounting succeeds, the webhook row is updated to `PROCESSED` and the transaction commits:
$$\text{PAYMENT\_PENDING} \longrightarrow \text{CREDIT\_PENDING}$$
The webhook handler immediately returns HTTP 200 `{"status": "SUCCESS"}` to the payment gateway.

---

## Phase 3: Reconciler Worker & Wallet Crediting

```text
               RECONCILER WORKER (1s Ticker)
                             │
                             ▼
                ┌─────────────────────────┐
                │ Non-Blocking Batch Claim│
                │                         │
                │ SELECT FOR UPDATE       │
                │ SKIP LOCKED LIMIT 50    │
                └────────────┬────────────┘
                             │
                             ▼
                ┌─────────────────────────┐
                │ Stamp Worker Lease      │
                │                         │
                │ status =                │
                │   CREDIT_PROCESSING     │
                │ claim_token = UUID_A    │
                │ claim_until = NOW() +60s│
                └────────────┬────────────┘
                             │
                             ▼
                     COMMIT TRANSACTION
                             │
                             ▼
                ┌─────────────────────────┐
                │ Worker Pool Dispatch    │
                │ Max 5 Goroutines        │
                └────────────┬────────────┘
                             │
                             ▼
                ┌─────────────────────────┐
                │ Core Wallet gRPC Client │
                │                         │
                │ DepositFunds()          │
                │ ref_id = order_id       │
                │ ref_type = TOPUP        │
                │ amount = USDT           │
                └────────────┬────────────┘
                             │
                             ▼
                ┌─────────────────────────┐
                │ Core Wallet Ledger      │
                │                         │
                │ UNIQUE (wallet_id,      │
                │   reference_id, type)   │
                └────────────┬────────────┘
                             │
              ┌──────────────┴──────────────┐
              │                             │
           SUCCESS                     ERROR / TIMEOUT
              │                             │
              ▼                             ▼
  ┌──────────────────────┐      ┌───────────────────────────┐
  │ Fencing Token Update │      │ RecordClaimFailure()      │
  │                      │      │                           │
  │ UPDATE topup_orders  │      │ last_error = errMsg       │
  │ SET status=COMPLETED │      │ claim_until = NOW()       │
  │ WHERE id = $1        │      │ (immediate retry release) │
  │  AND claim_token=$2  │      └───────────┬───────────────┘
  └──────────┬───────────┘                  │
             │                              ▼
      ┌──────┴──────┐               Order stays in
      │             │               CREDIT_PENDING
   Matches       Mismatch           for next worker cycle
(Rows = 1)      (Rows = 0)
      │             │
      ▼             ▼
  COMPLETED!   STALE WORKER
               FENCED OUT!
               (Token expired,
               Worker B owns it)
```

### Flow 15: Non-Blocking Batch Claiming (`FOR UPDATE SKIP LOCKED`)
In [internal/repository/postgres/topup_order_repo.go:L161-L169](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/repository/postgres/topup_order_repo.go#L161-L169):
1. Background `ReconcilerWorker` runs on a 1-second ticker.
2. Selects up to 50 eligible orders without blocking other workers:
   ```sql
   SELECT id FROM topup_orders
   WHERE status = 'CREDIT_PENDING'
      OR (status = 'CREDIT_PROCESSING' AND claim_until < NOW())
   ORDER BY created_at ASC
   LIMIT $1
   FOR UPDATE SKIP LOCKED;
   ```

---

### Flow 16: Worker Lease Stamping
In [topup_order_repo.go:L190-L200](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/repository/postgres/topup_order_repo.go#L190-L200):
Stamps the batch with a new UUID token and 60-second lease:
```sql
UPDATE topup_orders
SET status        = 'CREDIT_PROCESSING',
    claim_token   = $1,
    claimed_at    = NOW(),
    claim_until   = NOW() + ($2 * INTERVAL '1 millisecond'),
    attempt_count = attempt_count + 1,
    updated_at    = NOW()
WHERE id = ANY($3);
```
Transaction commits. The orders are now in `CREDIT_PROCESSING` under this worker's lease.

---

### Flow 17: Fencing Token Enforcement
If Worker 1 claims an order, suffers a 90-second GC pause, and Worker 2 re-claims the order after 60s:
1. Worker 2 stamps `claim_token = UUID_2`.
2. Worker 1 wakes up and attempts to complete the order with `UUID_1`:
   ```sql
   UPDATE topup_orders SET status = 'COMPLETED'
   WHERE id = $1 AND status = 'CREDIT_PROCESSING' AND claim_token = $2;
   ```
3. Zero rows match. Worker 1 is **fenced out**, preventing split-brain overwrites!

```text
                STALE WORKER FENCING TOKEN FLOW

     Worker A                                Worker B
        │                                       │
        │ Claims Order                          │
        │ claim_token = Token_A                 │
        │ claim_until = T + 60s                 │
        ▼                                       │
  RPC Call Begins                               │
  (Suffers 90s GC pause                         │
   or network stall)                            │
        │                                       │
        │ ─── 60s lease expires ───             │
        │                                       ▼
        │                               Re-claims same order
        │                               claim_token = Token_B
        │                               claim_until = T + 120s
        │                                       │
        ▼                                       ▼
  Wakes up at T + 90s                     Dispatches RPC
  Completes RPC Deposit                   DepositFunds()
        │                                 (Idempotent in Wallet)
        ▼                                       │
  UPDATE topup_orders                           │
  SET status = 'COMPLETED'                      │
  WHERE claim_token = Token_A                   │
        │                                       │
  ┌─────┴─────────────────┐                     │
  │ Result: 0 ROWS        │                     │
  │ Token mismatch!       │                     ▼
  │ WORKER A FENCED OUT!  │               UPDATE topup_orders
  └───────────────────────┘               SET status = 'COMPLETED'
                                          WHERE claim_token = Token_B
                                                │
                                          ┌─────┴─────────────────┐
                                          │ Result: 1 ROW         │
                                          │ Order = COMPLETED     │
                                          └───────────────────────┘
```

---

### Flow 18: Core Wallet gRPC Call (`DepositFunds`)
In [internal/service/reconciler.go:L114-L130](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/service/reconciler.go#L114-L130):
Orders are dispatched into a bounded concurrency pool (max 5 parallel goroutines):
```go
res, err := w.walletClient.DepositFunds(callCtx,
    o.UserID,
    "USDT",
    o.USDTAmount,
    o.ID,
    "TOPUP",
)
```

---

### Flow 19: Downstream Ledger Idempotency
In the Core Wallet Service (see [Core Wallet Flow Guide](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/docs/ENTIRE_FLOW.md#L66-L100)):
1. `DepositFunds` validates `reference_type == "TOPUP"` (enabled via [Migration 00009](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00009_allow_topup_reference_type.sql)) and checks `UNIQUE (wallet_id, reference_id, reference_type)`.
2. Since `reference_id = topup_order_id` and `reference_type = "TOPUP"`, if the reconciler retries an already completed deposit, the Wallet service returns the existing transaction without double-crediting balances.


---

### Flow 20: Successful Completion
In [internal/service/reconciler.go:L160-L177](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/service/reconciler.go#L160-L177):
1. Reconciler calls `CompleteOrder(ctx, o.ID, workerToken)`.
2. PostgreSQL transitions:
   $$\text{CREDIT\_PROCESSING} \longrightarrow \text{COMPLETED}$$
3. Logs transaction ID, new balance, and completes order lifecycle.

---

### Flow 21: Wallet Failure & Exponential Retry Flow
In [internal/service/reconciler.go:L132-L157](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/service/reconciler.go#L132-L157):
1. If Wallet gRPC fails (timeout, network drop), error precedence evaluates:
   `err != nil` > `res == nil` > `!res.Success`.
2. Calls `RecordClaimFailure(ctx, o.ID, workerToken, errMsg)`:
   - Sets `last_error = errMsg`
   - Sets `claim_until = NOW()` (immediately releasing the lease so the order can be retried in the next reconciler cycle).
3. The order remains safely in `CREDIT_PENDING` until the Wallet service recovers.

---

## Edge-Cases & Failure Recovery Flows

### Flow 22: Background Expiry Sweeper (`ExpiryWorker`)
In [internal/service/expiry_worker.go:L68-L93](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/service/expiry_worker.go#L68-L93):
1. Runs every 30 seconds.
2. Queries partial index:
   ```sql
   SELECT id, user_id, reservation_date, inr_amount FROM topup_orders
   WHERE status IN ('PAYMENT_PENDING', 'INITIATED') AND expires_at < NOW()
   LIMIT 100;
   ```
3. For each order, executes atomic single-transaction recovery:
   ```sql
   UPDATE topup_orders SET status = 'EXPIRED' WHERE id = $1;
   UPDATE daily_topup_limits SET reserved_inr = reserved_inr - $1 WHERE user_id = $2 AND usage_date = $3;
   ```
4. Quota is restored; order is marked `EXPIRED`.

---

### Flow 23: Race Between Expiry Sweeper & Webhook Confirmation

```text
           EXPIRY SWEEPER VS WEBHOOK RACE CONDITION

           ExpiryWorker                  Payment Webhook
       (Finds order > 15m)            (User Paid Checkout)
               │                                │
               │ SELECT FOR UPDATE              │ SELECT FOR UPDATE
               └───────────────┬────────────────┘
                               │
                     PostgreSQL Row Lock
                               │
            ┌──────────────────┴──────────────────┐
            │                                     │
    Webhook Locks First                  Expiry Locks First
            │                                     │
            ▼                                     ▼
   Order is PAYMENT_PENDING              Order is PAYMENT_PENDING
            │                                     │
   Transitions to:                       Transitions to:
   CREDIT_PENDING                        EXPIRED
            │                                     │
   Quota reserved -> consumed            Quota released to user
            │                                     │
   Tx Commits                            Tx Commits
            │                                     │
            ▼                                     ▼
   ExpiryWorker wakes up                 Webhook wakes up
            │                                     │
   Sees status=CREDIT_PENDING            Sees status=EXPIRED
   (Not in PAYMENT_PENDING)                       │
            │                            Transitions to:
   RowsAffected = 0                      REFUND_REQUIRED!
            │                                     │
   Graceful No-Op                        Safe & Audited for Refund
```

- Both call `SELECT ... FOR UPDATE` on the order row.
- **If Webhook locks first**: Order moves to `CREDIT_PENDING`. When ExpiryWorker runs, `status != 'PAYMENT_PENDING'` and `RowsAffected == 0`. Quota is consumed, wallet is credited.
- **If ExpiryWorker locks first**: Order moves to `EXPIRED` and quota is released. When Webhook runs, it sees `status == 'EXPIRED'` and moves the order to `REFUND_REQUIRED` ([tx_manager.go:L117-L135](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/repository/postgres/tx_manager.go#L117-L135)).

---

### Flow 24: Invalid Webhook Rejection Paths
1. **Invalid Signature**:
   `s.verifier.Verify` fails $\to$ logs to `webhook_events` with `SignatureValid: false, Status: FAILED` $\to$ returns HTTP 401.
2. **Expired Replay**:
   Header timestamp $|T_{now} - T_{header}| > 300\text{s}$ $\to$ returns HTTP 401.
3. **Payload Mismatch (Bad Amount / Wrong Provider)**:
   Signature verified, but `order.INRAmount != payload.INRAmount` $\to$ logs to `webhook_events` with `SignatureValid: true, Status: FAILED` $\to$ returns HTTP 400.
4. **Missing Mandatory Fields**:
   Missing `event_id`, `payment_id`, or `paid_at` $\to$ returns HTTP 400.

---

### Flow 25: Duplicate Webhook Storms
1. Gateway sends Webhook 1 $\to$ moves order to `CREDIT_PENDING` and commits.
2. Gateway sends Webhook 2 (retry) $\to$ `tx_manager.go` queries `webhook_events` for `(provider, event_id)`.
3. Detects existing event with `status == 'PROCESSED'`.
4. Returns `AlreadyProcessed: true` $\to$ returns immediate HTTP 200 OK without running quota or ledger logic.

```text
                 DUPLICATE WEBHOOK STORM FLOW

           Webhook #1                         Webhook #2 (Retry)
     (Arrival at T + 0.0s)                  (Arrival at T + 0.5s)
               │                                      │
               ▼                                      ▼
     HMAC Signature Verified                HMAC Signature Verified
               │                                      │
               ▼                                      ▼
       Begin Transaction                      Begin Transaction
               │                                      │
     SELECT FOR UPDATE                      SELECT FOR UPDATE
     (provider, event_id)                   (provider, event_id)
               │                                      │
               ▼                                      │
     No existing record                               │
     INSERT status='RECEIVED'                         │
               │                                      │
     Update Quota & Order                             │ (Blocks on DB row lock
     status = CREDIT_PENDING                          │  until Webhook #1 commits)
               │                                      │
     UPDATE webhook_events                            │
     SET status='PROCESSED'                           │
               │                                      │
     COMMIT Transaction                               │
               │                                      ▼
               │                            Row lock acquired!
               │                            Sees status = 'PROCESSED'
               │                                      │
               │                            Rollback / Done
               │                            AlreadyProcessed: true
               ▼                                      ▼
        HTTP 200 OK                            HTTP 200 OK
   (Financial state moved)               (No-op, Zero duplicate effect)
```

---

## Complete Lifecycle State Matrix

| Initial State | Event / Trigger | Target State | Quota Effect |
| :--- | :--- | :--- | :--- |
| **None** | `POST /api/v1/topups` | `INITIATED` | `reserved_inr += amount` |
| `INITIATED` | Gateway order created | `PAYMENT_PENDING` | None (still reserved) |
| `INITIATED` | Gateway order failed / timeout | `FAILED` | `reserved_inr -= amount` (Released) |
| `INITIATED` / `PAYMENT_PENDING` | 15 min expiry elapsed | `EXPIRED` | `reserved_inr -= amount` (Released) |
| `PAYMENT_PENDING` | `payment.captured` (Same Day) | `CREDIT_PENDING` | `reserved -= amt, consumed += amt` |
| `PAYMENT_PENDING` | `payment.captured` (Cross-Midnight, Day 2 OK) | `CREDIT_PENDING` | Day 1 reserved released, Day 2 consumed += amt |
| `PAYMENT_PENDING` | `payment.captured` (Cross-Midnight, Day 2 Full) | `REFUND_REQUIRED` | Day 1 reserved released, Day 2 untouched |
| `PAYMENT_PENDING` | Payment arrived after order expired | `REFUND_REQUIRED` | Day 1 quota already released |
| `CREDIT_PENDING` | Reconciler claims order | `CREDIT_PROCESSING` | Stamped with `claim_token` & 60s lease |
| `CREDIT_PROCESSING` | Wallet `DepositFunds` OK | `COMPLETED` | Wallet balance credited (+USDT) |
| `CREDIT_PROCESSING` | Wallet RPC failed / timeout | `CREDIT_PROCESSING` $\to$ `CREDIT_PENDING` | Lease reset (`claim_until = NOW()`), retried |
| `CREDIT_PROCESSING` | Worker lease expired | Re-claimed by Worker 2 | Old worker token fenced out |
