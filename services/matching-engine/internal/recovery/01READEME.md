# `internal/recovery` — Crash Recovery, State Replay & Replayer Orchestrator

**Package:** `recovery`  
**Service:** Matching Engine  
**Files Covered:** `replayer.go`, `partition.go`, `db.go`, `replayer_test.go`  
**Documentation:** `02READEME.md`  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Purpose

The `internal/recovery` package implements the **bootstrap crash-recovery, snapshot restoration, and deterministic log replay subsystem** for the Matching Engine.

When a Matching Engine node restarts after a machine failure, cluster deployment, or partition rebalance, all in-memory order books are completely empty. The `recovery` package orchestrates the end-to-end process of rebuilding every registered [`MarketEngine`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go#L101-L112) order book to its exact pre-crash state before live Kafka ingestion starts.

It guarantees that:
1. **No Lost Orders**: Every resting order is reconstructed with its exact original price-time queue priority.
2. **Zero Duplicate Output (Side-Effect-Free)**: Replay runs in `ModeRecovery`, regenerating state without re-publishing duplicate trade fills to Kafka or corrupting Redis.
3. **Deadlock-Free Queue Draining**: Concurrently drains engine output queues during log replay, preventing channel deadlocks even when replaying millions of historical events.
4. **Strict Consistency Verification**: Asserts that final in-memory offsets and sequence numbers match PostgreSQL authoritative database records before transitioning engines to `ModeLive`.

---

## 2. Core Problems Solved & Why This Package Is Needed

### 2.1 Instance-Scoped Assigned Partition Recovery (Issue #1)
In a scaled deployment, different matching engine nodes handle different trading pairs across different Kafka partitions.
- **The Problem**: Querying all topic partitions from Kafka would cause a node to replay partitions and markets owned by other nodes, wasting CPU and corrupting local state.
- **The Solution**: `ReplayAll` inspects only the registered engines in `manager.All()` to compute the exact subset of assigned partitions (`partitionsMap[engine.Partition()] = true`).

### 2.2 Pre-Flight High Watermark (HWM) Validation (Issue #3)
Before fetching messages, `replayPartition` queries the Kafka broker for the partition's log-end offset (`conn.ReadLastOffset()`):
- **Safety Invariant**: Asserts that the PostgreSQL checkpoint offset is strictly less than the broker log-end offset:
  $$\text{checkpointOffset} < \text{logEndOffset}$$
- If the checkpoint points to a non-existent or truncated future offset, recovery aborts immediately to prevent operating on corrupted state.

### 2.3 Snapshot-Accelerated Replay Window Optimization
Replaying historical logs from Kafka offset 0 on every restart would take minutes or hours as trade volume grows.
- **The Solution**:
  1. Loads the latest valid snapshot for each market satisfying $\text{offset} \le \text{checkpoint}$.
  2. Verifies snapshot SHA-256 checksum, schema version, market ID, partition ID, tick size, and lot size.
  3. Replays Kafka logs starting from $\text{startOffset} = \min(\text{snapshot.Offset}) + 1$.
  4. If any market on the partition lacks a snapshot, safely falls back to $\text{startOffset} = 0$.

### 2.4 Kafka Partition Offset Continuity Verification (Issue #5 & #9)
If a Kafka partition suffers data loss, silent message skips, or consumer offset jumps during replay:
- `replayPartition` enforces `msg.Offset == expectedOffset` on every single message.
- Any detected gap immediately aborts recovery with an error:
  `"partition offset continuity gap detected on partition X: expected Y, got Z"`.

### 2.5 Deadlock-Free OutputQueue Draining (Issue #1 & v9.6)
Each `MarketEngine.OutputQueue` has a buffered capacity of 1,000 items. During replay of large histories (e.g. 50,000 events), if output channels were not drained, the single-threaded Event Loop would block on `m.OutputQueue <- *res`, causing a fatal engine deadlock.
- **The Solution**: `ReplayAll` launches concurrent drain goroutines for every engine's `OutputQueue` *before* replaying partition messages, continuously discarding intermediate recovery outputs until encountering the [`EventRecoveryBarrier`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go#L47).

### 2.6 Recovery Barrier & Consistency Verification
1. After replaying Kafka logs up to `checkpointOffset`, the replayer pushes an `EventRecoveryBarrier` into each engine's `InputQueue`.
2. The drain goroutines wait for `res.BarrierReached == true` with `res.BarrierOffset == checkpoint`.
3. Asserts that `engine.GetLastAppliedOffset() == marketLastSeenOffset[marketID]`.
4. Asserts that `engine.GetSequence() == dbSequence` from PostgreSQL `market_sequences`.
5. Aligns the Kafka broker consumer group offset to `checkpoint`.
6. Calls `engine.SetLive()` to enable live order processing.

---

## 3. End-to-End Recovery Lifecycle Flow

```
                             STARTUP / RESTART
                                     │
                                     ▼
                        ┌─────────────────────────┐
                        │   MarketManager.All()   │  Discover assigned partitions
                        └────────────┬────────────┘
                                     │
                                     ▼
                        ┌─────────────────────────┐
                        │  Start Engine Goroutines│  engine.Run() starts in ModeRecovery
                        └────────────┬────────────┘
                                     │
                                     ▼
                        ┌─────────────────────────┐
                        │ Start OutputQueue Drain │  Concurrent draining prevents deadlocks
                        └────────────┬────────────┘
                                     │
                     ┌───────────────┴───────────────┐
                     │ For each assigned partition:  │
                     └───────────────┬───────────────┘
                                     │
                                     ▼
                        ┌─────────────────────────┐
                        │ Checkpoint & HWM Check  │  checkpoint < logEndOffset
                        └────────────┬────────────┘
                                     │
                                     ▼
                        ┌─────────────────────────┐
                        │ Restore Latest Snapshot │  Load latest snapshot <= checkpoint
                        └────────────┬────────────┘
                                     │
                                     ▼
                        ┌─────────────────────────┐
                        │   Replay Kafka Stream   │  Replay from (minSnapshotOffset + 1) -> checkpoint
                        │ (Continuity Check: N+1) │  Skip events <= snapshot.offset
                        └────────────┬────────────┘
                                     │
                                     ▼
                        ┌─────────────────────────┐
                        │ Inject Recovery Barrier │  InputQueue <- EventRecoveryBarrier
                        └────────────┬────────────┘
                                     │
                                     ▼
                        ┌─────────────────────────┐
                        │    Wait Barrier Drain   │  Wait for BarrierReached on OutputQueue
                        └────────────┬────────────┘
                                     │
                                     ▼
                        ┌─────────────────────────┐
                        │   Verify Assertions     │  Assert engine.offset == lastSeen
                        │                         │  Assert engine.sequence == dbSequence
                        └────────────┬────────────┘
                                     │
                                     ▼
                        ┌─────────────────────────┐
                        │ Commit Broker Offset    │  Commit checkpoint to Kafka consumer group
                        └────────────┬────────────┘
                                     │
                                     ▼
                        ┌─────────────────────────┐
                        │    engine.SetLive()     │  Transition all engines to LIVE mode
                        └─────────────────────────┘
```

---

## 4. External Packages & Dependencies

| Package | Purpose & Justification |
| :--- | :--- |
| `context` | Manages recovery lifecycle timeouts and query deadlines. |
| `encoding/json` | Unmarshals serialized `orderbook.BookSnapshot` JSON payloads from PostgreSQL `market_snapshots`. |
| `errors` & `fmt` | Sentinel error checking (`errors.Is(err, pgx.ErrNoRows)`) and contextual error wrapping (`%w`). |
| `log` | Comprehensive operational logging during partition replay, snapshot restores, and consistency assertions. |
| `sort` | Deterministically sorts assigned partition IDs (`sort.Ints`) for sequential partition processing. |
| `sync` | Goroutine coordination: `sync.WaitGroup` for background engine loops and output queue drain workers. |
| `time` | Controls timeout windows and sleep backoffs. |
| `github.com/jackc/pgx/v5` | Production PostgreSQL driver interface (`pgxConn`) for querying checkpoints, snapshots, and sequences. |
| `github.com/redis/go-redis/v9` | Redis client interface (`redisConn`) for pushing reconstructed market depth. |
| `github.com/segmentio/kafka-go` | Pure Go Kafka library. Used for partition discovery (`conn.ReadPartitions`), log-end offset inspection (`DialLeader`, `ReadLastOffset`), sequential message fetching (`Reader`), and consumer group offset commits (`CommitMessages`). |
| `tradedrift/.../market` | Internal domain package for `MarketManager`, `MarketEngine`, and `InputEvent`. |
| `tradedrift/.../orderbook` | Domain models (`BookSnapshot`, `OrderBook`, `ErrSnapshotBeyondCheckpoint`). |
| `tradedrift/.../kafka` | Topic constants (`TopicOrderCommands`) and command parser (`HandleOrderCommand`). |

---

## 5. Detailed Breakdown of Files, Structs & Functions

### 5.1 `replayer.go` — The Master Recovery Orchestrator

#### Interfaces & Structs
1. **`KafkaReader`**:
   ```go
   type KafkaReader interface {
       SetOffset(offset int64) error
       FetchMessage(ctx context.Context) (kafkago.Message, error)
       Close() error
   }
   ```
   - Abstracts Kafka consumer partitions for production (`kafkago.Reader`) and unit testing (`mockKafkaReader`).

2. **`pgxConn`** & **`redisConn`**:
   - Minimal interfaces abstracting PostgreSQL `QueryRow` and Redis `Set`.

3. **`Replayer`**:
   - Master orchestrator holding configuration, broker addresses, database connections, manager reference, and mockable function hooks.

#### Functions in `replayer.go`
- **`NewReplayer(...) *Replayer`**:
  - Initializes the `Replayer` with default production function pointers for `discoverPartitionsFunc`, `newReaderFunc`, and `queryHWMFunc`.
- **`OverrideDiscoveryAndReader(...)`**:
  - Test helper allowing unit tests to inject custom partition discovery, reader factories, and HWM query functions.
- **`ReplayAll(ctx context.Context, engineWg *sync.WaitGroup) error`**:
  - **The Main Orchestration Pipeline**:
    1. Identifies assigned partitions across registered engines.
    2. Spawns `engine.Run(ctx)` goroutines for all engines in `ModeRecovery`.
    3. Loads PostgreSQL checkpoints for all assigned partitions.
    4. Launches concurrent `OutputQueue` drain goroutines waiting for recovery barriers.
    5. Loops through partitions and executes `replayPartition()`.
    6. Injects `EventRecoveryBarrier` into each engine's `InputQueue`.
    7. Waits for all drain goroutines to confirm barrier receipt.
    8. Asserts `engine.GetLastAppliedOffset() == lastSeenOffset`.
    9. Asserts `engine.GetSequence() == dbSequence`.
    10. Transitions engines to `ModeLive` via `engine.SetLive()`.
    11. Commits contiguous checkpoint offsets to Kafka consumer group on the broker.

---

### 5.2 `partition.go` — Partition Log Replay & Message Routing

#### Functions in `partition.go`
- **`replayPartition(...) error`**:
  - Replays a single partition stream:
    1. Resolves all engines mapped to this partition.
    2. If `checkpointOffset < 0`, skips replay (empty/unprocessed partition).
    3. Queries Kafka broker for `logEndOffset` and validates $\text{checkpointOffset} < \text{logEndOffset}$.
    4. Inspects snapshots for all markets on partition:
       - Fails if any snapshot offset $> \text{checkpointOffset}$.
       - Calls `engine.RestoreFromSnapshot` for valid snapshots.
       - Records `minSnapshotOffset`.
    5. Computes $\text{startOffset} = \min(\text{snapshot.Offset}) + 1$ (or $0$ if any market lacks a snapshot).
    6. Creates `KafkaReader`, seeks to `startOffset`.
    7. Loops and fetches messages:
       - **Continuity Invariant**: Asserts `msg.Offset == expectedOffset++`.
       - Calls `routeMessage(msg)`.
       - Stops when `msg.Offset >= checkpointOffset`.

- **`routeMessage(msg kafkago.Message) (string, bool, error)`**:
  - Parses and routes the Kafka message using `intkafka.HandleOrderCommand`.
  - Filters out offsets $\le \text{engine.GetLastAppliedOffset()}$ (already incorporated in snapshot).

- **`commitKafkaGroupOffset(ctx context.Context, topic string, partition int, offset int64) error`**:
  - Commits the baseline checkpoint to the Kafka broker for consumer group positioning.

---

### 5.3 `db.go` — PostgreSQL Durability Repository

#### Functions in `db.go`
- **`loadCheckpoint(ctx context.Context, topic string, partition int) (int64, error)`**:
  - Queries `SELECT "offset" FROM kafka_checkpoints WHERE topic = $1 AND partition = $2`.
  - Returns `-1` if `pgx.ErrNoRows` (unprocessed partition).

- **`loadLatestSnapshot(ctx context.Context, marketID string, checkpoint int64) (*orderbook.BookSnapshot, []byte, error)`**:
  - Queries `market_snapshots` for the newest snapshot where $\text{offset} \le \text{checkpoint}$.
  - Unmarshals `BookSnapshot` JSON and validates DB column values against JSON properties to guard against database corruption.
  - Returns parsed `*BookSnapshot` and SHA-256 `checksum`.

- **`loadMarketSequence(ctx context.Context, marketID string) (uint64, error)`**:
  - Queries `SELECT sequence FROM market_sequences WHERE market_id = $1`.
  - Returns `0` if `pgx.ErrNoRows`.

---

## 6. Unit Test Suite Summary (`replayer_test.go`)

| Test Function | Scenario & Invariant Verified |
| :--- | :--- |
| `TestRecovery_MissingSnapshotReplay` | Verifies that if one market on a partition lacks a snapshot, replay safely starts from offset 0 across all partition messages. |
| `TestRecovery_PartitionGaps` | Verifies that non-contiguous Kafka offsets (e.g. 0, 1, 3 skipping 2) are detected and fail recovery with an offset gap error. |
| `TestRecovery_EmptyPartition` | Verifies that partitions with checkpoint -1 recover cleanly with sequence 0 and last applied offset -1 in LIVE mode. |
| `TestRecovery_CrashAfterTradePublish` | Verifies trade ID determinism: replaying identical matching inputs after a crash generates 100% identical Trade IDs (UUID v5). |
| `TestRecovery_MultiMarketBarrier` | Verifies that multiple concurrent engines on the same partition all drain and synchronize barriers before going LIVE. |
| `TestRecovery_OutputQueueBackpressure` | Verifies that replaying 1,500+ messages (exceeding channel buffer 1,000) does not deadlock due to active concurrent queue draining. |
