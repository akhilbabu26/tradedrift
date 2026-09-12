# HTTP Transport & Middleware Layer (`internal/handler`)

This document provides a comprehensive architectural and operational guide to the HTTP transport layer located in [`services/admin/internal/handler/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/).

---

## Table of Contents

1. [Package Overview & Purpose](#1-package-overview--purpose)
2. [What Problems This Package Solves](#2-what-problems-this-package-solves)
3. [File-by-File Deep Dive](#3-file-by-file-deep-dive)
   - [router.go (HTTP Multiplexer & Route Registry)](#routergo-http-multiplexer--route-registry)
   - [admin_handler.go (Administrative Mutation Handlers)](#admin_handlergo-administrative-mutation-handlers)
   - [health_handler.go (Liveness, Readiness & System Diagnostics)](#health_handlergo-liveness-readiness--system-diagnostics)
   - [middleware.go (Security, Idempotency & Observability)](#middlewarego-security-idempotency--observability)
   - [dto.go (Data Transfer Objects)](#dtogo-data-transfer-objects)
   - [validation.go (Strict Input Validators)](#validationgo-strict-input-validators)
4. [Critical Transport & Observability Mechanisms](#4-critical-transport--observability-mechanisms)
   - [Route Normalization (Preventing Cardinality Explosion)](#route-normalization-preventing-cardinality-explosion)
   - [Sub-Millisecond Cached System Health](#sub-millisecond-cached-system-health)
   - [Atomic Shutdown Traffic Drainage (Readiness 503)](#atomic-shutdown-traffic-drainage-readiness-503)
5. [Architectural & Execution Flows](#5-architectural--execution-flows)
   - [Flow 1: Inbound Administrative Request Middleware Pipeline](#flow-1-inbound-administrative-request-middleware-pipeline)
   - [Flow 2: Triple-Tier Health Check Routing Flow](#flow-2-triple-tier-health-check-routing-flow)
   - [Flow 3: Prometheus Route Normalization Flow](#flow-3-prometheus-route-normalization-flow)
6. [API Endpoints Reference Table](#6-api-endpoints-reference-table)

---

## 1. Package Overview & Purpose

The `internal/handler` package forms the **HTTP Transport Adapter** of the TradeDrift Admin Service under Clean Architecture principles.

It acts as the system's frontline boundary:
1. Listens for HTTP requests using Go 1.22+ enhanced `http.ServeMux` routing.
2. Validates incoming JWT tokens and asserts strict administrator privileges (`role == "admin"`).
3. Enforces required HTTP headers (`Idempotency-Key`, `X-Request-ID`).
4. Sanitizes dynamic path parameters and JSON request bodies using strict regular expressions.
5. Adapts transport-level requests into domain commands and delegates execution to `AdminService` or `HealthWorker`.
6. Translates domain sentinel errors into standardized REST status codes and JSON error envelopes.
7. Emits structured JSON access logs and Prometheus metrics.

---

## 2. What Problems This Package Solves

| Problem | Failure Scenario Without Handler Layer | How `internal/handler` Solves It |
| :--- | :--- | :--- |
| **Unauthorized Administrative Actions** | Rogue users or internal services invoke high-privilege endpoints (e.g. halting BTC market) without authentication. | `RequireAdmin` middleware validates HMAC-SHA256 JWT signatures and verifies the `role == "admin"` claim. |
| **Duplicate Requests & Replay Storms** | Network timeouts cause clients to retry mutations, risking double suspensions or state collisions. | `RequireIdempotencyKey` middleware rejects mutation requests lacking an `Idempotency-Key` header (HTTP 400). |
| **Prometheus Memory Crash (Cardinality Explosion)** | Recording metrics with raw URL paths (e.g. `/users/123e4567-e89b.../suspend`) creates millions of unique time series in Prometheus. | `NormalizeRoute` maps dynamic paths to static templates (`/api/v1/admin/users/{user_id}/suspend`). |
| **Slow Diagnostics & Cascading Timeouts** | Calling `/system/health` probes all 8 microservices synchronously inside the HTTP handler, causing 5-second hangs. | `HandleSystemHealth` reads directly from `HealthWorker`'s cached in-memory snapshot in **< 1ms**. |
| **Traffic Dropped During Deployments** | Kubernetes sends `SIGTERM` and continues routing HTTP requests while the pod is shutting down. | `SetShuttingDown()` causes `/ready` to instantly return **HTTP 503**, prompting ingress balancers to route traffic away immediately. |
| **Garbage / Malformed Data Injection** | Attackers send malformed market symbols (e.g. `DROP TABLE`) or invalid UUIDs. | `validation.go` enforces strict regex checks (`^[A-Z0-9]{2,10}-[A-Z0-9]{2,10}$`) before services execute. |

---

## 3. File-by-File Deep Dive

### `router.go` (HTTP Multiplexer & Route Registry)
- **File**: [`router.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/router.go)
- **Primary Function**: `NewRouter`

