# Snapshot Recovery & State Reconstruction Architecture

**Service:** Matching Engine (`services/matching-engine`)  
**Documentation:** `SNAPSHOT_RECOVERY.md`  
**Topic:** Point-in-Time Order Book Snapshotting, Cryptographic Verification & Fast Crash Recovery  
**Package Reference:** [`internal/orderbook/snapshot.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go)  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Core Concepts

In the TradeDrift Matching Engine, order books reside **100% in volatile RAM** to achieve sub-microsecond matching latencies. If the server loses power, crashes, or is restarted for a deployment, all in-memory book state is wiped clean.

Crash recovery in an event-driven matching engine requires solving two fundamental questions:

```
                      THE TWO PILLARS OF CRASH RECOVERY
                      ═════════════════════════════════

    1. THE STATE (Snapshot)                     2. THE DELTA (Kafka Replay)
  ┌─────────────────────────────────┐         ┌─────────────────────────────────┐
  │ "What resting orders were       │         │ "What order commands arrived    │
  │  sitting in the order book?"    │    +    │  after that snapshot was taken?"│
  │                                 │         │                                 │
  │ Solved by: snapshot.go          │         │ Solved by: checkpoint.go        │
  │ Store: PostgreSQL snapshots     │         │ Store: Kafka Topic Logs         │
  └─────────────────────────────────┘         └─────────────────────────────────┘
```

This overall architectural pattern is known as **Snapshot-Based State Recovery** (or **State Snapshotting + Event Sourcing Replay**).

---

## 2. Problems Solved, How Solved & Implementing Functions Matrix

| Problem Solved | Danger / Failure Scenario | How It Is Solved | Implementing Function(s) & Code Location |
| :--- | :--- | :--- | :--- |
| **1. $O(N)$ Historical Replay Bottleneck** | Replaying millions of orders from Offset 0 on reboot takes hours of downtime, halting all trading. | Serializes resting orders periodically to PostgreSQL; restores 500,000 orders into RAM in **~10ms**, replaying only the last few seconds of Kafka deltas. | [`Serialize`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go#L61-L82), [`Restore`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go#L115-L157) |
| **2. Database Bit-Rot & JSON Tampering** | Bit flip or manual DB tampering corrupts price/quantity decimals in resting orders. | Computes a **256-bit SHA-256 cryptographic hash** of the snapshot payload and asserts parity before loading. | [`Checksum`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go#L51-L58), [`Restore`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go#L141-L144) |
| **3. Snapshot Beyond Partition Checkpoint** | System loads a snapshot containing orders past the safe committed offset, causing double-matching or missing fills. | 5-Gate Validation Gate 4 asserts: `snap.Offset <= checkpointOffset`. | [`Restore`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go#L136-L138) |
| **4. Snapshot Freezing RAM Execution** | Taking snapshots blocks trading during active volatility surges. | Zero mutex locks; snapshot triggers execute synchronously inside the single-threaded event loop between discrete orders ($< 1\text{ms}$). | [`triggerPeriodicSnapshot`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go#L93-L108) |

---

## 3. The Core Problem: The $O(N)$ Historical Replay Bottleneck

Because the order book lives in RAM, when the server restarts after a crash:

* **Without Snapshots ($O(N)$ Log Replay):**  
  If the exchange has processed **10,000,000 orders** over the past year, the matching engine would have to read, parse, and match all 10 million orders from Kafka Offset 0 on every single reboot.  
  *Result:* **Hours of exchange downtime.**

* **With Snapshots ($O(1)$ State Restore + Tiny Delta Replay):**  
  The engine periodically takes a snapshot of the resting order book. On restart, it loads the snapshot into memory in **~10 milliseconds** and only replays the tiny delta of Kafka events that occurred after the snapshot.  
  *Result:* **Exchange comes back online in milliseconds.**

```
 WITHOUT SNAPSHOTS:
 Server Reboot ──► Replay 10,000,000 Kafka events from Year 1 ──────────► (Takes 3 Hours ⏳)

 WITH SNAPSHOTS:
 Server Reboot ──► Load Snapshot (~10ms) ──► Replay 50 Delta Events ────► (Takes 20 Milliseconds ⚡)
```

---

## 3. The 3 Primary Functions in `snapshot.go`

[`internal/orderbook/snapshot.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go) provides three essential capabilities:

