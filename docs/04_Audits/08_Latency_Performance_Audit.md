# TradeDrift — Latency & Performance Audit

> **Auditor Role:** Principal Performance Engineer
> **Audit Scope:** End-to-end latency profiling across all critical request paths, network hop analysis, gRPC, Kafka, Redis, PostgreSQL, lock contention, cache effectiveness, serialization overhead
> **Method:** Logical latency modeling from documented architecture. All estimates derived from component characteristics described in official specifications. No measurement data exists — this is a design-time latency budget analysis.
> **Date:** July 2026

---

## Latency Estimation Methodology

Component latency estimates are based on well-known operational characteristics:

| Component | P50 | P95 | P99 | Source |
|---|---|---|---|---|
| In-cluster gRPC (happy path, no retries) | 1–3ms | 5–10ms | 15–30ms | HTTP/2 multiplexed, in-cluster TCP |
| PostgreSQL simple SELECT (indexed, warm) | 1–3ms | 5–15ms | 20–50ms | Single-row primary key lookup |
| PostgreSQL write transaction (with outbox) | 3–8ms | 10–25ms | 30–80ms | Multi-row INSERT + COMMIT |
| PostgreSQL `SELECT FOR UPDATE` + write | 5–15ms | 15–40ms | 50–150ms | Lock acquisition + commit |
| Redis GET / SET (single key) | 0.3–0.8ms | 1–2ms | 3–5ms | In-cluster Redis Sentinel |
| Redis Pub/Sub publish | 0.3–1ms | 1–3ms | 3–8ms | Single-threaded Redis master |
| Kafka produce (acks=all, ISR confirmed) | 5–15ms | 20–40ms | 50–100ms | acks=all, replication across 3 brokers |
| Kafka consume (poll interval) | 0–5ms | 5–20ms | 20–50ms | Consumer poll loop latency |
| bcrypt verify (cost 10) | 80–120ms | 100–150ms | 150–200ms | CPU-bound, single core |
| bcrypt verify (cost 12) | 300–400ms | 400–500ms | 500–700ms | CPU-bound, single core |
| bcrypt verify (cost 14) | 1.1–1.3s | 1.3–1.5s | 1.5–1.8s | CPU-bound, single core |
| Outbox publisher polling (100–250ms interval) | 50–125ms | 200–250ms | 250ms | `08_Order_Service.md §Outbox` |
| ME Event Loop processing (in-memory) | 0.01–0.1ms | 0.1–1ms | 1–5ms | Pure memory, price tree traversal |
| ME Input Queue wait (at load) | 0–2ms | 2–10ms | 10–100ms | Bounded channel wait |
| JSON serialization per hop | 0.05–0.2ms | 0.2–0.5ms | 0.5–2ms | Protobuf faster than JSON by 3-5× |

---

## Critical Path 1: User Login

**Path:** `Client → NGINX Ingress → API Gateway → Auth Service → PostgreSQL + Redis`

### Hop-by-Hop Breakdown

```
Client
  │
  │  [1] TLS + NGINX Ingress (TCP handshake, TLS termination)
  ▼
NGINX Ingress                               P50: 2ms    P95: 5ms    P99: 15ms
  │
  │  [2] HTTP→gRPC gateway proxy
  ▼
API Gateway                                 P50: 1ms    P95: 3ms    P99: 8ms
  │
  │  [3] Redis: INCR rate-limit key
  │       ratelimit:{ip}:{minute}           P50: 0.5ms  P95: 1ms    P99: 3ms
  │  [4] Redis: EXPIRE (separate command)   P50: 0.5ms  P95: 1ms    P99: 3ms
  │
  │  [5] gRPC forward: API Gateway → Auth Service
  ▼
Auth Service                                P50: 2ms    P95: 5ms    P99: 15ms
  │
  │  [6] PostgreSQL: SELECT user by email
  │       (email is indexed)                P50: 2ms    P95: 8ms    P99: 20ms
  │
  │  [7] *** BLOCKING: bcrypt.Compare ***
  │       bcrypt cost factor UNSPECIFIED    P50: 100ms  P95: 150ms  P99: 200ms
  │       (cost 10 estimate — undocumented)
  │
  │  [8] PostgreSQL: SELECT account status  P50: 2ms    P95: 8ms    P99: 20ms
  │  [9] JWT sign (HMAC-SHA256 / EdDSA)     P50: 0.1ms  P95: 0.3ms  P99: 1ms
  │  [10] PostgreSQL: INSERT refresh_token  P50: 3ms    P95: 10ms   P99: 25ms
  │  [11] PostgreSQL: UPDATE last_login_at  P50: 2ms    P95: 8ms    P99: 20ms
  ▼
Auth Service → API Gateway (gRPC response)  P50: 1ms    P95: 3ms    P99: 8ms
  │
  │  [12] NGINX egress
  ▼
Client                                      P50: 2ms    P95: 5ms    P99: 15ms
```

