# `internal/checkpoint` — Contiguous Offset Coordinator & Persistence

**Package:** `checkpoint`  
**Service:** Matching Engine  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Purpose

The `checkpoint` package provides the **durability, offset coordination, and transactional state persistence layer** of the Matching Engine.

In an asynchronous event-driven matching architecture, incoming orders across different markets are multiplexed into shared Kafka partitions. Because distinct markets (e.g., `BTC-USD`, `ETH-USD`, `SOL-USD`) are processed concurrently by independent market engine worker goroutines, their processing speeds naturally diverge.

The Checkpoint Coordinator acts as a **central watermark synchronizer** between:
1. **Kafka Intake Consumers**: Dispatching ingested messages across concurrent market engines.
2. **Market Engines & Publishers**: Processing matching cycles and emitting trade events.
3. **PostgreSQL Durability Store**: Maintaining authoritative transaction records in `kafka_checkpoints`, `market_sequences`, and `market_snapshots`.
4. **Kafka Broker Consumer Groups**: Committing contiguous high-watermark offsets back to Kafka for consumer lag monitoring and rebalance safety.

---

## 2. The Core Problem Solved: Cross-Market Interleaving Races

### 2.1 The Race Condition Scenario

Consider a single Kafka partition containing interleaved command events for two distinct markets:

```
Kafka Partition 0 Stream:
┌──────────────┬──────────────┬──────────────┐
│  Offset 100  │  Offset 101  │  Offset 102  │
│   BTC-USD    │   ETH-USD    │   BTC-USD    │
└──────────────┴──────────────┴──────────────┘
```

1. **Offset 100 (`BTC-USD`)** is dispatched to `Engine[BTC]`. `BTC-USD` has a deep order book and takes **10ms** to execute price-time matching and generate trades.
2. **Offset 101 (`ETH-USD`)** is dispatched to `Engine[ETH]`. `ETH-USD` has an empty book and finishes matching in **1ms**.
3. **The Race Danger**: If `Engine[ETH]` immediately committed offset `101` to PostgreSQL and the Kafka broker, the recorded checkpoint would become `101`.
4. **The Crash Catastrophe**: If the matching engine crashes while offset `100` is still executing, upon restart recovery will read checkpoint `101` and seek to offset `102`. **Offset 100 (`BTC-USD`) is permanently skipped and lost**, resulting in silent order loss and book desynchronization!

```
                      WITHOUT COORDINATOR (VULNERABLE)
                      ═════════════════════════════════
Kafka Partition 0:
[Offset 100: BTC] ──────── (slow matching: 10ms) ──────────► [CRASH!] (Lost forever!)
[Offset 101: ETH] ── (fast: 1ms) ──► Committed Offset 101! ──► Restart seeks to 102!
```

### 2.2 How the Coordinator Solves This (Contiguous Watermark Algorithm)

The Checkpoint Coordinator guarantees that a checkpoint offset **only advances when every strictly preceding offset has been completely processed and persisted**:

```
                       WITH CHECKPOINT COORDINATOR
                       ═════════════════════════════
1. Track(100), Track(101), Track(102)
2. Offset 101 finishes FIRST  ──► Buffered in `completed[101]`. 
                                  Contiguous check: 100 is still pending. 
                                  Checkpoint STAYS at 99!
3. Offset 100 finishes LATER  ──► Contiguous chain (100 -> 101) is now complete!
                                  Atomically persists snapshots & sequences for 100 and 101.
                                  Watermark JUMPS to 101 in PostgreSQL & Kafka!
4. Offset 102 finishes        ──► Watermark advances to 102.
```

---

## 3. Architecture & Data Flow

