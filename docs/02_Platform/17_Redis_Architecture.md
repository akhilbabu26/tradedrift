# TradeDrift — Redis Architecture Specification

> **Status:** ✅ Designed (V1)
> **Document:** 17_Redis_Architecture.md
> **Service:** Platform Architecture
> **Version:** V1.0
> **Last Updated:** July 2026

---

## 1. Redis Topology & Deployment Configuration

The TradeDrift platform utilizes Redis as a high-performance in-memory database, distributed cache, and message routing backplane. 

```
                       [ Redis Sentinel (3 Nodes) ]
                                    │
                       (Monitors / Elects Master)
                                    ▼
       ┌───────────────────► [ Redis Master ] ◄───────────────────┐
       │                            │                             │
    (Reads /                     (Replicates)                  (Reads /
    Writes)                         ▼                           Writes)
       │                    [ Redis Replica ]                     │
       │                                                          │
[ API Gateway / Auth ]                                  [ Notification Service ]
```

### 1.1 Deployment Topology
* **Sentinel Cluster:** Master-replica setup consisting of **one master** and **two read replicas** monitored by a **three-node Redis Sentinel** cluster. Sentinels run quorum-based auto-failover (minimum quorum size of 2).
* **Connection Routing:** All platform services connect to the cluster via Sentinel client libraries, resolving the active master dynamically.
* **Eviction Policy (`maxmemory-policy`):** Configured as **`volatile-lru`**. This allows transient cache entries (which are stored with a TTL) to be evicted under memory pressure, while securing critical persistent states like JWT blacklists and rate-limiting keys from eviction.
* **Persistence Configuration:**
  - **Append Only File (AOF):** Enabled (`appendonly yes`, `appendfsync everysec`). AOF ensures that token blacklists and security states are durable and survive power cycles.
  - **RDB Snapshotting:** Disabled (`save ""`) to avoid disk write spikes blocking the event loop, relying entirely on AOF and read replicas for durability.

---

## 2. Platform Key Catalog & Schemas

The following tables describe all Redis keys, data structures, expiration details, and fallback procedures utilized across the microservices ecosystem.

### 2.1 Caching & Projection Keys

#### 2.1.1 `ticker:{market_id}`
* **Data Type:** Hash
* **Owner Service:** Market Service (Cron Role updates, API Role reads)
* **TTL Policy:** Persistent (No expiration; updated every 10 seconds via background cron)
* **Fields:**
  ```ini
  low_24h: "56500.0000000000"       ; Lowest match price in 24h (String)
  high_24h: "59100.0000000000"      ; Highest match price in 24h (String)
  volume_24h: "1420.5000000000"     ; Sum of match quantities in 24h (String)
  open_24h: "57200.0000000000"      ; First price matching t > NOW - 24h (String)
  last_price: "58200.0000000000"    ; Most recent execution price (String)
  price_change_percent: "1.74"      ; ((last_price - open_24h) / open_24h) * 100
  ```
* **Fallback Strategy:** If Redis is down, the Query API falls back to Postgres. To protect Postgres from connection spikes, the API uses Go's `singleflight` library to coalesce duplicate requests, caching the query result in local memory for **1 second**.

#### 2.1.2 `orderbook:{market_id}`
* **Data Type:** String (JSON representation of L2 orderbook snapshot)
* **Owner Service:** Matching Engine (writes), Market Service (reads)
* **TTL Policy:** Persistent (No expiration; overwritten on change)
* **Payload Structure:**
  ```json
  {
    "market_id": "BTC-USDT",
    "timestamp": 1783685240000,
    "bids": [["58200.00", "1.25"], ["58190.00", "0.50"]],
    "asks": [["58210.00", "0.85"], ["58220.00", "2.10"]]
  }
  ```
* **Fallback Strategy:** If Redis is down, the Market Service immediately returns `503 Service Unavailable` (`market_data_temporarily_unavailable`). Because the book is in-memory and transient, reconstructing depth from historical records is prohibited.

---

### 2.2 Security & Rate Limiting Keys

#### 2.2.1 `jwt:blacklist:{jti}`
* **Data Type:** String
* **Owner Service:** Authentication Service (writes), API Gateway (reads)
* **TTL Policy:** Set dynamically to the remaining lifespan of the JWT token.
* **Value:** `"1"`
* **Fallback Strategy:** If Redis is down, the API Gateway operates a **fail-closed** policy for JWT verification, returning a `500 Internal Server Error` to prevent potentially revoked access tokens from breaching security boundaries.