### Login Latency Summary

| | P50 | P95 | P99 |
|---|---|---|---|
| **Total Login Latency** | **~117ms** | **~207ms** | **~353ms** |
| bcrypt contribution | 100ms (86%) | 150ms (72%) | 200ms (57%) |
| Non-crypto overhead | ~17ms | ~57ms | ~153ms |

> **Dominant term:** bcrypt is 86% of P50 login latency. Every other optimization is irrelevant without knowing the bcrypt cost factor.

---

## Critical Path 2: Order Placement (Limit Order)

**Path:** `Client → NGINX → API Gateway (auth) → Order Service → Wallet Service → PostgreSQL → Outbox → Kafka`

This is the **most latency-sensitive path** in the system. The client receives a `201 Created` after the outbox commit — matching and fill happen asynchronously.

### Hop-by-Hop Breakdown

```
Client
  │
  │  [1] NGINX: TLS termination             P50: 2ms    P95: 5ms    P99: 15ms
  ▼
API Gateway
  │
  │  [2] gRPC forward to API GW             P50: 1ms    P95: 3ms    P99: 8ms
  │  [3] Redis: rate-limit INCR             P50: 0.5ms  P95: 1ms    P99: 3ms
  │  [4] Redis: EXPIRE (separate, race!)    P50: 0.5ms  P95: 1ms    P99: 3ms
  │  [5] Redis: GET jwt:blacklist:{jti}     P50: 0.5ms  P95: 1ms    P99: 3ms
  │       (JWT revocation check)
  │  [6] JWT signature + expiry verify      P50: 0.2ms  P95: 0.5ms  P99: 2ms
  │
  │  [7] gRPC: API Gateway → Order Service  P50: 2ms    P95: 5ms    P99: 15ms
  ▼
Order Service
  │
  │  [8] Generate UUIDv7                    P50: 0.01ms P95: 0.05ms P99: 0.1ms
  │
  │  [9] *** Market status cache check ***
  │      Cache HIT: in-memory/Redis GET     P50: 0.5ms  P95: 1ms    P99: 3ms
  │      Cache MISS: gRPC → Market Service  P50: 5ms    P95: 15ms   P99: 40ms
  │       + PostgreSQL read in Market Svc
  │      (10s TTL — miss on first req/pod)
  │
  │  [10] Input validation (CPU)            P50: 0.1ms  P95: 0.3ms  P99: 1ms
  │
  │  [11] gRPC: Order Service → Wallet Service
  ▼         (ReserveFunds)                  P50: 3ms    P95: 8ms    P99: 20ms

Wallet Service (ReserveFunds)
  │
  │  [12] PostgreSQL: SELECT wallet FOR UPDATE
  │        (row lock on wallets row)        P50: 3ms    P95: 10ms   P99: 30ms
  │  [13] Balance check (application logic) P50: 0.1ms  P95: 0.2ms  P99: 0.5ms
  │  [14] PostgreSQL: INSERT reservation
  │        + UPDATE wallet + INSERT txn     P50: 5ms    P95: 15ms   P99: 40ms
  │  [15] COMMIT                            P50: 2ms    P95: 5ms    P99: 15ms
  ▼
Wallet Service → Order Service (gRPC)       P50: 2ms    P95: 5ms    P99: 15ms

Order Service
  │
  │  [16] PostgreSQL transaction:
  │        INSERT order (status=OPEN)
  │        INSERT outbox row (OrderCreated) P50: 5ms    P95: 15ms   P99: 40ms
  │  [17] COMMIT                            P50: 2ms    P95: 5ms    P99: 15ms
  ▼
Order Service → API Gateway (201 Created)   P50: 1ms    P95: 3ms    P99: 8ms
  │
  │  [18] NGINX egress
  ▼
Client receives 201 Created                 P50: 2ms    P95: 5ms    P99: 15ms
```

### Order Placement (to Client ACK) Latency Summary

