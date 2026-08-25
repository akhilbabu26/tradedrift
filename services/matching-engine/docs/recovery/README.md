# TradeDrift Matching Engine — Complete Crash Recovery Architecture

**Service:** Matching Engine (`services/matching-engine`)  
**Documentation:** `docs/recovery/README.md`  
**Topic:** Master Recovery Pipeline, Subsystem Integration, and End-to-End Bootstrap Lifecycle  
**Last Updated:** August 2026  

---

## 1. The Unified 6-Stage Recovery Pipeline

When the Matching Engine starts up after a crash, node reboot, or rolling deployment, it executes a deterministic, 6-stage recovery pipeline to resurrect the volatile in-memory order book to 100% live state:

```
                      THE 6-STAGE CRASH RECOVERY PIPELINE
                      ═══════════════════════════════════

                                 [ COLD BOOT ]
                                       │
                                       ▼
  ┌────────────────────────────────────────────────────────────────────────┐
  │ 1. SNAPSHOT RESTORATION (internal/orderbook/snapshot.go)               │
  │    • Loads latest point-in-time snapshot <= checkpoint from PostgreSQL  │
  │    • 5-Gate Validation: Schema, MarketID, Partition, Offset, SHA-256   │
  │    • Restores 500,000 resting orders into RAM in ~10ms                 │
  │    📄 See: SNAPSHOT_RECOVERY.md                                        │
  └────────────────────────────────────┬───────────────────────────────────┘
                                       │
                                       ▼
  ┌────────────────────────────────────────────────────────────────────────┐
  │ 2. KAFKA DELTA REPLAY (internal/recovery/replayer.go & partition.go)   │
  │    • Pre-flight Kafka High Watermark (HWM) validation                  │
  │    • Calculates start offset = min(snapshot.Offset) + 1                │
  │    • Strict Continuity Guard: asserts msg.Offset == expectedOffset++   │
  │    📄 See: REPLAY.md & CHECKPOINTING.md                                │
  └────────────────────────────────────┬───────────────────────────────────┘
                                       │
                                       ▼
  ┌────────────────────────────────────────────────────────────────────────┐
  │ 3. IN-MEMORY DEDUPLICATION (internal/market/engine.go)                 │
  │    • Pre-filter: skips redelivered msg.Offset <= lastAppliedOffset     │
  │    • O(1) Hash Map + 50,000-slot FIFO Ring Buffer                      │
  │    • Prevents double-matching on at-least-once Kafka redeliveries      │
  │    📄 See: DEDUPLICATION_RING_BUFFER.md                                │
  └────────────────────────────────────┬───────────────────────────────────┘
                                       │
                                       ▼
  ┌────────────────────────────────────────────────────────────────────────┐
  │ 4. MONOTONIC SEQUENCE TRACKING (internal/market/event_loop.go)         │
  │    • Per-market independent uint64 logical clocks                      │
  │    • Tracks exact state mutation progression (1, 2, 3 ... N)           │
  │    📄 See: SEQUENCE_NUMBERING.md                                       │
  └────────────────────────────────────┬───────────────────────────────────┘
                                       │
                                       ▼
  ┌────────────────────────────────────────────────────────────────────────┐
  │ 5. RECOVERY BARRIER SYNCHRONIZATION (internal/recovery/replayer.go)    │
  │    • Injects `EventRecoveryBarrier` at the end of the input stream     │
  │    • OutputQueue drain workers prevent channel buffer deadlock         │
  │    • Confirms engine goroutines have 100% executed all replayed orders │
  │    📄 See: BARRIERS_AND_DRAIN.md                                       │
  └────────────────────────────────────┬───────────────────────────────────┘
                                       │
                                       ▼
  ┌────────────────────────────────────────────────────────────────────────┐
  │ 6. INTEGRITY VERIFICATION & LIVE MODE TRANSITION                       │
  │    • Asserts: engine.Sequence == db.market_sequences.sequence          │
  │    • Asserts: engine.GetLastAppliedOffset() == checkpointOffset        │
  │    • Seeds Redis Level-2 order book depth (`depth:market_id`)          │
  │    • Calls `engine.SetLive()` to enable live trade publication         │
  └────────────────────────────────────┬───────────────────────────────────┘
                                       │
                                       ▼
                              [ 🚀 LIVE TRADING ]
```

---

## 2. Directory Index & Documentation Map

| Documentation File | Core Topic & Responsibility | Key Implementing Files |
| :--- | :--- | :--- |
| **[`SNAPSHOT_RECOVERY.md`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/docs/recovery/SNAPSHOT_RECOVERY.md)** | Point-in-time order book serialization, 5-gate validation, SHA-256 cryptographic checksums, and $O(1)$ state restoration. | [`snapshot.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go) |
| **[`REPLAY.md`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/docs/recovery/REPLAY.md)** | Kafka delta replay orchestration, pre-flight broker HWM checks, and strict offset continuity enforcement (`expectedOffset++`). | [`replayer.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go), [`partition.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/partition.go) |
| **[`CHECKPOINTING.md`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/docs/recovery/CHECKPOINTING.md)** | Contiguous offset sliding watermark window (`inFlight` vs `completed` maps), dual-sink persistence, and dedicated partition mode. | [`coordinator.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/checkpoint/coordinator.go) |
| **[`DEDUPLICATION_RING_BUFFER.md`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/docs/recovery/DEDUPLICATION_RING_BUFFER.md)** | Fast $O(1)$ hash map lookup + 50,000-element FIFO circular ring buffer to prevent double-matching on Kafka redeliveries. | [`engine.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go#L68-L92), [`event_loop.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go#L61-L87) |
| **[`SEQUENCE_NUMBERING.md`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/docs/recovery/SEQUENCE_NUMBERING.md)** | Monotonic `uint64` sequence progression, per-market ownership, and post-recovery sequence assertion verification. | [`event_loop.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go#L20-L24), [`engine.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go#L40-L45) |
| **[`BARRIERS_AND_DRAIN.md`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/docs/recovery/BARRIERS_AND_DRAIN.md)** | `EventRecoveryBarrier` token synchronization and `OutputQueue` drain routines preventing channel buffer deadlocks during replay. | [`replayer.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go#L170-L215), [`event_loop.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go#L40-L54) |
| **[`DETERMINISTIC_TRADE_ID.md`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/docs/recovery/DETERMINISTIC_TRADE_ID.md)** | Deterministic UUIDv5 SHA-1 trade fill hashing guaranteeing downstream settlement and wallet service idempotency on crash replay. | [`matcher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L162-L164), [`result.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/result.go#L12-L26) |
| **[`GRACEFUL_SHUTDOWN_AND_ANCHOR.md`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/docs/recovery/GRACEFUL_SHUTDOWN_AND_ANCHOR.md)** | Deterministic 4-phase staged shutdown protocol and final state snapshotting enabling zero-delta reboots in $< 10\text{ms}$. | [`main.go:270-315`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/cmd/server/main.go#L270-L315), [`event_loop.go:159-173`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go#L159-L173) |

---

## 3. Summary of Failure Guarantees

* **Zero Lost Orders:** Contiguous checkpointing prevents committing past in-flight slow orders.
* **Zero Ghost Fills:** In-memory deduplication ignores redelivered Kafka messages.
* **Zero Channel Deadlocks:** Dedicated drain routines keep channel buffers empty during replay.
* **Zero Corrupted States:** Strict sequence assertions halt the system if in-memory sequence diverges from PostgreSQL by even 1 digit.
