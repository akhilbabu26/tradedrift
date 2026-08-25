# Contiguous Offset Checkpointing & Crash Recovery Architecture

**Service:** Matching Engine (`services/matching-engine`)  
**Documentation:** `CHECKPOINTING.md`  
**Topic:** Contiguous Offset Checkpointing, Multi-Market Watermark Coordination, Transactional Persistence & Crash Recovery  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Purpose

In the TradeDrift Matching Engine, order books reside **100% in volatile RAM** to achieve sub-microsecond matching latencies. If the server loses power, crashes, or restarts during a deployment, all in-memory book state is wiped clean.

The **Contiguous Offset Checkpointing** mechanism ([`internal/checkpoint`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/checkpoint/coordinator.go)) is the **durability and crash-recovery engine** that makes in-memory matching completely safe.

---

## 2. Problems Solved, How Solved & Implementing Functions Matrix

| Problem Solved | Danger / Failure Scenario | How It Is Solved | Implementing Function(s) & Code Location |
| :--- | :--- | :--- | :--- |
| **1. Out-of-Order Offset Commit Gap** | A fast market finishes Offset 101 while a slow market is still executing Offset 100. If committed naively, a crash will permanently skip and lose Offset 100 on reboot. | Coordinator maintains `inFlight` and `completed` hash maps, advancing checkpoint offset **only in strict contiguous order** ($N+1, N+2$). | [`Track`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/checkpoint/coordinator.go#L110-L117), [`MarkDoneWithSequence`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/checkpoint/coordinator.go#L128-L160) |
| **2. Multi-Sink Persistence Desync** | System crashes while saving snapshot to DB, but checkpoint advances in Kafka, leaving order book in an inconsistent state. | Commits `kafka_checkpoints`, `market_sequences`, and `market_snapshots` inside a **single atomic PostgreSQL transaction (`BEGIN ... COMMIT`)**. | [`commitTransaction`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/checkpoint/coordinator.go#L163-L248) |
| **3. Checkpoint Backward Regressions** | Network lag causes a delayed write from an older thread to overwrite a newer checkpoint in DB. | SQL UPSERT enforces a monotonic guard clause: `WHERE kafka_checkpoints.offset < EXCLUDED.offset`. | [`migration/00001_create_kafka_checkpoints.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/migration/00001_create_kafka_checkpoints.sql#L3-L9) |
| **4. In-Memory Goroutine Churn** | Spawning goroutines per offset creates CPU context switching and memory leaks during high traffic. | Zero per-offset goroutines; uses two synchronized in-memory hash maps with instantaneous $O(1)$ lookups. | [`partitionTracker`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/checkpoint/coordinator.go#L24-L35) |

---

## 3. The Core Problem: Multi-Market Speed Divergence

In high-throughput cryptocurrency exchanges, multiple trading pairs (`BTC-USDT`, `ETH-USDT`, `SOL-USDT`) are multiplexed onto shared Kafka partitions (e.g. Partition 0).

Because each market runs on an independent, concurrent Go goroutine, their execution speeds naturally diverge:
* `ETH-USDT` may process a small cancel command in **0.05 milliseconds**.
* `BTC-USDT` may process a large market order sweeping 10 price levels in **1.5 milliseconds**.

```
 KAFKA PARTITION 0 STREAM:
 ┌────────────────────────────────┬────────────────────────────────┬────────────────────────────────┐
 │ Offset 100: BTC-USDT (Sweep)   │ Offset 101: ETH-USDT (Cancel)  │ Offset 102: BTC-USDT (Limit)   │
 └───────────────┬────────────────┴───────────────┬────────────────┴────────────────────────────────┘
                 │                                │
                 ▼                                ▼
       BTC Worker Goroutine              ETH Worker Goroutine
       (Heavy Book - Takes 1.5ms)        (Light Book - Takes 0.05ms)
                 │                                │
                 │ (Still Matching...)            ▼ (Finished in 0.05ms!)
                 │                       ETH FINISHES OFFSET 101 FIRST
```

### 🔴 The Fatal Crash Window Without Contiguous Coordination
1. If the engine naively committed Offset `101` to PostgreSQL or Kafka immediately after ETH finished:
2. The committed checkpoint moves to `101`.
3. If the server **crashes right now**, BTC was still working on Offset `100`.
4. On reboot, the engine reads checkpoint `101` and starts Kafka ingestion from Offset `102`.
5. 💥 **Disaster:** Offset `100` (BTC order) was skipped and **permanently lost**, creating phantom balances and un-settled trades!

---

## 3. The Solution: In-Memory Hash Maps & The Sliding Watermark Window

The Coordinator does **NOT** spawn extra goroutines per offset. Instead, it maintains two lightweight, in-memory Go hash maps per Kafka partition:

```go
// internal/checkpoint/coordinator.go
type partitionTracker struct {
    lastCommitted int64                    // Highest contiguous offset saved in DB (e.g. 99)
    hasCommitted  bool                     // Whether lastCommitted is valid
    inFlight      map[int64]bool           // "Who is currently working on what?"
    completed     map[int64]*CompletedEvent // "Who has completely finished?"
}
```

### 3.1 The Two Hash Maps in Real-Time Action

Suppose **Offset 100** is `BTC-USDT` and **Offset 101** is `ETH-USDT` on Partition 0:

#### 1. When Orders Arrive:
Both offsets are recorded in the `inFlight` map via `coordinator.Track()`:
```go
inFlight  = { 100: true, 101: true }
completed = { }
```

#### 2. When ETH finishes Offset 101:
ETH's publisher calls `MarkDoneWithSequence(Offset 101)`:
```go
delete(inFlight, 101)
completed[101] = event

// Maps now look like:
inFlight  = { 100: true }
completed = { 101: CompletedEvent }
```
* **Contiguous check:** Coordinator looks for `100` (which is `lastCommitted + 1`).
* Is `100` in `completed`? **No.** (BTC is still working).
* **Action:** Nothing is committed to the database yet. Checkpoint remains held at `99`.

#### 3. When BTC finishes Offset 100:
BTC's publisher calls `MarkDoneWithSequence(Offset 100)`:
```go
delete(inFlight, 100)
completed[100] = event

// Maps now look like:
inFlight  = { }
completed = { 100: CompletedEvent, 101: CompletedEvent }
```
* **Contiguous check:** Coordinator looks for `100` $\to$ **Found!** Looks for `101` $\to$ **Found!** Looks for `102` $\to$ Not found.
* **Action:** 🚀 It opens **1 SQL transaction**, commits `offset = 101` to PostgreSQL and Kafka, and immediately prunes the map:
```go
delete(completed, 100)
delete(completed, 101)
```

---

## 4. Code Implementation & File Map

The entire checkpointing pipeline connects across 3 key files:

```
  ┌─────────────────────────┐          ┌──────────────────────────┐          ┌─────────────────────────┐
  │  internal/kafka         │          │   internal/checkpoint    │          │   internal/publisher    │
  │  (consumer.go)          │          │   (coordinator.go)       │          │   (publisher.go)        │
  └────────────┬────────────┘          └────────────┬─────────────┘          └────────────┬────────────┘
               │                                    │                                     │
               │ 1. Consumes Kafka message          │                                     │
               │───► calls Track(pos) ─────────────►│ (inFlight[offset] = true)           │
               │                                    │                                     │
               │ 2. Dispatches to MarketEngine      │                                     │
               │                                    │ 3. Market matches, publishes trades │
               │                                    │◄─── calls MarkDoneWithSequence() ───│
               │                                    │                                     │
               │                                    │ 4. Scans contiguous run & commits   │
               │                                    │───► Writes PostgreSQL & Kafka       │
```

### 4.1 In `coordinator.go`: The In-Flight Map (`Track`)
📍 **Lines 110–117** of [`coordinator.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/checkpoint/coordinator.go#L110-L117):
```go
// Track registers an incoming Kafka offset when consumed from the broker.
func (c *Coordinator) Track(pos orderbook.KafkaPosition) {
	pt := c.getTracker(pos.Topic, pos.Partition)
	pt.mu.Lock()
	defer pt.mu.Unlock()

	pt.inFlight[pos.Offset] = true // Marks offset as active in the map
}
```
* **Where it is called from:**  
  Inside [`internal/kafka/consumer.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/kafka/consumer.go) as soon as the consumer receives a message from Kafka, *before* sending it to the market engine queue.

---

### 4.2 In `coordinator.go`: The Completed Map & Contiguous Scan (`MarkDoneWithSequence`)
📍 **Lines 128–160** of [`coordinator.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/checkpoint/coordinator.go#L128-L160):
```go
func (c *Coordinator) MarkDoneWithSequence(ctx context.Context, event CompletedEvent) error {
	pt := c.getTracker(event.Pos.Topic, event.Pos.Partition)
	pt.mu.Lock()
	defer pt.mu.Unlock()

	// 1. Mark this offset in the completed map
	pt.completed[event.Pos.Offset] = &event

	// 2. Scan the contiguous chain starting from lastCommitted + 1
	var toCommit int64 = -1
	curr := pt.lastCommitted + 1
	var eventsToCommit []CompletedEvent

	for pt.completed[curr] != nil {
		toCommit = curr
		eventsToCommit = append(eventsToCommit, *pt.completed[curr])
		curr++ // Walk forward: 100 -> 101 -> 102 ...
	}

	if toCommit == -1 {
		// Contiguous chain is not yet complete (waiting for earlier in-flight offset)
		return nil // Checkpoint held at previous value!
	}

	// 3. Chain is complete! Open SQL transaction and commit all 3 tables...
```
* **Where it is called from:**  
  Inside [`internal/publisher/publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/publisher/publisher.go) as Step 3 of the egress pipeline, right after publishing the trade fills to Kafka and pushing depth to Redis.

---

### 4.3 In `coordinator.go`: Atomic Commit & Map Cleanup
📍 **Lines 163–231** of [`coordinator.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/checkpoint/coordinator.go#L163-L231):
```go
	// 1. Atomically write to PostgreSQL (market_sequences, market_snapshots, kafka_checkpoints)
	tx, err := c.db.Begin(ctx)
	// ... executes SQL inserts ...
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	// 2. Advance in-memory state strictly AFTER PostgreSQL succeeds
	prevCommitted := pt.lastCommitted
	pt.lastCommitted = toCommit

	// 3. Immediately prune committed offsets from memory maps
	for i := prevCommitted + 1; i <= toCommit; i++ {
		delete(pt.completed, i)
		delete(pt.inFlight, i)
	}
```

---

## 5. Where and How Data Is Committed (Dual-Sink Pipeline)

When a contiguous watermark is reached (`toCommit > lastCommitted`), the Coordinator executes a **Dual-Sink Persistence Pipeline**:

```
                       checkpoint.Coordinator.commitTransaction()
                                           │
                       ┌───────────────────┴───────────────────┐
                       │ 1. Open PostgreSQL Transaction (BEGIN)│
                       ▼                                       ▼
         ┌───────────────────────────┐           ┌───────────────────────────┐
         │     market_sequences      │           │     market_snapshots      │
         ├───────────────────────────┤           ├───────────────────────────┤
         │ Saves monotonic sequence  │           │ Saves serialized book +   │
         │ counter for each market   │           │ SHA-256 hash (if snap!=nil)│
         └─────────────┬─────────────┘           └─────────────┬─────────────┘
                       │                                       │
                       └───────────────────┬───────────────────┘
                                           │
                                           ▼
                             ┌───────────────────────────┐
                             │     kafka_checkpoints     │
                             ├───────────────────────────┤
                             │ Saves toCommit watermark  │
                             │ with monotonic constraint │
                             └─────────────┬─────────────┘
                                           │
                                     tx.Commit() ✅
                                           │
                                           ▼
                       ┌───────────────────────────────────────┐
                       │ 2. Advance In-Memory Coordinator State│
                       │    (lastCommitted = toCommit, prune)  │
                       └───────────────────┬───────────────────┘
                                           │
                                           ▼
                       ┌───────────────────────────────────────┐
                       │ 3. Kafka Broker Consumer Group Commit │
                       │    (committer.CommitMessages)         │
                       └───────────────────────────────────────┘
```

### 5.1 Sink 1: PostgreSQL Atomic Transaction (`BEGIN ... COMMIT`)

All 3 database updates are wrapped in a single database transaction (`tx, err := c.db.Begin(ctx)`):

#### A. `market_sequences` (Monotonic Sequence Record)
```sql
INSERT INTO market_sequences (market_id, sequence, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (market_id)
DO UPDATE SET
    sequence   = EXCLUDED.sequence,
    updated_at = NOW();
```

#### B. `market_snapshots` (Point-in-Time Order Book State)
*(Executed if a market engine produced a snapshot at this offset)*
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

#### C. `kafka_checkpoints` (Contiguous Durability Watermark)
```sql
INSERT INTO kafka_checkpoints (topic, partition, "offset", updated_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (topic, partition)
DO UPDATE SET
    "offset"   = EXCLUDED."offset",
    updated_at = NOW()
WHERE kafka_checkpoints."offset" < EXCLUDED."offset"; -- Prevents backwards regressions!
```

---

### 5.2 Sink 2: Kafka Broker Consumer Group Commit
Strictly **after** PostgreSQL transaction commits, the Coordinator aligns the consumer group offset on the Kafka broker:
```go
committer.CommitMessages(ctx, kafkago.Message{
    Topic:     event.Pos.Topic,
    Partition: event.Pos.Partition,
    Offset:    toCommit,
})
```

---

## 6. How It Guarantees Absolute Consistency

| Consistency Guard | Mechanism | Failure Scenario Prevented |
| :--- | :--- | :--- |
| **Strict Ordering Invariant** | Slotted `for pt.completed[curr] != nil` evaluation | Prevents fast markets from advancing the checkpoint past in-flight slow orders. |
| **Atomic Transaction Isolation** | Single `tx.Commit()` for sequences, snapshots, and checkpoints | Prevents partial persistence where a checkpoint advances but the sequence counter or snapshot fails. |
| **Postgres-First Memory Advancement** | `pt.lastCommitted = toCommit` is modified **only after** `tx.Commit()` succeeds | If PostgreSQL fails or rejects the write, in-memory state does not advance; retry remains possible. |
| **Monotonic Guard Clause** | `WHERE kafka_checkpoints.offset < EXCLUDED.offset` | Prevents a delayed or reordered publisher thread from writing an older offset over a newer one. |
| **Fail-Closed Egress Protection** | [`publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/publisher/publisher.go) logs `FATAL` and calls `HaltCallback()` if coordinator fails | Prevents engine from continuing to match orders when durability commits are broken. |

---

## 7. Market Partition Topologies & Production Standard

### 7.1 Production Standard: Dedicated Partition Mode (Option B: 1:1 Mapping)
TradeDrift standardizes on **Option B (Explicit Dedicated Partitioning)** to ensure zero Head-of-Line blocking and complete independence across trading pairs:
* **`BTC-USDT`** $\to$ **Kafka Partition 0** (`BTC_PARTITION`, default `0`)
* **`ETH-USDT`** $\to$ **Kafka Partition 1** (`ETH_PARTITION`, default `1`)
* **`SOL-USDT`** $\to$ **Kafka Partition 2** (`SOL_PARTITION`, default `2`)

The Coordinator maintains independent `partitionTracker` states for each partition:
```go
partitionTrackers: {
    "orders.commands/0": { lastCommitted: 54820 }, // Tracks BTC-USDT only
    "orders.commands/1": { lastCommitted: 12400 }, // Tracks ETH-USDT only
    "orders.commands/2": { lastCommitted: 8110  }, // Tracks SOL-USDT only
}
```
* **Zero Cross-Market Watermark Drag:** Each partition advances at its own rate. A slow deep book sweep on BTC never delays checkpoint commits on ETH.
* **Deterministic Producer Agreement:** The Order Service's `KafkaProducer` explicitly sets `msg.Partition` to match the exact same partition index.

### 7.2 Alternate Mode: Multiplexed Mode (Multi-Market Shared Partition)
* If multiple low-volume altcoins share a partition (e.g. Partition 3), the coordinator treats Partition 3 as a single contiguous stream, coalescing results across shared market engine workers using its sliding watermark window.

---

## 8. How Crash Recovery Is Achieved On Restart

When a node restarts after a crash or planned deployment:

```
                            NODE REBOOTS / RESTARTS
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ 1. Read PostgreSQL Checkpoint                                               │
│    SELECT "offset" FROM kafka_checkpoints WHERE topic = $1 AND partition = $2│
│    Result: Checkpoint = 101                                                 │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ 2. Restore Latest Snapshot <= Checkpoint                                    │
│    SELECT snapshot, checksum FROM market_snapshots                          │
│    WHERE market_id = $1 AND "offset" <= 101                                 │
│    ORDER BY "offset" DESC LIMIT 1                                           │
│    Result: Restores OrderBook to Offset 90 in RAM (~10ms)                    │
│    (Validated with 256-bit SHA-256 Checksum)                                │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ 3. Replay Delta Log From Kafka Stream                                       │
│    Seeks Kafka reader to startOffset = min(SnapshotOffset) + 1 (Offset 91)   │
│    Replays Offsets 91 → 101 in ModeRecovery (suppresses trade publication)  │
│    Enforces continuity check: msg.Offset == expectedOffset++                │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ 4. Verify Final State Assertions                                            │
│    Assert: engine.GetLastAppliedOffset() == 101                             │
│    Assert: engine.GetSequence() == db.market_sequences.sequence             │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ 5. Engine Goes LIVE 🚀                                                      │
│    • Pushes fresh reconstructed depth to Redis (depth:{market_id})          │
│    • Aligns Kafka consumer group reader to Checkpoint + 1 (Offset 102)      │
│    • Starts processing live trader commands                                 │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 9. Summary Table: Responsibilities Across Components

| Phase | Component | Action Performed |
| :--- | :--- | :--- |
| **Ingress** | [`kafka.Consumer`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/kafka/consumer.go) | Reads message $\to$ calls `coordinator.Track(pos)` $\to$ routes to `engine.InputQueue`. |
| **Execution** | [`market.MarketEngine`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go) | Matches order in RAM $\to$ advances `book.Sequence` $\to$ emits `MatchResult`. |
| **Egress** | [`publisher.Publisher`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/publisher/publisher.go) | Publishes Kafka trades $\to$ pushes Redis depth $\to$ calls `coordinator.MarkDoneWithSequence()`. |
| **Durability** | [`checkpoint.Coordinator`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/checkpoint/coordinator.go) | Calculates contiguous watermark $\to$ commits single PostgreSQL transaction $\to$ commits Kafka broker offset. |
| **Recovery** | [`recovery.Replayer`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go) | Reads checkpoint $\to$ restores snapshot $\to$ replays Kafka delta log $\to$ asserts sequence match $\to$ goes LIVE. |