```
                      INCOMING KAFKA MESSAGES
                                 │
                         Track(pos)
                                 ▼
                     ┌───────────────────────┐
                     │ Coordinator.trackers  │ ◄─── inFlight[offset] = true
                     └───────────────────────┘
                                 │
                 Dispatched to Market Engines
                                 │
                   [Engine BTC]     [Engine ETH]
                        │                │
                        ▼                ▼
            MarkDoneWithSequence(CompletedEvent)
                                 │
                                 ▼
                     ┌───────────────────────┐
                     │  Check Contiguity     │
                     │  (lastCommitted + 1)  │
                     └───────────┬───────────┘
                                 │
               ┌─────────────────┴─────────────────┐
        Gap Detected                        Contiguous Chain Complete
               │                                   │
               ▼                                   ▼
         Buffer event in                 PostgreSQL Transaction (Atomic)
         `completed[offset]`             ┌────────────────────────────────┐
         Return immediately              │ 1. INSERT INTO market_sequences│
         (Wait for missing offsets)      │ 2. INSERT INTO market_snapshots│
                                         │ 3. INSERT INTO kafka_checkpoints│
                                         └────────────────┬───────────────┘
                                                          │
                                         ┌────────────────┴───────────────┐
                                         │ Success                        │ Failure (Error)
                                         ▼                                ▼
                              Advance in-memory state           Preserve in-memory state
                              (lastCommitted = toCommit)        (Do NOT advance watermark)
                              Purge inFlight & completed        Return error for retry
                                         │
                                         ▼
                              Kafka Consumer Group Commit
                              (committer.CommitMessages)
```

---

## 4. PostgreSQL Database Schema Integration

The coordinator writes to three core PostgreSQL tables in a **single atomic transaction**:

### 4.1 `market_sequences`
Tracks the strictly monotonic sequence counter emitted by each market engine:
```sql
INSERT INTO market_sequences (market_id, sequence, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (market_id)
DO UPDATE SET
    sequence   = EXCLUDED.sequence,
    updated_at = NOW();
```

### 4.2 `market_snapshots`
Persists serialized binary/JSON order book snapshots along with schema versions and SHA-256 checksums:
```sql
INSERT INTO market_snapshots (market_id, sequence, partition, "offset", schema_version, snapshot, checksum, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
ON CONFLICT (market_id, sequence)
DO UPDATE SET
    partition      = EXCLUDED.partition,
    "offset"       = EXCLUDED."offset",
    schema_version = EXCLUDED.schema_version,
    snapshot       = EXCLUDED.snapshot,
    checksum       = EXCLUDED.checksum,
    created_at     = NOW();
```

### 4.3 `kafka_checkpoints`
The authoritative partition checkpoint consumed during startup recovery (`internal/recovery`):
```sql
INSERT INTO kafka_checkpoints (topic, partition, "offset", updated_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (topic, partition)
DO UPDATE SET
    "offset"   = EXCLUDED."offset",
    updated_at = NOW()
WHERE kafka_checkpoints."offset" < EXCLUDED."offset";
```
> [!NOTE]
> The `WHERE kafka_checkpoints."offset" < EXCLUDED."offset"` clause enforces monotonic progression at the database level, preventing any out-of-order writes from rewinding the committed checkpoint.

---

## 5. External Packages & Dependencies

| Package | Purpose & Justification |
| :--- | :--- |
| `context` | Standard Go context package used to pass request lifecycles, cancellation signals, and database execution timeouts down through PostgreSQL and Kafka API calls. |
| `encoding/json` | Serializes `orderbook.BookSnapshot` structs to JSON byte slices for storage in the `snapshot` column (`jsonb` / `text`) of `market_snapshots`. |
| `fmt` | Formats compound dictionary keys (`fmt.Sprintf("%s:%d", topic, partition)`) and provides structured error wrapping via `fmt.Errorf("...: %w", err)`. |
| `sync` | Provides concurrency primitives: `sync.RWMutex` on `Coordinator` for concurrent partition map reads/writes, and `sync.Mutex` on each `partitionTracker` for fine-grained per-partition lock striping. |
| `github.com/jackc/pgx/v5` | Production-grade PostgreSQL driver and toolkit for Go. Provides high-performance binary protocol support and clean transaction abstractions (`pgx.Tx`). |
| `github.com/jackc/pgx/v5/pgconn` | Low-level driver communication interfaces from `pgx`, supplying `pgconn.CommandTag` to represent SQL execution outcomes. |
| `github.com/jackc/pgx/v5/pgxpool` | Thread-safe PostgreSQL connection pooling (`*pgxpool.Pool`), allowing multiple partition tracker transactions to execute concurrently without connection starvation. |
| `github.com/segmentio/kafka-go` | High-performance Go Kafka library. Used for `kafkago.Message` struct definitions when committing synchronous consumer group offsets back to Kafka brokers. |
| `tradedrift/.../orderbook` | Internal dependency providing core domain types: `orderbook.KafkaPosition` (Topic, Partition, Offset) and `orderbook.BookSnapshot` (snapshot data and schema versions). |