| Segment | P50 | P95 | P99 |
|---|---|---|---|
| Ingress + API Gateway overhead | 4ms | 10ms | 30ms |
| JWT validation (Redis + verify) | 1.2ms | 2.5ms | 8ms |
| Market status check (cache HIT) | 0.5ms | 1ms | 3ms |
| Market status check (cache MISS) | 5ms | 15ms | 40ms |
| ReserveFunds gRPC + DB (wallet lock) | 15ms | 43ms | 120ms |
| Order DB write + outbox | 9ms | 25ms | 70ms |
| gRPC return + egress | 5ms | 13ms | 38ms |
| **Total (cache HIT)** | **~35ms** | **~95ms** | **~270ms** |
| **Total (cache MISS)** | **~40ms** | **~109ms** | **~307ms** |

> **Bottleneck:** The `ReserveFunds` call — specifically the `SELECT FOR UPDATE` on the wallet row followed by multi-row write — is 43% of P50 end-to-end latency. This is an inherently sequential, serialized database operation. Every order placement for the same user on the same asset contends for the same wallet row lock.

---

### Downstream async path (not in client latency, but in fill latency):

```
After 201 returns to client...

  Outbox publisher polls (100–250ms interval)     +50ms to +250ms
  Kafka produce (acks=all)                        +10ms to +40ms
  ME Kafka consumer poll                          +0ms to +20ms
  ME Input Queue wait                             +0ms to +10ms
  ME Event Loop: match algorithm (in-memory)      +0.01ms to +1ms
  ME publisher: Kafka produce TradeExecuted       +10ms to +40ms
  (If no match: order rests in book — no latency for client)
```

**Time from `POST /orders` to ME receiving the order:**

| | P50 | P95 | P99 |
|---|---|---|---|
| Outbox publish delay | 50ms | 200ms | 250ms |
| Kafka propagation | 10ms | 30ms | 60ms |
| ME consumer poll | 2ms | 10ms | 20ms |
| **Order → ME total** | **~62ms** | **~240ms** | **~330ms** |

> **Observation:** The outbox polling interval (100–250ms) is the single largest contributor to ME ingestion latency. It is the gap between "order accepted by Order Service" and "order enters ME book." At P99, a user could wait 330ms after their `201 Created` before their order is even visible in the order book.

---

## Critical Path 3: Trade Fill End-to-End (Order → Settled → WebSocket Push)

This path measures the full lifecycle from **order accepted by Order Service** to **user receiving a fill notification via WebSocket**.

```
                ┌─────────────────────────────────────────────────────┐
                │          ASYNC SETTLEMENT PIPELINE                  │
                └─────────────────────────────────────────────────────┘

[A] Outbox publisher picks up OrderCreated
    → Kafka produce (acks=all)                          P50: +60ms    P99: +310ms

[B] ME consumes OrderCreated
    → Event Loop match (in-memory, O(log n) price tree)
    → No I/O. Pure compute.                             P50: +0.1ms   P99: +5ms

[C] ME Kafka produce TradeExecuted
    → acks=all (3-broker ISR)                           P50: +12ms    P99: +60ms

[D] Settlement Service Kafka consume                    P50: +5ms     P99: +30ms

[E] Settlement Service → Wallet Service gRPC SettleTrade
    This is the most complex transaction in the system:

    [E.1] SettleTrade idempotency check:
          SELECT from wallet_transactions (indexed)     P50: +2ms     P99: +15ms

    [E.2] SELECT buyer reservation FOR UPDATE
          SELECT seller reservation FOR UPDATE
          (two row locks, ascending UUID order)         P50: +8ms     P99: +60ms

    [E.3] UPDATE buyer/seller wallets (2 rows)
          UPDATE 2 reservation rows
          INSERT 2 wallet_transactions rows
          INSERT 1 outbox row (TradeSettled)            P50: +10ms    P99: +80ms

    [E.4] COMMIT                                        P50: +3ms     P99: +20ms

    Total SettleTrade DB work:                          P50: +23ms    P99: +175ms

[F] Wallet Service gRPC response to Settlement Svc      P50: +2ms     P99: +15ms

[G] Outbox publisher picks up TradeSettled
    → Kafka produce (acks=all)                          P50: +60ms    P99: +310ms
    (2nd outbox polling delay — another 100–250ms wait)

[H] Notification Worker consumes TradeSettled           P50: +5ms     P99: +30ms

[I] Notification Worker:
    [I.1] processed_events INSERT (dedup check)         P50: +3ms     P99: +25ms
    [I.2] notifications INSERT (inbox entry)            P50: +4ms     P99: +30ms
    [I.3] notification_outbox INSERT                    P50: +3ms     P99: +20ms
    [I.4] COMMIT                                        P50: +2ms     P99: +15ms
    Total notification DB work:                         P50: +12ms    P99: +90ms

[J] Redis Pub/Sub publish to user channel               P50: +1ms     P99: +8ms

[K] Notification API pod receives Pub/Sub message
    → Write to client WebSocket                         P50: +1ms     P99: +5ms
```

