# Gateway Middleware

This package contains all HTTP middleware used by the API Gateway.
Middleware sits between the router and the handler — it runs on every matching request before the handler executes.

---

## What is Middleware?

```
HTTP Request
     │
     ▼
[ Recovery  ]   ← outermost: catch any panic below this point
     │
     ▼
[ RequestID ]   ← tag every request with a unique ID
     │
     ▼
[ Logger    ]   ← log method, path, status, duration immediately
     │
     ▼
[ CORS      ]   ← allow/deny cross-origin browser requests
     │
     ▼
[ RateLimit ]   ← reject too-frequent requests per IP (applied per route)
     │
     ▼
[ Auth      ]   ← validate JWT (applied only to protected routes)
     │
     ▼
[ Handler   ]   ← your actual business logic
     │
     ▼
HTTP Response
```

Each middleware either:
- **Passes** the request to the next layer (`next.ServeHTTP`)
- **Stops** the request and returns an error (`return` after `WriteError`)

---

## External Packages Used

### `golang.org/x/time/rate`

**What it is:** An official Go extended library that implements the **token bucket algorithm** for rate limiting.

**Why not implement it ourselves?**
The token bucket algorithm requires:
- Atomic counter management across concurrent goroutines
- Precise time-based token refill logic
- Thread-safe burst handling

This is error-prone to implement correctly. `golang.org/x/time/rate` is maintained by the Go team, battle-tested, and used in production systems worldwide.

**What it provides:**

| Type/Function | Purpose |
|---|---|
| `rate.Limit` | A float64 representing tokens-per-second |
| `rate.Every(d)` | Converts a duration to a rate (e.g. `rate.Every(time.Minute/5)` = 5/min) |
| `rate.NewLimiter(limit, burst)` | Creates a new token bucket limiter |
| `limiter.Allow()` | Returns true if a token is available right now |
| `limiter.Reserve()` | Reserves a future token (for systems that will wait) |

**Token bucket in plain English:**
```
Bucket has 5 tokens (burst=5)
New token added every 12 seconds (rate = 5/min)

Request 1: take 1 token → 4 remaining ✅
Request 2: take 1 token → 3 remaining ✅
...
Request 5: take 1 token → 0 remaining ✅
Request 6: no tokens → rejected 429 ❌
(12 seconds later)
Request 7: 1 new token → take it → allowed ✅
```

### `github.com/golang-jwt/jwt/v5`

**What it is:** The most widely used JWT library for Go. Used in `auth.go` middleware.

**Why not use the standard library?**
Go's standard library has no JWT support. JWT involves:
- Base64url encoding/decoding
- HMAC-SHA256 signature verification
- Claims parsing and expiry validation

`golang-jwt/jwt` handles all of this with a clean API and is the de-facto standard in the Go ecosystem.

### `github.com/google/uuid`

**What it is:** The official Google UUID library for Go. Used in `request_id.go`.

**Why UUIDv7?**
- **UUIDv4** (random): `550e8400-e29b-41d4-a716-446655440000` — random, not sortable
- **UUIDv7** (time-ordered): `0192f673-4e2b-7f11-80a2-c3bfde34aa5a` — starts with timestamp, sortable

UUIDv7 request IDs sort chronologically in logs, making it easier to find sequences of events.

### `go.uber.org/zap`

**What it is:** Uber's high-performance structured logger. Used in `logger.go` and `recovery.go`.

**Why not `log.Println`?**
- `log.Println` produces unstructured text — hard to parse, filter, or aggregate
- `zap` produces structured JSON — searchable, parseable by log systems (Datadog, ELK, CloudWatch)

```
log.Println:  "request GET /api/v1/auth/login 200 2.3ms"
zap:          {"method":"GET","path":"/api/v1/auth/login","status":200,"duration":"2.3ms","request_id":"abc-123"}
```

---

## Middleware Files

### `request_id.go` — Request Tracing

**Purpose:** Assigns a unique UUIDv7 to every incoming request and stores it in the response header and request context.

**Why it's needed:**
In a microservices system, a single user action touches multiple services (gateway → auth → wallet). Without a shared ID it's impossible to connect a gateway log entry to the auth service log that caused it. With a request ID, you can grep across all service logs and reconstruct the full lifecycle of any request.

**Key decisions:**
- Always generates a fresh internal ID — never trusts a client-provided `X-Request-ID` (prevents log injection/spoofing)
- Uses **UUIDv7** (time-sortable) consistent with project-wide ID standard
- Stores ID in the **request context** using a private typed key (`requestIDKey{}`) — impossible to collide with keys from other packages
- `RequestIDFromContext(ctx)` allows any downstream code (logger, handlers) to read the ID

