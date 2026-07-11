# TradeDrift — Scalability Architecture Audit

> **Auditor Role:** Principal Distributed Systems Architect
> **Audit Scope:** Horizontal scaling, stateless services, Kafka partitioning, consumer group scaling, Matching Engine scaling, symbol partitioning, Redis scaling, PostgreSQL scaling, read replicas, connection pooling, API Gateway scaling, WebSocket scaling, cache strategy, autoscaling, load balancing, bottlenecks, hot partitions
> **Date:** July 2026

---

## Audit Summary

| Severity | Count |
|---|---|
| **Critical** | 3 |
| **High** | 6 |
| **Medium** | 5 |
| **Low** | 2 |
| **Total** | 16 |

### Well-Scaled Areas (No Findings)
- **Stateless services** (`api-gateway`, `order-service`, `wallet-service`, `settlement-service`, `portfolio-service`) are horizontally scalable with HPA — correctly designed.
- **Kafka producer side**: `acks=all`, idempotent producer, `retries=MAX_INT` — durable under any scale.
- **Outbox pattern**: `SKIP LOCKED` fan-out supports N publisher daemons without contention — correct.
- **Settlement KEDA scaling**: consumer group scaling off `trades.executed.v1` Kafka lag — correct.
- **Topology Spread + PDB**: AZ distribution enforced for core services — correct.
- **Go WebSocket footprint**: `<4KB/connection` with `epoll`-backed server — correct and efficient.

---

## Section 1 — Matching Engine: The Central Bottleneck

### SCALE-001

**Severity:** Critical
**Domain:** Matching Engine Scaling — Singleton by Architecture, Not by Necessity
**Evidence:** `20_Deployment.md §2` (row: `matching-engine`, `StatefulSet`, replicas: `1`, "No Autoscaling")

`20_Deployment.md §2` explicitly documents:
```
matching-engine | StatefulSet | 1 | Single Active Replica (No Autoscaling)
```

`DEP-1 invariant` (`20_Deployment.md §6`):
> *"The Matching Engine StatefulSet... must never run with replicas > 1."*

`07_Concurrency_Model.md §2` documents 4 markets on one node with `4 CPU / 8 GB RAM` (`20_Deployment.md §4`).

The architecture acknowledges no scaling path for the ME. `16_Future_Enhancements.md §4.1` references V1.5 work (one consumer goroutine per partition), but no multi-node ME scaling path is documented.

**Capacity Analysis:**
- V1: 4 markets on 1 node = 4 cores, 1 Event Loop per market
- Scaling to 40 markets = 10× load on same single pod
- 100 markets = no viable single-node solution at any core count
- ME resource limits are fixed (`cpu: 4000m limit = 4000m request`) — no burst capacity

**Impact:**
The ME is the sole throughput ceiling of the exchange. Every `OrderCreated` event for every market across the entire platform passes through one Kubernetes pod. At thousands of concurrent users with hundreds of TPS per market, the ME becomes the first and hardest bottleneck. There is no documented horizontal scaling strategy for the matching hot path. Adding markets increases ME load linearly with no scale-out path documented for V1.

**Recommendation:**
Document the partition-based horizontal scaling path (`02_System_Architecture.md §17` references future cluster mode). Before production launch, establish a maximum market count per ME node based on measured per-market CPU consumption, and document the threshold at which a second ME node must be provisioned with a distinct partition assignment. V1.5's per-goroutine consumer fix is a prerequisite.

---

### SCALE-002

**Severity:** Critical
**Domain:** Single Kafka Consumer Goroutine — Cross-Market Input Starvation
**Evidence:** `07_Concurrency_Model.md §4` V1.5 note

```
V1 uses a single Kafka Consumer goroutine routing events to all market Input Queues.
If BTC-USDT's queue saturates, that single goroutine blocks and SOL-USDT routing also stalls.
```

This is self-documented as a known scalability defect, slated for V1.5. The impact:

- One consumer goroutine reads all partitions assigned to the ME node
- If any single market's Input Queue fills (bounded depth: 1000 per `§4`), the consumer goroutine **blocks**
- All other markets' order routing stops regardless of their own queue state
- At high BTC-USDT volume during a price event, SOL-USDT, ETH-USDT, DOGE-USDT all stop ingesting orders simultaneously

**Capacity Analysis:**
- Input Queue depth: 1000 events per market
- At 1000 TPS on BTC-USDT and 1ms/event processing: queue fills in 1 second
- All other markets stall for the duration of the BTC-USDT backpressure event

**Impact:**
The isolation guarantee — "markets share no state" — does not hold at the network I/O layer. A BTC-USDT trading surge (a common scenario during price discovery events) creates a platform-wide order ingestion stall. At thousands of users, simultaneous high-volume events across multiple markets (common during macro announcements) cause cascading stalls across all markets.

**Recommendation:**
Escalate the V1.5 fix (one consumer goroutine per Kafka partition) to V1 scope. This is a fundamental correctness property of the isolation guarantee, not an optimization. The implementation cost is minimal in Go.

---

### SCALE-003

**Severity:** High
**Domain:** ME Upgrade — Full Trading Halt During Deployment
**Evidence:** `20_Deployment.md §7.2`

```
7.2 Single Active Replica (Matching Engine) Upgrade Runbook:
1. Decommission Phase: active pod stops processing...
2. Re-routing: Settle/API routes pull ME pod from readiness registers
3. Provisioning Phase: new container pod starts up... replays Kafka offsets
4. Activation: Once offsets are caught up, readiness check OK
```

Every ME upgrade requires:
1. Full trading halt (all markets stop matching)
2. Kafka replay from checkpoint (duration: proportional to event backlog)
3. Zero ability to upgrade without downtime

At millions of users, any planned deployment causes a user-visible trading outage. The `Recreate` strategy enforces sequential pod replacement — there is no warm standby, no shadow deployment, no canary.

**Impact:**
At scale, a 2-minute ME downtime during peak hours affects all active traders simultaneously. Exchange SLAs (99.95% availability target per `21_Observability.md §5`) require < 26 minutes of downtime per month. A bi-weekly deployment cadence with 5-minute ME recovery each time consumes ~10 minutes of the 26-minute monthly budget — leaving almost no margin for unplanned incidents.

**Recommendation:**
Document a planned-downtime strategy with user communication: announce ME maintenance windows in advance. For the medium term, document the V2 path to hot-standby ME architecture (passive follower that mirrors book state from Kafka replay, promoted in < 30 seconds on primary failure, eliminating planned downtime).

---

## Section 2 — Kafka Partitioning

### SCALE-004

**Severity:** High
**Domain:** Hot Partition — BTC-USDT Dominates `market_id`-Keyed Topics
**Evidence:** `15_Kafka_Topic_Design.md §2`

```
orders.created.v1        | 12 partitions | key: market_id
orders.cancel-requested  | 12 partitions | key: market_id
trades.executed.v1       | 12 partitions | key: market_id
orders.cancelled.v1      | 12 partitions | key: market_id
```

All order and execution topics partition by `market_id`. With 4 markets in V1 and 12 partitions:
- BTC-USDT maps to 3 partitions (12 / 4 = 3 per market, assuming uniform hash)
- In practice, a single hash function maps each market to exactly 1 partition unless the market count equals or exceeds the partition count

At any realistic volume distribution: BTC-USDT typically generates 60–80% of total order flow on cryptocurrency exchanges. This means:
- BTC-USDT → ~1–3 partitions at ~60–80% of total throughput
- One Kafka partition = one sequential log = throughput limited to single broker write speed

With 12 partitions and 4 markets, each market gets approximately 3 partitions. But within those 3 partitions, all BTC-USDT events are co-located — BTC-USDT's 3 partitions each carry ~20-27% of total throughput while the other markets' 3 partitions each carry 7-13%.