### Full Fill Pipeline Latency (Order Accepted → WebSocket Fill Notification)

| Segment | P50 | P95 | P99 |
|---|---|---|---|
| [A] OrderCreated outbox → Kafka | 60ms | 200ms | 310ms |
| [B] ME in-memory match | 0.1ms | 1ms | 5ms |
| [C] TradeExecuted Kafka produce | 12ms | 30ms | 60ms |
| [D] Settlement consume | 5ms | 15ms | 30ms |
| [E] SettleTrade DB transaction | 23ms | 80ms | 175ms |
| [F] gRPC return | 2ms | 8ms | 15ms |
| [G] TradeSettled outbox → Kafka | 60ms | 200ms | 310ms |
| [H] Notification consume | 5ms | 15ms | 30ms |
| [I] Notification DB writes | 12ms | 40ms | 90ms |
| [J] Redis Pub/Sub publish | 1ms | 3ms | 8ms |
| [K] WebSocket push | 1ms | 2ms | 5ms |
| **Total** | **~181ms** | **~594ms** | **~1,038ms** |

> **Key insight:** The two outbox polling delays ([A] and [G]) together account for **120ms at P50** and **620ms at P99** — two-thirds of end-to-end fill notification latency. The actual matching and settlement computation is under 30ms at P50. The system's perceived speed is dominated by infrastructure polling intervals, not business logic.

---

## Critical Path 4: Order Cancellation

**Path:** `Client → API Gateway → Order Service → Kafka → ME → Kafka → Order Service → Wallet Service`

```
[1] Client → API Gateway → Order Service      P50: 8ms    P99: 50ms
    (same auth + routing as order placement)

[2] Order Service:
    SELECT order (ownership + status check)   P50: 2ms    P99: 20ms
    UPDATE status = CANCELLING
    INSERT outbox (OrderCancelRequested)      P50: 5ms    P99: 40ms
    COMMIT                                    P50: 2ms    P99: 15ms

[3] Return 202 Accepted to client             P50: 2ms    P99: 15ms

    Client P50 latency to 202 Accepted: ~19ms    P99: ~140ms
    ─────────────────────────────────────────────────────────
    [Async from here]

[4] Outbox polling delay                      P50: 60ms   P99: 310ms
[5] Kafka produce OrderCancelRequested        P50: 12ms   P99: 60ms
[6] ME consumes, removes from book           P50: 5ms    P99: 30ms
[7] ME Kafka produce OrderCancelled           P50: 12ms   P99: 60ms
[8] Order Service consumes OrderCancelled     P50: 5ms    P99: 30ms
[9] Order Service: UPDATE status=CANCELLED    P50: 3ms    P99: 20ms
[10] gRPC: Order Service → Wallet ReleaseFunds P50: 15ms  P99: 80ms
     (SELECT FOR UPDATE reservation + UPDATE wallet + COMMIT)
[11] TradeExecuted during cancel window?
     → Order Service handles race (CANCELLING + TradeExecuted → FILLED)
     → No additional latency if Fills take precedence
```

### Cancel Latency (Client to 202 Accepted)

| | P50 | P95 | P99 |
|---|---|---|---|
| **Client to 202 Accepted** | **~19ms** | **~60ms** | **~140ms** |

### Cancel Lifecycle Completion (funds returned to available_balance)

| | P50 | P95 | P99 |
|---|---|---|---|
| **From 202 to funds released** | **~112ms** | **~380ms** | **~590ms** |

> **Observation:** Users see `202 Accepted` quickly. But funds are not released back to `available_balance` until the `ReleaseFunds` gRPC call completes asynchronously, which takes 112ms (P50) after the client receives confirmation. During this window, cancelled funds are neither resting nor available — they are locked in `ACTIVE` reservation. A user who cancels and immediately places a new order may find their balance insufficient until the async cancel pipeline completes.

