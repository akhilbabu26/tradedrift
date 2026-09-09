# Notification Service — Transactional Outbox Publisher Guide

This document provides a technical explanation of the `internal/publisher` package in the **TradeDrift Notification Service** located in `services/notification/internal/publisher/`.

It details the implementation of the **Transactional Outbox Pattern**, the distributed systems failure scenarios it addresses, how it handles Redis outages, worker claim lease recovery, and complete runtime flows.

---

## Architecture Overview & Role of `internal/publisher`

The `internal/publisher` package implements the background asynchronous worker responsible for draining staged events from PostgreSQL's `notification_outbox` table and broadcasting them to Redis Pub/Sub channels for WebSocket consumption.

```
services/notification/internal/publisher/
├── publisher.go              # Publisher daemon, batching loop, retry backoff & lease recovery
├── publisher_test.go         # Unit tests with mock repository and mock Redis client
└── outage_showcase_test.go   # Integration test verifying Redis outage and recovery pipeline
```

### Core Responsibilities
1. **Asynchronous Decoupling**: Separates database transaction commits from real-time network broadcasting, ensuring that Redis latency never blocks Kafka consumers or gRPC RPCs.
2. **Guaranteed Delivery (At-Least-Once)**: Ensures every staged notification is published to Redis even across broker crashes, network partitions, or worker container restarts.
3. **Multi-Worker Concurrency Control**: Uses PostgreSQL's `SELECT ... FOR UPDATE SKIP LOCKED` combined with per-claim UUID tokens (`claim_token`) to allow multiple publisher instances to scale horizontally without duplicate publishing or claim race conditions.

---

## Problems Solved by `publisher.go`

| Problem | Failure Without Outbox Publisher | How `publisher.go` Solves It |
|---|---|---|
| **The Dual-Write Dilemma** | Publishing directly to Redis during a database transaction risks sending phantom alerts if PostgreSQL rolls back, or losing alerts if Redis is unreachable. | Persists the outbox record inside the **same ACID transaction** as the notification. The publisher drains rows only after PostgreSQL confirms disk commit. |
| **Redis Outages & Maintenance** | If Redis goes offline, direct publisher calls fail and user alerts are permanently dropped. | Events accumulate safely in PostgreSQL as `PENDING`. When Redis recovers, the publisher resumes draining in strict chronological order with zero message loss. |
| **Deadlock on Crashed Workers** | If a worker claims 50 rows (`status = 'PROCESSING'`) and crashes, those rows remain locked forever. | Enforces a **60-second lease timeout**. A background ticker every 15s sweeps abandoned rows and resets them to `PENDING`. |
| **Lease Expiry Claim Race** | Worker A claims row X. Worker A stalls for 65s. Worker B re-claims row X. Worker A recovers and tries to mark X as `PROCESSED` or reset to `PENDING`. | Uses a per-claim **`claim_token UUID`**. Worker A's token no longer matches, preventing Worker A from overwriting Worker B's active processing. |
| **Database Polling Saturation** | Continuously polling an empty outbox table at high frequency consumes excessive database CPU. | Adaptive polling: loops immediately if a batch is full (`count == BatchSize`), polls at 500ms when active, and backs off to 2s when idle. |

---

## Configuration & Tuning Parameters

The publisher behavior is configured via `publisher.Config`:

| Parameter | Type | Default | Operational Role |
|---|---|---|---|
| `BatchSize` | `int` | `50` | Maximum outbox rows claimed in a single `SELECT ... FOR UPDATE SKIP LOCKED` query. |
| `PollInterval` | `time.Duration` | `500ms` | Sleep duration between batches when partial data exists. |
| `IdleInterval` | `time.Duration` | `2s` | Sleep duration when the outbox table is empty (`count == 0`), reducing database CPU load. |
| `LeaseTimeout` | `time.Duration` | `60s` | Duration before an un-acknowledged `PROCESSING` row is considered abandoned by a dead worker. |
| `RecoveryInterval`| `time.Duration` | `15s` | Frequency of the background ticker that sweeps and resets expired leases back to `PENDING`. |
| `MaxPublishRetries`| `int` | `3` | Number of exponential backoff retry attempts per message before declaring batch failure. |

