# TradeDrift — Admin Service

> **Status:** ✅ Designed (V1.0)
> **Document:** Admin_Service.md
> **Service:** Admin Service
> **Version:** V1.0
> **Last Updated:** July 2026
> Sources: `08_Admin_API.md`, `24_Admin_Workflows.md`, `13_Event_Driven_Architecture.md`

---

## Purpose

The Admin Service is the **control-plane orchestrator** for all administrative and operational actions on the TradeDrift platform. It provides a restricted REST API that allows operators and compliance teams to suspend user accounts, freeze wallets, and halt or resume trading markets.

It is the **sole publisher** of the three admin-domain Kafka command topics. All downstream enforcement (order cancellation, market halt enforcement) is event-driven — the Admin Service fires and the consuming services enforce.

---

## Out of Scope

| Concern | Owning Service |
|---|---|
| JWT token issuance | Authentication Service |
| Balance mutation | Wallet Service |
| Order state management | Order Service |
| Matching Engine halt enforcement | Matching Engine (consumes admin command) |
| Settlement DLQ management | Settlement Service |
| User registration / profile | Authentication Service |

---

## 1. REST API

All endpoints require an `Authorization: Bearer <token>` header where the JWT carries `"role": "admin"` in its claims. Requests without this role claim are rejected at the Admin Service middleware layer with `403 Forbidden` before any business logic executes.

### 1.1 `POST /api/v1/admin/users/:id/suspend`

Suspends a user account, preventing login and rejecting future order placements.

**Request Body:**
```json
{
  "reason": "Suspected market manipulation rule breach"
}
```

**Response `200 OK`:**
```json
{
  "userId": "018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
  "status": "SUSPENDED",
  "suspendedAt": "2026-07-11T13:00:20Z"
}
```

**Delivery path:** Saga — see §3.1.

---

### 1.2 `POST /api/v1/admin/wallets/:id/freeze`

Freezes or unfreezes a target user's wallet for a specific asset. While frozen, `ReserveFunds` and `SettleTrade` gRPC calls against that wallet are rejected by the Wallet Service.

**Request Body:**
```json
{
  "asset": "BTC",
  "freeze": true,
  "reason": "Security investigation lock"
}
```

**Response `200 OK`:**
```json
{
  "userId": "018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
  "asset": "BTC",
  "isFrozen": true,
  "frozenAt": "2026-07-11T13:00:22Z"
}
```

**Delivery path:** Single synchronous gRPC call — see §3.2.

---

### 1.3 `POST /api/v1/admin/markets/:id/halt`

Halts trading for a market symbol. The Matching Engine stops accepting new orders and rejects incoming `OrderCreated` events for the halted pair.

**Request Body:**
```json
{
  "reason": "System maintenance pair decommission"
}
```

**Response `200 OK`:**
```json
{
  "marketId": "BTC-USDT",
  "status": "HALTED",
  "haltedAt": "2026-07-11T13:00:25Z"
}
```

**Delivery path:** Async Kafka command — see §3.3. The `200 OK` is returned before the Matching Engine confirms halt.

---

### 1.4 `POST /api/v1/admin/markets/:id/resume`

Resumes trading on a previously halted market.

**Response `200 OK`:**
```json
{
  "marketId": "BTC-USDT",
  "status": "ACTIVE",
  "resumedAt": "2026-07-11T13:00:30Z"
}
```

**Common Errors:**
- `404 Not Found` — market ID does not exist

**Delivery path:** Async Kafka command — same path as halt (§3.3).

---

## 2. Kafka Topics Published

The Admin Service is the **sole publisher** of all three admin Kafka topics. Events are published via the transactional outbox pattern — never directly.

| Topic | Partition Key | Consumer(s) | Trigger |
|---|---|---|---|
| `admin.user-suspended.v1` | `user_id` | Order Service, Wallet Service | User suspension saga (§3.1) |
| `admin.market-halted.v1` | `market_id` | Order Service | Market halt (§3.3) |
| `admin.market-commands.v1` | `market_id` | Matching Engine | Market halt and resume (§3.3) |

