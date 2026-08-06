# Gateway Middleware

This package contains all HTTP middleware used by the API Gateway.
Middleware sits between the router and the handler — it runs on every matching request before the handler executes.

---

## What is Middleware?

```
HTTP Request
     │
     ▼
[ RequestID ]   ← runs first, tags every request with a unique ID
     │
     ▼
[ CORS ]        ← checks if the origin is allowed
     │
     ▼
[ Auth ]        ← validates JWT (only on protected routes)
     │
     ▼
[ Handler ]     ← your actual business logic runs here
     │
     ▼
HTTP Response
```

Each middleware either:
- **Passes** the request to the next layer (`next.ServeHTTP`)
- **Stops** the request and returns an error response (`return` after `WriteError`)

---

## Middleware Files

### `request_id.go` — Request Tracing

**Purpose:** Assigns a unique UUIDv7 to every incoming request and returns it in the `X-Request-ID` response header.

**Why it's needed:**
- In a microservices system, a single user action touches multiple services (gateway → auth → wallet).
- Without a shared ID, it's impossible to connect a gateway log entry to the auth service log that caused it.
- With a request ID, you can grep across all service logs and reconstruct the full lifecycle of any request.

**Features:**
- Always generates a fresh internal ID — never trusts a client-provided `X-Request-ID` (prevents log injection/spoofing).
- Uses **UUIDv7** (time-sortable) consistent with the project-wide ID standard.
- Stores the ID in the **request context** so downstream middleware and handlers can read it via `RequestIDFromContext(ctx)`.
- Echoes the ID back in the response header per `docs/06_APIs/01_API_Standards.md` section 2.

**Example:**
```
Request:   GET /api/v1/wallets/balances
Response:  X-Request-ID: 0192f673-4e2b-7f11-80a2-c3bfde34aa5a
```

---

### `cors.go` — Cross-Origin Resource Sharing

**Purpose:** Allows the frontend (running on a different origin, e.g. `http://localhost:3000`) to call the gateway API from a browser.

**Why it's needed:**
- Browsers enforce the Same-Origin Policy by default. A page on `localhost:3000` cannot call `localhost:8080` without the server explicitly allowing it.
- Without CORS headers, the browser blocks every API call from the frontend.

**Features:**
- Accepts a `[]string` of allowed origins — supports multiple environments (dev, staging, prod) without code changes.
- Builds an O(1) lookup map at startup, not on every request.
- Sets `Vary: Origin` so CDN/proxy caches don't serve the wrong CORS response to different origins.
- Sets `Access-Control-Max-Age: 86400` so browsers cache the preflight response for 24 hours, reducing `OPTIONS` request spam.
- Handles `OPTIONS` preflight requests automatically.

**Example:**
```
Browser (localhost:3000) → OPTIONS /api/v1/auth/login
Gateway → 204 No Content + CORS headers
Browser → proceeds with actual POST request
```

---

### `auth.go` — JWT Authentication

**Purpose:** Validates the `Authorization: Bearer <token>` header on protected routes and injects the verified user identity into the request context.

**Why it's needed:**
- Protected endpoints (wallet balances, logout, change password) must only be accessible to authenticated users.
- Without this middleware, anyone could call `GET /api/v1/wallets/balances` with no token and get data — or crash the handler.

**Why it uses `platform/jwt.Validator` (not raw JWT parsing):**
- The Auth Service already owns the JWT validation logic (algorithm, secret, blacklist, token version).
- Duplicating `jwt.Parse()` in the gateway would create two places to update if the signing algorithm or claim structure ever changes.
- By injecting `platform/jwt.Validator`, the middleware doesn't know *how* validation works — it only knows the result: valid claims or an error.

**Features:**
- Reads and strips the `Bearer ` prefix from the `Authorization` header.
- Delegates all validation to `platform/jwt.Validator.Validate()` — covers signature, expiry, blacklist, and token version checks.
- Stores the full typed `*jwt.Claims` in the request context using `platform/jwt.WithClaims()`.
- Handlers retrieve the user identity via `middleware.GetUserID(r)` or `platform/jwt.FromContext(r.Context())`.
- Uses the platform's private `claimsContextKey{}` struct type — zero chance of key collision with other packages.

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

**Example flow:**
```
GET /api/v1/wallets/balances
Authorization: Bearer eyJhbGciOiJIUzI1NiJ9...

1. Strip "Bearer " prefix
2. Call validator.Validate() → returns *Claims or error
3. Error? → 401 AUTH_INVALID_TOKEN, request stops here
4. Success? → store claims in context, continue to handler
5. Handler calls GetUserID(r) → "018f67..."
6. Gateway calls wallet gRPC with that userID
```

---

## How Middleware is Wired

Middleware is applied in `cmd/server/main.go`:

```go
// Global middleware — runs on every request
handler := middleware.CORS(corsOrigins)(
    middleware.RequestID(mux),
)

// Per-route middleware — only on protected routes
mux.Handle("GET /api/v1/wallets/balances",
    authMiddleware(http.HandlerFunc(walletH.GetBalances)),
)
```

Global middleware wraps the entire router. Per-route middleware wraps individual handlers.
