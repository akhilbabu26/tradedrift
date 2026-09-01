# `internal/publisher` — Egress, Trade Publication, Depth Broadcasting & Checkpointing

**Package:** `publisher`  
**Service:** Matching Engine  
**Files Covered:** `publisher.go`, `publisher_test.go`, `retention_test.go`  
**Documentation:** `02READEME.md`  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Purpose

The `internal/publisher` package serves as the **downstream egress, broadcast, and durability layer** of the Matching Engine.

Positioned immediately after the in-memory matching loops ([`internal/market`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/02READEME.md)), the `Publisher` consumes matching results ([`orderbook.MatchResult`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/result.go#L67-L75)) from each engine's `OutputQueue` and coordinates writes across three critical infrastructure destinations:
1. **Kafka (`trades.executed`)**: Publishes individual matched trade events with deterministic trade IDs, buyer/seller IDs, prices, quantities, and authoritative sequence numbers.
2. **Redis (`depth:{market_id}`)**: Pushes real-time Level-2 Top-20 depth snapshots for low-latency market data consumption by API gateways and WebSockets.
3. **PostgreSQL Checkpoint Coordinator (`internal/checkpoint`)**: Submits completed events (sequences, optional snapshots with SHA-256 checksums, and source Kafka positions) to advance the contiguous durability watermark in `kafka_checkpoints`, `market_sequences`, and `market_snapshots`.
4. **Automated Snapshot Retention**: Runs a background maintenance job that prunes historical snapshots from PostgreSQL while strictly preserving the recovery anchor snapshot.

---

## 2. Core Problems Solved & Why This Package Is Needed

### 2.1 Multi-System Egress Coordination with Strict Ordering
A single matched order can produce multiple trade fills, a new Top-20 market depth, and a checkpoint advancement. If these writes were executed out of order or without coordination:
- A trade could be executed without being published to Kafka (lost trade execution).
- A checkpoint could advance before Kafka receives the trade, causing lost events on crash.
- **The Solution**: The `Publisher.process()` method enforces a strict 3-step sequential pipeline:
  1. **Publish Trades to Kafka**: If Kafka write fails, aborts immediately. Checkpoint does not advance.
  2. **Push Depth to Redis**: Updates the Level-2 depth projection.
  3. **Advance Checkpoint via Coordinator**: Submits event to `checkpointCoordinator.MarkDoneWithSequence()`. Offsets advance in PostgreSQL and Kafka broker only after all contiguous preceding events are complete.

### 2.2 Fail-Closed Egress Protection (Issue #1 & #2)
In financial exchange infrastructure, if the messaging bus (Kafka) or the state cache (Redis) fails, continuing to match orders without egress visibility creates unobservable, un-settled trades.
- If `process()` encounters an egress failure:
  - Logs a `FATAL` error with topic, partition, offset, and market ID.
  - Invokes `p.HaltCallback()` or `engine.HaltCallback()`, halting the matching engine immediately.
  - Prevents corrupt or uncommitted state progression.

### 2.3 Authoritative Monotonic Sequence Broadcasting
Every trade fill and depth snapshot emitted by the Publisher carries the authoritative matching engine sequence counter:
- **Kafka Trade Messages**: Include `Sequence` to enable downstream services (Order Service, Settlement/Wallet Service) to process trades in strictly deterministic order.
- **Redis Depth Snapshots**: Include `Sequence` so WebSocket clients and frontends can reject stale out-of-order network packets.

### 2.4 Automated Snapshot Pruning with Recovery Anchor Protection
As matching engines generate periodic snapshots, PostgreSQL `market_snapshots` table grows continuously.
- **The Risk**: Blindly deleting old snapshots (e.g. `DELETE WHERE created_at < NOW() - INTERVAL '1 day'`) could delete the *only* snapshot below the partition checkpoint, breaking startup recovery!
- **The Solution ([`runRetention`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/publisher/publisher.go#L129-L154))**: Runs hourly SQL pruning that keeps the 3 most recent snapshots per market **AND** strictly protects the recovery anchor snapshot:
  $$\text{Anchor} = \text{Latest snapshot satisfying } \text{offset} \le \text{kafka\_checkpoints.offset}$$

### 2.5 Graceful Shutdown Draining & Timeout Management
When the service receives a termination signal (`ctx.Done()`):
- Spawns a 5-second drain window (`context.WithTimeout(..., 5*time.Second)`).
- Empties all pending `MatchResult` events from `engine.OutputQueue`.
- Atomically tracks drain failures via `drainFailed` flag (`HasDrainFailed()`).

---

## 3. End-to-End Pipeline Architecture

```
                       MarketEngine.OutputQueue
                                  │
                          (chan receive)
                                  ▼
                        Publisher.process()
                                  │
     ┌────────────────────────────┼────────────────────────────┐
     │ Step 1                     │ Step 2                     │ Step 3
     ▼                            ▼                            ▼
[Kafka: trades.executed]   [Redis: depth:{id}]      [Checkpoint Coordinator]
- Key = MarketID           - Key = depth:BTC-USDT   - Sequence upsert
- Payload = Trade Details  - Payload = Top-20 JSON  - Snapshot + Checksum upsert
- Sequence included        - Sequence included      - Contiguous offset commit
     │                            │                            │
     └────────────────────────────┼────────────────────────────┘
                                  │ (All Steps Succeeded)
                                  ▼
                         Continue Next Event
```

---

## 4. External Packages & Dependencies

| Package | Purpose & Justification |
| :--- | :--- |
| `context` | Manages request lifecycles, cancellation propagation during graceful shutdown, and 5-second drain timeouts. |
| `encoding/json` | Marshals trade execution messages (`tradeExecutedMessage`) and depth messages (`depthSnapshotMessage`) into canonical JSON. |
| `fmt` | Error formatting and wrapping (`%w`). |
| `log` | Operational logging of matches, rested orders, periodic retention status, and fatal fail-closed alerts. |
| `sync` & `sync/atomic` | Mutex locking for depth retry buffers (`retryMu`) and thread-safe atomic flags for shutdown tracking (`atomic.StoreInt32`, `atomic.LoadInt32`). |
| `time` | Timestamping trade executions, depth snapshots (RFC3339Nano), retry tickers (500ms), and hourly retention job tickers (1h). |
| `github.com/jackc/pgx/v5/pgconn` | Low-level PostgreSQL execution tag model (`pgconn.CommandTag`). |
| `github.com/redis/go-redis/v9` | Redis client adapter for publishing Level-2 depth keys (`SET depth:{market_id}`). |
| `github.com/segmentio/kafka-go` | High-throughput Kafka writer (`kafkago.Writer`) configured for `TopicTradeExecuted`, partitioned by `MarketID`. |
| `tradedrift/.../checkpoint` | Coordinator interface for atomic multi-event contiguous watermark commits. |
| `tradedrift/.../market` | Internal domain engine and lifecycle callbacks. |
| `tradedrift/.../orderbook` | Domain models (`MatchResult`, `Fill`, `DepthSnapshot`, `KafkaPosition`, `Checksum`). |

---

## 5. Detailed Component & Function Breakdown

### 5.1 Interfaces

1. **`dbWriter`**:
   ```go
   type dbWriter interface {
       Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
   }
   ```
   - Used for executing snapshot retention pruning queries.

2. **`kafkaWriter`**:
   ```go
   type kafkaWriter interface {
       WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
   }
   ```
   - Abstracts publishing trade execution messages to Kafka.

3. **`redisWriter`**:
   ```go
   type redisWriter interface {
       Set(ctx context.Context, key string, value []byte, expiration time.Duration) error
   }
   ```
   - Abstracts writing depth snapshots to Redis.

4. **`checkpointCoordinator`**:
   ```go
   type checkpointCoordinator interface {
       MarkDoneWithSequence(ctx context.Context, event checkpoint.CompletedEvent) error
   }
   ```
   - Coordinates multi-market contiguous watermark advancement with PostgreSQL and Kafka broker commits.

---

### 5.2 Wire Format Payloads

#### `tradeExecutedMessage` (`trades.executed`)
```json
{
  "trade_id": "019163f5-93b6-710b-b187-2c93b6710bb1",
  "market_id": "BTC-USDT",
  "sequence": 183421,
  "maker_order_id": "019163f5-93b6-710b-b187-2c93b6710bb2",
  "taker_order_id": "019163f5-93b6-710b-b187-2c93b6710bb3",
  "buy_order_id": "019163f5-93b6-710b-b187-2c93b6710bb2",
  "sell_order_id": "019163f5-93b6-710b-b187-2c93b6710bb3",
  "buyer_user_id": "019163f5-93b6-710b-b187-2c93b6710bb4",
  "seller_user_id": "019163f5-93b6-710b-b187-2c93b6710bb5",
  "price": "65000.00",
  "quantity": "0.5000",
  "executed_at": "2026-08-25T11:00:00.123456789Z"
}
```

#### `depthSnapshotMessage` (`depth:{market_id}`)
```json
{
  "market_id": "BTC-USDT",
  "sequence": 183422,
  "bids": [{"price": "64990.00", "quantity": "1.2500"}],
  "asks": [{"price": "65010.00", "quantity": "0.8000"}],
  "snapshot_at": "2026-08-25T11:00:00.123456789Z"
}
```

---

### 5.3 Functions & Methods in `publisher.go`

#### `NewPublisher(brokers []string, rdb *redis.Client, coord checkpointCoordinator, db dbWriter) *Publisher`
- **Purpose**: Production constructor.
- **Details**:
  - Initializes `kafkago.Writer` with `TopicTradeExecuted`, `Balancer: LeastBytes{}`, `RequiredAcks: RequireOne`.
  - Instantiates `redisClientAdapter` wrapping Redis connection.
  - Spawns background goroutine for `startRetentionJob` if `db != nil`.

#### `startRetentionJob(ctx context.Context)` & `runRetention(ctx context.Context) error`
- **Purpose**: Runs periodic background snapshot pruning every 1 hour.
- **Retention SQL Logic**:
  ```sql
  WITH ranked AS (
      SELECT market_id, sequence,
             ROW_NUMBER() OVER (PARTITION BY market_id ORDER BY sequence DESC) as rn
      FROM market_snapshots
  ),
  anchors AS (
      SELECT DISTINCT ON (ms.market_id) ms.market_id, ms.sequence
      FROM market_snapshots ms
      JOIN kafka_checkpoints kc ON kc.partition = ms.partition AND kc.topic = 'orders.commands'
      WHERE ms.offset <= kc.offset
      ORDER BY ms.market_id, ms.offset DESC
  )
  DELETE FROM market_snapshots ms
  WHERE NOT EXISTS (
      SELECT 1 FROM ranked r
      WHERE r.market_id = ms.market_id AND r.sequence = ms.sequence AND r.rn <= 3
  )
  AND NOT EXISTS (
      SELECT 1 FROM anchors a
      WHERE a.market_id = ms.market_id AND a.sequence = ms.sequence
  );
  ```
- **Guarantees**: Always retains the latest 3 snapshots per market **AND** the recovery anchor snapshot.

#### `Run(ctx context.Context, engine *market.MarketEngine)`
- **Purpose**: Dedicated per-market output consumer loop.
- **Workflow**:
  1. Pulls `result` from `engine.OutputQueue`.
  2. Executes `p.process(ctx, result)`.
  3. If error occurs, logs a fatal message and calls `p.HaltCallback()` / `engine.HaltCallback()`.
  4. On 500ms ticker, calls `flushPendingDepthRetries`.
  5. On `ctx.Done()`, initiates a 5-second timeout drain loop, setting `p.drainFailed = 1` if drain fails.

#### `process(ctx context.Context, result orderbook.MatchResult) error`
- **Purpose**: The core 3-step egress pipeline.
- **Workflow**:
  1. **Publish Fills**: Calls `p.publishFills(ctx, result.Fills)`.
  2. **Push Depth**: Calls `p.pushDepth(ctx, result.DepthSnapshot)`.
  3. **Coordinator Checkpoint**: Calculates snapshot SHA-256 checksum (if snapshot is non-nil), constructs `checkpoint.CompletedEvent`, and invokes `p.coord.MarkDoneWithSequence(ctx, ev)`.

#### `publishFills(ctx context.Context, fills []orderbook.Fill) error`
- **Purpose**: Encodes and batches trade execution messages to Kafka.
- **Partition Key**: Sets `kafkago.Message.Key = []byte(fill.MarketID)` to ensure all trades for a given market preserve ordering on the same partition.

#### `pushDepth(ctx context.Context, snap orderbook.DepthSnapshot) error`
- **Purpose**: Serializes Top-20 depth to JSON and sets Redis key `depth:{market_id}` with TTL = 0 (infinite).

#### `Close() error`
- **Purpose**: Cancels retention context and closes Kafka writer.

#### `HasDrainFailed() bool`
- **Purpose**: Thread-safe atomic reader verifying whether shutdown drain succeeded or failed.

#### `NewTestable(...)` & `(tp *TestablePublisher) Process(...)`
- **Purpose**: Helper wrapper exposing `process` for unit tests with mock writers.

---

## 6. Unit Test Suites & Invariants

### 6.1 `publisher_test.go`
| Test Function | Verification / Invariant Tested |
| :--- | :--- |
| `TestProcess_OneFill_OneKafkaMessage` | Asserts a single trade fill produces exactly 1 Kafka message on `trades.executed`. |
| `TestProcess_MultipleFills_MultipleKafkaMessages` | Asserts multi-level sweeps produce $N$ distinct Kafka messages. |
| `TestProcess_MarketID_UsedAsPartitionKey` | Asserts Kafka message key matches `fill.MarketID`. |
| `TestProcess_DepthSnapshot_WrittenToRedis` | Asserts depth snapshots are written to `depth:{market_id}` in Redis. |
| `TestProcess_CheckpointCoordinator_MarkDoneCalled` | Asserts successful processing notifies checkpoint coordinator with exact source position. |
| `TestProcess_KafkaFailure_CoordinatorNotCalled` | Asserts that Kafka failures halt the pipeline and prevent checkpoint advancement. |
| `TestProcess_RedisFailure_FailsCheckpoint` | Asserts that Redis failures return an error and prevent checkpoint advancement. |
| `TestPublisher_EmitsAuthoritativeSequenceToKafkaAndRedis` | Asserts sequence numbers from match results are included in both Kafka and Redis payloads. |
| `TestPublisher_IntegratedWithCoordinator_PreventsGapCheckpoints` | Verifies cross-market gap prevention (ETH offset 101 finishes before BTC offset 100, holding checkpoint at 99 until 100 finishes, then leaping to 101). |

### 6.2 `retention_test.go`
| Test Function | Invariant Tested |
| :--- | :--- |
| `TestRetentionNeverDeletesRecoveryAnchor` | Asserts that the retention query includes `ms.offset <= kc.offset` and `AND NOT EXISTS` filters to protect recovery anchors. |