---

## 6. Detailed Code Walkthrough (`coordinator.go`)

### 6.1 Core Interfaces

#### `DB`
```go
type DB interface {
    Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
    Begin(ctx context.Context) (Tx, error)
}
```
- **Purpose**: Abstracts PostgreSQL query and transaction initiation.
- **Why Needed**: Allows decoupled production implementations using `pgxpool.Pool` while enabling lightweight, mockable unit testing (`fakeDB`) without requiring a live PostgreSQL daemon.

#### `Tx`
```go
type Tx interface {
    Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
    Commit(ctx context.Context) error
    Rollback(ctx context.Context) error
}
```
- **Purpose**: Abstracts an active database transaction.
- **Why Needed**: Ensures atomic commits across `market_sequences`, `market_snapshots`, and `kafka_checkpoints` such that partial writes never occur.

#### `KafkaCommitter`
```go
type KafkaCommitter interface {
    CommitMessages(ctx context.Context, msgs ...kafkago.Message) error
}
```
- **Purpose**: Abstracts Kafka consumer group offset commits (`segmentio/kafka-go.Reader`).
- **Why Needed**: Synchronizes the broker-side consumer group offset with the contiguous watermark calculated by the coordinator.

---

### 6.2 Data Structures

#### `CompletedEvent`
```go
type CompletedEvent struct {
    Pos      orderbook.KafkaPosition
    MarketID string
    Sequence uint64
    Snapshot *orderbook.BookSnapshot
    Checksum []byte
}
```
- **Fields**:
  - `Pos`: Kafka coordinates (`Topic`, `Partition`, `Offset`).
  - `MarketID`: Identifier of the market that processed the command (e.g. `"BTC-USD"`).
  - `Sequence`: The monotonic sequence number assigned to this event by the market engine.
  - `Snapshot`: Optional order book snapshot pointer (non-nil if this offset triggered a periodic snapshot).
  - `Checksum`: Cryptographic checksum (SHA-256) of the snapshot state.

#### `partitionTracker`
```go
type partitionTracker struct {
    mu            sync.Mutex
    lastCommitted int64
    hasCommitted  bool
    inFlight      map[int64]bool
    completed     map[int64]*CompletedEvent
}
```
- **Purpose**: Tracks offset progress for a single `(topic, partition)` pair.
- **Fields**:
  - `mu`: Dedicated mutex for this partition (lock striping across partitions).
  - `lastCommitted`: Highest contiguous offset committed to PostgreSQL.
  - `hasCommitted`: Flag indicating whether `lastCommitted` has been initialized.
  - `inFlight`: Hash set of offsets currently being processed by market engines.
  - `completed`: Map of completed events waiting for preceding gaps to close.

#### `Coordinator`
```go
type Coordinator struct {
    db         DB
    mu         sync.RWMutex
    trackers   map[string]*partitionTracker
    committers map[string]KafkaCommitter
}
```
- **Purpose**: Master coordinator managing all topic-partition trackers and Kafka committers.

---

### 6.3 Functions & Methods

#### `NewCoordinator(db DB) *Coordinator`
- **Purpose**: Instantiates and initializes a new `Coordinator`.
- **Behavior**: Allocates memory for `trackers` and `committers` lookup maps.

#### `RegisterCommitter(topic string, committer KafkaCommitter)`
- **Purpose**: Binds a Kafka committer (e.g., Kafka reader) to a specific topic name.
- **Thread Safety**: Acquires `c.mu.Lock()` to safely register committers during setup.

#### `getTracker(topic string, partition int) *partitionTracker`
- **Purpose**: Retrieves or lazily creates a `partitionTracker` for a given `(topic, partition)`.
- **Optimization**: Implements the **double-checked locking pattern** (`RLock` first, upgrade to `Lock` only on miss) to maximize read concurrency during high-throughput order ingestion.

#### `InitBaseline(topic string, partition int, baselineOffset int64)`
- **Purpose**: Sets the initial baseline committed offset for a partition.
- **When Used**: Called during engine startup recovery when reading the last committed checkpoint from PostgreSQL (`kafka_checkpoints`).

#### `Track(pos orderbook.KafkaPosition)`
- **Purpose**: Registers an incoming Kafka message offset as in-flight.
- **When Used**: Invoked by the Kafka Consumer immediately upon reading a message from the broker before dispatching it to a market engine worker.

