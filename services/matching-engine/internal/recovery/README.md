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

## 3. Configuration & Structs

### `Replayer`

```go
type Replayer struct {
    brokers                []string
    groupID                string
    db                     ReplayerDB
    redis                  ReplayerRedis
    manager                *market.MarketManager
    newReaderFunc          func(brokers []string, topic string, partition int) KafkaReader
    discoverPartitionsFunc func(topic string) ([]int, error)
    queryHWMFunc           func(ctx context.Context, topic string, partition int) (int64, error)
}
```

- **`ReplayAll(ctx, engineWg)`**: Sequential execution coordinator. Starts all registered market engines in recovery mode, runs partition replay, drains recovery barriers, asserts state alignment, and sets engines to live.
- **`replayPartition(ctx, topic, partition, checkpointOffset, marketLastSeenOffset)`**: Reconstructs state for all registered markets assigned to a single partition by restoring snapshots and replaying Kafka command logs up to the checkpoint offset.