**Example:**
```
Request:   GET /api/v1/wallets/balances
Response:  X-Request-ID: 0192f673-4e2b-7f11-80a2-c3bfde34aa5a
```

---

### `cors.go` — Cross-Origin Resource Sharing

**Purpose:** Allows the frontend (running on a different origin e.g. `http://localhost:3000`) to call the gateway from a browser.

**Why it's needed:**
Browsers enforce the Same-Origin Policy by default. A page on `localhost:3000` cannot call `localhost:8080` without the server explicitly allowing it. Without CORS headers, every API call from the frontend is blocked.

**Key decisions:**
- Accepts `[]string` of allowed origins — supports multiple environments (dev, staging, prod) without code changes
- Builds an O(1) lookup map at startup, not on every request
- `Vary: Origin` header tells CDN/proxy caches the response differs per origin
- `Access-Control-Max-Age: 86400` — browsers cache the preflight for 24 hours, reducing `OPTIONS` request spam
- Handles `OPTIONS` preflight automatically

---

### `auth.go` — JWT Authentication

**Purpose:** Validates `Authorization: Bearer <token>` on protected routes and injects verified user identity into the request context.

**Why it's needed:**
Protected endpoints (wallet balances, logout, change password) must only be accessible to authenticated users. Without this middleware, anyone can call `GET /api/v1/wallets/balances` with no token.

**Why it uses `platform/jwt.Validator` (not raw JWT parsing):**
The Auth Service already owns JWT validation logic (algorithm, secret, blacklist, token version). Duplicating `jwt.Parse()` in the gateway would create two places to update if the signing algorithm or claims structure changes. By injecting `platform/jwt.Validator`, the middleware doesn't know *how* validation works — only the result: valid claims or an error.

**Which routes use it:**

| Route | Protected |
|---|---|
| `POST /api/v1/auth/register` | No |
| `POST /api/v1/auth/login` | No |
| `POST /api/v1/auth/forgot-password` | No |
| `POST /api/v1/auth/logout` | **Yes** |
| `POST /api/v1/auth/change-password` | **Yes** |
| `GET  /api/v1/wallets/balances` | **Yes** |
| `GET  /api/v1/wallets/balances/:asset` | **Yes** |

---

### `logger.go` — Request Logging

**Purpose:** Logs every request with method, path, status, duration, bytes, client IP, user agent, and request ID.

**Why it's needed:**
Without logging you have no visibility into what the gateway is doing. When a user reports a bug, logs are the only way to reconstruct what happened.

**Key implementation detail — `responseWriter` wrapper:**
```go
type responseWriter struct {
    http.ResponseWriter
    status int   // captured status code
    bytes  int   // captured response size
}
```
The standard `http.ResponseWriter` doesn't expose the status code or bytes written after the handler runs. We wrap it to intercept `WriteHeader()` and `Write()` calls to capture these values for logging.

**Why log AFTER the handler runs:**
The logger calls `next.ServeHTTP()` first, then logs. This is intentional — we want to log the final status code and duration which are only known after the handler completes.

**Fields logged:**
```json
{
  "method": "POST",
  "path": "/api/v1/auth/login",
  "query": "",
  "status": 200,
  "bytes": 312,
  "duration": "2.3ms",
  "request_id": "0192f673-...",
  "client_ip": "192.168.1.10",
  "user_agent": "Mozilla/5.0...",
  "content_length": 45,
  "user_id": "018f67..."   ← only present on authenticated routes
}
```

---

### `recovery.go` — Panic Recovery

**Purpose:** Catches any `panic` in downstream handlers and returns a 500 instead of crashing the entire server process.

**Why it's needed:**
In Go, an unrecovered panic crashes the goroutine. In an HTTP server, each request runs in its own goroutine — a panic in a handler crashes that goroutine and the client gets a connection reset instead of a proper error response. Without recovery middleware, a single bug can take down the entire server.

**Why it must be outermost:**
```
Recovery    ← must wrap everything to catch panics from all layers below
RequestID
Logger
CORS
Auth
Handler     ← panic here is caught by Recovery above
```

**What gets logged on panic:**
```json
{
  "level": "error",
  "msg": "panic recovered",
  "error": "runtime error: index out of range",
  "stack": "goroutine 23 [running]:\n...\ntradedrift/gateway/internal/handler...",
  "method": "GET",
  "path": "/api/v1/wallets/balances",
  "request_id": "0192f673-..."
}
```
The full stack trace tells you exactly which line caused the panic.

