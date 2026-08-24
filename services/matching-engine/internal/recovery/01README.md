# `internal/recovery` — Crash Recovery and Engine Replay

**Package:** `recovery`  
**Service:** Matching Engine  
**Last Updated:** August 2026  

---

## 1. What This Package Does

This package handles the **startup recovery and durability layer** of the Matching Engine. When the engine restarts after a crash, a deployment, or a planned shutdown, all in-memory order books are lost. The `recovery` package rebuilds every `MarketEngine`'s order book to its exact pre-crash state before any live events are processed.

It does this by:

1. **Assigned Partition Scope**: Determining the unique set of Kafka partitions assigned to this instance based on registered market configurations (avoiding replaying partitions owned by other instances).
2. **PostgreSQL Authoritative Checkpoint**: Reading the last committed contiguous checkpoint offset from PostgreSQL (`kafka_checkpoints`) for each partition.
3. **Pre-flight High Watermark (HWM) Validation**: Querying the Kafka broker for the log-end offset (next offset to be written) for each partition, asserting that the database checkpoint offset is strictly less than the partition's log-end offset.
4. **Snapshot Restoration**: Loading the latest snapshot for each market satisfying `offset <= checkpoint` from PostgreSQL:
   - Validating the snapshot metadata, sequence structure, and checksum.
   - Performing a clean order book restoration bypassing normal matching side effects.
5. **Log Replay**: Seeking to `startOffset` (`min_snapshot_offset + 1` or `0` if snapshots are missing) and replaying Kafka command events up to `checkpointOffset`:
   - Enforcing strict offset-continuity checks (`msg.Offset == expectedOffset`).
   - Suppressing all external side effects (no Kafka trade publishes, no Redis depth updates, and no PostgreSQL checkpoint increments during replay).
6. **Recovery Barrier & Draining**: Appending a recovery barrier event (`EventRecoveryBarrier`) to each market engine's input queue. The replayer drains matching results from the output queue, discarding standard event outputs, until it receives the barrier confirmed with `BarrierOffset == checkpointOffset`.
7. **Post-Recovery State Verification**:
   - Asserting that `engine.lastAppliedOffset` matches `marketLastSeenOffset` (the highest partition offset routed to that market engine).
   - Asserting that `engine.Sequence == dbSequence` (from `market_sequences`).
8. **Redis Depth Synchronisation**: Publishing a fresh, reconstructed market depth snapshot to Redis immediately before going live.
9. **Transition to Live**: Transitioning all engines to live mode (`engine.SetLive()`), committing the baseline checkpoint to the Kafka broker, and seeking the consumer group reader to `checkpointOffset + 1` to start live intake.

---

## 2. Recovery Lifecycle Flow

```
                         ENGINE RESTART
                               │
                               ▼
                     ┌─────────────────────┐
                     │   Create Engines    │  NewMarketEngine() → RECOVERY mode
                     └──────────┬──────────┘
                                │
                                ▼
                     ┌─────────────────────┐
                     │  replayer.ReplayAll │  Calculates unique partitions owned by manager
                     └──────────┬──────────┘
                                │ (Sequential per partition)
                     ┌──────────┴──────────┐
                     │   Check HWM & Seek  │  checkpoint < logEndOffset
                     └──────────┬──────────┘
                                │
                                ▼
                     ┌─────────────────────┐
                     │  Restore Snapshots  │  Load latest snapshot <= checkpoint
                     └──────────┬──────────┘
                                │
                                ▼
                     ┌─────────────────────┐
                     │ Replay Partition    │  Replay from startOffset → checkpoint
                     │ Logs                │  Skip events <= snapshot.offset
                     └──────────┬──────────┘
                                │
                                ▼
                     ┌─────────────────────┐
                     │  Recovery Barrier   │  FIFO queue drain using EventRecoveryBarrier
                     └──────────┬──────────┘
                                │
                                ▼
                     ┌─────────────────────┐
                     │  Verify Assertions  │  Assert engine.offset == lastSeen
                     │                     │  Assert engine.sequence == dbSequence
                     └──────────┬──────────┘
                                │
                                ▼
                     ┌─────────────────────┐
                     │   Push Fresh Depth  │  Pushes reconstructed depth to Redis
                     └──────────┬──────────┘
                                │
                                ▼
                     ┌─────────────────────┐
                     │  SetLive() & Seek   │  Commit baseline offset to broker and seek
                     └─────────────────────┘
```

---

```

---

## 3. Modules & File Split

To enforce a strict separation of concerns, the recovery logic is structured into three clean source files:

1. **[`replayer.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go)** (Orchestrator): Defines the main orchestrator configuration and coordinates overall bootstrap recovery sequence via `ReplayAll()`. Starts all registered market engines in recovery mode, spawns concurrent `OutputQueue` draining goroutines to prevent deadlocks, runs partition replay, asserts final state alignments, and moves engines to live mode.
2. **[`partition.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/partition.go)** (Kafka Recovery Engine): Manages partition offsets, logs continuity verification, commits checkpoints, and handles loop message routing (`replayPartition`, `routeMessage`, `commitKafkaGroupOffset`).
3. **[`db.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/db.go)** (SQL Repository): Contains raw PostgreSQL database query helpers (`loadCheckpoint`, `loadLatestSnapshot`, `loadMarketSequence`).

---

## 4. Key Invariants

### 4.1 Side-Effect-Free Recovery
During the replay phase, recovery generates zero external side effects: no external trades are published, no snapshots are taken, and no Redis depth cache writes (`pushFreshDepth`) occur.

### 4.2 Concurrent OutputQueue Draining
Before replaying offsets, `ReplayAll` registers asynchronous drain goroutines for all engine `OutputQueues`. This keeps the queues empty during replay and prevents deadlocks when replaying massive historical logs exceeding the channel buffer size (1,000).