> **Why two halt topics?**  
> `admin.market-commands.v1` carries the `HaltMarket`/`ResumeMarket` command to the Matching Engine.  
> `admin.market-halted.v1` carries the notification to the Order Service so it can update its local market status cache and reject incoming `POST /orders` requests at the API layer before they reach Kafka.  
> These are two separate consumer needs and intentionally use two separate topics.

---

## 3. Workflow Sagas

### 3.1 Suspend User Account

**Goal:** Lock the user out of the platform, revoke active sessions, and cascade-cancel all resting orders.

**Call pattern:**
- Token revocation → **gRPC** → Auth Service (synchronous, blocking)
- Order cancellation → **Kafka event** → Order Service consumes `admin.user-suspended.v1` (asynchronous, non-blocking)

> [!IMPORTANT]
> Order cancellation is **not** a direct gRPC call to the Order Service. The Admin Service publishes `admin.user-suspended.v1` and the Order Service is responsible for detecting it, fetching all open orders for the user, and cascading `OrderCancelRequested` events to the Matching Engine. This is an intentional asymmetry: token revocation must be synchronous (session security), but order cleanup is eventually consistent and does not block the suspension from taking effect.

**Steps:**

```
Admin Service receives POST /api/v1/admin/users/:id/suspend

  Step 1 — DB Write (same transaction)
    UPDATE users SET status = 'SUSPENDED' WHERE id = $user_id
    INSERT INTO admin_audit_log (admin_id, action, target_id, reason, metadata, created_at)
    INSERT INTO outbox (event_type='admin.user-suspended.v1', partition_key=$user_id)

  Step 2 — gRPC: Auth Service
    Call Auth.InvalidateAllSessions(user_id)
    Auth Service immediately blacklists all active refresh tokens for the user in Redis.
    Existing access tokens expire within their remaining TTL (≤5 min).
    → If this gRPC call fails: rollback transaction, return 502 to caller.

  Step 3 — Return 200 OK to REST caller

  Step 4 — (Async, after ACK) Outbox publisher sends admin.user-suspended.v1 to Kafka
    Order Service consumer:
      → Fetches all OPEN/PARTIALLY_FILLED orders for user_id
      → Transitions each to CANCELLING
      → Publishes OrderCancelRequested per order → Matching Engine removes resting entries
    Wallet Service consumer:
      → Updates any service-level user-status cache (rejects future ReserveFunds calls)
```

---

### 3.2 Freeze Wallet

**Goal:** Prevent any movement of funds for a specific `(user_id, asset)` pair.

**Call pattern:** Single synchronous **gRPC** call to Wallet Service. No Kafka event published.

**Steps:**

```
Admin Service receives POST /api/v1/admin/wallets/:id/freeze

  Step 1 — gRPC: Wallet Service
    Call Wallet.FreezeWallet(user_id, asset, freeze=true, reason)
    Wallet Service sets is_frozen = TRUE on the wallets row for (user_id, asset).
    → If this gRPC call fails: return 502 to caller. No DB write on Admin side needed.

  Step 2 — DB Write (Admin's own DB)
    INSERT INTO admin_audit_log (admin_id, action='FREEZE_WALLET', target_id=$user_id,
                                  reason, metadata={asset, freeze}, created_at)

  Step 3 — Return 200 OK to REST caller
```

> **Enforcement by Wallet Service after freeze:**
> - `ReserveFunds`: returns `FAILED_PRECONDITION: WALLET_FROZEN` — order placement rejected
> - `ReleaseFunds`: permitted — cancellation fund returns are always allowed
> - `SettleTrade`: returns `FAILED_PRECONDITION: WALLET_FROZEN` — trade goes to Settlement DLQ

---

### 3.3 Halt / Resume Market