---

### `rate_limit.go` — Per-IP Rate Limiting

**Purpose:** Rejects requests that exceed a configured rate limit for a given client IP, returning `429 Too Many Requests`.

**Why it's needed:**
Per `docs/06_APIs/01_API_Standards.md` section 5: login endpoints are limited to 5 requests/minute. Without rate limiting, attackers can brute-force passwords or spam the auth service.

#### How the Token Bucket Algorithm Works

The `golang.org/x/time/rate` package implements a **token bucket**:

```
Bucket configuration: limit=5/min, burst=1

Timeline:
t=0s:    Token available (1) → Request allowed ✅
t=1s:    No token yet → 429 ❌
t=12s:   New token added → Request allowed ✅
t=13s:   No token → 429 ❌
t=24s:   New token added → Request allowed ✅
```

- `limit` = how many tokens are replenished per second (e.g. `rate.Every(time.Minute/5)` = one token every 12 seconds)
- `burst` = maximum tokens the bucket can hold (allows short bursts above the sustained rate)

#### Why `Allow()` not `Reserve()`

```go
// ✅ Allow() — "can I proceed RIGHT NOW?"
if !limiter.Allow() {
    return 429
}

// ❌ Reserve() — "reserve a future slot, I will WAIT"
r := limiter.Reserve()
time.Sleep(r.Delay())  // wrong for HTTP — don't make the client wait
```

An HTTP gateway rejects immediately. `Allow()` is the correct API.

#### Per-IP Design

Each IP gets its own independent `rate.Limiter`:
```
192.168.1.10 → limiter (5/min)   ← User A's limit
192.168.1.20 → limiter (5/min)   ← User B's limit (unaffected by A)
```

Stored in a `map[string]*client` protected by `sync.Mutex`.

#### Memory Management — `cleanup(ctx)`

New IPs create new map entries. Without cleanup, the map grows forever as new IPs hit the gateway. The cleanup goroutine runs every `cleanupInterval` (1 minute) and evicts entries not seen in `staleThreshold` (3 minutes):

```go
// Idiomatic Go ticker pattern — NOT time.Sleep
ticker := time.NewTicker(cleanupInterval)
defer ticker.Stop()  // releases the ticker when function exits

for {
    select {
    case <-ticker.C:     // runs every cleanupInterval
        // evict stale entries
    case <-ctx.Done():   // stops when gateway shuts down
        return
    }
}
```

**Why `ticker` over `time.Sleep`?**
- `time.Sleep` cannot be interrupted — the goroutine is stuck sleeping during shutdown
- `ticker.C + ctx.Done()` in a `select` responds immediately to context cancellation

#### Defer Execution Order

In `getLimiter`:
```go
rl.mu.Lock()          // 1. Lock acquired
defer rl.mu.Unlock()  // 2. Registered — runs when function returns
// ... work under lock ...
return c.limiter      // 3. Function returns → Unlock() executes ✅
```

In `cleanup`:
```go
ticker := time.NewTicker(cleanupInterval)
defer ticker.Stop()   // registered — runs when cleanup() returns (on ctx cancel)
```

Go defers are LIFO (last registered = first executed). If multiple defers existed, the last `defer` line runs first. This matters for nested locks — unlock in the reverse order you locked.

#### Applied per route in `main.go`
```go
// Different limits for different endpoints (per API standards)
loginLimiter    := middleware.NewRateLimiter(ctx, rate.Every(time.Minute/5), 1)   // 5/min
registerLimiter := middleware.NewRateLimiter(ctx, rate.Every(time.Minute/2), 1)   // 2/min

mux.Handle("POST /api/v1/auth/login",
    loginLimiter.Middleware(http.HandlerFunc(authH.Login)))

mux.Handle("POST /api/v1/auth/register",
    registerLimiter.Middleware(http.HandlerFunc(authH.Register)))
```

---

## How Middleware is Wired in `main.go`

```go
// Global middleware — runs on every request
handler := middleware.Recovery(log)(
    middleware.RequestID(
        middleware.Logger(log)(
            middleware.CORS(corsOrigins)(mux),
        ),
    ),
)

// Per-route auth — only on protected routes
mux.Handle("GET /api/v1/wallets/balances",
    authMiddleware(http.HandlerFunc(walletH.GetBalances)),
)

// Per-route rate limiting — only on sensitive routes
mux.Handle("POST /api/v1/auth/login",
    loginLimiter.Middleware(http.HandlerFunc(authH.Login)),
)
```
