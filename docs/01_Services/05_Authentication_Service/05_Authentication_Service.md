# TradeDrift — Authentication Service

> **Status:** ✅ Designed (V2)
> Revision notes: merges two improvements from a parallel draft into the full V1 spec — wallet-init failure now compensates by deleting the created user (simpler than a `wallet_status = PENDING` approach), and adds explicit JWT lifetimes plus a standalone Security section. All seven flows are retained in full.

## Purpose

The Authentication Service owns user identity, credentials, and session lifecycle. It is the only service that creates user records, issues and validates JWTs, and is the synchronous trigger for wallet creation via Wallet Service's `InitializeWallet`.

## Responsibilities

- Register new users: validate input, hash passwords, create the user record, trigger wallet initialization, issue tokens.
- Authenticate login attempts and issue access + refresh token pairs.
- Validate JWTs on behalf of the API Gateway (signature, expiry, revocation) for every authenticated request.
- Rotate refresh tokens on use and detect reuse of already-rotated tokens.
- Serve and update user profile data.
- Log out sessions (single session or all sessions).
- Change passwords and revoke all other sessions when a password changes.

## Out of Scope

- Does not own wallet balances — only triggers `InitializeWallet` and waits for confirmation.
- Does not own order, trade, or portfolio data.
- Does not send notification emails/SMS directly — out of scope for V1.

## Architecture Topology
![Authentication Service Architecture](diagrams/architecture/auth-architecture.svg)

---

## 1. Register Flow

Validate → check duplicate → hash password → create user → initialize wallet → generate tokens → return.

```
Register Request (email, username, password)
  ↓
Validate Input (email format, password rules, username rules)
  ↓
Check Duplicate (email / username exists?)
  ├── Yes → 409 Conflict: email/username already exists
  └── No  → Hash Password (bcrypt / argon2)
              ↓
            Create User (insert into users table, id = UUIDv7)
              ↓
            InitializeWallet(user_id)   -- gRPC call to Wallet Service
              ↓
            Generate Tokens (JWT access + refresh)
              ↓
            Registration Success — return user info + tokens
```