**Goal:** Stop the Matching Engine from processing new orders for a market symbol.

**Call pattern:** The REST endpoint does **not** call the Matching Engine via gRPC. It publishes a command to the `admin.market-commands.v1` Kafka topic, which the Matching Engine consumes asynchronously. The `200 OK` is returned to the REST caller **before** the Matching Engine confirms halt.

> [!IMPORTANT]
> **Async ack semantics:** There is no synchronous confirmation from the Matching Engine that the halt has taken effect. The REST response acknowledges that the command has been durably committed to the outbox and will be delivered. The caller must not assume the market is halted the instant `200 OK` is returned — it will be halted within milliseconds once the ME consumes the command from its partition.

**Steps (Halt):**

```
Admin Service receives POST /api/v1/admin/markets/:id/halt

  Step 1 — DB Write (same transaction)
    INSERT INTO admin_audit_log (admin_id, action='HALT_MARKET', target_id=$market_id,
                                  reason, metadata={}, created_at)
    INSERT INTO outbox (event_type='admin.market-commands.v1',
                        payload={command:'HaltMarket', market_id},
                        partition_key=$market_id)
    INSERT INTO outbox (event_type='admin.market-halted.v1',
                        payload={market_id, reason},
                        partition_key=$market_id)

  Step 2 — Return 200 OK to REST caller

  Step 3 — (Async) Outbox publisher sends both events to Kafka
    Matching Engine consumer (admin.market-commands.v1):
      → Reads HaltMarket command
      → Sets market state = HALTED in memory
      → Subsequent OrderCreated events for this market_id are rejected
      → Returns OrderCancelled with reason=market_halted
    Order Service consumer (admin.market-halted.v1):
      → Updates local market status cache
      → Rejects POST /orders requests for this symbol at the service layer
```

**Steps (Resume):** Identical to Halt, with `command:'ResumeMarket'` and `event_type='admin.market-commands.v1'`. No separate `market-resumed` notification topic needed — Order Service clears its halt cache when the ME resumes accepting orders.

---

## 4. gRPC API (Internal)

Admin Service does **not** expose a gRPC server interface. It acts exclusively as a **gRPC client** calling Auth and Wallet Services.

### 4.1 Calls Made

| Target | RPC | When |
|---|---|---|
| Auth Service | `InvalidateAllSessions(user_id)` | User suspension Step 2 |
| Wallet Service | `FreezeWallet(user_id, asset, freeze, reason)` | Wallet freeze Step 1 |

---

## 5. Database Schema

Admin Service owns a single dedicated Postgres database. It does not share databases with any other service.

### 5.1 `admin_audit_log` Table

```sql
CREATE TABLE admin_audit_log (
    id          UUID PRIMARY KEY,             -- UUIDv7, generated by Admin Service
    admin_id    UUID NOT NULL,                -- JWT subject claim of the acting administrator
    action      VARCHAR(50) NOT NULL,         -- 'SUSPEND_USER' | 'FREEZE_WALLET' | 'HALT_MARKET' | 'RESUME_MARKET'
    target_id   UUID NOT NULL,                -- The entity being acted on (user_id or market internal UUID)
    target_ref  VARCHAR(50) NOT NULL,         -- Human-readable ref: user_id string or market_id e.g. 'BTC-USDT'
    reason      TEXT NOT NULL,                -- Operator-supplied reason string (required, non-empty)
    metadata    JSONB NOT NULL DEFAULT '{}',  -- Action-specific extra fields (e.g. {asset: 'BTC', freeze: true})
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_admin_id    ON admin_audit_log(admin_id, created_at DESC);
CREATE INDEX idx_audit_target_id   ON admin_audit_log(target_id, created_at DESC);
CREATE INDEX idx_audit_action      ON admin_audit_log(action, created_at DESC);
```