**Capacity Analysis:**
- Kafka single partition throughput ceiling: ~100-300 MB/s depending on broker
- At 100,000 TPS (millions-of-users scale), each `OrderCreated` event ≈ 200 bytes = 20 MB/s
- BTC-USDT at 70% share = 14 MB/s across 3 partitions ≈ 4.7 MB/s per partition — within limits
- **But:** all events for a market must be in the same partition (ordering guarantee for the ME). So BTC-USDT gets exactly 1 assigned partition on the ME side, regardless of how many broker partitions exist

**Impact:**
The ME reads BTC-USDT from a single Kafka partition (required for ordering). That single partition becomes the throughput ceiling for the highest-volume market. At millions of users, BTC-USDT order throughput is bounded by the speed of one Kafka partition write + one ME Event Loop — both serial.

**Recommendation:**
Document the per-market throughput ceiling in `15_Kafka_Topic_Design.md`. For high-volume markets at scale, the only scaling path is vertical (faster broker node, faster ME CPU) until a sharded ME design is implemented. This is a known fundamental constraint of price-time priority matching — document it explicitly so capacity planning is based on reality.

---

### SCALE-005

**Severity:** Medium
**Domain:** `trades.settled.v1` — 12 Partitions by `user_id` vs Fan-Out Volume
**Evidence:** `15_Kafka_Topic_Design.md §4.5`

```
A single executed trade produces TWO separate TradeSettled messages —
one for the buyer (key: buyer_id) and one for the seller (key: seller_id)
```

Each `TradeExecuted` event produces 2× `TradeSettled` events on `trades.settled.v1`. At scale:
- 10,000 trades/second = 20,000 `TradeSettled` messages/second
- Portfolio Service, Trade Service, and Notification Service all consume this topic
- Each consumer group needs enough partition parallelism to keep up with 20,000 msg/sec

With 12 partitions and 3 consumer replicas per group (`notification-worker` is fixed at `replicas: 3` per `20_Deployment.md §2`), each worker handles 4 partitions. At 20,000 msg/sec across 12 partitions ≈ 1,667 msg/sec per partition per consumer group. This is within range for V1 volume but will saturate `notification-worker` as trading scales.

**Impact:**
Notification workers are the first downstream service to hit throughput ceilings at scale. Each worker must write to PostgreSQL (notification inbox), query `processed_events`, and publish to Redis Pub/Sub — for every `TradeSettled` event. At 20,000/sec the notification worker DB write rate is 40,000 writes/sec (two notifications per trade), which exceeds practical PostgreSQL write throughput for the notification DB.

**Recommendation:**
Separate the notification inbox write path (latency-tolerant, can batch) from the real-time push path (latency-sensitive, via Redis Pub/Sub). Document KEDA autoscaling for `notification-worker` keyed on `trades.settled.v1` lag (the `settlement-service` already uses this pattern correctly). Currently `notification-worker` is hardcoded at `replicas: 3` with no autoscaling trigger — this must become KEDA-backed before high-volume operation.

---

### SCALE-006

**Severity:** Medium
**Domain:** `portfolios.updated.v1` — Partition Count Assumption vs User Scale
**Evidence:** `15_Kafka_Topic_Design.md §2`

```
portfolios.updated.v1 | 12 partitions | key: user_id
```

At 1 million users each with an active `PortfolioUpdated` event per trade they participate in:
- 12 partitions across 1M users: ~83,333 users share each partition
- Portfolio updates are sequential per user (correct) but all users in a partition queue behind each other's update
- Portfolio Service consumer replicas are bounded by partition count (max 12 parallelism)

At millions of users with high trading activity, the 12-partition ceiling limits Portfolio Service parallelism to 12 consumer pods. Adding more pods provides no additional throughput (Kafka consumer group semantics: max active consumers = partition count).