---

## Critical Path 5: Balance Read (`GET /wallets/balances`)

**Path:** `Client → NGINX → API Gateway (auth) → Wallet Service → PostgreSQL`

```
[1] NGINX + API Gateway overhead           P50: 4ms    P99: 25ms
[2] JWT verify + Redis blacklist check     P50: 1.2ms  P99: 8ms
[3] gRPC: API GW → Wallet Service          P50: 2ms    P99: 15ms
[4] PostgreSQL: SELECT from wallets
    WHERE user_id = $1                     P50: 2ms    P99: 20ms
    (indexed on user_id)
[5] gRPC response + NGINX egress           P50: 3ms    P99: 23ms
```

| | P50 | P95 | P99 |
|---|---|---|---|
| **Balance Read Total** | **~12ms** | **~35ms** | **~91ms** |

> **No caching is documented for balance reads.** Every `GET /wallets/balances` hits the Wallet Service PostgreSQL primary. At high user count, the balance read path creates steady-state read load on the PostgreSQL primary — a target for read replica routing.

---

## Performance Findings

---

### PERF-001

**Severity:** High
**Category:** Blocking CPU Operation on Critical Path
**Path:** Login
**Evidence:** `05_Authentication_Service.md §2`

```
Hash Password (bcrypt / argon2)
Verify Password (compare hashes)
```

The bcrypt cost factor is **not documented**. The difference in login latency between cost factors is:

| Cost Factor | Compute Time | Login P50 |
|---|---|---|
| Cost 10 (minimum recommended) | ~100ms | ~117ms |
| Cost 12 (OWASP recommended 2024) | ~300ms | ~317ms |
| Cost 14 (conservative) | ~1200ms | ~1217ms |

bcrypt is a **synchronous CPU-bound operation**. In Go, `bcrypt.CompareHashAndPassword` blocks the calling goroutine for its entire duration. The Auth Service must provision sufficient CPU cores to avoid goroutine saturation during login bursts. With default `GOMAXPROCS = 1 CPU`, a burst of 100 concurrent logins at cost 12 produces a goroutine queue where P99 latency = 100 × 300ms = 30 seconds.

**Impact:** Login P99 latency is completely unpredictable without knowing the bcrypt cost factor. Under login storm conditions (market open, news event), all available CPU cycles on Auth Service pods are saturated with bcrypt computation, starving JWT refresh and other operations.

**Recommendation:**
1. Document the bcrypt cost factor explicitly in `05_Authentication_Service.md §2.1` (add a `Security Parameters` section).
2. Set `Auth Service` CPU request to at minimum `2000m` (2 cores) to allow concurrent bcrypt operations.
3. Consider bcrypt worker pool with bounded concurrency to prevent goroutine saturation.
4. Evaluate argon2id (superior memory-hardness, parallelizable) as the algorithm choice.

---

### PERF-002

**Severity:** High
**Category:** Outbox Polling Delay — Dominant Latency Term
**Path:** Order Placement → ME, TradeExecuted → Notification
**Evidence:** `08_Order_Service.md §Outbox Publisher`

```
V1 uses a polling publisher (short interval, e.g. 100–250ms).
A background loop queries WHERE published_at IS NULL,
publishes each row to Kafka...
```

The 100–250ms outbox polling interval appears **twice** in the end-to-end fill pipeline (once for `OrderCreated`, once for `UserTradeSettled`). Combined:

- P50 impact: +120ms (two polls × 60ms average)
- P99 impact: +620ms (two polls × 250ms + Kafka produce)

This is the single largest contributor to end-to-end fill notification latency — greater than matching, settlement, and all database operations combined.

**Impact:** A user who submits a market order during high liquidity (instant fill expected) waits 181ms (P50) to receive their fill notification, with 62% of that time being outbox poll wait. A user on a slow connection at P99 waits over 1 second.

**Recommendation:**
The outbox publisher should switch from polling to **listen/notify**:
- PostgreSQL `NOTIFY` on INSERT to the outbox table
- Publisher daemon uses `pg_listen` to wake immediately on new rows
- Expected improvement: polling delay drops from 50–250ms to 1–5ms
- Net effect: P50 fill latency drops from ~181ms to ~61ms; P99 drops from ~1,038ms to ~418ms