```
   LIVE ORDER BOOK IN RAM                           PERSISTENT JSON IN POSTGRESQL
 (Linked Lists, Maps, Pointers)                      (market_snapshots Table)
 ┌───────────────────────────┐                      ┌───────────────────────────┐
 │ Bids / Asks Sorted Prices │                      │ "schema_version": 1,      │
 │ PriceLevel Doubly Linked  │ ──► Serialize() ──►  │ "market_id": "BTC-USDT",  │
 │ OrderIndex Hash Map       │                      │ "sequence": 54820,        │
 └───────────────────────────┘                      │ "orders": [ ... ]         │
                                                    └───────────────────────────┘
                                                                  │
                                   CRASH OCCURS                   │
                                                                  ▼
 ┌───────────────────────────┐                      ┌───────────────────────────┐
 │ FULLY RECONSTRUCTED BOOK  │                      │ Read snapshot row and     │
 │ In RAM in ~10 milliseconds│ ◄─── Restore() ◄─────│ verify SHA-256 Checksum   │
 └───────────────────────────┘                      └───────────────────────────┘
```

### 3.1 `Serialize()` — RAM Pointers to Persistent Format
Live order books consist of complex, pointer-heavy memory structures:
* Sorted price slices (`[]decimal.Decimal`)
* Price level hash maps (`map[string]*PriceLevel`)
* Doubly-linked FIFO queues (`*list.List` with `*list.Element`)
* Global order hash index (`map[uuid.UUID]*OrderNode`)

