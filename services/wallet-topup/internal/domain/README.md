# Domain Layer Architecture (`internal/domain`)

The `internal/domain` package is the **core heartbeat** of the Wallet Top-Up microservice. Following **Domain-Driven Design (DDD)** and **Clean Architecture** principles, this package is completely pure:
- It has **zero external dependencies** (only standard Go packages: `encoding/json`, `errors`, `time`).
- It defines the business entities, state invariants, and sentinel errors that the Repository, Service, and HTTP layers depend on.

---

## 1. File-by-File Purpose & Problem Breakdown

```
internal/domain/
├── errors.go      # Sentinel business errors enforcing system invariants
├── models.go      # Entity structs, status constants, and domain helper methods
└── README.md      # This architectural guide
```

---

### File 1: `internal/domain/errors.go`

#### What Problem Does This File Solve?
1. **Prevents Fragile String-Matching**: Instead of inspecting error messages with strings (e.g. `strings.Contains(err.Error(), "limit")`), calling layers (Service, HTTP, Reconciler) use typed sentinel comparisons (`errors.Is(err, domain.ErrDailyLimitExceeded)`).
2. **Standardizes HTTP Status Mapping**: Handlers can cleanly map each domain error to its exact RFC-compliant HTTP status code (e.g., 400 Bad Request, 401 Unauthorized, 404 Not Found, 409 Conflict, 422 Unprocessable Entity).
3. **Fences Distributed Worker Races**: Sentinel errors like `ErrStaleLease` signal when a background worker took too long, allowing the reconciler to halt cleanly before overwriting newer state.

#### Error Definitions & Their Purpose

| Sentinel Error | Problem It Solves | Which Layer Checks It? |
| :--- | :--- | :--- |
| `ErrDailyLimitExceeded` | Triggered when `reserved + consumed + amount > limit`. Halts top-up creation immediately. | Service & Tx Manager $\to$ HTTP 422 |
| `ErrIdempotencyConflict` | Client reused an existing idempotency key with a **different** amount or user. Prevents fraud / payload spoofing. | Service & Postgres $\to$ HTTP 409 |
| `ErrInvalidAmount` | Prevents non-standard amounts (e.g., negative, fractional, or exceeding ₹10 dev / ₹50k prod limit). | Service & Handler $\to$ HTTP 400 |
| `ErrOrderNotFound` | Returned when an order ID or provider order ID is not found in the database. | Service $\to$ HTTP 404 |
| `ErrOrderExpired` | Attempt to pay or verify an order that has passed `expires_at`. | Webhook / Expiry Worker |
| `ErrInvalidSignature` | Cryptographic HMAC-SHA256 signature verification failed. Blocks fake webhooks. | Webhook Verifier $\to$ HTTP 400 |
| `ErrWebhookReplay` | Webhook timestamp is outside the allowed drift window. Blocks replayed packets. | Webhook Verifier $\to$ HTTP 400 |
| `ErrStaleLease` | A background worker tried to finalize a wallet deposit, but its `claim_token` expired or was claimed by another worker. | Reconciler $\to$ Aborts update |
| `ErrOrderTerminalStatus` | Prevents modifying an order already in a terminal state (`COMPLETED`, `FAILED`, `EXPIRED`). | Service & Tx Manager |
| `ErrDuplicateWebhook` | Webhook event already processed. Allows idempotent HTTP 200 return to gateway. | Webhook Service $\to$ HTTP 200 |
| `ErrMissingIdempotency`| Rejects `POST /api/v1/topups` requests missing the `X-Idempotency-Key` header. | HTTP Middleware $\to$ HTTP 400 |
| `ErrUnauthorized` | User token missing or invalid. | HTTP Middleware $\to$ HTTP 401 |
| `ErrProviderMismatch` | Order's recorded provider does not match the webhook route (e.g. Stripe webhook for a Razorpay order). | Webhook Service $\to$ HTTP 400 |
| `ErrInvalidEventType` | Gateway sent an event we don't care about (e.g. `payment.failed` or `order.created`). | Webhook Service $\to$ HTTP 200 (Ignored) |
| `ErrInvalidIdempotencyKey` | Key is empty or exceeds 100 characters. Prevents DB B-Tree index bloat or DOS. | HTTP Handler $\to$ HTTP 400 |

---

### File 2: `internal/domain/models.go`

#### What Problem Does This File Solve?
1. **Decouples Database & gRPC Drivers**: The business logic works with `TopUpOrder` and `DailyTopUpLimit` structs, never with raw SQL rows or generated protobuf structs.
2. **Defines Single Source of Truth for State Constants**: Prevents typos like `"PAYMENT_PENDING"` vs `"payment_pending"`.
3. **Encapsulates Core Calculations**: Calculates remaining daily balances directly on the domain model, ensuring logic consistency across tests and APIs.

#### State Constants & Enums

##### 1. Order Status Lifecycle (`Status*`)
```
INITIATED ──> PAYMENT_PENDING ──> CREDIT_PENDING ──> CREDIT_PROCESSING ──> COMPLETED
    │                │                                      │
    ▼                ▼                                      ▼
  FAILED          EXPIRED                             REFUND_REQUIRED
```

