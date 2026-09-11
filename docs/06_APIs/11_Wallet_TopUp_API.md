# TradeDrift — Wallet Top-Up API

> **Status:** ✅ Designed — Production Hardened  
> **Document:** 11_Wallet_TopUp_API.md  
> **Directory:** docs/06_APIs/  
> **Last Updated:** September 2026  

---

## 1. Top-Up Endpoints

### 1.1 POST `/api/v1/topups`
Initiates a new fiat-to-USDT top-up order. Validates that the requested whole-rupee amount does not breach the user's available daily quota and reserves the allowance before returning payment order details.

* **Authentication:** Bearer Access Token (Authenticated User)
* **Headers:**
  - `Authorization: Bearer <token>`
  - `Idempotency-Key: <UUID>` *(Required)* — Protects against duplicate checkout creation on network retries.
* **Rate Limit:** 10 requests / minute per user
* **Request Body:**
  ```json
  {
    "inrAmount": 5
  }
  ```
* **Validation Rules:**
  - `inrAmount`: Required integer, $1 \le \text{inrAmount} \le 10$. Whole rupees only (no decimals).
  - Must not exceed available daily quota ($\text{DailyLimit} - \text{ReservedINR} - \text{ConsumedINR}$).
* **Idempotency Conflict Behavior:**
  - **Same Idempotency-Key + Same `inrAmount`:** Returns the existing top-up order (`201 Created` or `200 OK`).
  - **Same Idempotency-Key + Different `inrAmount`:** Rejected with `409 Conflict` (`IDEMPOTENCY_KEY_REUSED`).
* **Response `201 Created`:**
  ```json
  {
    "topupId": "018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
    "userId": "018f673a-4e2b-7f11-80a2-c3bfde34bb6b",
    "inrAmount": 5,
    "usdtAmount": "5000.0000000000",
    "exchangeRate": "1000.00",
    "status": "PAYMENT_PENDING",
    "provider": "MOCK",
    "providerOrderId": "order_mock_99214a7c",
    "reservationDate": "2026-09-10",
    "expiresAt": "2026-09-10T15:45:00Z",
    "createdAt": "2026-09-10T15:30:00Z"
  }
  ```
* **Common Errors:**
  - `400 Bad Request`: `{"code": "DAILY_LIMIT_EXCEEDED", "message": "requested top-up of ₹5 exceeds remaining daily allowance of ₹2", "availableInr": 2}`
  - `400 Bad Request`: `{"code": "INVALID_AMOUNT", "message": "inrAmount must be an integer between 1 and 10"}`
  - `400 Bad Request`: `{"code": "MISSING_IDEMPOTENCY_KEY", "message": "Idempotency-Key header is required"}`
  - `401 Unauthorized`: Missing or invalid JWT token.
  - `409 Conflict`: `{"code": "IDEMPOTENCY_KEY_REUSED", "message": "Idempotency-Key was already used with different request parameters"}`

---

### 1.2 GET `/api/v1/topups/daily-usage`
Returns the user's current daily top-up metrics, separating reserved and consumed amounts.

* **Authentication:** Bearer Access Token (Authenticated User)
* **Response `200 OK`:**
  ```json
  {
    "userId": "018f673a-4e2b-7f11-80a2-c3bfde34bb6b",
    "dailyLimitInr": 10,
    "reservedInr": 3,
    "consumedInr": 4,
    "availableInr": 3,
    "timezone": "Asia/Kolkata (IST)",
    "usageDate": "2026-09-10",
    "resetsAt": "2026-09-11T00:00:00+05:30"
  }
  ```

---

### 1.3 GET `/api/v1/topups/:id`
Queries the live status and execution timestamps of an existing top-up order.

* **Authentication:** Bearer Access Token (Authenticated User)
* **Response `200 OK`:**
  ```json
  {
    "topupId": "018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
    "inrAmount": 5,
    "usdtAmount": "5000.0000000000",
    "status": "COMPLETED",
    "provider": "MOCK",
    "providerOrderId": "order_mock_99214a7c",
    "paymentId": "pay_mock_1829abc",
    "createdAt": "2026-09-10T15:30:00Z",
    "paidAt": "2026-09-10T15:31:05Z",
    "completedAt": "2026-09-10T15:31:08Z"
  }
  ```
* **Common Errors:**
  - `404 Not Found`: Top-up ID does not exist or does not belong to caller.

---

## 2. Webhook Endpoints

