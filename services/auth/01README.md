# Auth Service

The auth service is the security backbone of TradeDrift. It handles everything related to user
identity: account creation, email verification, login, session management, password management,
and token lifecycle. It exposes all functionality over **gRPC** and communicates with other
services (e.g. Wallet) via gRPC clients.

---

## Directory Structure

```
services/auth/
├── cmd/
│   └── server/
│       └── main.go              ← Entry point: wires everything, starts gRPC server
├── internal/
│   ├── handler/
│   │   └── handler.go           ← gRPC handler: translates proto requests → service calls
│   ├── mail/
│   │   └── mail.go              ← Email interface + log-based mock implementation
│   ├── otp/
│   │   └── otp.go               ← OTP generation, storage, and verification via Redis
│   ├── repository/
│   │   ├── token.go             ← RefreshToken model, status constants, repository interface
│   │   ├── user.go              ← User model, repository interface
│   │   ├── README.md            ← Deep explanation of interfaces and patterns
│   │   └── postgres/
│   │       ├── token_repository.go ← PostgreSQL implementation of token operations
│   │       └── user_repository.go  ← PostgreSQL implementation of user operations
│   └── service/
│       ├── service.go           ← Service struct, constructor, shared helpers, DTOs
│       ├── register.go          ← User registration logic
│       ├── verification.go      ← Email verification + wallet init + event publishing
│       ├── login.go             ← Login with brute-force protection
│       ├── refresh.go           ← Token rotation + hijack detection
│       ├── logout.go            ← Single logout + logout all devices
│       └── password.go          ← Forgot password, reset password, change password
└── migrations/
    └── 00001_create_auth_tables.sql ← Goose migration: all DB tables and indexes
```

---

## Why This Structure?

### `cmd/` — Entry Point Only

`main.go` is deliberately thin. Its only job is to:
1. Read environment config
2. Run migrations
3. Create DB/Redis connections
4. Wire all dependencies together
5. Start the gRPC server

No business logic lives here. This makes the service easy to test without starting
a real server.

### `internal/` — Not Importable Outside This Service

Go's `internal/` convention means no other service can import these packages directly.
This enforces hard service boundaries — other services must call auth over the network
(gRPC), never by importing Go code.

### `handler/` — Thin gRPC Adapter

The handler knows nothing about business rules. It:
- Reads the incoming proto request
- Calls the appropriate service method
- Maps the result back to a proto response

If the transport layer ever changes (e.g. HTTP/REST), only this layer needs to change.

### `service/` — Pure Business Logic

The service layer is split one file per use case. This keeps each flow readable in isolation:
- `register.go` → account creation only
- `login.go` → login only
- `refresh.go` → token rotation only
- etc.

The service depends only on **interfaces** (`UserRepository`, `RefreshTokenRepository`, `Mailer`)
— it has zero knowledge of PostgreSQL, Redis, or gRPC. This makes it fully unit-testable
with mocks.

### `repository/` — Contract vs Implementation Split

```
repository/         ← defines what operations exist (interfaces + models)
  postgres/         ← implements those operations with real SQL
```

The service imports only `repository/` (the contract). It never imports `postgres/` directly.
This means you can swap the database implementation without touching business logic.
See [repository/README.md](./internal/repository/README.md) for a deep explanation.

### `migrations/` — Schema Version Control

All database schema changes are Goose migration files. They run automatically on every startup
before the connection pool is created. This ensures the schema is always in sync with the code.

---

## What Each File Does

### `cmd/server/main.go`

The startup sequence:
1. Initialize structured logger (zap)
2. Load config from environment variables
3. Run Goose migrations against PostgreSQL
4. Create PostgreSQL connection pool
5. Create Redis client (supports standalone + Sentinel HA)
6. Instantiate repository adapters
7. Instantiate the domain `Service` with all dependencies
8. Wire the JWT auth interceptor (validates tokens on protected gRPC routes)
9. Start gRPC server with graceful shutdown on `SIGINT`/`SIGTERM`

---

### `internal/handler/handler.go`

The gRPC handler that implements the `AuthServiceServer` proto interface.  
Maps every RPC method to the corresponding `Service` method:

| gRPC Method | Service Call |
|---|---|
| `Register` | `service.Register` |
| `VerifyEmail` | `service.VerifyEmail` |
| `ResendVerificationCode` | `service.ResendVerificationCode` |
| `Login` | `service.Login` |
| `RefreshToken` | `service.RefreshToken` |
| `Logout` | `service.Logout` |
| `LogoutAll` | `service.LogoutAll` |
| `ForgotPassword` | `service.ForgotPassword` |
| `ResetPassword` | `service.ResetPassword` |
| `ChangePassword` | `service.ChangePassword` |