> **Integration point:** `InitializeWallet` is called synchronously, in the critical path of registration, before tokens are issued (see `06_Wallet_Service/06_Wallet_Service.md`, "Why Synchronous"). If `InitializeWallet` fails, registration itself fails — see [Section 8](../../#8-failure-handling) for exactly how.

## 2. Login Flow

### Login Flow Diagram
![Authentication Service Login Flow](diagrams/flow/login-flow.svg)

```
Login Request (email/username, password)
  ↓
Find User (query by email/username)
  ↓
User Found?
  ├── No  → 401 Unauthorized: invalid credentials
  └── Yes → Verify Password (compare hashes)
              ↓
            Password Valid?
              ├── No  → 401 Unauthorized: invalid credentials
              └── Yes → Check Account Status (active / suspended / banned)
                          ↓
                        Account Active?
                          ├── No  → 403 Forbidden: account not active
                          └── Yes → Generate Tokens (JWT access + refresh)
                                      ↓
                                    Update Last Login
                                    (last_login_at, ip, user_agent)
                                      ↓
                                    Login Success — return user info + tokens
```

Invalid-credentials responses are identical whether the user doesn't exist or the password is wrong — intentional, so the endpoint never reveals which part of a login attempt was incorrect.

## 3. JWT Validation Flow

Called by the API Gateway's JWT middleware for every request that requires auth — shared logic, not duplicated between the gateway and Authentication Service (see [Section 10](../../#10-shared-jwt-validation-logic)).

```
Token Received (from API Gateway)
  ↓
Extract Token (from Authorization header)
  ↓
Verify Signature (using secret / public key)
  ↓
Signature Valid?
  ├── No  → 401 Unauthorized: invalid signature
  └── Yes → Token Expired?
              ├── Yes → 401 Unauthorized: token expired
              └── No  → Token Revoked? (Redis blacklist check)
                          ├── Yes → 401 Unauthorized: token revoked
                          └── No  → Token Valid — attach user claims to context
                                     (user_id, roles, permissions)
```

## 4. Refresh Token Flow

Mandatory rotation and reuse detection — a refresh token can only be used once; using an already-rotated token revokes every session for that user.

### Token Rotation & Reuse Flow
![Authentication Service Token Rotation & Reuse](diagrams/flow/token-refresh-flow.svg)

```
Refresh Request (refresh token)
  ↓
Find Refresh Token (in database)
  ↓
Token Found?
  ├── No  → 401 Unauthorized: invalid/expired refresh token
  └── Yes → Validate Token (check signature + expiry)
              ↓
            Token Valid?
              ├── No → Reuse Detected?
              │         ├── Yes → 403 Forbidden: reuse detected — revoke ALL
              │         │         user refresh tokens (force logout everywhere)
              │         └── No  → 401 Unauthorized: invalid or expired
              └── Yes → Rotate Refresh Token (mandatory)
                          — issue new refresh token, invalidate old one
                          ↓
                        Generate New Access Token
                          ↓
                        Refresh Success — return new tokens
```

> **Why mandatory rotation + reuse detection:** A refresh token is only ever valid for one use. If a token that has already been rotated is presented again, that's a signal the token was compromised — the correct response is to revoke every session for that user, forcing re-authentication everywhere, not just reject the one request.

## 5. Profile Flow

```
Get/Update Profile Request (with JWT access token)
  ↓
Validate JWT (verify signature, expiry, revocation)
  ↓
Extract User ID (from token claims)
  ↓
Token Valid?
  ├── No  → 401 Unauthorized: invalid token
  └── Yes → [GET]   Fetch User Profile → Return Profile
            [PATCH] Validate Update Data (check allowed fields)
                      → Update User Profile → Return Updated Profile
```

## 6. Logout Flow

```
Logout Request (with refresh token + JWT access token)
  ↓
Validate JWT (verify signature, expiry, revocation)
  ↓
Invalidate Refresh Token (remove / mark as revoked in database)
  ↓
Blacklist Access Token (store jti in Redis with TTL until natural expiry)
  ↓
(Optional) Invalidate All Sessions (logout from other devices)
  ↓
Logout Success — 204 No Content
```

Blacklisting the access token matters because a valid, non-expired access token would otherwise keep working after logout until it naturally expires — the Redis blacklist closes that window.

## 7. Change Password Flow

```
Change Password Request (current_password, new_password)
  ↓
Validate JWT (verify token)
  ↓
Fetch User (get user by user_id from token)
  ↓
Token Valid?
  ├── No → 401 Unauthorized: invalid token
  └── Yes → Verify Current Password (compare with stored hash)
              ↓
            Password Correct?
              ├── No  → 401 Unauthorized: incorrect password
              └── Yes → Validate New Password (check strength rules)
                          ↓
                        Hash New Password (bcrypt / argon2)
                          ↓
                        Update Password (in users table)
                          ↓
                        Revoke All Refresh Tokens
                        (logout from all devices/sessions)
                          ↓
                        Password Changed — success
```

Revoking all refresh tokens on password change is deliberate: if the password was changed because it was compromised, every existing session (including an attacker's) is killed, not just the one making the request.

## 8. Failure Handling

- **Register:** duplicate email/username → `409 Conflict`, no user row created, no wallet call made.
- **Login/Refresh:** transient DB failure → `503`, client retries; no partial token issuance ever occurs (tokens are generated only after every prior check passes).
- **Refresh reuse detected** → revoke all sessions for that user, log the event for security review.

### Register: InitializeWallet Failure — Compensating Delete

> **Decision:** If the `InitializeWallet` gRPC call fails after the user row has committed, Authentication Service compensates by **deleting the just-created user row** and returning a registration failure to the client — rather than a `wallet_status = PENDING` + reconciliation-job approach. This is simpler: no orphan `PENDING` users, no background job to maintain, and it's safe specifically because nothing else can reference this `user_id` yet at this point in the flow (registration hasn't returned success, so no token was issued and no other service has seen this user). The client sees a clean failure and can simply retry registration.

```
Create User (commit)
  ↓
InitializeWallet(user_id)
  ↓
Fails?
  ├── Yes → Delete User (compensating action) → 500: registration failed, please retry
  └── No  → continue to Generate Tokens
```

*If `InitializeWallet` is retried with backoff first (recommended, e.g. 2–3 attempts) and still fails, only then is the user row deleted — so a single transient blip doesn't fail registration unnecessarily.*

### Register: Double-Failure Recovery (Compensating Delete Fails)

> If `InitializeWallet` fails **and** the compensating `DELETE FROM users` also fails (e.g., DB temporarily unavailable, process crash), an orphan user row remains — exists in `users` table with no wallet and no token ever issued. The next registration attempt with the same email would hit `409 Conflict`.
>
> **Recovery:** On startup, Authentication Service runs a reconciliation query: `SELECT u.id FROM users u WHERE NOT EXISTS (SELECT 1 FROM ...)` (checked via a lightweight `GetBalance` gRPC call to Wallet Service for each candidate). For each orphan, either retry `InitializeWallet` or delete the user row. Additionally, during registration's duplicate-email check: if a matching user exists but was created very recently (within a configurable window, e.g. 5 minutes) and has no active refresh tokens, treat it as a likely orphan — delete and proceed with fresh registration rather than returning `409`.

## 9. JWT Strategy

| Token | Lifetime | Storage |
|---|---|---|
| Access token | 15 minutes | Client memory / short-lived storage; never persisted server-side except revocation blacklist |
| Refresh token | 7 days | Hashed at rest in `refresh_tokens` table; rotated on every use |

Short access-token lifetime limits the damage window if one leaks; the refresh token is the long-lived credential, protected by rotation and reuse detection ([Section 4](../../#4-refresh-token-flow)).

## 10. Shared JWT Validation Logic

The JWT Validation Flow ([Section 3](../../#3-jwt-validation-flow)) and the API Gateway's JWT middleware step must be the same shared library/package, not two independent implementations. If they diverged, a token accepted by the gateway but rejected by Authentication Service (or vice versa) would be a real, hard-to-diagnose bug.

> **Decision (V1):** JWT verification (signature check, expiry check, Redis revocation check) lives in one internal shared package, imported by both the API Gateway and Authentication Service. Authentication Service is authoritative for issuing and revoking tokens; the shared package is just the verification logic, built once and reused. The API Gateway validates tokens **locally** using this shared package — no gRPC round-trip to Authentication Service on every authenticated request. This requires the Gateway to have access to (1) the JWT signing key / public key and (2) Redis for blacklist checks.

## 11. Security

- All traffic to and from Authentication Service is over TLS — no plaintext credentials or tokens in transit, including internal gRPC calls.
- Passwords are hashed with bcrypt/argon2, never stored or logged in plaintext.
- Refresh tokens are stored hashed (not plaintext) in `refresh_tokens`, so a database read alone cannot be used to impersonate a session.
- Every authentication-relevant event (login, logout, password change, refresh reuse detection) is written to an audit log with `user_id`, `ip`, `user_agent`, and timestamp, for security review.
- Rate limiting on login and refresh endpoints is enforced at the API Gateway (Redis token bucket), not duplicated here.
- **V1 deferral — Account lockout:** No per-account lockout after failed login attempts in V1. Gateway rate limiting (per-IP) provides baseline protection. A `failed_login_count` + `locked_until` mechanism on the `users` table is planned for V2.
- **V1 deferral — Email verification:** No email verification on registration in V1. TradeDrift is a simulator with no real assets, so the risk of unverified emails is acceptable. Email verification deferred to V2.

## 12. Identifiers

`user_id` is a PostgreSQL `UUID`, generated as **UUIDv7** by Authentication Service before the user row is inserted — per `TradeDrift_ID_Correlation_Standard.md`. This is the same `user_id` passed to `InitializeWallet`, and later reused unchanged by Order Service, Wallet Service, and every other service that references this user.

## 13. Database Schema

```sql
users(
  id UUID PRIMARY KEY,          -- UUIDv7, generated by Authentication Service
  email, username,
  password_hash,
  status,                        -- ACTIVE | SUSPENDED | BANNED
  created_at, updated_at, last_login_at, last_login_ip, last_login_ua
)

refresh_tokens(
  id UUID PRIMARY KEY,
  user_id UUID,
  token_hash,
  status,                        -- ACTIVE | ROTATED | REVOKED
  expires_at, created_at
)
```

*`wallet_status` is not part of this schema — a `PENDING`-flag approach was considered and rejected in favor of compensating delete ([Section 8](../../#8-failure-handling)).*

## 14. gRPC / REST APIs

### REST (via grpc-gateway, browser-facing)

- `POST /auth/register`
- `POST /auth/login`
- `POST /auth/refresh`
- `POST /auth/logout`
- `GET /auth/profile`
- `PATCH /auth/profile`
- `POST /auth/change-password`

### gRPC (internal)

> **Removed:** `ValidateToken(token)` was originally listed here. Per [Section 10](../../#10-shared-jwt-validation-logic), the API Gateway imports the shared JWT verification package directly and validates tokens locally — no per-request gRPC call. A network round-trip on every authenticated request adds unnecessary latency.

## 15. Service Interactions

| Service | Interaction |
|---|---|
| Wallet Service | `InitializeWallet(user_id)` — synchronous gRPC, called once during registration; compensating delete on failure ([Section 8](../../#8-failure-handling)). |
| API Gateway | Imports the shared JWT verification package directly ([Section 10](../../#10-shared-jwt-validation-logic)); validates tokens locally without a gRPC call. |
| Redis | Access-token blacklist (logout). Rate-limit state is owned by the Gateway, not Authentication Service. |

## 16. Scalability

- Stateless service — horizontal scaling behind the API Gateway.
- Refresh token rotation state lives in PostgreSQL, not in-memory, so any instance can validate/rotate any user's token.
- Redis blacklist is shared across instances, so logout takes effect immediately regardless of which instance handles the next request.

## 17. Future-Proofing

- OAuth / social login as an additional registration path, without changing the core user schema.
- Multi-factor authentication as an additional step between password verification and token generation in the Login Flow.
- Role-based permissions beyond the current single-tier user model, carried in JWT claims already (roles, permissions attached at validation).