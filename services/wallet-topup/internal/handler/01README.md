# HTTP Transport Layer Architecture (`internal/handler`)

The `internal/handler` package implements the **HTTP REST API & Webhook Ingestion Layer** of the Wallet Top-Up microservice. It translates external HTTP requests into internal domain service calls, enforces authentication, standardizes camelCase JSON payloads, and maps domain errors to precise RFC-compliant HTTP status codes.

---

## 1. Directory Structure

```
internal/handler/
├── dto.go                 # Data Transfer Objects (camelCase client contracts)
├── middleware.go          # Auth enforcement (X-User-ID) and structured latency logging
├── router.go              # Standard library HTTP router (Go 1.22+ method routing)
├── topup_handler.go       # Endpoints: POST /topups, GET /daily-usage, GET /topups/{id}
├── webhook_handler.go     # Endpoint: POST /webhooks/payment/{provider}
└── README.md              # This architectural and technical documentation
```

---

## 2. File-by-File Purpose & Problem Breakdown

### File 1: `dto.go`

#### What Problems Does This File Solve?
1. **Decouples Internal Database Models from Public API Contracts**:
   Internal PostgreSQL columns use `snake_case` (e.g. `inr_amount`, `provider_order_id`), whereas mobile apps and web frontends consume standardized `camelCase` JSON (e.g. `inrAmount`, `providerOrderId`). DTOs insulate public clients from internal database refactorings.
2. **Standardized ISO-8601 Timestamps**:
   Converts Go `time.Time` values into RFC3339 strings (`2026-09-11T14:30:00Z`).

#### Key DTOs & Mappers:
- `CreateTopUpRequest`: Accepts `{ "inrAmount": 10 }`.
- `CreateTopUpResponseDTO`: Formats order confirmation with exchange rate (`1000 USDT/INR`) and expiration timestamp.
- `ToTopUpDTO(order)`: Pure converter function from `*domain.TopUpOrder` to `*CreateTopUpResponseDTO`.
- `DailyUsageResponseDTO` & `ToDailyUsageDTO(usage)`: Formats daily usage metrics and midnight reset time (`resetsAt`).

---

### File 2: `middleware.go`

#### What Problems Does This File Solve?
1. **User Identity Injection (`RequireAuth`)**:
   Extracts `X-User-ID` from the HTTP header, validates that it is non-empty, and injects it into `r.Context()`. Downstream handlers retrieve the authenticated `userID` via `UserIDFromContext(r.Context())`. If missing, returns `HTTP 401 Unauthorized`.
2. **Observability (`LoggingMiddleware`)**:
   Instruments every incoming HTTP request with microsecond-level latency, HTTP method, and path using `zap.Logger`.
3. **Safe JSON Serialization (`writeJSON`)**:
   Centralizes setting `Content-Type: application/json`, writing status codes, and encoding payloads.

---

### File 3: `topup_handler.go`

#### What Problems Does This File Solve?
1. **Idempotency Key Enforcement**:
   Ensures `X-Idempotency-Key` header is present. Rejects requests lacking it with `HTTP 400 Bad Request`.
2. **Dual-Key Amount Compatibility**:
   Accepts both `inrAmount` and legacy `inr_amount` in JSON request bodies to prevent breaking older clients.
3. **RFC-Compliant Domain Error Translation**:
   Maps internal domain errors to exact HTTP status codes:

| Domain Error | HTTP Status | Response Code / Body |
| :--- | :--- | :--- |
| `ErrInvalidAmount`, `ErrInvalidIdempotencyKey` | `400 Bad Request` | `{"error": "..."}` |
| `ErrUnauthorized` | `401 Unauthorized` | `{"error": "unauthorized"}` |
| `ErrOrderNotFound` | `404 Not Found` | `{"error": "top-up order not found"}` |
| `ErrIdempotencyConflict` | `409 Conflict` | `{"code": "IDEMPOTENCY_KEY_REUSED", "error": "..."}` |
| `ErrDailyLimitExceeded` | `429 Too Many Requests` (or 422) | `{"code": "DAILY_LIMIT_EXCEEDED", "error": "..."}` |
| Internal / DB Errors | `500 Internal Server Error` | `{"error": "failed to create top-up order"}` |

---

### File 4: `webhook_handler.go`

#### What Problems Does This File Solve?
1. **Denial-of-Service (DOS) Body Memory Protection**:
   Webhooks are public endpoints. An attacker could stream a 5 GB file to exhaust server RAM. `webhook_handler.go` enforces a strict 64 KB limit:
   ```go
   r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
   ```
2. **Multi-Gateway Header Tolerance**:
   Extracts signatures from standard `X-Webhook-Signature` or provider-specific headers (e.g. `X-Razorpay-Signature`).
3. **Timestamp Validation**:
   Parses `X-Webhook-Timestamp` as epoch integer and ensures it is positive.
4. **Fast Gateway Acknowledgement**:
   Returns `HTTP 200 OK` with `{"status": "SUCCESS"}` upon successful verification.

---

### File 5: `router.go`

#### What Problems Does This File Solve?
1. **Zero External Router Dependencies**:
   Uses Go 1.22+ standard library `http.NewServeMux()` with native method routing (`"POST /api/v1/topups"`) and wildcards (`"{id}"`, `"{provider}"`).
2. **Centralized Route Definition**:

| Method | Path | Middleware | Handler |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/v1/topups` | `RequireAuth` | `HandleCreateTopUp` |
| `GET` | `/api/v1/topups/daily-usage` | `RequireAuth` | `HandleGetDailyUsage` |
| `GET` | `/api/v1/topups/{id}` | `RequireAuth` | `HandleGetTopUpByID` |
| `POST` | `/api/v1/webhooks/payment/{provider}` | None (Signed) | `HandleProviderWebhook` |
| `GET` | `/health` | None | Anonymous Health Check |

---

## 3. Why We Need Specific Packages

| Package | Purpose & Problem Solved |
| :--- | :--- |
| `net/http` | Standard HTTP server, multiplexer, and status code constants. |
| `encoding/json` | Streaming JSON decoding and response encoding. |
| `io` | Reads raw request body (`io.ReadAll`) for cryptographic signature verification. |
| `strconv` | Parses timestamp string headers to `int64`. |
| `go.uber.org/zap` | Zero-allocation structured logging for HTTP traffic. |