---

### `internal/service/service.go`

Defines the `Service` struct and its dependencies:
- `userRepo` — user database operations
- `tokenRepo` — refresh token database operations
- `otpMgr` — OTP generation and verification
- `mailer` — email sending
- `walletCl` — gRPC client to Wallet service
- `rdb` — Redis client for caching and blacklisting
- `jwtSecret`, `accessTTL`, `refreshTTL` — JWT config
- `log` — structured logger

Also contains shared private helpers:
- `issueTokenPair` — creates access + refresh token pair and saves refresh token to DB
- `hashToken` — SHA-256 hashes a raw token for safe DB storage
- `compareBcrypt` / `hashBcrypt` — CPU-bounded bcrypt operations

---

### `internal/service/register.go`

**Problem:** How to create a user account safely.

**Flow:**
1. Validate email format, password length, username length
2. Hash password with bcrypt
3. Create user in DB with `PENDING_VERIFICATION` status
4. Generate a 6-digit OTP and store it in Redis (5-minute TTL)
5. Send verification email (or log it in dev)

**Why `PENDING_VERIFICATION` first?**  
The user cannot log in until they verify their email. This prevents fake accounts from
accessing the platform.

---

### `internal/service/verification.go`

**Problem:** How to confirm a real email address and activate an account.

**Flow:**
1. Verify the OTP from Redis (max 5 attempts before lockout)
2. Look up the user by email
3. Call Wallet service (gRPC, 5-second timeout) to initialize wallets
4. Update user status to `VERIFIED` + write an event to the Outbox table (in one DB transaction)
5. Issue a full access + refresh token session pair immediately

**Why a transactional outbox?**  
The status update and event publishing must happen atomically. If the DB write succeeds but
the event publish fails, downstream services (e.g. notification service) would never know the
user registered. The outbox table ensures the event is reliably delivered even if the message
broker is temporarily unavailable.

---

### `internal/service/login.go`

**Problem:** How to authenticate a user securely.

**Flow:**
1. Look up user by email or username
2. Return same error regardless of whether the user exists (anti-enumeration: prevents attackers from discovering valid usernames/emails)
3. Check if account is temporarily locked (brute-force protection)
4. Verify bcrypt password hash
5. On wrong password: increment failed attempt counter; lock for 15 minutes after 5 failures
6. Check account status (must be `VERIFIED`)
7. Reset failed attempt counter on success
8. Issue token pair

**Problems solved:**
- **User enumeration** — identical error response for "wrong password" and "user not found"
- **Brute-force** — account locked for 15 minutes after 5 failed attempts
- **Suspended/banned accounts** — blocked at login

---

### `internal/service/refresh.go`

**Problem:** How to issue a new session without forcing re-login, while detecting token theft.

**Flow:**
1. Hash the incoming raw refresh token and look it up in DB
2. **If status is `ROTATED`** → the token was already used once. This means either
   the client replayed an old token or an attacker stole it. Response: **revoke all sessions
   for this user**, bump `token_version` (instantly invalidates all access tokens too),
   clear Redis cache → force login on every device.
3. **If status is `REVOKED`** → session was already ended. Return error.
4. **If expired** → return error.
5. Mark old token as `ROTATED`, create new token — both in a single DB transaction (atomic).
6. Issue new access token.

**Problems solved:**
- **Token theft (Refresh Token Rotation)** — every use of a refresh token invalidates it and issues a new one. Using an old token signals a security breach.
- **Session hijacking** — breach detection triggers a full account-wide session wipe.

---

### `internal/service/logout.go`

Two modes of logout:

#### `Logout` — Single Session
1. Revoke the refresh token in PostgreSQL (marks as `REVOKED`)
2. Blacklist the access token JTI in PostgreSQL (durable)
3. Cache the blacklist entry in Redis (fast lookup until access token expires)

**Why blacklist the access token?**  
Access tokens are stateless JWTs — they're valid until they expire. Without blacklisting, a
logged-out user could still use their access token until it naturally expires (up to 15 minutes).

#### `LogoutAll` — All Sessions / All Devices
1. Increment `token_version` on the user row in PostgreSQL
2. Revoke all refresh tokens in PostgreSQL
3. Delete the token version from Redis cache