---

## Function-by-Function Deep Dive

### 1. `NewPublisher` & `DefaultConfig`

```go
func NewPublisher(repo repository.NotificationRepository, redis RedisClient, log *zap.Logger, cfg Config) *Publisher
func DefaultConfig() Config
```
- **Purpose**: Initializes the publisher worker, enforcing positive boundary clamping on all configuration parameters to prevent division-by-zero or infinite tight loops.
- **Decoupling**: Accepts the `RedisClient` interface, allowing test suites to inject mock Redis instances to simulate network disconnects.

---

### 2. `Start(ctx context.Context) error`

```go
func (p *Publisher) Start(ctx context.Context) error
```

#### Startup & Execution Mechanics:
1. **Initial Lease Sweep**: Immediately runs `RecoverStaleOutboxClaims()` on boot to reclaim any rows left `PROCESSING` by previous container crashes.
2. **Periodic Recovery Ticker**: Spawns a 15-second background ticker (`recoveryTicker`) that runs concurrent with the main polling loop.
3. **Adaptive Polling Loop**:
   - Calls `ProcessBatch(ctx)`.
   - If `count == p.cfg.BatchSize`: Immediately loops to drain the next batch without sleeping (high-throughput mode).
   - If `0 < count < p.cfg.BatchSize`: Sleeps `p.cfg.PollInterval` (500ms).
   - If `count == 0`: Sleeps `p.cfg.IdleInterval` (2s).
   - On error: Logs error, sleeps `p.cfg.IdleInterval` (2s), and retries.

---

### 3. `ProcessBatch(ctx context.Context) (int, error)`

```go
func (p *Publisher) ProcessBatch(ctx context.Context) (int, error)
```

#### Step-by-Step Batch Execution:
1. **Claim Phase**: Calls `p.repo.FetchPendingOutbox(ctx, BatchSize)`. PostgreSQL executes a CTE with `SELECT ... FOR UPDATE SKIP LOCKED` and stamps a fresh `claim_token` on all claimed rows.
2. **Iterative Publishing**: Iterates sequentially through each `model.OutboxEvent`.
3. **Publishing with Retry**: Calls `p.publishWithRetry(ctx, ev)`:
   - On Success:
     - Calls `p.repo.MarkOutboxPublished(ctx, ev.ID, ev.ClaimToken)`.
     - Validates `claim_token`. If `ErrOutboxClaimLost` is returned, logs a `Warn` (normal concurrency condition) and continues.
     - Increments `OutboxEventsPublishedTotal`.
   - On Failure:
     - Increments `OutboxPublishErrorsTotal`.
     - Calls `IncrementOutboxRetry(ctx, ev.ID, err.Error(), ev.ClaimToken)` to record failure diagnostics.
     - Calls `ReleaseOutboxClaims(ctx, remainingIDs, ev.ClaimToken)` to immediately release unhandled batch items back to `PENDING`.
     - Aborts the batch and returns an error, triggering publisher backoff.

---

### 4. `publishWithRetry(ctx context.Context, ev *model.OutboxEvent) error`

```go
func (p *Publisher) publishWithRetry(ctx context.Context, ev *model.OutboxEvent) error
```

#### Retry & Backoff Strategy:
- Executes `p.redis.Publish(ctx, ev.TargetChannel, ev.Payload)`.
- If Redis fails, backs off exponentially:
  - Attempt 1: 50ms
  - Attempt 2: 100ms
  - Attempt 3: 200ms
- Context-Aware: Immediately aborts retry backoff if `ctx.Done()` triggers.
- Returns an explicit error once all 3 attempts are exhausted.

---

## End-to-End Operational Flows & Scenarios