Alternatively, reduce poll interval to 10–25ms. This increases PostgreSQL query load (approximately 4–10× more polls/sec) but removes 75% of polling latency with minimal implementation risk.

---

### PERF-003

**Severity:** High
**Category:** Synchronous gRPC in Critical Client-Facing Path
**Path:** Order Placement → ReserveFunds
**Evidence:** `08_Order_Service.md §Order Processing Model`

```
Synchronous part: generate order_id, validate request,
then reserve funds via gRPC to Wallet Service.
This must complete before an order can be considered accepted.
```

`ReserveFunds` is synchronous on the client-facing order placement path. This means:
- Wallet Service PostgreSQL latency (SELECT FOR UPDATE + multi-row write) = 15–23ms P50, 70–120ms P95
- gRPC network hop = 3–8ms
- Total `ReserveFunds` contribution = 18–31ms P50, 78–128ms P95

A Wallet Service slowdown (high lock contention at peak, slow PostgreSQL, GC pause) directly extends order placement P99 latency. At high user volumes, multiple concurrent orders from different users all contend for wallet row locks on the same wallet service PostgreSQL instance.

**Impact:** At 1000 TPS of order placements, wallet row lock contention grows O(concurrent_users × order_rate). Users with the same wallet asset contend for the same row. Order placement P99 latency degrades rapidly under lock pressure.

**Recommendation:**
This architectural choice is well-documented and intentional ("Synchronous part: must complete before order is considered accepted"). Short-term: document the P99 latency budget for `ReserveFunds` explicitly and set a circuit breaker threshold — if `ReserveFunds` exceeds 500ms, the API Gateway should return `503 Retry-After` rather than holding the connection open to the 2000ms gRPC deadline. Long-term (V2): evaluate optimistic reservation (reserve asynchronously, reject at ME if reservation not confirmed) — but this requires significant redesign.

---

### PERF-004

**Severity:** High
**Category:** SettleTrade — Heaviest Database Transaction in the System
**Path:** TradeExecuted → Wallet SettleTrade
**Evidence:** `07_Wallet_Service.md §7`

`SettleTrade` performs within a single ACID transaction:
1. `SELECT from wallet_transactions` (idempotency check)
2. `SELECT buyer reservation FOR UPDATE`
3. `SELECT seller reservation FOR UPDATE`
4. `UPDATE buyer wallet`
5. `UPDATE seller wallet`
6. `UPDATE buyer reservation (consumed_amount)`
7. `UPDATE seller reservation (consumed_amount)`
8. `INSERT 2 wallet_transactions rows` (DEBIT ledger entries)
9. `INSERT 1 outbox row`
10. `COMMIT`

This is a **9-statement transaction with 2 exclusive row locks** across 2 users. Estimated breakdown:

| Operation | P50 | P95 |
|---|---|---|
| Idempotency SELECT | 2ms | 10ms |
| Lock acquisition (2 rows) | 5–10ms | 20–40ms |
| 7 UPDATEs / INSERTs | 8ms | 25ms |
| COMMIT (WAL flush) | 3ms | 10ms |
| **Total SettleTrade DB** | **~23ms** | **~85ms** |

At high throughput (10,000 trades/sec), Settlement Service runs 10,000 `SettleTrade` transactions/sec, each locking 2 wallet rows. Lock contention probability grows with the number of simultaneous trades involving the same user. A user with many resting orders across multiple markets could see their wallet row locked by multiple concurrent `SettleTrade` calls, serializing their settlement.

**Impact:** P99 `SettleTrade` latency under lock contention can reach 150–300ms. Combined with the outbox publishing delay, end-to-end fill notification latency at P99 exceeds 1 second.

**Recommendation:**
Document the per-transaction locking profile in `07_Wallet_Service.md §7`. Ensure Settlement Service KEDA autoscaling (`trades.executed.v1` lag) is configured with an aggressive scale-out threshold (e.g., lag > 100 messages → add pod) to distribute settlement load. Investigate whether the idempotency `SELECT` check in `§8.1` can use a covering index to reduce to a pure index scan: `CREATE INDEX idx_wallet_txn_settle ON wallet_transactions(reference_id, reference_type) WHERE reference_type = 'SETTLEMENT'`.

---

### PERF-005

**Severity:** Medium
**Category:** Two-Command Redis Rate Limit — Unatomic + Extra RTT
**Path:** Every Authenticated Request
**Evidence:** `17_Redis_Architecture.md §2.2.2`