**Impact:**
Portfolio calculation staleness increases linearly with user volume beyond 12× the per-partition throughput capacity. Users see delayed PnL/holdings updates. This is a user-experience degradation at scale, not a correctness issue (portfolio data will eventually catch up).

**Recommendation:**
Document that partition counts should be re-evaluated at 500K active users. Increasing `portfolios.updated.v1` to 48 or 96 partitions allows 48/96 parallel Portfolio Service pods. Partition count changes on existing topics require data migration planning — establish a partition scaling runbook in `15_Kafka_Topic_Design.md §2` before hitting the ceiling under production load.

---

## Section 3 — Redis Scaling

### SCALE-007

**Severity:** Critical
**Domain:** Redis Pub/Sub Backplane — Single Master Bottleneck for All WebSocket Delivery
**Evidence:** `17_Redis_Architecture.md §1.1` + `12_Notification_Service.md §2`

`17_Redis_Architecture.md §1.1`:
```
Sentinel Cluster: one master and two read replicas monitored by three-node Sentinel
```

`12_Notification_Service.md §2`:
```
notification-service-worker → publish to Redis Pub/Sub channel: user:notifications:{user_id}
notification-service-api (Pod 1 ... N) → subscribed to Redis channels
```

**Redis Pub/Sub is single-threaded on the Redis master.** All publish operations from all notification workers and all subscribe operations from all API nodes go through one Redis master's event loop. The two read replicas are for data reads — Pub/Sub messages are not replicated to replicas. Every real-time event (trade fill, portfolio update, orderbook snapshot, market trade) passes through this single Redis master.

**Capacity Analysis:**
- Redis Pub/Sub throughput: ~1M messages/sec (single-threaded, small payloads)
- At 100K concurrent WebSocket clients, each subscribed to 3 channels = 300K subscriptions on one Redis master
- At 10,000 trades/sec → 20,000 `TradeSettled` + 10,000 `TradeExecuted` events/sec → 30,000 Pub/Sub publishes/sec
- Market orderbook: N API pods polling Redis every 250ms per active market (4 markets × N pods × 4/sec) → at 20 API pods: 320 reads/sec (acceptable)
- Total Pub/Sub load scales with `active_users × subscriptions_per_user × event_rate` — all funneled through one master

At millions of users with 3–5 subscriptions each and high trade volume, Pub/Sub load exceeds single-master capacity. Redis Cluster does not support cross-slot Pub/Sub — Pub/Sub channels are not distributed.

**Impact:**
Redis Pub/Sub becomes the platform-wide WebSocket delivery bottleneck before any other component. A Redis master outage or saturation causes all real-time feeds to all users to stop simultaneously. No documented fallback exists for Pub/Sub saturation — the Redis `500 fail-closed` policy covers JWT checks, not Pub/Sub.

**Recommendation:**
Document the Redis Pub/Sub throughput ceiling in `17_Redis_Architecture.md §1`. For the scaling path, document migration to Redis Cluster with a consistent-hashing pub/sub routing layer (route user-specific channels to specific cluster shards) or replace Pub/Sub with a purpose-built fanout system (e.g., NATS JetStream, Centrifuge/Centrifugo) as a V2 target. Define the metrics that indicate approaching saturation (`redis_connected_clients`, `redis_pubsub_channels`, `redis_pubsub_patterns`).

---

### SCALE-008

**Severity:** High
**Domain:** Redis Sentinel — No Cluster Mode, Memory Ceiling is Single-Node
**Evidence:** `17_Redis_Architecture.md §1.1`

```
Sentinel Cluster: Master-replica setup, ONE master and TWO read replicas
eviction policy: volatile-lru
```

Redis Sentinel is a high-availability topology, not a horizontal scaling topology. All writes go to the single master. The total usable memory is bounded by the master node's memory limit. There is no documented memory limit or eviction watermark for the Redis master.