#### 2.2.2 `auth:token_version:{user_id}`
* **Data Type:** String (integer)
* **Owner Service:** Authentication Service (writes on password change / logout-all), API Gateway (reads on every authenticated request)
* **TTL Policy:** `86400` seconds (24 hours). Refreshed from PostgreSQL on cache miss.
* **Value:** Current integer token version (e.g. `"3"`)
* **Purpose:** Every JWT access token embeds the `token_version` at issuance time. On each request, the API Gateway compares the token's embedded version against this Redis value. A mismatch (e.g. after a password change) immediately invalidates all previously issued tokens for that user without requiring a database query on every request.
* **Cache-Aside Flow:**
  ```
  1. Read Redis key auth:token_version:{user_id}
  2. HIT  → compare with JWT claim → pass or reject
  3. MISS → query PostgreSQL users.token_version → write to Redis (TTL 24h) → compare
  ```
* **Fallback Strategy:** If Redis is down, fall back directly to PostgreSQL for token version lookup. Do not skip the check — this is a security invariant.

#### 2.2.3 `otp:{user_id}`
* **Data Type:** String
* **Owner Service:** Authentication Service
* **TTL Policy:** `300` seconds (5 minutes). Deleted immediately on successful verification.
* **Value:** The 6-digit OTP code (e.g. `"482910"`)
* **Purpose:** Stores the time-limited one-time password issued during email verification, password reset, and re-verification flows. Redis TTL provides automatic expiry without requiring a cleanup job.
* **Fallback Strategy:** If Redis is down, OTP issuance and verification are unavailable. Return `503 Service Unavailable`. Do not fall back to PostgreSQL — OTPs must not be persisted to durable storage.

#### 2.2.4 `otp:attempts:{user_id}`
* **Data Type:** String (integer counter)
* **Owner Service:** Authentication Service
* **TTL Policy:** Matches the TTL of the corresponding `otp:{user_id}` key (5 minutes).
* **Value:** Number of failed verification attempts (e.g. `"3"`)
* **Purpose:** Brute-force protection. Incremented on every failed OTP attempt. When the counter reaches 5, the `otp:{user_id}` key is deleted immediately — invalidating the OTP and forcing the user to request a new one.
* **Command Sequence:**
  ```redis
  INCR otp:attempts:{user_id}
  -- If result >= 5:
  DEL otp:{user_id}
  DEL otp:attempts:{user_id}
  ```
* **Fallback Strategy:** Same as `otp:{user_id}` — OTP operations unavailable when Redis is down.

#### 2.2.5 `ratelimit:{user_id | ip_address}:{minute_timestamp}`
* **Data Type:** String (Integer counter)
* **Owner Service:** API Gateway
* **TTL Policy:** `60` seconds (Expired automatically)
* **Command Sequence:**
  ```redis
  INCR ratelimit:018f60f3-a120-7798-8422-cfb6a29e11aa:1783685240
  EXPIRE ratelimit:018f60f3-a120-7798-8422-cfb6a29e11aa:1783685240 60
  ```
* **Fallback Strategy:** If Redis is down, the API Gateway falls back to local in-memory token-bucket rate limiters per replica pod, preventing service blockages.

---

### 2.3 Deduplication Keys

#### 2.3.1 `dedup:trades:{trade_id}`
* **Data Type:** String
* **Owner Service:** Notification Service
* **TTL Policy:** `3600` seconds (1 hour)
* **Value:** `"1"`
* **Deduplication Check Command:**
  ```redis
  SET dedup:trades:018f60f3-c540-7798-8422-efa6b29f1234 1 EX 3600 NX
  ```
* **Fallback Strategy:** If Redis is down, bypass deduplication. Event notifications are published to the backplane immediately (duplicate feeds may show temporarily on visual tickers, but ledger balances remain unaffected).

---

## 3. WebSocket Pub/Sub Routing Backplane

Redis Pub/Sub acts as the distribution mesh routing real-time updates to WebSocket server instances holding active client socket connections.

```
                  [ TradeExecuted Kafka Event ]
                                │
                  [ Notification Worker Pod ]
                                │
                 (Deduplicates via SETNX key)
                                │
               (Publishes to Redis Pub/Sub backplane)
                                │
                    ┌───────────┴───────────┐
                    ▼                       ▼
           [ WebClient API Pod 1 ] [ WebClient API Pod 2 ]
             (Subscribes to channel) (Subscribes to channel)
                    │                       │
              (Client Socket)         (Client Socket)
```

### 3.1 Pub/Sub Channel Catalog

