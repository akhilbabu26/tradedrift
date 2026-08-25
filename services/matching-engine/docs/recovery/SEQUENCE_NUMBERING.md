# Sequence Numbering in TradeDrift Matching Engine

**Service:** Matching Engine (`services/matching-engine`)  
**Documentation:** `SEQUENCE_NUMBERING.md`  
**Topic:** Monotonic Sequence Progression, Per-Market Ownership, and Downstream Synchronization  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Definition

In the TradeDrift Matching Engine, the **Sequence Number** is an unsigned 64-bit integer (`uint64`) maintained **independently per trading pair** (`market_id`).

It represents the **authoritative logical clock** of the order book. Every time an event permanently modifies the in-memory state of a market (such as an executed trade fill, a new resting limit order, or an order cancellation), the market's sequence counter increments monotonically:

$$\text{Sequence}_{n+1} = \text{Sequence}_n + 1$$

It creates a deterministic, gapless chronological timeline ($1, 2, 3, \dots, N$) for each market, independent of physical server clocks, network delays, or cross-market trading activity.

---

## 2. Problems Solved, How Solved & Implementing Functions Matrix

| Problem Solved | Danger / Failure Scenario | How It Is Solved | Implementing Function(s) & Code Location |
| :--- | :--- | :--- | :--- |
| **1. Undetected Packet Loss & Gaps** | A trade fill message is dropped over Kafka/WebSockets. Downstream settlement calculates balances on missing data. | Monotonic sequence increment ($N \to N+1$). Downstream consumers detect gaps if `receivedSeq > lastSeenSeq + 1`. | [`nextSequence`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go#L20-L24), [`Publisher.process`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/publisher/publisher.go#L95-L105) |
| **2. Post-Recovery State Divergence** | Engine finishes Kafka replay on reboot, but subtle math bugs caused an order to match differently or drop. | Asserts `engine.GetSequence() == db.market_sequences.sequence`. Halts boot immediately if sequence counters diverge. | [`replayer.ReplayAll`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go#L243-L247) |
| **3. Non-Deterministic Trade ID Collisions** | Random UUIDs cause duplicate fills to generate new IDs upon retry. | Feeds monotonic sequence into deterministic UUIDv5 SHA-1 hash for `TradeID`, guaranteeing 100% idempotent IDs. | [`generateTradeID`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L340-L360) |
| **4. Concurrency Mutex Lock Bottlenecks** | Global sequence counter requires cross-core locking between BTC and ETH. | Strict per-market ownership. Each single-threaded event loop increments its own counter without any mutexes. | [`MarketEngine.sequence`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go#L40-L45) |

---

## 3. Why Sequence Numbers Are Used (Core Advantages)

```
       WITHOUT SEQUENCE NUMBERS                       WITH SEQUENCE NUMBERS
 ┌──────────────────────────────────┐        ┌──────────────────────────────────┐
 │ Packet 101 arrives               │        │ Packet 101 arrives (Seq: 101)    │
 │ Packet 103 arrives               │        │ Packet 103 arrives (Seq: 103)    │
 │                                  │        │                                  │
 │ ❓ "Did we drop a trade, or was   │        │ 🚨 GAP DETECTED!                 │
 │     there simply no trade?"      │        │ "Packet 102 was dropped. Pause   │
 │                                  │        │  and fetch missing trade 102."   │
 └──────────────────────────────────┘        └──────────────────────────────────┘
```

### 2.1 Gap & Packet Loss Detection
Asynchronous message brokers (Kafka) and WebSocket networks can occasionally drop, buffer, or re-transmit packets.
* Downstream consumers (Order Service, Wallet/Settlement Service, Market Data Feeds) track `last_seen_sequence`.
* If a downstream service receives sequence **`104`** immediately after **`102`**, it **instantly detects** that packet **`103`** was dropped or delayed. It can pause processing and request resynchronization rather than executing on incomplete data.

### 2.2 Out-of-Order Network Rejection (Stale Data Protection)
Under heavy network traffic or WebSocket reconnects, packets can arrive out of order.
* If a frontend UI chart or WebSocket client receives an older depth snapshot with `Sequence: 50` after already rendering `Sequence: 51`, it applies a simple check:
  ```go
  if packet.Sequence <= lastSeenSequence {
      // Discard stale network packet
      return
  }
  ```
* This prevents order book flicker and guarantees that charts never move backward in time.

### 2.3 Eliminating Physical Clock Drift & Sub-Microsecond Collisions
* **Physical Clocks Drift:** Two physical servers (or even two CPU cores on the same motherboard) never share the exact same nanosecond clock.
* **Sub-Microsecond Matching:** The Matching Engine matches multiple orders in the exact same microsecond. Standard timestamps (`time.Now()`) would collide and produce duplicate timestamps.
* **The Sequence Counter:** Provides an infallible, mathematically ordered progression ($1 \to 2 \to 3 \dots$).

### 2.4 Crash Recovery Verification Invariant
When the matching engine restarts after a crash, [`internal/recovery`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/02READEME.md) restores the order book from a snapshot, replays the Kafka command log, and queries PostgreSQL [`market_sequences`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/migration/00002_create_market_sequences.sql):
$$\text{Assert: } \text{engine.GetSequence()} == \text{db.market\_sequences.sequence}$$
If the counters do not match, recovery halts immediately. This prevents corrupt, duplicate, or skipped trades from entering live trading.

---

## 3. Why Each Market Owns Its Own Independent Sequence

In TradeDrift, sequence numbers are **strictly per-market**, not a single shared global counter across all pairs:

```
 ┌─────────────────────────┐     ┌─────────────────────────┐     ┌─────────────────────────┐
 │   BTC-USDT OrderBook    │     │   ETH-USDT OrderBook    │     │   SOL-USDT OrderBook    │
 │   Sequence: 1, 2, 3...  │     │   Sequence: 1, 2, 3...  │     │   Sequence: 1, 2, 3...  │
 └─────────────────────────┘     └─────────────────────────┘     └─────────────────────────┘
              │                               │                               │
              ▼                               ▼                               ▼
 ┌─────────────────────────────────────────────────────────────────────────────────────────┐
 │                       PostgreSQL Table: market_sequences                                │
 ├─────────────────────────────────────────────┬───────────────────────────────────────────┤
 │ market_id (PRIMARY KEY)                     │ sequence                                  │
 ├─────────────────────────────────────────────┼───────────────────────────────────────────┤
 │ "BTC-USDT"                                  │ 54,821                                    │
 │ "ETH-USDT"                                  │ 12,403                                    │
 │ "SOL-USDT"                                  │ 8,119                                     │
 └─────────────────────────────────────────────┴───────────────────────────────────────────┘
```

### 3.1 Zero Lock Contention & Lock-Free Actor Concurrency
* Each market is exclusively owned by a single Go goroutine ([`MarketEngine.Run`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go#L114-L165)).
* **If sequence numbers were global:** Every trade on `BTC-USDT` would have to acquire a shared global mutex or atomic CPU lock against `ETH-USDT` and `SOL-USDT`, causing severe CPU cache line bouncing and slowing down matching.
* **With per-market sequences:** Each engine runs in pure RAM with **zero locks** and zero cross-market dependencies.

### 3.2 Clean, Continuous Streams for Traders
* A trader watching the `BTC-USDT` order book receives a clean, gapless sequence:
  $$\text{Trade 1} \longrightarrow \text{Trade 2} \longrightarrow \text{Trade 3} \longrightarrow \text{Trade 4}$$
* **If sequence numbers were global:** A BTC trader would see sequence numbers jump randomly whenever an ETH trade occurred ($1 \to 5 \to 12 \to 19$). The BTC client would be unable to distinguish between a dropped BTC trade and an unrelated ETH trade.

### 3.3 Independent Sharding, Migration & Crash Recovery
* If `SOL-USDT` crashes, it restores its own order book and verifies only its own sequence counter ($8,119$).
* It never interrupts, locks, or synchronizes with `BTC-USDT` or `ETH-USDT`.

---

## 4. Exact Operational Scenarios: When Does the Sequence Change?

```
                           INCOMING COMMAND ARRIVES
                                      │
                                      ▼
                       ┌──────────────────────────────┐
                       │   Pre-Match Validation Gate  │ ──► [INVALID TICK/LOT] ──► Sequence unchanged
                       └──────────────┬───────────────┘
                                      │ (Valid Order)
                                      ▼
                       ┌──────────────────────────────┐
                       │     Execute Matcher Loop     │
                       └──────────────┬───────────────┘
                                      │
        ┌─────────────────────────────┼─────────────────────────────┐
        ▼                             ▼                             ▼
 [Trade 1 Matched]             [Trade 2 Matched]            [Remaining Limit Order]
 book.Sequence++ (e.g., 101)   book.Sequence++ (e.g., 102)  book.Sequence++ (e.g., 103)
```

### ✅ When the Sequence INCREASES (`book.Sequence++`):

| # | Scenario | What Happens | Sequence Mutation |
| :---: | :--- | :--- | :---: |
| **1** | **Single 1-to-1 Trade Fill** | A market or limit order matches 1 resting order on the opposite side. | **+1** |
| **2** | **Multi-Level Sweep (N Fills)** | A large order sweeps through 3 price levels, matching against 3 resting makers. | **+3** (one per fill) |
| **3** | **Partial Match + Resting Remainder** | A limit order matches 1 order, and its remaining unfilled quantity rests on the book. | **+2** (+1 for fill, +1 for resting placement) |
| **4** | **Pure Resting Limit Order** | A limit order does not cross any prices and is inserted into the book. | **+1** |
| **5** | **Successful Order Cancellation** | A resting order is found in `book.OrderIndex` and unlinked via `matcher.Cancel()`. | **+1** |

---

### ❌ When the Sequence DOES NOT Change:

| # | Scenario | Why the Sequence Does NOT Change |
| :---: | :--- | :--- |
| **6** | **Invalid Order Parameters** | Order fails tick size modulo (`Price % TickSize != 0`) or lot size modulo. Rejected at validation gate. |
| **7** | **Duplicate Ingestion Command** | Event ID already exists in the 50,000-entry deduplication ring buffer. Rejected before matcher. |
| **8** | **Unfilled Market Order (IOC)** | Unfilled remainder of a market order is discarded (`ioc_expired`). Never placed on book. |
| **9** | **Cancel for Filled / Unknown Order** | Order ID not found in `book.OrderIndex` (already filled or already cancelled). `matcher.Cancel()` returns `nil`. |

---

## 5. Code Implementation Walkthrough

### 5.1 In-Memory Representation ([`internal/orderbook/book.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/book.go#L37-L43))

```go
type OrderBook struct {
    MarketID   string
    Sequence   uint64                   // Monotonically increasing sequence per market
    Bids       Side
    Asks       Side
    OrderIndex map[uuid.UUID]*OrderNode
}
```

### 5.2 Matcher Loop Execution ([`internal/matcher/matcher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go))

```go
// Inside matcher.Match / matchLimit:
for incoming.RemainingQty.GreaterThan(decimal.Zero) {
    bestMaker := ExecuteBest(oppositeSide)
    if bestMaker == nil || !crossable(incoming, bestMaker) {
        break
    }

    fillQty := decimal.Min(incoming.RemainingQty, bestMaker.RemainingQty)

    // 1. Increment sequence for each trade fill
    book.Sequence++

    fill := orderbook.Fill{
        TradeID:   TradeID(eventID, bestMaker.OrderID, incoming.OrderID, fillIndex),
        Sequence:  book.Sequence, // Attached to trade payload!
        MarketID:  book.MarketID,
        Price:     bestMaker.Price,
        Quantity:  fillQty,
        // ...
    }
    fills = append(fills, fill)
    fillIndex++
    // ...
}

// 2. Increment sequence if unfilled limit order rests on book
if incoming.OrderType == orderbook.OrderTypeLimit && incoming.RemainingQty.GreaterThan(decimal.Zero) {
    book.Sequence++
    Insert(book, incoming)
}
```

### 5.3 Order Cancellation ([`internal/market/event_loop.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go))

```go
// Inside Event Loop handling EventOrderCancel:
if cancelledNode := matcher.Cancel(m.book, event.OrderID); cancelledNode != nil {
    // 3. Increment sequence when book state changes due to cancellation
    m.book.Sequence++
    // Emits cancellation result with updated sequence
}
```

### 5.4 Egress Broadcast ([`internal/publisher/publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/publisher/publisher.go))

* **Kafka Trade Message (`trades.executed`):**
  ```json
  {
    "trade_id": "019163f5-93b6-710b-b187-2c93b6710bb1",
    "market_id": "BTC-USDT",
    "sequence": 101,
    "price": "65000.00",
    "quantity": "0.5000",
    "executed_at": "2026-08-25T11:00:00.123456789Z"
  }
  ```
* **Redis Depth Snapshot (`depth:BTC-USDT`):**
  ```json
  {
    "market_id": "BTC-USDT",
    "sequence": 103,
    "bids": [{"price": "64990.00", "quantity": "1.2500"}],
    "asks": [{"price": "65010.00", "quantity": "0.8000"}],
    "snapshot_at": "2026-08-25T11:00:00.123456789Z"
  }
  ```

### 5.5 PostgreSQL Persistence ([`migration/00002_create_market_sequences.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/migration/00002_create_market_sequences.sql))

```sql
CREATE TABLE IF NOT EXISTS market_sequences (
    market_id  VARCHAR(64)  NOT NULL,
    sequence   BIGINT       NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (market_id)
);
```

Whenever a batch of contiguous events completes, [`checkpoint.Coordinator.commitTransaction`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/checkpoint/coordinator.go#L190-L245) executes:

```sql
INSERT INTO market_sequences (market_id, sequence, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (market_id)
DO UPDATE SET
    sequence   = EXCLUDED.sequence,
    updated_at = NOW();
```

---

## 6. Summary Matrix

| Property | Specification |
| :--- | :--- |
| **Type** | `uint64` (starts at `0`, initial trade = `1`) |
| **Scope** | **Per Market** (Each trading pair maintains its own independent sequence) |
| **Mutating Events** | Trade fills, resting limit placements, order cancellations |
| **Non-Mutating Events** | Invalid orders, duplicate events, IOC market order drops |
| **Durability Store** | PostgreSQL `market_sequences` (updated atomically in `BEGIN ... COMMIT`) |
| **Egress Inclusions** | Included on every Kafka trade event and every Redis depth snapshot |
| **Primary Advantage** | Infallible gap detection, stale packet rejection, and crash-recovery verification |