#### 1. Purpose
Assembles the complete HTTP routing tree using Go 1.22+ method-and-path matching (`"GET /health"`, `"POST /api/v1/admin/markets/{market_id}/halt"`), wiring appropriate middleware chains for each endpoint.

#### 2. Architecture & Hierarchy
```go
func NewRouter(...) http.Handler {
    mux := http.NewServeMux()
    adminAuth := RequireAdmin(jwtValidator)

    // Unauthenticated: /health, /ready, /metrics
    // Authenticated System Health: /api/v1/admin/system/health (adminAuth)
    // Mutation Endpoints: /api/v1/admin/... (adminAuth + RequireIdempotencyKey)

    // Outer global wrappers:
    return StructuredLoggingMiddleware(log)(MetricsMiddleware()(mux))
}
```

---

### `admin_handler.go` (Administrative Mutation Handlers)
- **File**: [`admin_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/admin_handler.go)
- **Primary Struct**: `AdminHandler`

#### Handlers Breakdown

| Method | Endpoint Pattern | Description |
| :--- | :--- | :--- |
| `HandleSuspendUser` | `POST /api/v1/admin/users/{user_id}/suspend` | Validates UUID, extracts reason, calls `adminSvc.SuspendUser`. |
| `HandleUnsuspendUser` | `POST /api/v1/admin/users/{user_id}/unsuspend` | Validates UUID, calls `adminSvc.UnsuspendUser`. |
| `HandleFreezeWallet` | `POST /api/v1/admin/users/{user_id}/wallets/{asset}/freeze` | Validates user UUID and asset code (e.g. `BTC`), calls `adminSvc.FreezeWallet(freeze=true)`. |
| `HandleUnfreezeWallet` | `POST /api/v1/admin/users/{user_id}/wallets/{asset}/unfreeze` | Validates user UUID and asset code, calls `adminSvc.FreezeWallet(freeze=false)`. |
| `HandleHaltMarket` | `POST /api/v1/admin/markets/{market_id}/halt` | Validates market pair (e.g. `BTC-USDT`), calls `adminSvc.HaltMarket`. |
| `HandleResumeMarket` | `POST /api/v1/admin/markets/{market_id}/resume` | Validates market pair, calls `adminSvc.ResumeMarket`. |
| `handleServiceError` | *(Internal helper)* | Maps domain errors to HTTP 400, 409, 503, or 500. |

---

### `health_handler.go` (Liveness, Readiness & System Diagnostics)
- **File**: [`health_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/health_handler.go)
- **Primary Struct**: `HealthHandler`

#### Handlers Breakdown

| Method | Route | Auth Required | Purpose & Semantics |
| :--- | :--- | :--- | :--- |
| `HandleLiveness` | `GET /health` | No | Simple probe returning `{"status":"ok"}` with HTTP 200 as long as the process is alive. |
| `HandleReadiness` | `GET /ready` | No | Verifies Admin Service direct dependencies (PostgreSQL, Kafka brokers, Auth, Wallet). Returns **HTTP 503** if `isShuttingDown` is true or if any direct dependency is unreachable. |
| `HandleSystemHealth` | `GET /api/v1/admin/system/health` | Yes (`admin`) | Serves comprehensive platform-wide health diagnostic report cached by `HealthWorker`. Completes in sub-millisecond time without on-demand network probing. |

---