- `StatusInitiated`: Quota has been reserved in PostgreSQL, but gateway order has not yet been generated.
- `StatusPaymentPending`: Gateway order created (`provider_order_id` assigned). Waiting for user to complete checkout.
- `StatusCreditPending`: Webhook received and verified (`payment.captured`). Quota converted from reserved to consumed. Ready for wallet deposit.
- `StatusCreditProcessing`: Background reconciler worker has claimed this order with a `claim_token` and `claim_until` lease.
- `StatusCompleted`: gRPC call to `WalletService.DepositFunds()` succeeded. USDT added to wallet. **Terminal state**.
- `StatusFailed`: Order creation or payment permanently failed. Quota released. **Terminal state**.
- `StatusExpired`: User did not pay within the 15-minute checkout window. Quota released. **Terminal state**.
- `StatusRefundRequired`: Money was captured, but repeated wallet ledger deposits failed irrevocably. Requires human intervention / refund.

##### 2. Webhook Status Lifecycle (`WebhookStatus*`)
- `WebhookStatusReceived`: Payload received and stored.
- `WebhookStatusProcessed`: Payment captured and top-up order moved to `CREDIT_PENDING`.
- `WebhookStatusIgnored`: Valid signature, but event type is unhandled (e.g. `order.paid` when we listen to `payment.captured`).
- `WebhookStatusFailed`: Webhook processing failed due to internal error or missing order.

---

## 2. Models & Methods In-Depth

### Model: `DailyTopUpLimit`
Represents the user's daily financial boundary for a given calendar date (`usage_date`).

```go
type DailyTopUpLimit struct {
	UserID      string    `json:"user_id"`
	UsageDate   string    `json:"usage_date"` // YYYY-MM-DD
	LimitINR    int64     `json:"limit_inr"`
	ReservedINR int64     `json:"reserved_inr"`
	ConsumedINR int64     `json:"consumed_inr"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
```

#### Function: `RemainingINR() int64`
- **What it does**: Computes `LimitINR - (ReservedINR + ConsumedINR)`.
- **Why it matters**: If concurrent requests or clock skews cause reserved + consumed to reach limit, it returns `0` (never a negative number). This powers the `GET /api/v1/topups/daily-usage` endpoint.

---

### Model: `TopUpOrder`
Represents the entire lifecycle of a top-up request.

| Field | Type | Architectural Purpose |
| :--- | :--- | :--- |
| `ID` | `string` (UUID) | Internal primary key. |
| `UserID` | `string` (UUID) | User requesting the deposit. |
| `IdempotencyKey` | `string` | Unique per user. Prevents double-charging. |
| `INRAmount` | `int64` | Fiat amount in paise (or ₹1-₹10). |
| `USDTAmount` | `string` | Converted simulated USDT (1 INR = 1000 USDT). Formatted as high-precision decimal string to prevent floating-point inaccuracies. |
| `ReservationDate` | `string` | The calendar date (`YYYY-MM-DD`) against which daily quota was reserved. Crucial for cross-midnight releases! |
| `Provider` | `string` | E.g. `"razorpay"`. |
| `ProviderOrderID` | `*string` | The ID returned by Razorpay (e.g., `order_EKZ98Z`). |
| `PaymentID` | `*string` | The gateway payment capture ID (e.g., `pay_FL1234`). |
| `Status` | `string` | Current state from the state machine. |
| `ClaimToken` | `*string` | Random UUID generated by the background worker that holds the lock on this order. |
| `ClaimUntil` | `*time.Time` | Timestamp when the worker's lease expires. If worker crashes, another worker picks it up after this time. |
| `AttemptCount` | `int` | Number of reconciliation attempts to Wallet service. |
| `LastError` | `*string` | Diagnostics for failed attempts. |
| `ExpiresAt` | `time.Time` | Checkout expiry timestamp (now + 15 min). |
| `CompletedAt` | `*time.Time` | Timestamp when wallet balance was credited. |

---

### Model: `WebhookEvent`
Represents the audit log entry for payment gateway webhooks.

| Field | Type | Architectural Purpose |
| :--- | :--- | :--- |
| `ID` | `string` (UUID) | Event log primary key. |
| `Provider` | `string` | Payment gateway name (`razorpay`). |
| `EventID` | `string` | Unique event identifier from the provider header/body. |
| `PaymentID` | `*string` | Associated payment ID if present in payload. |
| `SignatureValid` | `bool` | `true` if HMAC signature matches; `false` if forged/tampered. |
| `Payload` | `json.RawMessage` | Untouched, byte-for-byte incoming JSON payload for legal and debugging audits. |
| `Status` | `string` | Processing state (`RECEIVED`, `PROCESSED`, `IGNORED`, `FAILED`). |
| `ErrorMessage` | `*string` | Detailed error if processing failed. |
| `ReceivedAt` | `time.Time` | Timestamp when our HTTP server received the request. |
| `ProcessedAt` | `*time.Time` | Timestamp when processing finished. |