| Channel Pattern | Event Publisher | Payload Content | Subscriber |
|---|---|---|---|
| `market:trades:{market_id}` | Notification Service Worker | Individual executed trade details | API Gateway WebSocket Nodes |
| `market:orderbook:{market_id}` | Notification Service Worker | L2 depth updates (JSON) | API Gateway WebSocket Nodes |
| `user:notifications:{user_id}` | Notification Service Worker | Transaction fill status / Wallet deposits | API Gateway WebSocket Nodes |
| `user:portfolio:{user_id}` | Portfolio Service | Updated holdings balance / PnL profiles | API Gateway WebSocket Nodes |

### 3.2 Dynamic Subscription Flow & Memory Leak Avoidance
To prevent memory leaks and subscription saturation:
1. **Subscribe on Connect:** When a client establishes a WebSocket connection and successfully requests a feed, the holding API pod joins the corresponding Redis channel via `SUBSCRIBE`.
2. **Multiplexing:** The API pod maintains a single Redis connection client for subscriptions, multiplexing multiple client sockets subscribing to the same public channels.
3. **Cleanup on Disconnect:** The moment a socket connection closes or drops, the API pod decrements the channel's subscription count and issues `UNSUBSCRIBE` if the subscription count for that channel reaches zero.

---

## 4. Performance & Querying Standards

To maintain high performance and low CPU overhead:
* **Batch Operations (`MGET`):** When fetching tickers for all active trading pairs (e.g. in `GET /markets/tickers`), the Market Service must perform a single batch call using `MGET` (or pipeline hashes) instead of multiple sequential `HGETALL` commands.
* **Connection Pooling:** All microservices must use a connection pool (e.g. `go-redis` Cluster/Sentinel client) with limits configured to match resource limits:
  - `MaxActiveConnections`: `100` (per pod replica)
  - `IdleTimeout`: `60` seconds
  - `ConnectTimeout`: `2` seconds

---

## 5. Service Invariants

- **RED-1 (Fail-Closed Token Verification):** The API Gateway must treat Redis connection timeouts or unreachability during JWT blacklist checks as validation failures, rejecting requests with status `500`.
- **RED-2 (Atomic Rate Limit Increment):** Rate limiters must use atomic increments (`INCR`) and set expirations to prevent race conditions from inflating limits or creating permanent locks.
- **RED-3 (Redis Subscription Cleanup):** API WebSocket nodes must clean up and unsubscribe from unused Redis channels immediately upon socket closure to prevent subscription leaks.

---

## 6. Architectural Principle — Shared Redis, Separate PostgreSQL

Every microservice in TradeDrift owns its own **private PostgreSQL database**. No service ever reads another service's database directly. This is the microservices data ownership rule.

However, all services connect to the **same shared Redis cluster**. This is intentional and correct because Redis serves a different purpose — it is a performance layer and communication backplane, not a source of truth.

```
Matching Engine ──────────────┐
Market Service ───────────────┤
Auth Service ─────────────────┤──► Same Redis Sentinel Cluster
API Gateway ──────────────────┤
Portfolio Service ────────────┤
Notification Service ─────────┘

Each service has its OWN PostgreSQL:
  auth_db        ← Auth Service only
  order_db       ← Order Service only
  wallet_db      ← Wallet Service only
  market_db      ← Market Service only
  portfolio_db   ← Portfolio Service only
```

**Why Redis can be shared but PostgreSQL cannot:**

PostgreSQL holds permanent, authoritative business data (user accounts, balances, orders, trades). Sharing it would create tight coupling and allow one service to corrupt another's data.

Redis holds derived, temporary, or cacheable data (snapshots, cache entries, pub/sub channels). If Redis loses all its data, every piece of it can be rebuilt from PostgreSQL. No permanent data is ever lost.

> **PostgreSQL is the source of truth. Redis is the performance layer.**

---

## 7. Key Name Convention — The Shared Contract Between Services

Services communicate through Redis using a simple convention: a **fixed, agreed-upon key name**. No service passes a pointer or ID to another service. Both sides simply know the key pattern from the start.

Example — Matching Engine and Market Service share the order book:

```go
// Matching Engine writes (after every match):
redis.Set(ctx, "orderbook:BTC_USDT", snapshot, 0)

// Market Service reads (on every API request):
val := redis.Get(ctx, "orderbook:BTC_USDT")
```

No gRPC call. No Kafka message. No ID exchanged. The key name `orderbook:{market_id}` is the contract — both services know it from the architecture definition.

### Complete Key Name Registry