### `middleware.go` (Security, Idempotency & Observability)
- **File**: [`middleware.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/middleware.go)

#### Middleware Components

1. **`RequireAdmin`**:
   - Parses the `Authorization: Bearer <token>` header.
   - Validates HMAC signature via `platformjwt.HMACValidator`.
   - Asserts `claims.Role == "admin"`.
   - Injects `admin_id` and `request_id` into the request context.
2. **`RequireIdempotencyKey`**:
   - Checks for `Idempotency-Key` header on mutation endpoints.
   - Verifies length between 1 and 128 characters.
   - Injects key into request context.
3. **`StructuredLoggingMiddleware`**:
   - Captures status code and request execution duration using `statusRecorder`.
   - Emits structured JSON log with method, path, status, duration, IP, user-agent, `admin_id`, and `request_id`.
4. **`MetricsMiddleware`**:
   - Records request count and latency histograms in Prometheus.
   - Uses `NormalizeRoute()` to ensure bounded label cardinality.
5. **`NormalizeRoute(pattern, path string)`**:
   - Normalizes dynamic URL paths containing UUIDs, market pairs, or assets into parameterized route templates.

---

### `dto.go` (Data Transfer Objects)
- **File**: [`dto.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/dto.go)

Defines JSON request and response payloads:
- **Request DTOs**: `SuspendUserRequest`, `UnsuspendUserRequest`, `FreezeWalletRequest`, `UnfreezeWalletRequest`, `HaltMarketRequest`, `ResumeMarketRequest`. Each enforces a required `reason` string.
- **Response DTO**: `OperationResponseDTO`:
  ```json
  {
    "operation_id": "0191eb2a-...",
    "admin_id": "0191eb2a-...",
    "operation_type": "HALT_MARKET",
    "target_id": "BTC-USDT",
    "status": "COMPLETED",
    "response": { "market_id": "BTC-USDT", "is_halted": true },
    "created_at": "2026-09-12T12:00:00Z"
  }
  ```
- **Converter**: `ToOperationDTO(op *domain.AdminOperation)` converts internal domain entities to client-safe DTOs.

---

