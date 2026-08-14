# 📘 Educational Guide: Gateway Common Utilities (`internal/handler/common`)

> **Package:** `tradedrift/services/gateway/internal/handler/common`  
> **Directory:** `services/gateway/internal/handler/common/`  
> **Primary Design Patterns:** Adapter Pattern, DRY (Don't Repeat Yourself), Distributed Tracing, Error Translation

---

## 1. 🎯 Why Does the `common` Folder Exist?

In a microservices architecture, the **API Gateway** acts as the front door. It receives external **HTTP/REST requests** from browsers and mobile apps and translates them into internal **gRPC calls** to backend services (`Auth`, `Wallet`, `Order`, `Market`).

Without a `common` package, every single domain handler would have to repeatedly implement:
1. Creating timeouts so a slow backend service doesn't crash the Gateway.
2. Manually extracting and attaching distributed tracing IDs to gRPC calls.
3. Translating internal gRPC status codes (`codes.NotFound`, `codes.Unauthenticated`) into HTTP status codes (`404`, `401`).
4. Parsing protobuf timestamps into standard JSON date strings.

The `common` package centralizes these cross-cutting concerns into a reusable layer so domain handlers remain lightweight, clean, and focused solely on their specific business logic.

---

## 2. 📂 Deep-Dive into the Files

```
services/gateway/internal/handler/common/
├── context.go    <-- Distributed Tracing & Timeout Enforcement
├── errors.go     <-- Protocol Error Translation (gRPC ➔ HTTP REST)
└── dto.go        <-- Shared Data Formatting & Nil-Safe Timestamp Utilities
```

---

## 3. 🔍 File 1: `context.go` — Distributed Tracing & Resilience

### ❓ Why Is It Needed?
When a user clicks **"Place Order"**, that single HTTP request might touch:
`API Gateway` ➔ `Order Service` ➔ `Wallet Service` ➔ `PostgreSQL`.

If an error happens or an order is delayed, how do engineers find the logs for that exact request across 4 different servers?

### 🛠️ The Solution: Context Metadata Propagation
`context.go` provides `OutgoingCtx`:

```go
func OutgoingCtx(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	md := metadata.Pairs(
		MetadataRequestID, middleware.RequestIDFromContext(r.Context()),
	)
	return metadata.NewOutgoingContext(ctx, md), cancel
}
```

### 🌟 Key Features Explained:

#### 1. Distributed Tracing via `x-request-id`
* The Gateway extracts the `x-request-id` UUID from the HTTP request context.
* Using `metadata.Pairs(...)` and `metadata.NewOutgoingContext(...)`, it injects this ID into the **binary gRPC transport headers**.
* When the `Order` or `Wallet` service receives the gRPC call, it reads the same `x-request-id` and includes it in all its Zap logs.
* **Benefit:** You can search `x-request-id: "a1b2c3d4"` in Datadog/Elasticsearch and see the complete journey of the request across all microservices.

#### 2. Strict Per-Call Timeouts (5s)
* If the backend database locks or network slows down, an HTTP connection shouldn't hang forever.
* `context.WithTimeout(r.Context(), timeout)` enforces a strict deadline. If the downstream service doesn't reply within 5 seconds, the context is cancelled, freeing up gateway threads and memory.

```
[Browser] ──HTTP (Header: x-request-id: 123)──► [API Gateway]
                                                     │
                                             OutgoingCtx (5s timeout)
                                             Metadata: x-request-id=123
                                                     │
                                                     ▼
                                          [gRPC Microservice]
                                       (Logs with x-request-id: 123)
```

---

## 4. 🔍 File 2: `errors.go` — Protocol Translation & Information Protection

### ❓ Why Is It Needed?
Backend microservices speak **gRPC**, which communicates errors using standard integer codes (`codes.Code`). 

External REST clients (React, Flutter, iOS) speak **HTTP**, which communicates errors using status codes (`400`, `401`, `404`, `500`) and JSON error bodies.

`errors.go` acts as an **Adapter**, bridging the two protocols.

```go
func WriteGRPCError(w http.ResponseWriter, err error) { ... }
```

### 🌟 Key Features Explained:

#### 1. Protocol Mapping Table

| Downstream gRPC Code | Meaning in Microservice | Translated HTTP Status | TradeDrift Error Code |
| :--- | :--- | :--- | :--- |
| `codes.InvalidArgument` | Malformed parameters / bad validation | `400 Bad Request` | `"INVALID_ARGUMENT"` |
| `codes.Unauthenticated` | Missing or invalid auth token | `401 Unauthorized` | `"UNAUTHENTICATED"` |
| `codes.PermissionDenied` | User does not own the resource | `403 Forbidden` | `"PERMISSION_DENIED"` |
| `codes.NotFound` | User, Order, or Market not in database | `404 Not Found` | `"NOT_FOUND"` |
| `codes.AlreadyExists` | Duplicate email / duplicate order key | `409 Conflict` | `"ALREADY_EXISTS"` |
| `codes.FailedPrecondition` | Insufficient balance / frozen wallet | `422 Unprocessable Entity` | `"FAILED_PRECONDITION"` |
| `codes.Internal` / other | Database down / unexpected error | `500 Internal Server Error` | `"INTERNAL_ERROR"` |

#### 2. Preventing Internal Information Leaks (Security)
If PostgreSQL crashes with `pq: relation "users" does not exist`, returning that raw database error to a public hacker exposes database table names and structure. `WriteGRPCError` sanitizes internal errors into a clean, safe JSON envelope:

```json
{
  "success": false,
  "error": {
    "code": "INTERNAL_ERROR",
    "message": "an unexpected error occurred"
  }
}
```

---

## 5. 🔍 File 3: `dto.go` — Safe Data Transformation & Formatting

### ❓ Why Is It Needed?
gRPC uses Protobuf messages where timestamps are represented as `google.protobuf.Timestamp` struct pointers containing seconds and nanoseconds.

In web/mobile frontends, JavaScript expects standard **ISO8601 / RFC3339 date strings** (e.g. `"2026-08-14T10:00:00Z"`).

### 🛠️ The Solution: `FormatTimestamp`
```go
func FormatTimestamp(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	return ts.AsTime().Format(time.RFC3339)
}
```

### 🌟 Key Features Explained:
1. **Nil Pointer Safety:** In Go, calling `.AsTime()` on a `nil` pointer causes a `runtime panic: invalid memory address or nil pointer dereference`. `FormatTimestamp` safely returns `""` if the timestamp was optional or not set by the microservice.
2. **Standardized RFC3339 Output:** Guarantees every date sent to frontend clients (order creation dates, token expirations, candle start times) uses the exact same UTC timezone format.
3. **`SuccessResponse` Envelope:** Standard JSON struct for endpoints that return simple confirmation messages (`{"success": true}`).

---

## 6. 🎓 Educational Summary

| File | Core Responsibility | Real-World Benefit |
| :--- | :--- | :--- |
| **`context.go`** | Distributed tracing & per-call timeouts | Enables cross-service log debugging and stops cascading server crashes. |
| **`errors.go`** | gRPC-to-HTTP error status code mapping | Converts microservice errors into user-friendly HTTP JSON while shielding DB internals. |
| **`dto.go`** | Protobuf timestamp & response formatting | Prevents nil-pointer panics and standardizes UTC date representations for web apps. |