#### `MarkDone(ctx context.Context, pos orderbook.KafkaPosition) error`
- **Purpose**: Convenience wrapper for marking an offset complete without associated market sequences or snapshots (e.g., skipped/poison messages).
- **Delegates To**: `MarkDoneWithSequence(ctx, CompletedEvent{Pos: pos})`.

#### `MarkDoneWithSequence(ctx context.Context, event CompletedEvent) error`
- **Purpose**: **The primary coordination engine.**
- **Step-by-step Execution**:
  1. **Baseline Fallback**: If no baseline was set, initializes `lastCommitted = offset - 1`.
  2. **Idempotency Filter**: If `event.Pos.Offset <= pt.lastCommitted`, discards duplicate/replayed event and returns `nil`.
  3. **Buffer Completed Event**: Inserts `event` into `pt.completed[offset]`.
  4. **Contiguity Scan**: Iterates forward from `curr = pt.lastCommitted + 1`. If `completed[curr]` exists, advances `toCommit` and accumulates events.
  5. **Gap Check**: If `toCommit == -1` (earlier offset still in flight), returns immediately without advancing the database.
  6. **Atomic PostgreSQL Commit**:
     - Begins transaction (`db.Begin(ctx)`).
     - Loops over `eventsToCommit` and upserts `market_sequences`.
     - Upserts `market_snapshots` if `ev.Snapshot != nil`.
     - Upserts `kafka_checkpoints` with `toCommit`.
     - Commits transaction (`tx.Commit(ctx)`).
  7. **In-Memory Watermark Update**: Advances `pt.lastCommitted = toCommit` and deletes resolved offsets from `pt.completed` and `pt.inFlight`.
  8. **Kafka Broker Commit**: Calls `committer.CommitMessages()` to notify the Kafka broker of the new consumer group offset.

#### `GetCommittedOffset(topic string, partition int) (int64, bool)`
- **Purpose**: Returns the currently committed contiguous offset and initialization status.
- **When Used**: For telemetry, unit testing, health check endpoints, and monitoring lag.

#### `WrapPGXPool(pool *pgxpool.Pool) DB`
- **Purpose**: Adapter constructor wrapping a concrete `*pgxpool.Pool` into the `DB` interface.
- **Associated Structs**: `pgxPoolWrapper` and `pgxTxWrapper` provide transparent delegation to standard `pgx` calls.

---

## 7. Failure Handling & Invariants

### 7.1 PostgreSQL Failure Resilience
If the database connection times out or fails during `tx.Commit()`:
1. `tx.Rollback()` is triggered via `defer`.
2. In-memory `pt.lastCommitted` **remains unadvanced**.
3. `pt.completed` preserves all completed events.
4. Subsequent calls will re-attempt committing the contiguous batch, ensuring **zero state corruption**.

### 7.2 Poison / Skipped Message Handling
If a message cannot be processed by an engine (e.g. unknown market ID, unmarshal error), the Consumer calls `MarkDone(ctx, pos)`. The coordinator tracks it as completed, allowing contiguous checkpoints to advance without getting permanently blocked.

---

## 8. Unit Test Coverage Summary (`coordinator_test.go`)

| Test Name | Scenario Validated |
| :--- | :--- |
| `TestCoordinator_ContiguousAdvancement` | Verifies that out-of-order completion (offset 101 finishes before 100) buffers 101, holds the checkpoint at 99, and then leaps to 101 when 100 completes. |
| `TestCoordinator_MultiGapResolution` | Verifies multi-gap resolution where offsets 101, 103, and 104 complete, and resolving missing offsets 100 and 102 leaps the watermark to 104. |
| `TestCoordinator_KafkaConsumerGroupCommitted` | Verifies that committing a contiguous offset synchronizes both the PostgreSQL checkpoint and the Kafka broker consumer group. |
| `TestCoordinator_SkippedUnknownMarketAdvancesCheckpoint` | Verifies that skipped or poison messages marked done do not block subsequent valid order checkpoints. |
| `TestCoordinator_PostgresFailure_PreservesInMemoryStateForRetry` | Verifies that when PostgreSQL returns a connection error, in-memory state is preserved and subsequent retries succeed without corrupting offsets. |