Key space at scale:
- `jwt:blacklist:{jti}` — one entry per active access token, per logged-in user. At 1M concurrent sessions = 1M keys. Each key ≈ 60 bytes = ~60 MB (acceptable).
- `ratelimit:{user_id}:{minute}` — one key per (user × minute window). At 1M users, 60 active-window keys = 60M entries at peak ≈ 3.6 GB.
- `dedup:trades:{trade_id}` (1-hour TTL) — at 10,000 TPS × 3600s = 36M keys ≈ 2.2 GB.
- `orderbook:{market_id}` — 4 keys, small (< 10 KB each) — negligible.
- `ticker:{market_id}` — 4 keys, small — negligible.
- Pub/Sub channel subscriptions: 1M users × 3 channels = 3M subscriptions tracked in Redis master memory.

**Estimated total memory at 1M concurrent users, 10K TPS:** ~6–10 GB plus overhead. A single Redis master needs 16–32 GB to operate safely under load.

**Impact:**
The `volatile-lru` eviction policy protects keys **without TTL** (jwt:blacklist, orderbook, ticker keys are persistent — they are eviction-exempt). Under memory pressure, rate-limit keys and dedup keys are evicted first. Rate-limit key eviction silently disables rate limiting per affected user. Dedup key eviction allows duplicate notifications. Both are silent correctness regressions.

**Recommendation:**
Set an explicit `maxmemory` limit on the Redis master and document the expected memory consumption at each user-scale tier (100K, 500K, 1M) in `17_Redis_Architecture.md §1.1`. Document the migration path to Redis Cluster at the 500K user mark. Consider adding a `maxmemory-samples` and `maxmemory-policy: allkeys-lru` for the dedup and rate-limit namespaces if they can tolerate occasional eviction, while keeping jwt:blacklist keys under a no-evict policy (e.g., separate Redis instance for security-critical keys).

---

## Section 4 — WebSocket Scaling

### SCALE-009

**Severity:** High
**Domain:** Order Book Feed — 250ms Polling Loop Scales with Market Count, Not Traffic
**Evidence:** `12_Notification_Service.md §5.3`

```
For any active market with active WebSocket subscribers, the node reads the
orderbook:{market_id} snapshot from Redis every 250ms.
Known V1 Architectural Limitations: Polling Overhead: The query cost scales
linearly with the number of active markets, not the trade rate
(e.g. a completely inactive market is still queried every 250ms).
```

Each `notification-service-api` pod runs one polling goroutine per market, regardless of whether any subscriber is connected or any trades are occurring. With N API pods and M markets:
- Redis reads/sec = N × M × 4 (250ms interval = 4 reads/sec)
- At 20 API pods × 10 markets = 800 reads/sec
- At 20 API pods × 100 markets = 8,000 reads/sec
- At 100 API pods × 100 markets = 40,000 reads/sec (entirely from polling, zero trading activity required)

The document acknowledges this: *"This does not scale indefinitely, but is suitable for V1's limited market set."* V1 has 4 markets. The threshold for "does not scale" is not defined.

**Impact:**
As market count grows, Redis read pressure grows O(api_pods × markets) regardless of trading volume. A market with zero subscribers still consumes polling capacity. This is a documented architectural limitation that becomes a production bottleneck the moment market count grows or API pods autoscale aggressively.

**Recommendation:**
The document already identifies the correct fix (ME → Redis Pub/Sub event-driven push). As an interim improvement before V2, reduce polling interval to 50ms (5× improvement, 5× Redis read load increase). At 20 API pods × 4 markets × 20 polls/sec = 1,600 reads/sec — well within Redis single-master capacity.

---

### SCALE-010

**Severity:** Medium
**Domain:** WebSocket Connection Affinity — Redis Pub/Sub Subscription Memory Per-Pod
**Evidence:** `17_Redis_Architecture.md §3.1` + `12_Notification_Service.md §7.1`

`12_Notification_Service.md §7.1`:
```
Optimization: Use epoll-backed server. Reduces footprint to <4KB per socket.
A single replica can easily hold 50,000+ idle connections.
```