```redis
INCR ratelimit:{user_id}:{minute_timestamp}
EXPIRE ratelimit:{user_id}:{minute_timestamp} 60
```

Two separate Redis commands per request = **two round trips** to Redis per authenticated request (in addition to the JWT blacklist check). Three Redis operations per request:
1. `GET jwt:blacklist:{jti}` — 0.5ms
2. `INCR ratelimit:...` — 0.5ms
3. `EXPIRE ratelimit:...` — 0.5ms

Total Redis overhead per request: **~1.5ms P50, ~6ms P95, ~12ms P99**

This is avoidable. A single Lua script or `SET NX EX` + `INCR` pattern achieves rate limiting in **one round trip**:
- Save: ~0.5ms P50, ~2ms P95 per request
- Also fixes the race condition (SCALE-015 / ARCH-015 from prior audits)

**Impact:** Low absolute latency (1–2ms saved per request) but affects every single authenticated request across the platform.

**Recommendation:**
Replace with Lua script (one round trip):
```lua
local key = KEYS[1]
local count = redis.call('INCR', key)
if count == 1 then redis.call('EXPIRE', key, 60) end
return count
```
Saves ~0.5ms per authenticated request at P50, eliminates race condition, and reduces Redis command throughput by 33%.

---

### PERF-006

**Severity:** Medium
**Category:** Order Book Feed — 250ms Latency Floor
**Path:** ME Match → User WebSocket Order Book Update
**Evidence:** `12_Notification_Service.md §5.3`

```
The notification-service-api node reads the orderbook:{market_id}
snapshot from Redis every 250ms.
Known V1 limitation: Latency Floor up to 250ms on order book updates.
```

The documented order book feed latency floor is **250ms** regardless of trade frequency. After the ME executes a match and writes the updated order book to Redis:
- The notification API pod reads Redis **at most** every 250ms
- A match that occurs 1ms after the last poll waits 249ms for the next poll
- P99 order book update latency for clients = 250ms + Redis read + WebSocket push ≈ **255ms**

By comparison, professional exchange order book latency targets:
- Tier 1 (NYSE, Binance): < 1ms
- Tier 2 (crypto mid-tier): 10–50ms
- V1 TradeDrift documented ceiling: 250ms

**Impact:** During high-volume periods, the order book displayed to users is up to 250ms stale. Users making decisions based on the displayed order book depth see prices that may already be consumed. This is explicitly documented as accepted, but the 250ms ceiling should be surfaced as a user-facing constraint.

**Recommendation:**
The document already identifies the correct fix (ME → Redis Pub/Sub event-driven push). As an interim improvement before V2, reduce polling interval to 50ms (5× improvement, 5× Redis read load increase). At 20 API pods × 4 markets × 20 polls/sec = 1,600 reads/sec — well within Redis single-master capacity.

---

### PERF-007

**Severity:** Medium
**Category:** Market Status Cache Miss — Extra gRPC Hop on Critical Path
**Path:** First Order After Pod Start / Cache Expiry
**Evidence:** `08_Order_Service.md §Validation Rules`

```
cached in-memory/Redis with a 10-second TTL.
Fail-closed Policy: If Market Service is unreachable
and cache is cold/expired... reject all incoming order placement requests.
```

On cache miss (every 10 seconds, or on pod start), `Order Service` makes a synchronous gRPC call to `Market Service`:
- In-cluster gRPC to Market Service: +3–8ms
- Market Service PostgreSQL read: +2–8ms
- Total cache miss penalty: +5–16ms

With `HPA` scaling Order Service pods in response to load, every new pod starts with a cold cache. During a scaling event (pod scale-out takes ~30 seconds), new pods have 10-second cold-cache windows, and every order during that window pays the extra 5–16ms. More critically, **if Market Service is unreachable and the cache is expired, all orders are rejected** — this is the `fail-closed` behavior. A 10-second Market Service outage causes all order placements to fail after each pod's cache expires.

**Impact:** Cache miss penalty is acceptable (5–16ms). The availability concern is more significant: a brief Market Service disruption drains the cache within 10 seconds, causing all order placements to fail until Market Service recovers. This is intentional fail-closed behavior but has a tighter blast radius than documented.