### 2.1 POST `/api/v1/webhooks/payment/:provider`
Ingests asynchronous payment capture callbacks from payment providers (`mock`, `razorpay`, `cashfree`).

* **Authentication:** Provider-Specific Signature Verification via Headers. No JWT.
* **Path Parameter:**
  - `:provider`: `mock` | `razorpay` | `cashfree`
* **Mock Provider Required Headers:**
  - `X-Signature`: Hex-encoded HMAC-SHA256 signature generated over `timestamp + "." + rawBody`.
  - `X-Timestamp`: Unix epoch timestamp string (seconds).
* **Razorpay Provider Required Headers:**
  - `X-Razorpay-Signature`: HMAC-SHA256 signature generated over `rawBody`.
* **Generic Request Body:**
  ```json
  {
    "eventId": "evt_018f673a_89bc",
    "eventType": "payment.captured",
    "providerOrderId": "order_mock_99214a7c",
    "paymentId": "pay_mock_1829abc",
    "amountInr": 5,
    "currency": "INR",
    "status": "SUCCESS",
    "paidAt": "2026-09-10T15:31:05Z"
  }
  ```

### Strict Webhook Security & Verification Pipeline:

```
1. Receive Request ──► Read Raw Body & Provider Headers
                            │
2. Cryptographic Check ◄────┘
   • Calculate Expected Signature
   • subtle.ConstantTimeCompare(expected, received)
   • If MISMATCH:
       → Write webhook_events (signature_valid=FALSE, status='FAILED')
       → Return 401 Unauthorized immediately.
                            │
3. Replay Window Check ◄────┘
   • Check |now - timestamp| <= 300s
   • If EXPIRED:
       → Write webhook_events (signature_valid=FALSE, status='FAILED')
       → Return 401 Unauthorized immediately.
                            │
4. Deduplication Check ◄────┘
   • INSERT INTO webhook_events (provider, event_id, signature_valid=TRUE, status='RECEIVED')
     ON CONFLICT (provider, event_id) WHERE signature_valid = TRUE DO NOTHING
   • If RowsAffected == 0:
       → Return 200 OK {"status": "ALREADY_PROCESSED"}
                            │
5. Payload Verification ◄───┘ (CRITICAL)
   • Fetch topup_order by (provider, provider_order_id) FOR UPDATE
   • Verify: provider == expected provider
   • Verify: currency == "INR"
   • Verify: payment status is captured/successful
   • Verify: webhook amountInr == topup_order.inr_amount (EXACT MATCH!)
   • Note: Server uses persisted inr_amount to credit USDT (Never trusts client/webhook USDT calculation)
                            │
6. Atomic Quota & State Tx ─┘
   • Begin DB Transaction:
       → Update order status = 'PAYMENT_CONFIRMED' (transient in-transaction intermediate state)
       → Commit quota for paid_at date (Same Day vs Cross-Midnight)
       → If quota ok: update order status = 'CREDIT_PENDING'
       → If cross-midnight quota exhausted: update order status = 'REFUND_REQUIRED' (consumed quota never incremented)
       → Update webhook_events SET status = 'PROCESSED', processed_at = NOW()
   • Commit DB Transaction
   • (Note: On payload validation failure in Step 5: updates webhook_events SET status = 'FAILED', error_message = reason, processed_at = NOW())
                            │
7. Fast Webhook Response ◄──┘
   • Return 200 OK {"status": "CONFIRMED", "topupId": "..."} immediately!
   • (WalletService.DepositFunds is executed asynchronously by the reconciler worker)
```

* **Response `200 OK` (Payment Confirmed & Queued for Credit):**
  ```json
  {
    "status": "CONFIRMED",
    "topupId": "018f673a-4e2b-7f11-80a2-c3bfde34aa5a"
  }
  ```
* **Response `200 OK` (Duplicate Acknowledged):**
  ```json
  {
    "status": "ALREADY_PROCESSED"
  }
  ```
* **Common Errors:**
  - `400 Bad Request`: `{"code": "AMOUNT_MISMATCH", "message": "webhook amount does not match order amount"}`
  - `401 Unauthorized`: `{"code": "INVALID_SIGNATURE", "message": "signature verification failed"}`
  - `401 Unauthorized`: `{"code": "WEBHOOK_EXPIRED", "message": "webhook timestamp is older than 5 minutes"}`
  - `404 Not Found`: `{"code": "ORDER_NOT_FOUND", "message": "order does not exist for provider_order_id"}`