`20_Deployment.md §4`:
```
notification-gateway | memory: 2Gi request, 4Gi limit | sized for 10k connections
```

There is a documented inconsistency in the connection capacity claim:
- `12_Notification_Service.md §7.1` states one replica can hold 50,000+ connections
- `20_Deployment.md §4` sizes memory for 10,000 connections

The lower figure (10K) governs the deployment, but the higher figure (50K) governs the design rationale. If HPA scales based on connection count, the trigger threshold and pod memory sizing are misaligned: a pod sized for 10K connections will OOM before reaching the 50K design capacity.

Additionally, each API pod maintains Redis Pub/Sub subscriptions for every connected user's private channels (3 channels × users_per_pod). At 10,000 users/pod × 3 channels = 30,000 Redis subscriptions per pod. As pods scale out, total Redis subscription count = 30,000 × num_pods — all held on the single Redis master.

**Impact:**
Memory sizing discrepancy means HPA may not scale soon enough, or pods OOM before the documented 50K connection ceiling. The Redis subscription pressure grows linearly with pod count — at 20 pods and 10K users/pod = 600,000 Redis Pub/Sub subscriptions on a single master.

**Recommendation:**
Align `20_Deployment.md §4` memory allocation with the documented connection capacity. If targeting 50K connections/pod: `memory: 512MB base + 4KB × 50K = ~712MB per pod` — revise the 2Gi/4Gi allocation to match actual measured footprint. Add a `websocket_active_connections` HPA rule to `notification-gateway` explicitly (currently only listed for `notification-gateway`, not defined).

---

## Section 5 — PostgreSQL Scaling

### SCALE-011

**Severity:** High
**Domain:** No PgBouncer / Connection Pooler — Direct PostgreSQL Connections at Scale
**Evidence:** `18_PostgreSQL_Design.md` + `17_Redis_Architecture.md §4` (connection pool limits for Redis)

`18_PostgreSQL_Design.md` documents no connection pooler (PgBouncer, pgpool, RDS Proxy). Each service pod opens direct PostgreSQL connections.

`17_Redis_Architecture.md §4` explicitly documents Redis connection pool limits:
```
MaxActiveConnections: 100 (per pod replica)
IdleTimeout: 60 seconds
```

No equivalent connection pool limit is documented for any PostgreSQL client. `20_Deployment.md §2` shows:
- `wallet-service`: `3+` replicas
- `order-service`: `3+` replicas
- `settlement-service`: `3+` replicas
- `notification-worker`: `3` replicas
- `portfolio-service`: `2+` replicas
- `trade-service`: `2+` replicas

Each pod maintains its own Go `database/sql` connection pool. At HPA-scaled counts of 10 pods per service and 10 connections per pool minimum = 100+ concurrent connections per service × 6 services = 600+ connections to each respective PostgreSQL instance. RDS/Aurora PostgreSQL max_connections on `db.r6g.large` ≈ 1000. At peak HPA scale (30+ pods per service), connection count easily exhausts the PostgreSQL connection limit.

**Impact:**
Without a connection pooler, HPA-driven pod scale-out directly translates to connection count growth on PostgreSQL. Beyond ~1000 total connections (across all pods to one PostgreSQL instance), new pods fail to acquire database connections. HPA cannot solve the problem — adding pods makes it worse. This is a well-known PostgreSQL scaling failure mode.

**Recommendation:**
Add PgBouncer (or Amazon RDS Proxy for Aurora) in transaction pooling mode for every PostgreSQL-backed service. Document in `18_PostgreSQL_Design.md §4`: maximum pool size per pod, pooler connection limit, and the threshold at which a second pooler instance is needed. This is not redesign — it is a mandatory operational component at any scale beyond tens of pods.

---

### SCALE-012

**Severity:** Medium
**Domain:** Single PostgreSQL Instance Per Service — No Read Replica Routing
**Evidence:** `22_Disaster_Recovery.md §2.1` + `18_PostgreSQL_Design.md`