### Flow 1: Happy Path Batch Publishing
```
PostgreSQL "notification_outbox" (50 rows PENDING)
       │
       ▼
publisher.ProcessBatch()
       │
       ▼
repository.FetchPendingOutbox(limit=50)
       │ SELECT ... FOR UPDATE SKIP LOCKED
       │ SET status = 'PROCESSING', claim_token = "018f-token", claimed_at = NOW()
       ▼
For each event:
       │
       ├── publishWithRetry() ──► Redis PUBLISH to user:notifications:{uid}
       │                              └── Success (cmd.Err() == nil)
       ▼
repository.MarkOutboxPublished(id, claim_token="018f-token")
       │ UPDATE notification_outbox 
       │ SET status = 'PROCESSED', published_at = NOW(), claim_token = NULL
       │ WHERE id = $id AND status = 'PROCESSING' AND claim_token = $claimToken
       ▼
Metrics Inc: OutboxEventsPublishedTotal
       │
       ▼
Batch complete (count=50) ──► Immediately loops to fetch next batch
```

---

### Flow 2: Redis Outage with In-Flight Claim Release
```
publisher.ProcessBatch() claims 10 events (IDs: 1..10, Token: "token-A")
       │
       ▼
Events 1, 2 published successfully ──► Marked PROCESSED
       │
       ▼
Event 3: Redis connection drops (connection refused)
       │
       ▼
publishWithRetry() executes:
       ├── Attempt 1 (50ms)  ──► Fails
       ├── Attempt 2 (100ms) ──► Fails
       └── Attempt 3 (200ms) ──► Exhausted retries
       │
       ▼
1. repository.IncrementOutboxRetry(id=3, last_error="connection refused", token="token-A")
2. Collect remaining IDs: [3, 4, 5, 6, 7, 8, 9, 10]
3. repository.ReleaseOutboxClaims(ids=[3..10], token="token-A")
       │ UPDATE notification_outbox
       │ SET status = 'PENDING', claimed_at = NULL, claim_token = NULL
       │ WHERE id = ANY([3..10]) AND status = 'PROCESSING' AND claim_token = "token-A"
       ▼
Rows 3..10 safely revert to PENDING in PostgreSQL (Zero messages dropped)
       │
       ▼
Publisher sleeps IdleInterval (2s) awaiting Redis recovery
```

---

### Flow 3: Multi-Worker Race Prevention via `claim_token`
```
Worker A                                Worker B
   │                                       │
Claims Event X (Token = "Token-A")         │
Status: PROCESSING, ClaimedAt: T0          │
   │                                       │
[Worker A suffers 65s network freeze]       │
   │                                       │
   │                              [60s Lease Timeout Expires]
   │                              Worker B sweeps / claims Event X
   │                              Status: PROCESSING, ClaimedAt: T65
   │                              Token stamped: "Token-B"
   │                                       │
[Worker A unfreezes at T68]                │
Worker A finishes Redis publish            │
Calls: MarkOutboxPublished(X, "Token-A")   │
   │                                       │
WHERE id = X                               │
  AND status = 'PROCESSING'                │
  AND claim_token = 'Token-A'              │
   │                                       │
PostgreSQL evaluates:                      │
Current token is 'Token-B' != 'Token-A'    │
RowsAffected == 0                          │
   │                                       │
Returns: repository.ErrOutboxClaimLost     │
   │                                       │
Worker A logs Warn & drops ownership ──────┼──► Worker B completes publish
(Zero data corruption or double publish)          and marks PROCESSED with "Token-B"
```

---

### Flow 4: Database Failure on `MarkOutboxPublished` with Immediate Batch Release
```
publisher.ProcessBatch() claims 10 events (IDs: 1..10, Token: "token-A")
       │
       ▼
Event 1: Redis publish ✅
       │
       ▼
MarkOutboxPublished(id=1, token="token-A") fails (e.g. PostgreSQL connection timeout)
       │
       ▼
1. Worker catches real DB error (not ErrOutboxClaimLost)
2. Collects current + remaining IDs: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
3. repository.ReleaseOutboxClaims(ids=[1..10], token="token-A")
       │ UPDATE notification_outbox
       │ SET status = 'PENDING', claimed_at = NULL, claim_token = NULL
       │ WHERE id = ANY([1..10]) AND status = 'PROCESSING' AND claim_token = "token-A"
       ▼
Rows 1..10 immediately revert to PENDING
(Zero 60-second lease stalling; next poll or another worker can pick them up immediately)
```