[`Serialize()`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go#L60-L112) walks through all resting bids (sorted price descending) and asks (sorted price ascending) in strict price-time priority and flattens them into a clean, deterministic `BookSnapshot` structure:

```go
type BookSnapshot struct {
    SchemaVersion uint32          `json:"schema_version"`
    MarketID      string          `json:"market_id"`
    Partition     int             `json:"partition"`
    Offset        int64           `json:"offset"`    // Kafka offset at snapshot time
    Sequence      uint64          `json:"sequence"`  // Monotonic sequence counter
    Orders        []SnapshotOrder `json:"orders"`
}
```

---

### 3.2 `Checksum()` — Cryptographic Bit-Rot & Tamper Defense
```go
func Checksum(snap BookSnapshot) ([]byte, error) {
    data, err := json.Marshal(snap)
    if err != nil {
        return nil, fmt.Errorf("marshal for checksum: %w", err)
    }
    hash := sha256.Sum256(data)
    return hash[:], nil
}
```
Computes a **256-bit SHA-256 cryptographic hash** of the snapshot payload:
* **During Live Operations:** The computed checksum is stored alongside the snapshot in the `market_snapshots.checksum` column (`BYTEA`).
* **During Recovery:** The checksum is recomputed and compared against the stored hash. If disk corruption, database bit-rot, or tampering has altered even a single satoshi of price or quantity, the checksum fails, preventing the engine from trading against corrupt state.

---

### 3.3 `Restore()` — Persistent Format to RAM Structures
The inverse of `Serialize()`. Rebuilds the in-memory doubly-linked lists, price level buckets, sorted slices, and `OrderIndex` hash maps from the flat snapshot in **~10 milliseconds**.

---

## 4. The 5 Strict Validation Gates in `Restore()`

Before any snapshot is trusted and restored into live memory, [`Restore()`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go#L114-L160) validates **5 strict safety gates**:

```
                          SNAPSHOT ARRIVES FROM POSTGRESQL
                                         │
                                         ▼
                 ┌───────────────────────────────────────────────┐
                 │ Gate 1: Schema Check (SchemaVersion == 1)     │ ──► [Mismatch] ──► ABORT ❌
                 ├───────────────────────────────────────────────┤
                 │ Gate 2: Market ID Check (snap.Market == Mkt)  │ ──► [Mismatch] ──► ABORT ❌
                 ├───────────────────────────────────────────────┤
                 │ Gate 3: Partition Check (snap.Partition == Pt)│ ──► [Mismatch] ──► ABORT ❌
                 ├───────────────────────────────────────────────┤
                 │ Gate 4: Checkpoint Check (Offset <= Checkpoint│ ──► [Beyond]   ──► ABORT ❌
                 ├───────────────────────────────────────────────┤
                 │ Gate 5: SHA-256 Cryptographic Hash Check      │ ──► [Mismatch] ──► ABORT ❌
                 └───────────────────────┬───────────────────────┘
                                         │ (All 5 Gates Pass ✅)
                                         ▼
                  Reconstructs all Linked Lists, Maps, and
                  Order Indexes in RAM in ~10 Milliseconds!
```

1. **Gate 1 — Schema Version Validation (`snap.SchemaVersion == CurrentSchemaVersion`):**  
   Protects against loading an outdated snapshot format after an application upgrade.
2. **Gate 2 — Market ID Validation (`snap.MarketID == marketID`):**  
   Guarantees that an `ETH-USDT` snapshot is never accidentally loaded into a `BTC-USDT` order book.
3. **Gate 3 — Partition Validation (`snap.Partition == partition`):**  
   Ensures the snapshot matches the Kafka partition assigned to this market worker.
4. **Gate 4 — Checkpoint Boundary Validation (`snap.Offset <= checkpoint`):**  
   Asserts that the snapshot does not claim an offset in the future beyond the confirmed PostgreSQL checkpoint.
5. **Gate 5 — SHA-256 Cryptographic Checksum Validation:**  
   Recalculates the SHA-256 hash over the payload and asserts `computed == expectedChecksum`.

---

## 5. Lock-Free Inline Snapshot Triggering

A common misconception is that a background timer goroutine polls the order book to take snapshots.

In TradeDrift, **no separate background goroutine touches the order book**.  
*(Why? Because a background goroutine would require a mutex lock on the order book, which would destroy sub-microsecond matching performance!)*

Instead, the snapshot check executes **inline, lock-free, directly inside the single-threaded Market Event Loop** ([`internal/market/event_loop.go:93-108`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go#L93-L108)):

```go
// internal/market/event_loop.go
eventCountSinceLastSnapshot++

timeElapsed  := time.Since(lastSnapshotTime) >= snapshotDuration // (Default: 60 seconds)
countElapsed := eventCountSinceLastSnapshot >= snapshotInterval  // (Default: 10,000 orders)
isFirstEvent := m.book.Sequence == 1

if isFirstEvent || timeElapsed || countElapsed {
    // 1. Serialize in-memory order book (snapshot.go)
    snap := orderbook.Serialize(m.book, m.config.Partition, event.Offset)
    res.Snapshot = &snap
    
    // 2. Compute 256-bit SHA-256 Checksum (snapshot.go)
    chk, _ := orderbook.Checksum(snap)
    res.Checksum = chk

    // 3. Reset trigger counters
    lastSnapshotTime = time.Now()
    eventCountSinceLastSnapshot = 0
}

// 4. Send to OutputQueue -> Publisher -> Checkpoint Coordinator -> PostgreSQL!
m.OutputQueue <- res
```

### Snapshot Trigger Matrix

| Trigger | Condition | Why It Exists |
| :--- | :--- | :--- |
| **Event Count Trigger** | Every **10,000 orders** | In high-volume markets (e.g. 5,000 orders/sec), takes a snapshot every 2 seconds to keep recovery replay minimal. |
| **Time Trigger** | Every **60 seconds** | In low-volume / quiet markets, ensures an up-to-date snapshot is persisted at least once per minute. |
| **First Event Trigger** | Sequence == **1** | Guarantees that newly launched trading pairs immediately have a baseline snapshot in PostgreSQL. |
| **Graceful Shutdown** | On engine exit (`m.triggerFinalSnapshot()`) | Captures the exact final state before planned maintenance or rolling upgrades. |

---

## 6. Crash Scenarios: Sudden Crash vs. Graceful Shutdown

```
                                CRASH OCCURS
                                     │
                 ┌───────────────────┴───────────────────┐
                 ▼                                       ▼
        [1. SUDDEN CRASH]                       [2. GRACEFUL SHUTDOWN]
  (Power Cut / Kernel Panic)                (Planned Rolling Upgrade)
                 │                                       │
                 ▼                                       ▼
   • No time to write to disk.              • Engine receives SIGTERM signal.
   • PostgreSQL has last periodic           • Stops accepting new commands.
     snapshot (e.g. Offset 20,000).         • Calls Serialize() on live book.
   • Kafka has all raw commands.            • Flushes final snapshot to DB.
                 │                                       │
                 ▼                                       ▼
   RESTART:                                 RESTART:
   1. Restore snapshot from DB (10ms).      1. Restore final snapshot (10ms).
   2. Replay 50 delta events from Kafka.    2. Zero delta replay needed!
   3. Fully restored in < 30ms!             3. Live in < 15ms!
```

### Summary Comparison Table

| What is stored? | Where is it stored? | When is it stored? |
| :--- | :--- | :--- |
| **All Inbound Order Events** | **Apache Kafka** | Instantly upon order placement. |
| **Periodic Order Book Snapshots** | **PostgreSQL** (`market_snapshots`) | Every 10,000 events or 60 seconds (and on graceful shutdown). |
| **Contiguous Resume Offset** | **PostgreSQL** (`kafka_checkpoints`) | Continuously as contiguous runs complete. |
| **Live Active Order Books** | **Volatile RAM** | In real-time for sub-microsecond matching. |

---

## 7. How Checkpointing and Snapshotting Work Together

Checkpointing and Snapshotting are two interlocking pieces of the same recovery engine:

* **Checkpoint Coordinator** answers: *"How far have we safely processed Kafka?"*  
  $\to$ `kafka_checkpoints = 500,000`
* **Order Book Snapshot** answers: *"What did the order book look like around that point?"*  
  $\to$ `market_snapshots: offset = 499,950, sequence = 10,000`

```
                                  RECOVERY PIPELINE
                                  ═════════════════

                 1. Read Checkpoint: Offset = 500,000
                                 │
                                 ▼
                 2. Load Snapshot: Offset = 499,950
                    (Rebuilds book in RAM in 10ms)
                                 │
                                 ▼
                 3. Replay Delta Events: 499,951 → 500,000
                    (Replays 50 commands from Kafka)
                                 │
                                 ▼
                 4. Verify Market Sequence:
                    engine.Sequence == db.market_sequences.sequence
                                 │
                                 ▼
                 5. Resume Live Trading! 🚀
```

---

## 8. Summary Table: Responsibilities Across Files

| File | Primary Responsibility |
| :--- | :--- |
| [`internal/orderbook/snapshot.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go) | Defines `BookSnapshot` data struct, `Serialize()`, `Checksum()`, and `Restore()`. |
| [`internal/market/event_loop.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go) | Triggers inline lock-free snapshot generation every 10k orders / 60s. |
| [`internal/publisher/publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/publisher/publisher.go) | Routes snapshot on egress and runs retention cleanup (keeping last 3 snapshots). |
| [`internal/checkpoint/coordinator.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/checkpoint/coordinator.go) | Commits snapshot JSON and SHA-256 hash atomically to PostgreSQL. |
| [`internal/recovery/replayer.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go) | Queries PostgreSQL for latest snapshot $\le \text{checkpoint}$, calls `Restore()`, and replays Kafka delta. |
| [`migration/00003_create_market_snapshots.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/migration/00003_create_market_snapshots.sql) | DDL schema with compound descending index `idx_market_snapshots_market_sequence`. |
