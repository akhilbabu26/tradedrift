# Wallet Top-Up Payment Flow (`PAYMENT_FLOW.md`)

This document details the complete end-to-end payment lifecycle for the **TradeDrift Wallet Top-Up Service** (`services/wallet-topup`). It explains how user requests, payment gateways (Razorpay/Mock), cryptographic webhooks, and the internal Wallet Service interact step-by-step.

---

## 1. High-Level Architecture Overview

```text
               USER (Browser / Mobile App)
                     │                 ▲
        1. Buy ₹2    │                 │ 3. Return Order ID &
       (POST /topups)│                 │    Checkout URL
                     ▼                 │
        ┌──────────────────────────────┴──────────────┐
        │          WALLET TOP-UP SERVICE              │
        │                                             │
        │  • Validates Quota (₹1–₹10 cap)             │
        │  • Initiates order in PostgreSQL            │
        │  • Calls Payment Gateway API                │
        └───────┬───────────────────────────────▲─────┘
                │                               │
    2. Create   │                               │ 6. HTTP Webhook
       Order    │                               │    (payment.captured)
                ▼                               │
        ┌───────────────────────────────────────┴─────┐
        │        PAYMENT GATEWAY (Razorpay)           │
        │                                             │
        │  • Collects payment via UPI / Cards / Net   │
        │  • Signs payload with HMAC-SHA256           │
        └───────────────────────▲─────────────────────┘
                                │
                                │ 4. User Scans QR / Pays UPI
                                │    (GPay, PhonePe, Paytm)
                                │
                        ┌───────┴───────┐
                        │   USER / UPI  │
                        └───────────────┘
                                
        ┌─────────────────────────────────────────────┐
        │  WALLET TOP-UP BACKGROUND WORKER            │
        │                                             │
        │  7. Reconciler picks up CREDIT_PENDING      │
        │  8. Calls Wallet Service via gRPC           │
        └───────────────────────┬─────────────────────┘
                                │
                                │ gRPC DepositFunds()
                                ▼
        ┌─────────────────────────────────────────────┐
        │            WALLET SERVICE                   │
        │                                             │
        │  • Credits 2,000 USDT to User               │
        │  • Appends immutable 'TOPUP' ledger row     │
        └─────────────────────────────────────────────┘
```

---

## 2. Complete Chronological Timeline