### `validation.go` (Strict Input Validators)
- **File**: [`validation.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/validation.go)

| Function | Validation Rule | Example Valid | Example Invalid |
| :--- | :--- | :--- | :--- |
| `ValidateReason` | 5 to 500 characters after whitespace trimming. | `"Suspected wash trading activity"` | `"abc"` (too short) |
| `ValidateUserID` | Standard UUID format (`uuid.Parse`). | `"0191eb2a-7c90-7d31-9f12-..."` | `"user-123"` |
| `ValidateAsset` | `^[A-Z0-9]{2,10}$` | `"BTC"`, `"USDT"`, `"INR"` | `"btc"`, `"X"` |
| `ValidateMarketID` | `^[A-Z0-9]{2,10}-[A-Z0-9]{2,10}$` | `"BTC-USDT"`, `"ETH-INR"` | `"BTC_USDT"`, `"INVALID"` |

---

## 4. Critical Transport & Observability Mechanisms

### Route Normalization (Preventing Cardinality Explosion)
If an attacker or monitoring script makes 100,000 requests to `/api/v1/admin/users/<random_uuid>/suspend`, recording raw URLs creates 100,000 unique Prometheus metric series, consuming gigabytes of RAM.

`NormalizeRoute` maps all such paths to:
```
/api/v1/admin/users/{user_id}/suspend
```
Result: **Exactly 1 time series in Prometheus** regardless of how many distinct users are suspended.

---

### Sub-Millisecond Cached System Health
The `/api/v1/admin/system/health` endpoint serves high-level platform telemetry. Rather than probing 8 microservices, Kafka brokers, and database pools during the HTTP request (which takes 50ms - 5s), it reads the cached diagnostic snapshot stored in `HealthWorker`:

```go
latest := h.healthWorker.GetLatestHealth()
writeJSON(w, http.StatusOK, latest)
```
Result: HTTP response latency is **< 1ms**, completely decoupled from downstream network latency.

---

### Atomic Shutdown Traffic Drainage (Readiness 503)
When `SIGTERM` is received, `main.go` invokes:
```go
healthHandler.SetShuttingDown()
```
Internally, `isShuttingDown.Store(true)` ensures `/ready` returns HTTP 503 immediately. This prevents Kubernetes from sending new ingress traffic to this instance while in-flight requests finish processing.

---

## 5. Architectural & Execution Flows

### Flow 1: Inbound Administrative Request Middleware Pipeline

```
                         Admin Client Request
                                  │
                                  ▼
                     StructuredLoggingMiddleware
                                  │
                                  ▼
                          MetricsMiddleware
                      (Start Latency Stopwatch)
                                  │
                                  ▼
                         RequireAdmin (JWT)
                      /                      \
             (Invalid)                        (Role == "admin")
                ▼                                     ▼
         HTTP 401 / 403                     RequireIdempotencyKey
                                            /                  \
                                   (Missing/TooLong)          (Valid)
                                          ▼                      ▼
                                     HTTP 400 BadReq       AdminHandler
                                                                 │
                                                           AdminService
                                                                 │
                                                        HTTP 200 OK (DTO)
                                                                 │
                                                                 ▼
                                                        Metrics Recorded &
                                                        Structured Logged
```

---

### Flow 2: Triple-Tier Health Check Routing Flow

```
                       Incoming HTTP Request
                                 │
         ┌───────────────────────┼───────────────────────┐
         ▼                       ▼                       ▼
    GET /health              GET /ready            GET /system/health
  (Liveness Probe)        (Readiness Probe)        (Diagnostic Report)
         │                       │                       │
         ▼                       ▼                       ▼
   Process Alive?         isShuttingDown?            RequireAdmin
    /          \           /           \               (JWT Auth)
 (Yes)         (No)     (Yes)          (No)              │
  ▼             ▼        ▼              ▼                ▼
200 OK        503      503        Direct Dependency  HealthWorker Cache
                      Shutdown         Probes         (In-Memory Snapshot)
                                    /          \         │
                                (All UP)    (Any DOWN)   ▼
                                   ▼           ▼       200 OK
                                200 OK       503     (Full Metrics JSON)
```

---

### Flow 3: Prometheus Route Normalization Flow

```
 Raw Request Path: /api/v1/admin/users/0191eb2a-7c90-7d31-9f12/suspend
                                │
                                ▼
                       NormalizeRoute(path)
                                │
                                ▼
  Parameterized Route Template: /api/v1/admin/users/{user_id}/suspend
                                │
                                ▼
    Prometheus Series Label (Zero Cardinality Explosion):
    tradedrift_admin_http_requests_total{route="/api/v1/admin/users/{user_id}/suspend"}
```

---

## 6. API Endpoints Reference Table

| HTTP Method | Path Pattern | Required Headers | Description |
| :--- | :--- | :--- | :--- |
| `GET` | `/health` | None | Kubernetes liveness probe. |
| `GET` | `/ready` | None | Kubernetes readiness probe (checks DB, Kafka, gRPC). |
| `GET` | `/metrics` | None | Prometheus telemetry scraping exposition. |
| `GET` | `/api/v1/admin/system/health` | `Authorization: Bearer <jwt>` | Platform diagnostic report cached by `HealthWorker`. |
| `POST` | `/api/v1/admin/users/{user_id}/suspend` | `Authorization`, `Idempotency-Key` | Suspend user account and enqueue session invalidation saga. |
| `POST` | `/api/v1/admin/users/{user_id}/unsuspend` | `Authorization`, `Idempotency-Key` | Restore suspended user account. |
| `POST` | `/api/v1/admin/users/{user_id}/wallets/{asset}/freeze` | `Authorization`, `Idempotency-Key` | Synchronously freeze user balance for a specific asset. |
| `POST` | `/api/v1/admin/users/{user_id}/wallets/{asset}/unfreeze` | `Authorization`, `Idempotency-Key` | Unfreeze user balance for a specific asset. |
| `POST` | `/api/v1/admin/markets/{market_id}/halt` | `Authorization`, `Idempotency-Key` | Halt trading on an active order book. |
| `POST` | `/api/v1/admin/markets/{market_id}/resume` | `Authorization`, `Idempotency-Key` | Resume trading on a halted order book. |
