# Kafka Delta Replay & Offset Continuity Guard Architecture

**Service:** Matching Engine (`services/matching-engine`)  
**Documentation:** `REPLAY.md`  
**Topic:** Bootstrap Orchestration, Kafka High Watermark (HWM) Validation, Offset Continuity Enforcement, and Mode Transition Protocol  
**Package Reference:** [`internal/recovery/replayer.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go)  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Purpose

The Matching Engine runs **100% in volatile RAM** to execute trades with sub-microsecond latency. When the process starts up (after a node reboot, server crash, or rolling deployment), RAM is completely empty.

The **Replayer** ([`internal/recovery/replayer.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go)) is the **master bootstrap orchestrator** that transitions the cold Matching Engine from an empty state into a fully synchronized, verified, live trading state before live trader traffic is admitted.

```
       COLD BOOT (RAM is Empty) ──► [REPLAYER ORCHESTRATION PIPELINE] ──► LIVE TRADING READY 🚀
                                    • Load Snapshot (10ms)
                                    • Replay Kafka Delta Log
                                    • Enforce Offset Continuity
                                    • Verify Sequences & Seed Redis
```

---

## 2. Problems Solved, How Solved & Implementing Functions Matrix

| Problem Solved | Danger / Failure Scenario | How It Is Solved | Implementing Function(s) & Code Location |
| :--- | :--- | :--- | :--- |
| **1. Truncated / Wiped Kafka Cluster Boot** | Server is accidentally pointed to an empty or truncated Kafka broker where messages were lost. Starting live would skip historical un-executed orders. | Connects to Kafka broker before replay, queries High Watermark (HWM), and aborts boot if `checkpoint > HWM`. | [`FetchLatestOffset`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go#L80-L100), [`replayPartition`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/partition.go#L38-L46) |
| **2. $O(N)$ Kafka Log Replay Bottleneck** | Replaying millions of orders from Offset 0 takes hours of downtime on reboot. | Restores latest snapshot $\le \text{checkpoint}$ into RAM in 10ms, then starts Kafka reader at `min(snapshot.Offset) + 1`. | [`replayPartition`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/partition.go#L83-L90), [`loadLatestSnapshot`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/db.go#L28-L58) |
| **3. Kafka Message Stream Gaps / Out-of-Order Delivery** | Network drop or partition rebalance skips an offset (e.g. 95 $\to$ 97), causing cancelled orders or matching against wrong order books. | Enforces strict gapless continuity on every single message: asserts `msg.Offset == expectedOffset++`. | [`replayPartition`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/partition.go#L123-L127) |
| **4. Duplicate Trade Egress During Replay** | Replaying historical orders accidentally re-emits trade fills to Kafka and WebSockets, double-filling trader balances. | Starts market engines in `ModeRecovery`, which matches in RAM but suppresses output publication until `SetLive()`. | [`ModeRecovery`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go#L16-L19), [`Publisher.process`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/publisher/publisher.go#L102-L115) |

---

## 3. Core Mechanism: Kafka Delta Replay & Offset Continuity Guard

```
                           KAFKA DELTA REPLAYER LIFECYCLE
                           ══════════════════════════════

      PostgreSQL kafka_checkpoints                       Kafka Broker Partition
   ┌────────────────────────────────┐              ┌────────────────────────────────┐
   │ Checkpoint Offset = 101        │              │ High Watermark (HWM) = 105     │
   └───────────────┬────────────────┘              └───────────────┬────────────────┘
                   │                                               │
                   └───────────────────────┬───────────────────────┘
                                           │
                                           ▼ (Pre-Flight Validation)
                   ┌───────────────────────────────────────────────┐
                   │  Assert: Checkpoint (101) <= HWM (105)        │ ──► [Checkpoint > HWM] ──► ABORT ❌
                   └───────────────────────┬───────────────────────┘
                                           │
                                           ▼
      PostgreSQL market_snapshots          │
   ┌────────────────────────────────┐      │
   │ Latest Snapshot Offset = 90    │      │
   └───────────────┬────────────────┘      │
                   │                       │
                   ▼                       ▼
   ┌───────────────────────────────────────────────────────────────┐
   │ 1. Restore Snapshot: Offset 90 into RAM (~10ms)               │
   │ 2. Calculate Start Offset: min(SnapshotOffset) + 1 = 91       │
   │ 3. Seek Kafka Reader to Offset 91                             │
   └───────────────────────────────┬───────────────────────────────┘
                                   │
                                   ▼
   ┌───────────────────────────────────────────────────────────────┐
   │ Stream Offsets 91 → 101 with Strict Offset Continuity Guard:  │
   │                                                               │
   │   for each msg in Kafka:                                      │
   │       Assert: msg.Offset == expectedOffset++                  │
   │                                                               │
   │   (If msg.Offset != expectedOffset ──► Immediate ABORT ❌)     │
   └───────────────────────────────┬───────────────────────────────┘
                                   │
                                   ▼
   ┌───────────────────────────────────────────────────────────────┐
   │ Replay Completed up to Checkpoint 101!                        │
   └───────────────────────────────────────────────────────────────┘
```

### 2.1 Pre-Flight Kafka High Watermark (HWM) Discovery
Before reading any historical logs, the replayer connects to Kafka brokers via [`FetchLatestOffset()`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go#L80-L100) to find the broker's current end-of-partition log offset (High Watermark):
* It queries the confirmed checkpoint from PostgreSQL (`kafka_checkpoints`).
* It asserts:
  $$\text{CheckpointOffset} \le \text{KafkaHWM}$$
* **Why this is critical:** If an operator accidentally points the matching engine to a newly wiped, truncated, or incorrect Kafka cluster where the broker's HWM is smaller than the database checkpoint, the engine **refuses to boot** instead of starting with missing historical orders.

---

### 2.2 Replay Start Offset Calculation
To avoid replaying the entire historical Kafka topic from offset 0, the replayer queries PostgreSQL for the latest snapshot $\le \text{checkpointOffset}$ for all markets on that partition:
$$\text{startOffset} = \min_{m \in \text{markets}}(\text{snapshot}_m.\text{Offset}) + 1$$
* If a snapshot exists at Offset 90, the replayer seeks the Kafka reader directly to **Offset 91**.
* If no snapshot exists (brand new market), $\text{startOffset} = 0$.

---

### 2.3 Strict Offset Continuity Guard
As messages are fetched sequentially from Kafka, the replayer enforces **gapless stream continuity**:

```go
// internal/recovery/partition.go
if msg.Offset != expectedOffset {
    return fmt.Errorf("offset continuity violation on %s/%d: expected %d, got %d",
        topic, partition, expectedOffset, msg.Offset)
}
expectedOffset++
```

* **Why this is critical:** If network corruption, broker partition rebalancing, or log compaction causes a message to be skipped, this guard trips immediately. 
* The engine **fails closed** rather than matching orders out of order or skipping cancellations.

---

## 3. How `replayer.go` Achieves Recovery in Detail (The 8-Phase Workflow)

The recovery workflow executes across 8 sequential phases:

```
  ┌────────────────────────────────────────────────────────────────────────────┐
  │ Phase 1: Assigned Partition Discovery                                      │
  │          • Inspects all registered MarketEngines                           │
  │          • Filters only partitions assigned to this node instance          │
  ├────────────────────────────────────────────────────────────────────────────┤
  │ Phase 2: Start Market Engine Loops in ModeRecovery                         │
  │          • Spawns `go engine.Run(ctx)` for each market                     │
  │          • ModeRecovery: suppresses trade emissions to Kafka/WebSockets    │
  ├────────────────────────────────────────────────────────────────────────────┤
  │ Phase 3: Pre-Flight HWM & Checkpoint Verification                          │
  │          • Queries PostgreSQL `kafka_checkpoints`                          │
  │          • Queries Kafka broker High Watermark                             │
  │          • Asserts: `checkpointOffset <= latestOffset`                     │
  ├────────────────────────────────────────────────────────────────────────────┤
  │ Phase 4: Snapshot State Restoration                                        │
  │          • Loads latest `market_snapshots` <= checkpoint from PostgreSQL   │
  │          • Verifies SHA-256 checksum and 5 safety gates                    │
  │          • Restores resting order nodes into RAM in ~10ms                  │
  ├────────────────────────────────────────────────────────────────────────────┤
  │ Phase 5: Kafka Delta Stream Ingestion                                      │
  │          • Seeks Kafka reader to `startOffset = min(snap.Offset) + 1`      │
  │          • Ingests Kafka events up to `checkpointOffset`                   │
  │          • Dispatches commands to corresponding `engine.InputQueue`        │
  │          • Enforces `msg.Offset == expectedOffset++` on every message      │
  ├────────────────────────────────────────────────────────────────────────────┤
  │ Phase 6: Recovery Barrier Synchronization                                  │
  │          • Injects `EventRecoveryBarrier` into all market InputQueues      │
  │          • Blocks until all market worker goroutines drain and acknowledge │
  ├────────────────────────────────────────────────────────────────────────────┤
  │ Phase 7: Final State & Sequence Assertions                                 │
  │          • Asserts: `engine.GetLastAppliedOffset() == checkpointOffset`     │
  │          • Asserts: `engine.GetSequence() == db.market_sequences.sequence` │
  ├────────────────────────────────────────────────────────────────────────────┤
  │ Phase 8: Pre-Live Redis Depth Seed & Transition to ModeLive 🚀             │
  │          • Computes fresh Level-2 depth and seeds Redis: depth:{market_id} │
  │          • Calls `engine.SetLive()` to enable live trade broadcasting      │
  │          • Launches live Kafka consumer at `checkpointOffset + 1`          │
  └────────────────────────────────────────────────────────────────────────────┘
```

---

## 4. In-Depth Component Walkthrough

### 4.1 Phase 2 & 8: Dual-Mode Operation (`ModeRecovery` vs. `ModeLive`)

```
               DURING RECOVERY (ModeRecovery)                DURING LIVE (ModeLive)
             ┌────────────────────────────────┐        ┌────────────────────────────────┐
             │ Orders matched in RAM          │        │ Orders matched in RAM          │
             │ Trades generated               │        │ Trades generated               │
             │                                │        │                                │
             │ 🛑 Output to Kafka: SUPPRESSED │        │ 🚀 Output to Kafka: PUBLISHED  │
             │ 🛑 Output to Redis: SUPPRESSED │        │ 🚀 Output to Redis: PUBLISHED  │
             └────────────────────────────────┘        └────────────────────────────────┘
```

* **In `ModeRecovery`:** Orders are matched in RAM to reconstruct the book, but the Publisher suppresses trade fills and depth pushes. This prevents emitting duplicate trade events to Kafka or spamming WebSocket clients.
* **In `ModeLive`:** Once recovery asserts 100% sequence parity, `engine.SetLive()` is called, enabling full live publication.

---

### 4.2 Phase 6: The Recovery Barrier Token (`EventRecoveryBarrier`)

Because market engines run concurrently on their own goroutines, the replayer cannot simply assume memory is updated as soon as Kafka finishes reading.

```
 Replayer Goroutine                       MarketEngine Goroutine (BTC-USDT)
 ──────────────────                       ─────────────────────────────────
 
 Injects EventRecoveryBarrier ────────►  Queue: [Order 99] ──► [Order 100] ──► [BARRIER]
                                                  │                  │             │
                                                  ▼                  ▼             ▼
                                             Matched in RAM    Matched in RAM   Reaches Barrier!
                                                                                   │
 Barrier Acknowledged ◄────────────────── Emits MatchResult{BarrierReached: true} ─┘
 (Replayer unblocks ✅)
```

The replayer injects `EventRecoveryBarrier` into each market's `InputQueue` and blocks on `OutputQueue` until all markets emit `BarrierReached: true`. This guarantees that **100% of replayed orders have been matched in RAM before live trading starts**.

---

### 4.3 Phase 7: Mathematical Integrity Assertions

Before declaring the engine ready for live traffic, the replayer executes two strict mathematical assertions:

1. **Applied Offset Assertion:**
   ```go
   if engine.GetLastAppliedOffset() != checkpointOffset {
       return fmt.Errorf("offset mismatch on %s: applied %d != checkpoint %d",
           engine.MarketID, engine.GetLastAppliedOffset(), checkpointOffset)
   }
   ```
2. **Sequence Counter Parity Assertion:**
   ```go
   dbSeq, _ := r.db.GetMarketSequence(ctx, engine.MarketID)
   if engine.GetSequence() != uint64(dbSeq) {
       return fmt.Errorf("integrity violation on %s: engine sequence %d != db sequence %d",
           engine.MarketID, engine.GetSequence(), dbSeq)
   }
   ```

If even a single satoshi was miscalculated, or an event was dropped, sequence numbers will diverge and the engine halts immediately before any live trader can be affected.

---

## 5. Recovery Scenarios Matrix

| Scenario | Snapshot State | Kafka Range | Replayer Action | Outcome |
| :--- | :--- | :--- | :--- | :--- |
| **Normal Crash Recovery** | Snapshot at Offset 90 | Checkpoint at Offset 101 | Restores snapshot in 10ms; replays offsets 91 $\to$ 101. | Recovered in $< 30\text{ms}$. |
| **Clean Restart (Graceful Shutdown)** | Snapshot at Offset 101 | Checkpoint at Offset 101 | Restores snapshot in 10ms; zero delta replay needed. | Recovered in $< 15\text{ms}$. |
| **Brand New Exchange / Fresh Market** | No snapshot found | Checkpoint at Offset -1 | Starts replay directly from Kafka Offset 0. | Normal cold startup. |
| **Corrupted / Truncated Kafka Cluster** | Snapshot at Offset 90 | Checkpoint 101, Kafka HWM = 50 | Pre-flight check detects `checkpoint > HWM`. | **Aborts with error ❌**. |
| **Kafka Offset Gap Detected** | Snapshot at Offset 90 | Kafka skips 95 $\to$ 97 | Offset continuity check detects `expected 96, got 97`. | **Aborts with error ❌**. |

---

## 6. Summary Table: Responsibilities Across Files

| File | Function / Component | Responsibility in Recovery |
| :--- | :--- | :--- |
| [`internal/recovery/replayer.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go) | `ReplayAll`, `ReplayPartition` | Master bootstrap coordinator; discovers assigned partitions, queries HWM, orchestrates lifecycle. |
| [`internal/recovery/partition.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/partition.go) | `replayPartitionStream` | Replays Kafka message delta, enforces `expectedOffset++`, manages barrier synchronization. |
| [`internal/recovery/db.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/db.go) | `GetCheckpoint`, `GetLatestSnapshot` | Reads durable checkpoints, snapshots, and sequence numbers from PostgreSQL. |
| [`internal/orderbook/snapshot.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go) | `Restore` | 5-gate validation (Schema, Market, Partition, Offset, SHA-256) and RAM order book reconstruction. |
| [`internal/market/engine.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go) | `ModeRecovery`, `SetLive` | Suppresses trade egress during replay; flips to live mode upon barrier completion. |
| [`internal/publisher/publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/publisher/publisher.go) | `Publisher.process` | Respects `ModeRecovery`, drains output queue concurrently without polluting Kafka. |