`22_Disaster_Recovery.md §2.1`:
```
Multi-AZ replication is enabled for the primary cluster. For cross-region failover,
a passive Read Replica runs asynchronously in the designated DR region.
```

The DR read replica exists for failover, not for read scaling. No document specifies:
- Whether read-heavy queries (`ListOrders`, `GetPortfolioSummary`, `ListUserTrades`) route to read replicas
- Whether any service is configured with a read-replica connection string separate from the write primary
- What the read-vs-write ratio is per service

`16_gRPC_Contracts.md §3` shows retry behavior for read queries:
```
Read queries (e.g. GetBalance) | Read-Only | 3 retries | Linear 200ms
```
These are explicitly marked read-only, but no documentation shows them routed to a read replica.

**Impact:**
At millions of users, read-heavy query patterns (`GET /orders`, `GET /portfolio`, `GET /trades`, `GET /balance`) hit the primary PostgreSQL writer, consuming its I/O and connection capacity alongside write operations. Without read replica routing, the PostgreSQL primary becomes both the write bottleneck and the read bottleneck simultaneously.

**Recommendation:**
Document read replica routing policy in `18_PostgreSQL_Design.md §5 (new)`: specify which query paths use `db_read` (replica DSN) vs `db_write` (primary DSN). For V1, at minimum: `GetBalance`, `ListOrders`, `ListUserTrades`, `GetPortfolioSummary` should use the replica connection. The DR replica can double as a read replica for the primary region (accepting replica lag for read-only queries).

---

## Section 6 — API Gateway Scaling

### SCALE-013

**Severity:** Medium
**Domain:** API Gateway HPA Trigger — CPU/Memory Only, No Request-Rate Metric
**Evidence:** `20_Deployment.md §2`

```
api-gateway | Deployment | 3+ | HPA: CPU/Memory > 70% or request rate
```

"Or request rate" is mentioned in the comment but no request-rate HPA trigger is defined with a specific threshold or metric source. The HPA for `api-gateway` is effectively CPU/Memory only, since no KEDA configuration for request rate is documented.

API Gateway is a proxy — its CPU utilization from forwarding requests is low relative to its connection-handling and routing overhead. A high-concurrency API Gateway node can be saturated at `30% CPU` while handling 10,000 concurrent connections, because the bottleneck is goroutine scheduler overhead and file descriptor limits, not CPU math.

**Impact:**
CPU/Memory HPA may not scale the API Gateway fast enough during connection storms (e.g., market open, exchange announcement). A 30-second HPA scale-out delay during a reconnection storm means thousands of users receive `503` while waiting for pods to come online.

**Recommendation:**
Add a KEDA `ScaledObject` for `api-gateway` keyed on `http_requests_total` rate (via Prometheus adapter). Define: scale out when `rate(http_requests_total[1m]) > 5000` per pod. This is a request-rate-sensitive service; latency-based or RPS-based autoscaling is more responsive than resource-based for a proxy workload.

---

## Section 7 — Market Service Cron — Singleton at Scale

### SCALE-014

**Severity:** High
**Domain:** Market Service Cron — Single Replica Ticker Calculation Blocks All Markets
**Evidence:** `20_Deployment.md §2`

```
market-service-cron | Deployment | 1 | Single Active Replica (No Autoscaling) | N/A (Recreate Strategy)
```

`17_Redis_Architecture.md §2.1.1`:
```
TTL Policy: Persistent (No expiration); updated every 10 seconds via background cron
```

The cron role computes 24h ticker stats (OHLCV) for every active market and writes to `ticker:{market_id}` every 10 seconds. This runs as a **single pod with `Recreate` strategy** — identical scaling constraints to the Matching Engine.

At 100 markets, the cron must:
1. Query the Wallet/Trade PostgreSQL database for 24h OHLCV per market
2. Write 100 Redis HSET operations
3. Complete all of this within 10 seconds to avoid stale tickers

