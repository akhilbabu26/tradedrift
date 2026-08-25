# 🔄 The 7 Lifecycles of the TradeDrift Matching Engine

This document provides a clear, step-by-step breakdown of all **7 operational lifecycles** in the TradeDrift Matching Engine using intuitive, top-to-bottom architectural flow diagrams.

---

## 📑 Table of Contents

- [The 7 Lifecycles Master Map](#-the-7-lifecycles-master-map)
- [1. Bootstrap & Crash Recovery Lifecycle](#1--bootstrap--crash-recovery-lifecycle)
- [2. Limit Order Matching & Publication Lifecycle](#2--limit-order-matching--publication-lifecycle)
- [3. Market Order (IOC) Execution Lifecycle](#3--market-order-ioc-execution-lifecycle)
- [4. Order Cancellation Lifecycle](#4--order-cancellation-lifecycle)
- [5. Graceful Two-Context Teardown Lifecycle](#5--graceful-two-context-teardown-lifecycle)
- [6. Depth Projection Query Lifecycle (Read Path)](#6--depth-projection-query-lifecycle-read-path)
- [7. Invalid Order Validation & Rejection Lifecycle](#7--invalid-order-validation--rejection-lifecycle)
- [Summary Matrix of All 7 Lifecycles](#-summary-matrix-of-all-7-lifecycles)

---

## 🗺 The 7 Lifecycles Master Map

```
                          [Service Starts]
                                 │
                   1. Bootstrap & Recovery
                                 │
                   ┌─────────────┴─────────────┐
                   ↓                           ↓
        [Core Trading Operations]       [Auxiliary Operations]
        ├─ 2. Limit Order Matching      ├─ 6. Depth Query (Read Path)
        ├─ 3. Market Order (IOC)        └─ 7. Invalid Order Rejection
        └─ 4. Order Cancellation
                   │
                   │ (SIGTERM / Shutdown Signal)
                   ↓
        5. Graceful Teardown & Exit
```

---

## 1. 🚀 Bootstrap & Crash Recovery Lifecycle

### Purpose
When the engine starts up or recovers from a crash, it reconstructs the in-memory order book by replaying Kafka history from the last committed database checkpoint up to the current Kafka High-Water Mark (HWM).

### Flow Diagram

```
                   [Service Start / Restart]
                              │
                    Postgres Checkpoint
               (Load Last Saved Offset: e.g. 500)
                              │
                    Kafka Partition Leader
               (Fetch High-Water Mark: e.g. 550)
                              │
                    Replayer (replayer.go)
               (Replays Events 501..549 in Loop)
                              │
                    Market Engine (RAM)
               (Runs in ModeRecovery — Rebuilds
                OrderBook; Trade Fills Suppressed)
                              │
                              ▼
                        engine.SetLive()
                              │
                              ▼
                   [System in LIVE Mode]
               (Start Publishers & Live Consumers)
```

### Step-by-Step Explanation:
1. **Load Checkpoint:** Queries PostgreSQL `kafka_checkpoints` to find the exact last processed offset.
2. **Fetch HWM:** Queries Kafka to determine how many unread messages exist.
3. **Replay in Recovery Mode:** Historical messages are fed into `matcher.Match()` in `ModeRecovery`. The in-memory order book is reconstructed, but **no duplicate trades are emitted to Kafka**.
4. **Switch to Live:** Switches the engine to `ModeLive` and starts publisher goroutines and live Kafka consumers. No Redis depth hydration occurs during recovery, keeping the phase completely side-effect-free.

* **Code Reference:** [`cmd/server/main.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/cmd/server/main.go) & [`internal/recovery/replayer.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go)

---

## 2. ⚡ Limit Order Matching & Publication Lifecycle

### Purpose
Processes a new `LIMIT` order, matches it against crossing price levels in FIFO order, rests any unfilled balance in the order book, and executes the sequential egress pipeline.

### Flow Diagram

```
                         Trader
                           │
                     Kafka Ingress
                  (orders.commands)
                           │
                    Kafka Consumer
                           │ (InputQueue)
                    Matching Engine
                  (FIFO Match in RAM)
                  (Rests balance in book)
                           │ (OutputQueue)
                      Publisher
             ┌─────────────┼─────────────┐
             ↓ (Step 1)    ↓ (Step 2)    ↓ (Step 3)
           Kafka         Redis        PostgreSQL
     (trades.executed) (depth:market_id) (Monotonic Checkpoint)
             │             │                 │
             │        Projection             │
             │             │                 │
             │       WebSocket Hub           │
             │             │                 │
             └─────────────┴─────────────────→ Downstream Systems
                                               (Wallets, UI & DB)
```

### Step-by-Step Explanation:
1. **Ingress:** Trader submits an order $\to$ Kafka topic `orders.commands` $\to$ routed to `engine.InputQueue`.
2. **RAM Matching:** The single-threaded engine matches crossing orders in RAM (sub-microsecond) and rests any unfilled quantity in the book.
3. **Publisher Pipeline (Strict Order):**
   * **Step 1 (Kafka):** Publishes executed trade fills to `trades.executed` for financial settlement.
   * **Step 2 (Redis):** Pushes the new L2 depth snapshot to Redis `depth:{market_id}` for live charts.
   * **Step 3 (Postgres):** Saves the Kafka offset checkpoint in PostgreSQL.

* **Code Reference:** [`internal/matcher/matcher.go:matchLimit`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L35) & [`internal/publisher/publisher.go:process`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/publisher/publisher.go#L145)

---

## 3. 🏃 Market Order (IOC) Execution Lifecycle

### Purpose
Executes a `MARKET` order by immediately sweeping available resting liquidity across price levels. Any portion that cannot be immediately filled is **instantly cancelled (IOC)** and is never placed in the order book.

### Flow Diagram

```
                         Trader
                           │
                     Kafka Ingress
                  (orders.commands)
                           │ (MARKET Order)
                    Matching Engine
                  (Sweeps Opposite Book:
                   Best to Worst Price)
                           │
                 ┌─────────┴─────────┐
                 ↓                   ↓
             Filled Qty         Unfilled Qty
          (e.g., 3.5 BTC)      (e.g., 1.5 BTC)
                 │                   │
                 │              ioc_expired
                 │             (Instant Cancel,
                 │              Never in Book)
                 └─────────┬─────────┘
                           │ (MatchResult)
                       Publisher
             ┌─────────────┼─────────────┐
             ↓ (Step 1)    ↓ (Step 2)    ↓ (Step 3)
           Kafka         Redis        PostgreSQL
     (trades.executed) (depth:market_id) (Checkpoint Advance)
```

### Step-by-Step Explanation:
1. **Ingress:** Market order arrives with no limit price.
2. **Book Sweep:** The engine sweeps resting limit orders from best available price to worst.
3. **IOC Expiry:** Any remaining unfilled quantity produces a `CancelledOrder` entry with reason `ioc_expired` (it is never rested in RAM).
4. **Egress:** Publishes fills to Kafka $\to$ updates Redis depth $\to$ commits checkpoint to PostgreSQL.

* **Code Reference:** [`internal/matcher/matcher.go:matchMarket`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L110)

---

## 4. ❌ Order Cancellation Lifecycle

### Purpose
Cancels an active resting limit order, removes it from the in-memory doubly-linked list in $O(1)$ time, updates Redis depth, and advances the database checkpoint.

### Flow Diagram

```
                         Trader
                           │
                     Kafka Ingress
               (orders.cancel-requested)
                           │
                    Matching Engine
                           │
                     OrderBook RAM
                 (OrderIndex Hash Map)
                           │
                 ┌─────────┴─────────┐
                 ↓                   ↓
           [Order Found]      [Order Not Found]
                 │                   │
         Unlink from FIFO      Already Filled /
         Price Level Queue       Cancelled
                 │                   │
           CancelResult           No-Op
         (user_requested)            │
                 └─────────┬─────────┘
                           │ (MatchResult)
                       Publisher
                     ┌─────┴─────┐
                     ↓ (Step 1)  ↓ (Step 2)
                   Redis     PostgreSQL
            (depth:market_id) (Checkpoint Advance)
```

### Step-by-Step Explanation:
1. **Ingress:** Cancel request arrives on `orders.cancel-requested` with `OrderID`.
2. **$O(1)$ Hash Map Lookup:** Engine locates the order directly in `OrderIndex`.
3. **Branching:**
   * **If Found:** Unlinks node from the doubly-linked list, removes empty price levels, and emits `CancelResult`.
   * **If Not Found:** Safely treats as an idempotent no-op (already filled or cancelled).
4. **Egress:** Pushes updated depth to Redis $\to$ advances PostgreSQL checkpoint.

* **Code Reference:** [`internal/orderbook/book.go:Cancel`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/book.go#L95)

---

## 5. 🛑 Graceful Two-Context Teardown Lifecycle

### Purpose
Guarantees clean shutdown during system updates or OS interrupts (`SIGTERM`/`SIGINT`) by draining all in-flight channel events before closing network connections.

### Flow Diagram

```
                   [SIGTERM / SIGINT]
                           │
                   cmd/server/main.go
                           │
                   Cancel opCtx
                           │
                 ┌─────────┴─────────┐
                 ↓                   ↓
          Stop Kafka Ingestion   Wait for Drain
          (consumer.Close())     (Publishers flush
                                  in-flight channels)
                 │                   │
                 └─────────┬─────────┘
                           │ (All goroutines done)
                  Close Infrastructure
                  ┌────────┴────────┐
                  ↓                 ↓
                Redis           PostgreSQL
             (rdb.Close())     (db.Close())
                           │
                   [Clean Exit (Code 0)]
```

### Step-by-Step Explanation:
1. **Interrupt Signal:** OS sends `SIGTERM` or `SIGINT`.
2. **Stop Ingestion:** Cancels `opCtx` and closes Kafka consumers to prevent accepting new requests.
3. **Drain In-Flight Events:** Publisher goroutines have up to a **15-second grace deadline** to finish matching and checkpointing any in-flight messages in the queues.
4. **Close Connections:** Safely closes Redis client and PostgreSQL connection pools.
5. **Exit:** Process terminates cleanly with exit code `0`.

* **Code Reference:** [`cmd/server/main.go:224-266`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/cmd/server/main.go#L224-L266)

---

## 6. 🔍 Depth Projection Query Lifecycle (Read Path)

### Purpose
Provides lock-free, sub-millisecond Level-2 order book depth to external WebSocket gateways and REST APIs directly from Redis without touching the matching engine.

### Flow Diagram

```
                         Clients
                   (UI, Charts, Apps)
                           │
                     WebSocket Hub /
                      REST API Gateway
                           │
                    projection.Reader
                           │
                 ┌─────────┴─────────┐
                 ↓                   ↓
            Single Market      Multi-Market Batch
             (reader.Get)        (reader.MGet)
                 │                   │
             Redis GET           Redis MGET
          (depth:BTC-USDT)    (50+ Markets in 1 RTT)
                 │                   │
                 └─────────┬─────────┘
                           │
                   parseAndValidate()
                 (Check MarketID, Timestamp,
                  Price > 0, Quantity > 0)
                           │
                           ↓
                   Real-Time OrderBook
                 (Delivered to Trader UI)
```

### Step-by-Step Explanation:
1. **Client Request:** Frontend or WebSocket hub requests the order book.
2. **Redis Fetch:**
   * **Single Market:** Calls `GET depth:{market_id}`.
   * **Multiple Markets:** Calls `MGET depth:BTC-USDT depth:ETH-USDT ...` (fetches all in **1 network round-trip**).
3. **Validation:** [`reader.go:parseAndValidateSnapshot`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/projection/reader.go#L115) enforces strict decimal formatting, positive prices ($>0$), positive quantities ($>0$), and valid timestamps.
4. **Delivery:** Delivers clean `OrderBookProjection` to client.

* **Code Reference:** [`internal/projection/reader.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/projection/reader.go)

---

## 7. 🛡 Invalid Order Validation & Rejection Lifecycle

### Purpose
Intercepts and rejects malformed orders (negative numbers, invalid tick/lot sizes) before they touch the matching book, preventing memory corruption while ensuring the Kafka checkpoint advances safely.

### Flow Diagram

```
                     Kafka Ingress
                   (orders.commands)
                           │
                   Kafka Consumer
                           │
                    Matching Engine
                  (Pre-Match Validation)
                           │
                [Validation FAILED]
             (Bad Tick/Lot Size, Qty <= 0)
                           │
                 ┌─────────┴─────────┐
                 ↓                   ↓
            OrderBook RAM        Publisher
          (ZERO modifications,   (Empty MatchResult,
           NO state corrupted)    0 fills, NO trades)
                                     │
                                     ↓
                                 PostgreSQL
                           (Checkpoint Advances Safely)
```

### Step-by-Step Explanation:
1. **Ingress:** Malformed message arrives (e.g. price $0.003 against a $0.01 tick size, or negative quantity).
2. **Pre-Match Rejection:** `processEvent()` detects the invalid boundary condition before invoking the matcher.
3. **RAM Protection:** The in-memory order book is **not modified**, and zero trade fills are generated.
4. **Deterministic Progress:** An empty `MatchResult` is emitted to the publisher so the PostgreSQL checkpoint advances past the bad message, preventing infinite crash-retry loops.

* **Code Reference:** [`internal/market/event_loop.go:processEvent`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go)

---

## 📊 Summary Matrix of All 7 Lifecycles

| # | Lifecycle Name | Trigger | RAM Engine Action | Egress Mutations |
| :---: | :--- | :--- | :--- | :--- |
| **1** | **Bootstrap & Recovery** | Startup / Crash Restart | Replays Kafka logs; suppresses fills | Re-aligns offsets; sets `ModeLive` |
| **2** | **Limit Order Matching** | `OrderCreated` (`LIMIT`) | Matches FIFO; rests remaining balance | Kafka trades $\to$ Redis depth $\to$ Postgres checkpoint |
| **3** | **Market Order (IOC)** | `OrderCreated` (`MARKET`) | Sweeps book; cancels unfilled remainder | Kafka trades $\to$ Redis depth $\to$ Postgres checkpoint |
| **4** | **Order Cancellation** | `OrderCancel` | $O(1)$ lookup; unlinks from FIFO queue | Redis depth $\to$ Postgres checkpoint |
| **5** | **Graceful Teardown** | OS `SIGTERM` / `SIGINT` | Stops ingestion; drains in-flight channels | Flushes queues; closes DB & Redis pools |
| **6** | **Depth Query (Read)** | WebSocket / REST API | None (Zero engine load; queried from Redis) | Single `GET` or batch `MGET` from Redis |
| **7** | **Invalid Order Rejection** | Malformed Order | Validation failure; RAM book untouched | Skips trade publishing; advances Postgres checkpoint |