**Schema rules:**
- **One row per action, same transaction:** The audit log INSERT always executes in the same database transaction as the primary mutation (user status update, outbox insert). If the transaction rolls back, the audit log row is also rolled back — there is never a partial audit entry.
- **Append-only:** No `UPDATE` or `DELETE` ever executes on this table. Audit records are immutable.
- `reason` is **mandatory and non-empty** — enforced at the service layer before the transaction begins.
- `metadata` captures action-specific fields in free-form JSON to avoid needing a separate table per action type.

### 5.2 `outbox` Table

Admin Service follows the standard outbox schema defined in `18_PostgreSQL_Design.md §3.1`. No deviations.

---

## 6. JWT Role Enforcement

Admin endpoints require the `"role": "admin"` claim in the JWT. This is enforced by a dedicated admin middleware layer — **not** the shared user JWT middleware from `platform/jwt/transport/http/middleware.go`.

```go
// services/admin/internal/middleware/admin_auth.go

func AdminOnly(validator jwt.Validator) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            claims, err := validator.VerifyToken(r.Context(), extractBearer(r))
            if err != nil || claims.Role != "admin" {
                writeError(w, http.StatusForbidden, "ADMIN_ROLE_REQUIRED")
                return
            }
            ctx := context.WithValue(r.Context(), adminIDKey, claims.UserID)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

All four REST endpoints are wrapped by this middleware. No admin endpoint is reachable without a valid admin-role JWT.

---

## 7. Internal Package Structure

```
services/admin/
  ├── go.mod
  ├── cmd/server/main.go
  ├── internal/
  │   ├── handler/
  │   │   ├── suspend_user.go         # POST /admin/users/:id/suspend
  │   │   ├── freeze_wallet.go        # POST /admin/wallets/:id/freeze
  │   │   ├── halt_market.go          # POST /admin/markets/:id/halt
  │   │   └── resume_market.go        # POST /admin/markets/:id/resume
  │   ├── service/
  │   │   ├── suspend_service.go      # Saga: DB txn → gRPC Auth → outbox
  │   │   ├── freeze_service.go       # gRPC Wallet → audit log
  │   │   └── market_service.go       # Outbox: market-commands.v1 + market-halted.v1
  │   ├── repository/
  │   │   └── audit_repository.go     # INSERT into admin_audit_log
  │   ├── model/
  │   │   └── audit.go                # AuditLog struct
  │   ├── middleware/
  │   │   └── admin_auth.go           # Admin-role JWT enforcement
  │   └── client/
  │       ├── auth_client.go          # gRPC: Auth.InvalidateAllSessions
  │       └── wallet_client.go        # gRPC: Wallet.FreezeWallet
  ├── migrations/
  │   ├── 001_create_audit_log.sql
  │   └── 002_create_outbox.sql
  └── config/
      └── config.go
```

---

## 8. Service Invariants

- **AI-1 (Audit Atomicity):** Every admin action writes one `admin_audit_log` row in the same database transaction as any primary mutation or outbox insert. There is no admin action without an audit trail.
- **AI-2 (Outbox for Kafka):** Admin Service never publishes directly to Kafka. All Kafka events are committed to the `outbox` table first and published by the outbox background engine.
- **AI-3 (Role Gate):** No admin REST handler executes any business logic without first confirming `role == "admin"` from the JWT. The role check happens in middleware before the handler is invoked.
- **AI-4 (Async Halt Ack):** The `200 OK` for halt/resume indicates durable command commit to the outbox, not Matching Engine confirmation. Callers must not treat the response as a synchronous enforcement acknowledgment.
- **AI-5 (Order Cascade via Event):** The Admin Service never calls the Order Service via gRPC. Order cancellation triggered by user suspension is delivered exclusively through the `admin.user-suspended.v1` Kafka event. The Order Service is the enforcer.
- **AI-6 (Freeze via gRPC):** Wallet freeze is the only admin action delivered synchronously via gRPC. Unlike order cancellation, a freeze must be atomically confirmed before returning 200 — a half-committed freeze (DB record with no Wallet enforcement) is a security violation.