At 1000 markets, this becomes a 100× heavier query load on a single pod with no parallelism path.

**Impact:**
- Pod failure → all tickers go stale immediately (no replica takes over during `Recreate`)
- Ticker staleness leads to incorrect portfolio valuations (Portfolio Service uses `ticker.last_price`)
- At high market count, cron execution time may exceed 10 seconds, causing ticker refresh lag

**Recommendation:**
Document the maximum market count at which the cron can complete one full cycle within 10 seconds based on the query profile. Consider sharding the cron by market (e.g., cron-A handles markets A-M, cron-B handles N-Z) or switching to an event-driven ticker update (ME publishes ticker deltas after each match, eliminating the polling cron entirely).

---

## Section 8 — Connection Pooling and Autoscaling

### SCALE-015

**Severity:** Medium
**Domain:** Order Service Velocity Limit — In-Memory Counter Lost on Pod Restart
**Evidence:** `24_Admin_Workflows.md §3.2`

```
Order Placement Velocity Limit: 10 order placements per second per user account
Mechanism: Verified in-memory at the Order Service layer using a sliding-window counter in Redis
```

The velocity limit uses Redis for the sliding-window counter. This is correctly designed for horizontal scaling (Redis is shared across pods). However:

- `17_Redis_Architecture.md §2.2.2` documents the rate limit key: `ratelimit:{user_id | ip_address}:{minute_timestamp}`
- The key includes `{minute_timestamp}` — this is a per-minute fixed window, not a per-second sliding window
- The order velocity limit is "10 per second" but the rate limit key resets at minute boundaries

A user placing 9 orders at 11:00:59 and 9 orders at 11:01:00 (crossing a minute boundary) submits 18 orders in 1 second while each individual minute-window counter shows only 9 — below the limit. The "per second" limit is expressed as a per-minute fixed-window bucket at implementation.

**Impact:**
The velocity control is bypassable by timing order submissions to cross minute boundaries. At scale, sophisticated users can exploit this to flood the Matching Engine with 2× the intended per-second rate during boundary windows, generating double the expected Kafka throughput per user.

**Recommendation:**
Replace the `{minute_timestamp}` fixed-window approach with a true sliding-window counter: use a Redis sorted set with timestamp scores, counting entries in the last N seconds. Document the implementation detail in `24_Admin_Workflows.md §3.2` to match the stated "per second" semantics.

---

### SCALE-016

**Severity:** Low
**Domain:** NGINX Ingress — No Documented Connection Limit or Throughput Ceiling
**Evidence:** `20_Deployment.md §5`

```
Public HTTP/REST and WebSocket traffic routes through the NGINX Ingress Controller.
proxy-read-timeout: 3600  # Keep WebSockets open
```

The NGINX Ingress is deployed as a `Deployment` (not specified in the scaling table), with no documented:
- Replica count or HPA trigger
- `worker_processes` or `worker_connections` tuning
- Total concurrent WebSocket connection capacity
- TLS termination throughput

WebSocket connections persist for the session duration (up to 15 minutes idle timeout per `12_Notification_Service.md §4.1`). Each NGINX worker process holds `worker_connections` file descriptors. Default NGINX configuration supports ~1024 connections per worker. At 100K concurrent WebSocket users and default NGINX config, the ingress layer saturates well before the application layer.

**Impact:**
The ingress layer is not sized or scaled for WebSocket-heavy workloads. At scale, NGINX becomes the unexpected bottleneck before the application pods, because persistent WebSocket connections hold file descriptors for minutes/hours while HTTP API connections are short-lived.

**Recommendation:**
Document NGINX Ingress worker configuration in `20_Deployment.md §5`:
- `worker_connections 65535` per worker process
- `worker_processes auto` (= CPU count)
- Dedicate a separate NGINX Ingress instance for WebSocket traffic (`/ws` path) vs REST traffic (different resource profiles and scaling policies for each)
- Add Ingress HPA triggered on active connections count.