| Key Pattern | Writer | Readers |
|---|---|---|
| `orderbook:{market_id}` | Matching Engine | Market Service |
| `ticker:{market_id}` | Market Service cron | Market Service API, Portfolio Service |
| `jwt:blacklist:{jti}` | Auth Service | API Gateway |
| `auth:token_version:{user_id}` | Auth Service | API Gateway |
| `otp:{user_id}` | Auth Service | Auth Service |
| `otp:attempts:{user_id}` | Auth Service | Auth Service |
| `ratelimit:{id}:{timestamp}` | API Gateway | API Gateway |
| `dedup:trades:{trade_id}` | Notification Service | Notification Service |
| `market:trades:{market_id}` (Pub/Sub) | Notification Service | WebSocket nodes |
| `user:notifications:{user_id}` (Pub/Sub) | Notification Service | WebSocket nodes |

---

## 8. Redis Failure & Recovery Strategy

### 8.1 Primary Protection — Redis Sentinel Failover

TradeDrift deploys Redis in **Sentinel mode** (1 master + 2 replicas + 3 sentinel monitors). If the Redis master crashes, Sentinel automatically elects a replica as the new master within approximately 5–10 seconds. All services reconnect automatically via the Sentinel client library. Most failures are handled transparently with only a brief pause.

### 8.2 If Redis is Completely Unavailable — Per-Feature Behaviour

Each Redis use case has an independently defined fallback to prevent total system failure:

| Feature | Redis Down → Behaviour |
|---|---|
| **Order book** (`orderbook:`) | Market Service returns `503 Service Unavailable`. Trading continues. |
| **Ticker** (`ticker:`) | Market Service falls back to PostgreSQL query using `singleflight` coalescing. Slightly slower, fully functional. |
| **JWT blacklist** (`jwt:blacklist:`) | API Gateway **fails closed** — returns `500`. All requests blocked. Security is preserved over availability. |
| **Token version** (`auth:token_version:`) | Falls back to PostgreSQL directly. Correct but slower. |
| **Rate limiting** (`ratelimit:`) | Falls back to local in-memory rate limiter per pod. Still protected, not globally coordinated. |
| **OTP codes** (`otp:`) | OTP issuance and verification return `503`. Users cannot verify email until Redis recovers. |
| **WebSocket Pub/Sub** | Live feeds pause. No data loss — events remain in Kafka for replay when recovered. |
| **Trade deduplication** (`dedup:`) | Deduplication bypassed. Duplicate feeds may appear briefly on visual tickers; ledger balances are unaffected. |

### 8.3 After Redis Restarts — Data Recovery

Redis is never the source of truth. Every key can be rebuilt from PostgreSQL or from the owning service's own state.

```
Redis restarts (empty memory)
      │
      ├── orderbook:{market_id}
      │     Recovery: Matching Engine writes a fresh snapshot after the
      │     very next order event. Recovered automatically in seconds.
      │
      ├── ticker:{market_id}
      │     Recovery: Market Service cron queries PostgreSQL trades table
      │     for all trades in the last 24 hours and recalculates all
      │     ticker fields on startup. Fully recovered in one query.
      │
      │     SELECT price, qty, executed_at FROM trades
      │     WHERE market_id = 'BTC_USDT'
      │       AND executed_at > NOW() - INTERVAL '24 hours'
      │     ORDER BY executed_at ASC
      │     → recalculate last_price, high_24h, low_24h, volume_24h, open_24h
      │     → HSET ticker:BTC_USDT ...
      │
      ├── jwt:blacklist:{jti}
      │     Recovery: AOF (Append Only File) is enabled. Redis replays
      │     the AOF log on startup and restores all blacklist keys
      │     automatically. No manual intervention needed.
      │
      ├── auth:token_version:{user_id}
      │     Recovery: Cache-aside pattern. Each user's version is fetched
      │     from PostgreSQL on their first request after restart and written
      │     back to Redis (TTL 24h). Repopulates organically under traffic.
      │
      ├── ratelimit:{id}:{timestamp}
      │     Recovery: Not needed. Counters reset to zero is acceptable.
      │     A brief window of unenforced rate limits is tolerable.
      │
      ├── otp:{user_id}
      │     Recovery: Not needed. OTPs are short-lived (5 minutes).
      │     Users request a new OTP if theirs expired during the outage.
      │
      └── Pub/Sub channels
            Recovery: Notification Service re-subscribes automatically
            on reconnect. No historical messages are lost — they remain
            in Kafka and will be processed when consumers resume.
```

### 8.4 The AOF Exception — JWT Blacklist Must Survive Restarts

All Redis data is considered ephemeral **except the JWT blacklist**. A revoked token must remain revoked even if Redis crashes and restarts. A user who logged out must not be able to log back in using their old access token.

This is why TradeDrift enables AOF persistence specifically for this requirement:

```
appendonly yes
appendfsync everysec
```

AOF writes every Redis operation to disk. On restart, Redis replays the log and restores the blacklist state before accepting any connections. This is the only Redis data that is not fully reconstructible from PostgreSQL alone.

