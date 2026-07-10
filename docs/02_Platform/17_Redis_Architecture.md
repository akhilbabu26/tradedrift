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

#### 2.2.2 `ratelimit:{user_id | ip_address}:{minute_timestamp}`
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