**Recommendation:**
Extend cache TTL to 30–60 seconds for a halted-to-running market (market configuration changes infrequently). When Market Service is unreachable, serve the stale cache value with a log warning rather than failing closed, unless the cache is older than 60 seconds. This reduces the blast radius of a brief Market Service disruption from "all orders fail within 10s" to "all orders fail within 60s."

---

### PERF-008

**Severity:** Medium
**Category:** No Connection Pooler — PostgreSQL Latency Under Scale
**Path:** All PostgreSQL-backed services
**Evidence:** `18_PostgreSQL_Design.md` (absence of PgBouncer)

Without a connection pooler (PgBouncer), each HPA-scaled pod maintains its own Go `database/sql` pool. Under high load:
- PostgreSQL connection establishment: 5–15ms per new connection
- Each HPA scale-out event adds new pods with cold connection pools, incurring connection setup latency on the first transactions
- PostgreSQL's per-connection overhead (memory, background worker) grows with connection count, increasing lock scheduling latency across all connections

At scale (>50 pods across all services), PostgreSQL connection overhead starts degrading all query latencies by 5–20% as the connection scheduler cycles through more connections.

**Impact:** Gradual P99 latency degradation across all PostgreSQL-backed operations as pod count grows. Not a sharp cliff but a steady slope.

**Recommendation:** Add PgBouncer in transaction mode between each service and its PostgreSQL instance. PgBouncer reduces effective connection count from `pods × pool_size` to `pgbouncer_pool_size` (typically 10–20 connections), eliminating connection scaling overhead entirely.

---

### PERF-009

**Severity:** Medium
**Category:** Portfolio Summary — Cross-Service Fan-Out on Read Path
**Path:** `GET /portfolio/summary`
**Evidence:** `16_gRPC_Contracts.md §4.8`

`PortfolioSummaryResponse` includes:
```
cash_balance: "Dynamically pulled from Wallet USDT"
unrealized_pnl: "based on current market"
```

The `GetPortfolioSummary` gRPC call requires:
1. PostgreSQL read: portfolio holdings table
2. gRPC call to Wallet Service: `GetBalance` (for USDT cash balance)
3. Redis read: `ticker:{market_id}` for each held asset (for current price)
4. Computation: unrealized PnL per asset

If the Portfolio Service makes these calls sequentially:
- `GetBalance` gRPC: +10ms (P50)
- N × Redis `HGET ticker:{market_id}`: +0.5ms × N (P50)
- Total for 3 assets: +11.5ms P50, ~35ms P95

If made in parallel (not documented either way), the latency reduces to `max(GetBalance, ticker reads)` ≈ 10ms P50.

**Impact:** Sequential fan-out adds 10–35ms to portfolio reads for each additional cross-service call. At N held assets, Redis reads are negligible, but the `GetBalance` gRPC call to Wallet Service is on the critical read path for every portfolio request.

**Recommendation:** Document whether `GetPortfolioSummary` makes cross-service calls sequentially or in parallel (using Go goroutines + `errgroup`). If sequential, mandate parallel execution in the Portfolio Service implementation spec. A 10ms difference matters for dashboard load latency.

---

### PERF-010

**Severity:** Low
**Category:** Redis Fail-Closed on JWT Blacklist — 500ms Potential Stall
**Path:** Every Authenticated Request
**Evidence:** `17_Redis_Architecture.md §2.2.1` + `RED-1 invariant`

```
RED-1 (Fail-Closed Token Verification): The API Gateway must treat
Redis connection timeouts or unreachability during JWT blacklist checks
as validation failures, returning 500 Internal Server Error.
```

The Redis client `ConnectTimeout` is **2 seconds** (`17_Redis_Architecture.md §4`). If the Redis master is slow or unreachable, every authenticated request blocks for up to 2 seconds before returning 500. With 1000 concurrent authenticated requests and a 2-second Redis timeout, all 1000 goroutines in the API Gateway are blocked simultaneously — this is a goroutine pool exhaustion scenario.

**Impact:** A brief Redis slowdown (e.g., during failover — Sentinel promotion takes 5–30 seconds) causes a 2-second latency spike for every authenticated request, followed by 500 errors. This converts a Redis availability event into an apparent API Gateway outage.

**Recommendation:** Set Redis read timeout to **200ms** (not 2 seconds) for JWT blacklist checks. If the check times out, fail closed (as documented) but quickly — 200ms blocked goroutines recover far faster than 2 seconds. Document the timeout separately from the `ConnectTimeout` (connection establishment) in `17_Redis_Architecture.md §4`.
