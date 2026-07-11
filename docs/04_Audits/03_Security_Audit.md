# TradeDrift Audit — 03. Security & Access Control

> **Status:** ✅ Validated (V1.0)
> **Document:** 03_Security_Audit.md
> **Domain:** Authentication, Authorization, and Network Boundaries

---

## 1. Scope

This audit validates security policies across the platform: session lifetime verification, refresh token invalidation, service-to-service gRPC access controls (mTLS), gateway rate-limiting, and client placement velocity filters.

---

## 2. Scenario Validations

### 2.1 Refresh Token Revocation & Blacklisting
* **Workflow:** Upon logout, token refresh, or account suspension:
  - The client's active refresh tokens are written to a distributed blacklist in Redis.
  - The blacklist entries carry a Time-To-Live (TTL) matching the remaining expiry window of the token, allowing automatic cache eviction.
  - API Gateway validates incoming headers against this blacklist before executing routing.

### 2.2 Service-to-Service gRPC mTLS Boundaries
To prevent lateral movements inside our Kubernetes clusters, the platform enforces mutual TLS (mTLS) with SPIFFE/SPIRE SVID validations:
* **Caller Validations:** Services do not accept raw gRPC connections. A gRPC authorization interceptor parses the client certificate's Subject Alternative Name (SAN) and extracts the SPIFFE ID.
* **Access Matrix Enforcement:**
  - `WalletService` only permits `ReserveFunds` and `ReleaseFunds` from `spiffe://cluster.local/ns/tradedrift/sa/order-service`.
  - `WalletService` only permits `SettleTrade` from `spiffe://cluster.local/ns/tradedrift/sa/settlement-service`.
  - `UserService` only permits `InitializeWallet` from `spiffe://cluster.local/ns/tradedrift/sa/auth-service`.
  - Any unauthorized identity calls are rejected with a gRPC status code `PERMISSION_DENIED`.

### 2.3 API Gateway Rate Limiting & Throttling
* **Strategy:** Redis-backed Token Bucket algorithm fends off brute-force and request flooding at the edge.
* **Throttling Thresholds:**
  - Public Unauthenticated Routes (login/signup): 5 requests/sec.
  - Authenticated Queries (fetch history): 50 requests/sec.
  - Authenticated Mutations (place/cancel orders): 100 requests/sec (burst up to 150).

### 2.4 Order placement velocity limits
* **Problem:** Submitting orders at a rate higher than the matching partition can consume causes queue backup.
* **Audit Resolution:**
  - Order Service enforces an account velocity filter: maximum **10 placements per second per user**.
  - Verified in-memory using a sliding-window counter in Redis.
  - Placements exceeding this threshold are blocked immediately at the API layer with a `429 Too Many Requests` response, bypassing database writes and Kafka publishing.

---

## 3. Discovered Inconsistencies & Resolutions

* **Service Authorization Gaps:** Early service manual drafts omitted the explicit SPIFFE ID authorization matrix, describing internal communication as generic unsecured HTTP. This was resolved by designing and locking the S2S mTLS grid in `24_Admin_Workflows.md`.