| Time | Actor / Component | Action Description | Code Location |
| :--- | :--- | :--- | :--- |
| **T = 0s** | **User (Client)** | Clicks *"Buy ₹2 of USDT"*. Client sends `POST /api/v1/topups` with `X-User-ID`, `X-Idempotency-Key`, and `{"inrAmount": 2}`. | [topup_handler.go:L27](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/handler/topup_handler.go#L27) |
| **T = 1s** | **Top-Up Service** | Validates amount ($\ge ₹1$ and $\le ₹10$), pre-reserves daily quota, creates order with status `INITIATED`, calls Gateway API, and updates status to `PAYMENT_PENDING`. | [topup_service.go:L55-L150](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/service/topup_service.go#L55-L150) |
| **T = 2s** | **Frontend Client** | Receives `201 Created` with `providerOrderId` and opens Razorpay Checkout modal or UPI QR code. | [topup_handler.go:L75](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/handler/topup_handler.go#L75) |
| **T = 10s** | **User & Bank** | User approves the payment in Google Pay / PhonePe with their UPI PIN. Bank settles ₹2 with Razorpay. | External Banking Network |
| **T = 12s** | **Razorpay Gateway** | Marks payment as `captured`. Generates an HMAC-SHA256 signature using the shared webhook secret. | External Payment Gateway |
| **T = 13s** | **Gateway $\to$ Webhook** | Razorpay server makes an HTTP POST request to `/api/v1/webhooks/payment/razorpay` carrying the payload and signature. | [router.go:L18](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/handler/router.go#L18) |
| **T = 14s** | **Webhook Handler** | Verifies HMAC signature, checks anti-replay timestamp window ($\pm 300\text{s}$), converts `reserved_inr` $\to$ `consumed_inr`, marks order `CREDIT_PENDING`, and returns `200 OK`. | [tx_manager.go:L43-L263](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/repository/postgres/tx_manager.go#L43-L263) |
| **T = 15s** | **Reconciler Worker** | Background goroutine claims `CREDIT_PENDING` order using `FOR UPDATE SKIP LOCKED`, calls Wallet Service via gRPC `DepositFunds()`, and marks order `COMPLETED`. | [reconciler_worker.go:L50-L130](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/service/reconciler_worker.go#L50-L130) |

---

## 3. Step-by-Step Code Execution

### Step 1: User Initiates Top-Up (`POST /api/v1/topups`)
**File:** [internal/handler/topup_handler.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/handler/topup_handler.go)
1. Middleware extracts authenticated user ID (`X-User-ID`).
2. Extracts `X-Idempotency-Key` header to prevent double-clicks.
3. Supports both `inrAmount` (camelCase) and `inr_amount` (snake_case).
4. Invokes `topupService.CreateTopUp(...)`.

---

### Step 2: Atomic Pre-Reservation & Gateway Order Creation
**File:** [internal/service/topup_service.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/service/topup_service.go)
1. **Pre-Reserve Quota (DB Transaction):**
   ```go
   existing, err := s.txManager.InitiateTopUpTx(ctx, candidateOrder, s.dailyLimitINR)
   ```
   - Checks if this idempotency key was already submitted.
   - Atomically increments `reserved_inr` in `daily_topup_limits`.
   - Inserts order in `INITIATED` status.
2. **Call Payment Gateway API:**
   ```go
   provRes, err := s.paymentProvider.CreateOrder(ctx, orderID, inrAmount, "INR")
   ```
   - Makes a network call to Razorpay to register the order.
   - If the gateway fails/times out, calls `CancelInitiatedOrderTx(...)` to roll back the reserved quota immediately.
3. **Activate to `PAYMENT_PENDING`:**
   ```go
   s.txManager.ActivatePaymentPending(ctx, orderID, provRes.ProviderOrderID)
   ```
   - Updates status to `PAYMENT_PENDING` and attaches `provider_order_id`.
   - Returns order details to the client with HTTP `201 Created`.

---

### Step 3: User Pays on Payment Gateway / UPI App
- The user is shown the checkout interface.
- Payment is handled directly between the user, their bank/UPI app, and the gateway.
- The Top-Up Service sits completely idle while waiting for the webhook callback.

---

### Step 4: How the Webhook is Called (Mock vs Production Gateway)
**File:** [internal/handler/webhook_handler.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/handler/webhook_handler.go)  
**Route:** `POST /api/v1/webhooks/payment/{provider}`

The webhook endpoint is **dynamic** based on the `{provider}` URL path parameter:

#### A. In Local Development & Automated Tests (`MockPaymentProvider`):
Because we don't connect to a live banking network during local development or unit tests, the test runner / Postman acts as the payment gateway:
1. It creates the top-up order.
2. It generates an HMAC-SHA256 signature using the mock secret (`mockProvider.GenerateSignature(...)`).
3. It makes an HTTP POST request to:
   ```http
   POST /api/v1/webhooks/payment/mock HTTP/1.1
   Host: localhost:8081
   Content-Type: application/json
   X-Webhook-Signature: <hmac_sha256_hash>
   X-Webhook-Timestamp: 1694445013

   {
     "event_id": "evt_test_1",
     "event_type": "payment.captured",
     "payment_id": "pay_test_1",
     "order_id": "mock_order_123",
     "inr_amount": 2,
     "currency": "INR"
   }
   ```

#### B. In Production (Razorpay Gateway):
In production, Razorpay's external cloud servers call this exact endpoint automatically once the user's UPI/Card transaction completes:
```http
POST /api/v1/webhooks/payment/razorpay HTTP/1.1
Host: api.tradedrift.com
Content-Type: application/json
X-Razorpay-Signature: 4a3f9e...
X-Webhook-Timestamp: 1694445013

{
  "event": "payment.captured",
  "payload": {
    "payment": {
      "id": "pay_987654",
      "order_id": "order_xyz",
      "amount": 200,
      "currency": "INR",
      "status": "captured"
    }
  }
}
```

#### What WebhookHandler does upon receiving either request:
1. Limits payload size to 64 KB (`http.MaxBytesReader`) to protect against memory exhaustion attacks.
2. Forwards raw body, signature, and timestamp to `WebhookService.ProcessWebhook(...)`.


---

### Step 5: Cryptographic Validation & Atomic Database Transition
**Files:** [internal/service/webhook_service.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/service/webhook_service.go) & [internal/repository/postgres/tx_manager.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/repository/postgres/tx_manager.go)

Inside `ProcessPaymentConfirmationTx(...)`:
1. **HMAC-SHA256 Signature Verification:**
   Computes `HMAC-SHA256(timestamp + "." + rawPayload, secret)` and compares it in constant time (`hmac.Equal`).
2. **Anti-Replay Protection:**
   Verifies $|T_{\text{now}} - T_{\text{webhook}}| \le 300\text{s}$ (5-minute window).
3. **Webhook Deduplication:**
   Queries `webhook_events` with `FOR UPDATE`. If already processed, returns fast `200 OK` without re-running mutations.
4. **Order Lock (`SELECT ... FOR UPDATE`):**
   Locks the order row in `topup_orders`.
5. **Daily Quota Accounting:**
   - Converts `reserved_inr` $\to$ `consumed_inr`.
   - If payment crossed midnight, correctly credits the capture date without exceeding daily caps.
6. **Status Transition:**
   Updates status to `CREDIT_PENDING` and records `payment_id = "pay_987654"`.
7. **Webhook Event Recorded:**
   Marks webhook event as `PROCESSED`.
8. **Commits Transaction:** All steps commit together atomically.

---

### Step 6: Background Reconciler Credits Wallet via gRPC
**File:** [internal/service/reconciler_worker.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet-topup/internal/service/reconciler_worker.go)

A background goroutine polls the database:
```go
// 1. Claim orders needing credit using PostgreSQL SKIP LOCKED:
orders, claimToken, err := w.orderRepo.ClaimPendingCredits(ctx, w.batchSize, w.leaseDuration)

// 2. Call Wallet Service via gRPC:
for _, order := range orders {
    err := w.walletClient.DepositFunds(ctx, &walletpb.DepositFundsRequest{
        WalletId:      order.WalletID,
        Amount:        order.USDTAmount.String(), // "2000.0000000000"
        ReferenceId:   order.ID,                  // Order ID as idempotency key
        ReferenceType: "TOPUP",                   // RefType allowed in wallet
    })

    // 3. Mark completed:
    if err == nil {
        w.orderRepo.MarkCompleted(ctx, order.ID, claimToken)
    }
}
```

---

## 4. Edge Cases & Defensive Architecture

```text
                                 STATUS: PAYMENT_PENDING
                                            │
               ┌────────────────────────────┴────────────────────────────┐
               │                                                         │
       User Pays in Time                                    User Abandons / Payment Delay
               │                                                         │
               ▼                                                         ▼
    Webhook arrives < 15 min                                  15-Minute Timer Expires
               │                                                         │
               ▼                                                         ▼
    Status: CREDIT_PENDING                                    ExpiryWorker runs
    reserved -> consumed                                      Status: EXPIRED
               │                                              reserved_inr released (+₹2 back)
               ▼                                                         │
    Wallet credited with USDT                                            ▼
    Status: COMPLETED                                         User can try again with ₹10
                                                                         │
                                                                         ▼
                                                              [Late Webhook Arrives!]
                                                                         │
                                                                         ▼
                                                              Status: REFUND_REQUIRED
                                                              (Prevents daily limit bypass;
                                                               queues user bank refund)
```

1. **Duplicate Webhook Storm:**
   - Razorpay retries webhooks until it receives a 200 OK.
   - The PostgreSQL unique constraint on `(provider, event_id)` and `FOR UPDATE` check guarantees the order is only credited once.
2. **User Abandons Payment:**
   - The order has an `expires_at = NOW() + 15m`.
   - If no payment arrives, `ExpiryWorker` transitions status to `EXPIRED` and decrements `reserved_inr`, releasing the quota back to the user.
3. **Late-Arriving Webhook on Expired Order:**
   - If the user completes payment on UPI after our 15m timer expired and the order is already `EXPIRED`, we **cannot** fulfill the order because their quota was already returned!
   - The system transitions the order to **`REFUND_REQUIRED`** and stores `payment_id`, ensuring user funds are automatically refunded to their bank account.
4. **Network Timeout Calling Wallet Service:**
   - If the gRPC call to the Wallet Service times out, the order remains in `CREDIT_PENDING`.
   - The `ReconcilerWorker` retries the call. The Wallet Service uses `(wallet_id, reference_id, reference_type)` as an idempotency boundary, guaranteeing the wallet is never credited twice.