**How `token_version` works:**  
Every access token embeds the `token_version` at the time of issue. On every request,
the middleware checks the token's version against the current value in DB (cached in Redis).
Incrementing the version instantly makes all existing access tokens invalid — no need to
blacklist them one by one.

---

### `internal/service/password.go`

Three password operations:

#### `ForgotPassword`
- Looks up user silently — returns success even if email not found (anti-enumeration)
- Only sends an OTP if the account is `VERIFIED`

#### `ResetPassword`
- Verifies OTP from Redis
- Updates password hash in DB
- Revokes all refresh tokens + increments token version + clears Redis cache
- Result: user is logged out of every device after a password reset

#### `ChangePassword`
- Requires the current password (user must be logged in)
- Verifies old password with bcrypt before accepting new one
- Revokes all sessions (other devices are logged out, current session can re-authenticate)

---

### `internal/otp/otp.go`

Manages one-time password codes stored in Redis.

**How it works:**
- `Generate` — creates a cryptographically secure 6-digit code using `crypto/rand`
  (not `math/rand` — that is not safe for security codes). Stores it in Redis with a TTL.
- `Verify` — retrieves the code from Redis and compares. On wrong code: increments an
  attempt counter. After **5 wrong attempts**, the OTP is deleted from Redis entirely,
  forcing the user to request a new code.

**Why `crypto/rand`?**  
`math/rand` produces predictable sequences that can be guessed if the seed is known.
`crypto/rand` uses the OS's entropy source — genuinely unpredictable.

Two Redis keys per OTP:
```
otp:code:<key>      ← the actual code
otp:attempts:<key>  ← failed attempt counter
```

---

### `internal/mail/mail.go`

Defines the `Mailer` interface:
```go
type Mailer interface {
    SendVerificationCode(ctx context.Context, email, code string) error
    SendPasswordResetCode(ctx context.Context, email, code string) error
}
```

Currently implemented by `LogMailer` — a mock that prints emails to the application log
instead of sending real ones. The real implementation (e.g. SendGrid, SES) would satisfy
the same interface with zero changes to the service layer.

---

## Database Tables (from `migrations/`)

| Table | Purpose |
|---|---|
| `users` | User accounts, credentials, status, brute-force tracking |
| `refresh_tokens` | Active sessions — stores token hashes, not raw tokens |
| `blacklisted_tokens` | Revoked access token JTIs for single logout |
| `outbox` | Transactional outbox for reliable event publishing |

See `migrations/00001_create_auth_tables.sql` for full schema and indexes.

---

## Security Model Summary

| Threat | How It's Handled |
|---|---|
| Password brute-force | Account locked for 15 min after 5 failed attempts |
| User enumeration | Identical error response for wrong password and user not found |
| Stolen refresh token | Token rotation — reuse triggers full account session wipe |
| Session after logout | Access token JTI blacklisted in Redis + PostgreSQL |
| Instant global revoke | `token_version` increment invalidates all access tokens |
| Fake email accounts | Users stay in `PENDING_VERIFICATION` until OTP confirmed |
| OTP brute-force | OTP deleted from Redis after 5 wrong verification attempts |
| Weak OTPs | Codes generated with `crypto/rand`, not `math/rand` |
| JWT secret exposure | Secret required at startup — service fails fast if missing |

---

## Future Improvements

| Area | Improvement |
|---|---|
| **Email** | Replace `LogMailer` with a real provider (SendGrid / AWS SES) by implementing the `Mailer` interface |
| **Wallet Client** | Replace `mockWalletClient` with a real gRPC client once the Wallet service proto is stable |
| **Outbox Worker** | Add a background worker that reads the `outbox` table and publishes events to a message broker (Kafka/RabbitMQ) |
| **OAuth / SSO** | Add Google/GitHub login by implementing a new auth flow in `service/` without changing existing flows |
| **Device Management** | Surface the `device_name` / `user_agent` / `ip_address` fields on refresh tokens via a "manage sessions" endpoint |
| **Rate Limiting** | Add per-IP gRPC rate limiting at the interceptor layer to complement per-account brute-force protection |
| **Token Cleanup** | Add a scheduled job or DB trigger to delete expired/revoked refresh tokens and blacklisted JTIs from PostgreSQL |
| **2FA / TOTP** | Add time-based OTP (Google Authenticator style) as a second factor on login |
| **Audit Logging** | Persist security events (login, logout, password change, breach detection) to an audit log table |
