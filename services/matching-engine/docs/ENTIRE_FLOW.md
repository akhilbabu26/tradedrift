# TradeDrift Matching Engine — End-to-End Architectural Master Guide

**Service:** TradeDrift Matching Engine (`services/matching-engine`)  
**Scope:** Complete Architecture, Inter-file Connections, Concurrency Invariants, Edge Cases Resolution, Lifecycle Flows & V2 Roadmap  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Design Philosophy

The TradeDrift Matching Engine is an ultra-low-latency, deterministic, in-memory cryptocurrency and asset matching system written in Go.

### Core Architectural Axioms

1. **Single-Goroutine Ownership per Market**:
   Each trading pair (e.g., `BTC-USDT`, `ETH-USDT`) is assigned an isolated `MarketEngine` running a dedicated Event Loop goroutine. Only this goroutine is permitted to touch the in-memory `OrderBook`. As a result, **zero locks/mutexes** are acquired on the critical matching path.
2. **1-in $\rightarrow$ 1-out Deterministic Invariant**:
   Every single `InputEvent` arriving on an engine's `InputQueue` produces **exactly one** `MatchResult` onto its `OutputQueue`. This guarantees that every Kafka event gets an exact 1:1 checkpoint and depth update.
3. **Sequential Pipeline Execution**:
   For any processed match result, mutations occur in strict sequence:  
   $$\text{Match Result} \longrightarrow \text{Publish Kafka Trades} \longrightarrow \text{Push Redis Depth} \longrightarrow \text{Commit Postgres Checkpoint}$$
4. **Clean Decoupling of Ingestion, Execution, and Publication**:
   - **Ingestion (`internal/kafka`)**: Raw byte deserialization and channel routing.
   - **Execution (`internal/market`, `internal/matcher`, `internal/orderbook`)**: Pure deterministic state transitions.
   - **Publication (`internal/publisher`)**: External I/O and checkpoint durability.
   - **Recovery (`internal/recovery`)**: Crash replay from durable storage.
   - **Orchestration (`cmd/server`)**: Process lifecycle and graceful teardown.

---

## 2. Complete File Directory & Responsibility Map

```
services/matching-engine/
│
├── cmd/
│   └── server/
│       ├── main.go                     # Service bootstrap, configuration, recovery coordination, shutdown
│       └── README.md                   # Command/Server documentation
│
├── internal/
│   ├── orderbook/                      # Pure In-Memory Data Structures
│   │   ├── node.go                     # OrderNode struct, linked list pointers, order metadata
│   │   ├── level.go                    # PriceLevel struct, doubly-linked FIFO order queue
│   │   ├── side.go                     # OrderSide struct, BTree/sorted price level storage
│   │   ├── book.go                     # OrderBook struct, Bids, Asks, OrderIndex (UUID map)
│   │   ├── result.go                   # MatchResult, Fill, CancelledOrder, DepthSnapshot, KafkaPosition
│   │   └── README.md                   # OrderBook package documentation
│   │
│   ├── matcher/                        # Core Matching Engine Algorithms
│   │   ├── matcher.go                  # Limit/Market matching algorithms, Price-Time priority matching
│   │   ├── matcher_test.go             # 23 Unit tests validating FIFO, crossing limits, IOC, partial fills
│   │   └── README.md                   # Matcher package documentation
│   │
│   ├── market/                         # Engine Lifecycle & Single-Goroutine Event Loop
│   │   ├── engine.go                   # MarketEngine struct, Mode (Recovery vs Live), Input/Output queues
│   │   ├── event_loop.go               # EventLoop Run(ctx), processEvent, pre-match validation
│   │   ├── manager.go                  # MarketManager, thread-safe registry of active engines
│   │   └── README.md                   # Market package documentation
│   │
│   ├── kafka/                          # Ingestion Layer
│   │   ├── consumer.go                 # Consumer struct, multi-topic readers, HandleOrderCreated/Cancel
│   │   ├── consumer_test.go            # 12 Unit tests validating deserialization, routing, corrupt messages
│   │   └── README.md                   # Kafka package documentation
│   │
│   ├── publisher/                      # Downstream Egress & Checkpointing
│   │   ├── publisher.go                # Publisher struct, sequential Kafka -> Redis -> Postgres pipeline
│   │   ├── publisher_test.go           # 13 Unit tests validating ordering, error propagation, monotonic DB
│   │   └── README.md                   # Publisher package documentation
│   │
│   ├── recovery/                       # Durability & Crash Recovery
│   │   ├── replayer.go                 # Orchestrates startup bootstrap & concurrent draining
│   │   ├── partition.go                # Kafka partition message iteration & routing
│   │   ├── db.go                       # Postgres database query encapsulation
│   │   └── 01README.md                 # Recovery package documentation
│   │
│   └── projection/                     # Read-Only Depth Projection Client Layer
│       ├── snapshot.go                 # OrderBookProjection, DepthLevel, analytical helpers
│       ├── reader.go                   # Reader struct, Redis Getter, single/batch MGET, validator
│       ├── reader_test.go              # 13 Unit tests validating parsing, missing keys, validations
│       └── README.md                   # Projection package documentation
│
├── migration/
│   ├── 00001_create_kafka_checkpoints.sql # DDL for PostgreSQL durable checkpoint tracking
│   └── README.md                       # Migration guide
│
├── go.mod                              # Go module definition
├── go.sum                              # Cryptographic checksums of dependencies
└── ENTIRE_FLOW.md                      # This master architecture document
```

---

## 3. High-Level System Architecture Diagram

```
                        ┌──────────────────────────────────────────────────┐
                        │             KAFKA INGESTION TOPICS               │
                        │   • orders.submitted                             │
                        │   • orders.cancel-requested                      │
                        └────────┬────────────────────────────────┬────────┘
                                 │                                │
                       (Live Ingestion)                   (Startup Replay)
                                 │                                │
                                 ▼                                ▼
                        ┌──────────────────┐            ┌──────────────────┐
                        │  kafka.Consumer  │            │ recovery.Replayer│
                        │  (internal/kafka)│            │(internal/recovery)│
                        └────────┬─────────┘            └────────┬─────────┘
                                 │                                │
                                 │ InputEvent                     │ InputEvent
                                 ▼                                ▼
┌──────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                       MARKET ENGINE LAYER                                        │
│                                        (internal/market)                                         │
│                                                                                                  │
│   ┌──────────────────────────────────────────────────────────────────────────────────────────┐   │
│   │  MarketEngine [BTC-USDT]                                                                 │   │
│   │                                                                                          │   │
│   │   InputQueue (chan InputEvent, cap: 1000)                                                │   │
│   │        │                                                                                 │   │
│   │        ▼                                                                                 │   │
│   │   Event Loop: Run(ctx) (Single Goroutine)                                                │   │
│   │        │                                                                                 │   │
│   │        ├── validTickAndLot() ──[Invalid]──> Emit CancelResult (invalid_order_parameters) │   │
│   │        │                                                                                 │   │
│   │        └── matcher.Match() / Cancel() (internal/matcher)                                 │   │
│   │                 │                                                                        │   │
│   │                 ▼                                                                        │   │
│   │             OrderBook (internal/orderbook)                                               │   │
│   │             • Bids (Sorted PriceLevels -> FIFO Queue of OrderNodes)                      │   │
│   │             • Asks (Sorted PriceLevels -> FIFO Queue of OrderNodes)                      │   │
│   │             • OrderIndex (map[uuid.UUID]*OrderNode for O(1) Cancel)                      │   │
│   │                 │                                                                        │   │
│   │        ┌────────┴─────────────────────────────────┐                                      │   │
│   │        │                                          │                                      │   │
│   │   [ModeRecovery]                             [ModeLive]                                  │   │
│   │        │                                          │                                      │   │
│   │        ▼                                          ▼                                      │   │
│   │   Drained by Replayer                    OutputQueue (chan MatchResult, cap: 1000)       │   │
│   │   (Discarded - No Egress)                         │                                      │   │
│   └───────────────────────────────────────────────────┼──────────────────────────────────────┘   │
└───────────────────────────────────────────────────────┼──────────────────────────────────────────┘
                                                        │
                                                        ▼
                                       ┌──────────────────────────────────┐
                                       │       publisher.Publisher        │
                                       │       (internal/publisher)       │
                                       │   (1 Goroutine per MarketEngine) │
                                       └────────────────┬─────────────────┘
                                                        │
                         ┌──────────────────────────────┼──────────────────────────────┐
                         │ Step 1                       │ Step 2                       │ Step 3
                         ▼                              ▼                              ▼
            ┌────────────────────────┐     ┌────────────────────────┐     ┌────────────────────────┐
            │         KAFKA          │     │         REDIS          │     │       POSTGRESQL       │
            │ Topic: trades.executed │     │ Key: depth:{market_id} │     │ Table: kafka_checkpoint│
            │ (Downstream Services)  │     │ (Real-Time WebSockets) │     │ (Monotonic Checkpoint) │
            └────────────────────────┘     └────────────────────────┘     └────────────────────────┘
```

---

## 4. End-to-End Execution Flows

### Flow 1: Engine Bootstrap, Crash Recovery & Transition to Live

```
cmd/server/main.go               recovery/replayer.go            market/event_loop.go     matcher/matcher.go        Redis/Postgres
       │                                  │                               │                     │                          │
       │── 1. Connect Postgres & Redis ──>│                               │                     │                          │
       │── 2. Create MarketEngines ──────>│                               │                     │                          │
       │      (All in ModeRecovery)       │                               │                     │                          │
       │                                  │                               │                     │                          │
       │── 3. replayer.ReplayAll(ctx) ───>│                               │                     │                          │
       │      (Blocks main thread)        │── 4. loadCheckpoint() ────────────────────────────────────────────────────────>│ Query Postgres
       │                                  │<── returns savedOffset ────────────────────────────────────────────────────────│ (e.g. offset 500)
       │                                  │                               │                     │                          │
       │                                  │── 5. highWatermark() ─────────────────────────────────────────────────────────>│ Query Kafka Leader
       │                                  │<── returns HWM offset ────────────────────────────────────────────────────────│ (e.g. offset 550)
       │                                  │                               │                     │                          │
       │                                  │── 6. go engine.Run(ctx) ─────>│ (Runs in ModeRecovery)                         │
       │                                  │                               │                     │                          │
       │                                  │── 7. Replay messages 501..549>│                     │                          │
       │                                  │      (routeMessage sent=true) │── processEvent() ──>│                          │
       │                                  │                               │                     │── Match() in Recovery ──>│ (Updates OrderBook,
       │                                  │                               │<── nil Fills ───────│                          │  suppresses fills)
       │                                  │                               │── OutputQueue ─────>│                          │
       │                                  │<── 8. Drain OutputQueue ──────│   (Exact count)     │                          │
       │                                  │                                                     │                          │
       │                                  │── 9. engine.SetLive() ───────>│ (Transitions to ModeLive)                      │
       │<── 10. ReplayAll returns OK ─────│                               │                     │                          │
       │                                                                  │                     │                          │
       │── 12. Start Publishers (1 per engine)                            │                     │                          │
       │── 13. consumer.Start(ctx) ───────────────────────────────────────────────────────────────────────────────────────>│ (Live Kafka reads)
```

---

### Flow 2: Live LIMIT Order Matching & Publication

```
Kafka: orders.submitted       kafka.Consumer             market.MarketEngine         matcher.Match()             publisher.Publisher        Kafka/Redis/Postgres
          │                         │                             │                         │                             │                      │
          │── 1. OrderCreated msg ─>│                             │                         │                             │                      │
          │   (Topic, Part, Offset) │── 2. HandleOrderCreated() ─>│                         │                             │                      │
          │                         │      Validate & Route       │                         │                             │                      │
          │                         │── 3. Send InputEvent ──────>│                         │                             │                      │
          │                         │      to InputQueue          │                         │                             │                      │
          │                                                       │── 4. processEvent() ───>│                             │                      │
          │                                                       │      validTickAndLot()  │                             │                      │
          │                                                       │                         │── 5. matchLimit() ─────────>│                      │
          │                                                       │                         │      • Match crossing asks  │                      │
          │                                                       │                         │      • Deduct quantities    │                      │
          │                                                       │                         │      • Create Fills         │                      │
          │                                                       │                         │      • Rest remainder in book                      │
          │                                                       │<── 6. Return Fills ─────│                             │                      │
          │                                                       │── 7. Put MatchResult ────────────────────────────────>│                      │
          │                                                       │      onto OutputQueue   │                             │                      │
          │                                                       │                         │                             │── 8. publishFills() ─> Kafka (trades.executed)
          │                                                       │                         │                             │── 9. pushDepth() ────> Redis (depth:{market_id})
          │                                                       │                         │                             │── 10. writeCheckpt ──> Postgres (Monotonic UPSERT)
```

---

### Flow 3: Live MARKET Order (IOC) Execution & Leftover Cancellation

```
market.MarketEngine                          matcher.matchMarket()                 orderbook.OrderBook          publisher.Publisher
        │                                              │                                    │                            │
        │── 1. processEvent(EventOrderCreated) ───────>│                                    │                            │
        │      OrderType == MARKET                     │── 2. Sweep opposite side levels ──>│                            │
        │                                              │      (Best Price -> Worst Price)   │                            │
        │                                              │<── 3. Return generated Fills ──────│                            │
        │                                                                                   │                            │
        │── 4. Check node.RemainingQty > 0?                                                 │                            │
        │      YES (Market IOC expired remainder)                                           │                            │
        │      Create CancelResult{Reason: "ioc_expired"}                                   │                            │
        │                                                                                   │                            │
        │── 5. Send MatchResult{Fills, CancelResult, DepthSnapshot, SourcePosition} ────────────────────────────────────>│
        │                                                                                                                │── 6. Publish Fills to Kafka
        │                                                                                                                │── 7. Push Depth to Redis
        │                                                                                                                │── 8. Commit Checkpoint to DB
```

---

### Flow 4: Order Cancellation Flow

```
kafka.Consumer                     market.MarketEngine                   matcher.Cancel()              orderbook.OrderBook       publisher.Publisher
      │                                     │                                    │                             │                          │
      │── 1. OrderCancel msg ──────────────>│                                    │                             │                          │
      │   (OrderID, UserID, MarketID)       │── 2. processEvent() ──────────────>│                             │                          │
      │                                     │      EventOrderCancel              │── 3. book.Cancel(orderID) ─>│                          │
      │                                     │                                    │      Remove from FIFO level │                          │
      │                                     │                                    │      Remove from OrderIndex │                          │
      │                                     │<── 4. Return removed OrderNode ────│<────────────────────────────│                          │
      │                                     │                                                                                             │
      │                                     │── 5. If node != nil: CancelResult{Reason: "user_requested"}                                 │
      │                                     │      If node == nil: CancelResult is nil (Idempotent No-Op)                                 │
      │                                     │                                                                                             │
      │                                     │── 6. Send MatchResult{CancelResult, DepthSnapshot, SourcePosition} ────────────────────────>│
      │                                     │      (Always sent to advance Kafka checkpoint even if cancel was a no-op)                   │── 7. Push Depth to Redis
      │                                     │                                                                                             │── 8. Commit Checkpoint to DB
```

---

### Flow 5: Graceful Two-Context Service Teardown

```
OS Signal (SIGTERM/SIGINT)          cmd/server/main.go                kafka.Consumer              publisher.Publisher       Postgres / Redis
            │                               │                                │                             │                       │
            │── 1. SIGTERM Interrupt ──────>│                                │                             │                       │
            │                               │── 2. opCancel() (Live Context) │                             │                       │
            │                               │                                │                             │                       │
            │                               │── 3. consumer.Close() ────────>│                             │                       │
            │                               │      Stop FetchMessage() loops │                             │                       │
            │                               │      Close Kafka Readers       │                             │                       │
            │                               │                                                              │                       │
            │                               │── 4. wg.Wait() with 15s Deadline ───────────────────────────>│                       │
            │                               │      Publishers finish current in-flight MatchResult         │                       │
            │                               │      and exit on ctx.Done()                                  │                       │
            │                               │<── 5. All Goroutines Done ───────────────────────────────────│                       │
            │                               │                                                                                      │
            │                               │── 6. Run deferred closes ───────────────────────────────────────────────────────────>│ Close Redis Connection
            │                               │                                                                                      │ Close Postgres Pool
            │                               │── 7. Process Exit (Code 0)
```

---

## 5. Comprehensive Edge Case Catalog & Resolution Mechanics

This section lists every tricky concurrency, recovery, and matching edge case encountered in the system, together with the exact function and file responsible for solving it.

| # | Edge Case Category | Specific Scenario | Risk / Failure Mode | Concrete Solution & Responsible Function | File Location |
|---|---|---|---|---|---|
| **1** | **Multi-Engine Checkpoint Regression** | Multiple `MarketEngine` publishers write to the same `(topic, partition)` Postgres row. Fast market (ETH) writes offset 101; slower market (BTC) finishes offset 100 later. | Slower publisher moves offset backwards ($101 \rightarrow 100$), causing duplicate replays upon restart. | Monotonic UPSERT with `WHERE kafka_checkpoints.offset < EXCLUDED.offset`. | [`publisher.go:257`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/publisher/publisher.go#L257) (`writeCheckpoint`) |
| **2** | **Recovery Drain Deadlock** | During recovery, replayer routes messages for all markets. Messages for foreign markets return `nil` channel and are not queued. | If foreign messages are counted in the drain tally, the drain loop waits forever for an `OutputQueue` item that never arrives. | `routeMessage()` captures `sent` boolean inside route closure; only increments `totalEvents` when `sent == true`. | [`replayer.go:210`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go#L210) (`routeMessage`) |
| **3** | **Shutdown Channel Close Panic** | Consumer goroutine is writing to `InputQueue` while shutdown sequence closes the channel. | Go runtime panic: `panic: send on closed channel`. | `InputQueue` is **never closed**. `event_loop.Run(ctx)` uses `select { case <-ctx.Done(): return }` driven by context cancellation. | [`event_loop.go:20`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go#L20) (`Run`) |
| **4** | **Replay Output Pollution** | Replaying 100,000 historical events during startup. | If engines are live, millions of duplicate trade executions and depth snapshots are published downstream. | `ModeRecovery` flag suppresses fill creation inside `matcher.Match()`. Publishers are not started until recovery finishes. | [`matcher.go:42`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L42) (`Match`) & [`replayer.go:94`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go#L94) |
| **5** | **Cross-Topic Replay Causality** | Kafka topics `orders.submitted` and `orders.cancel-requested` are replayed sequentially (submitted first). | Risk that a cancel is processed before its corresponding submit, corrupting state. | Three-layer defence: (1) Domain invariant — Order Service cannot emit a `CancelRequested` event before the `OrderCreated` event for the same `order_id` has been committed and acknowledged. (2) Replay strategy — Replayer always processes `orders.submitted` fully to HWM before starting `orders.cancel-requested`. (3) Defensive matcher — `Cancel()` on an unknown/filled `order_id` returns `nil` (idempotent no-op), never panics. | [`replayer.go:28`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go#L28) (ordering contract) & [`matcher.go:120`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L120) (`Cancel`) |
| **6** | **Market Order (IOC) Partial Fill** | Market buy order arrives for 10 BTC, but ask book only contains 4 BTC. | Leftover 6 BTC rests on book as an invalid Limit order with price 0. | `processEvent()` detects `OrderTypeMarket && RemainingQty > 0` and emits a synthetic `CancelledOrder{Reason: "ioc_expired"}`. | [`event_loop.go:68`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go#L68) (`processEvent`) |
| **7** | **Invalid Order Parameter Injection** | Incoming order has price `$45,123.456` when tick size is `$0.01`, or quantity `0.000005` when lot size is `0.0001`. | Broken price-level indexing or fractional dust precision corruption. | `validTickAndLot()` calculates `Price.Mod(TickSize) == 0` and rejects with `CancelledOrder{Reason: "invalid_order_parameters"}`. | [`event_loop.go:127`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go#L127) (`validTickAndLot`) |
| **8** | **Zero Config Bypass** | Startup config has `TickSize: 0` or `LotSize: 0`. | Validation is silently skipped, allowing corrupt orders through. | `validateMarketConfigs()` enforces `TickSize > 0` and `LotSize > 0` before DB or Kafka connects. | [`main.go:93`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/cmd/server/main.go#L93) (`validateMarketConfigs`) |
| **9** | **Postgres Checkpoint Absence** | Brand new deployment on empty database table. | `Scan()` returns error on missing row, causing startup crash. | `loadCheckpoint()` detects `errors.Is(err, pgx.ErrNoRows)` and returns `-1` (signals replay from offset 0). | [`replayer.go:240`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go#L240) (`loadCheckpoint`) |
| **10** | **Redis Desynchronization After Crash** | Redis container restarts or purges memory during engine crash. | Redis depth key `depth:BTC-USDT` is empty while order book is fully populated. | Live publisher naturally pushes fresh Top-20 depth to Redis on the first live transaction/order event. | [`publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/publisher/publisher.go) |
| **11** | **Missing Market Identification on Fills** | Downstream Trade Service needs to settle trades by market ID. | `Fill` struct missing `MarketID`, causing downstream settlement failure. | `matcher.go` explicitly injects `MarketID: book.MarketID` onto every `Fill` struct. | [`matcher.go:195`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L195) (`matchLimit`) |
| **12** | **At-Least-Once Replay Duplication** | Crash occurs after trade published to Kafka but before Postgres checkpoint committed. | On restart, engine replays that offset and republishes the trade. | Documented architectural invariant: downstream services MUST be idempotent on `TradeID` (UUID). | [`README.md (publisher)`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/publisher/01README.md) |
| **13** | **Redis Outage Causes Duplicate Kafka Trades** | Redis goes down for N minutes during a live market. Publisher writes Kafka trades successfully for every event. | If Redis failure is treated as a blocking error, the Postgres checkpoint never advances. On restart, all events from the pre-outage checkpoint are replayed — emitting duplicate `TradeExecuted` events for the entire outage window. | Redis push failure is **non-blocking**: logs warning, continues to Postgres checkpoint. Redis self-heals on the next successful event. Kafka and Postgres remain in sync throughout the outage. | [`publisher.go:process()`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/publisher/publisher.go#L145) |

---

## 6. Concurrency, Memory & Thread-Safety Model

### Goroutine Allocation Table

In steady-state live operation with 3 registered markets (`BTC-USDT`, `ETH-USDT`, `SOL-USDT`), exactly **8 goroutines** are active:

```
┌───────────────────────────────────────────────┬───────────────┬──────────────────────────────────────────┐
│ Goroutine Identity                            │ Count         │ Lifetime & Responsibility                │
├───────────────────────────────────────────────┼───────────────┼──────────────────────────────────────────┤
│ Consumer: orders.submitted reader             │ 1             │ Lifetime of app; reads Kafka Submit topic│
│ Consumer: orders.cancel-requested reader      │ 1             │ Lifetime of app; reads Kafka Cancel topic│
│ MarketEngine: EventLoop (Run)                 │ 3 (1/market)  │ Spawns in Recovery, runs forever         │
│ Publisher: OutputQueue worker                 │ 3 (1/market)  │ Spawns post-recovery, runs forever       │
│ Main Orchestrator                             │ 1             │ Blocks on OS signal, handles teardown    │
└───────────────────────────────────────────────┴───────────────┴──────────────────────────────────────────┘
```

### Channel Sizing & Backpressure Invariant

- `InputQueue`: Buffered `chan InputEvent` (capacity: **1,000**).
- `OutputQueue`: Buffered `chan MatchResult` (capacity: **1,000**).

**Backpressure Behavior:**  
If Kafka ingestion bursts faster than the Event Loop can match orders, `InputQueue` absorbs up to 1,000 events. If full, the Consumer's `FetchMessage()` goroutine blocks, exerting natural TCP backpressure back onto the Kafka broker partition.

---

### Cross-Topic Ordering Invariant

The matching engine consumes two independent Kafka topics:

```
 orders.submitted          orders.cancel-requested
 ────────────────          ───────────────────────
  [ORDER_A created]          [ORDER_A cancel req]
  [ORDER_B created]          [ORDER_C cancel req]
        ...                        ...
```

Kafka guarantees **within-partition ordering** but provides **no global ordering across two topics**. This is the most commonly questioned aspect of this architecture. The correctness relies on three independent layers of defence:

| Layer | Guarantee | Where Enforced |
| :--- | :--- | :--- |
| **1. Domain Invariant** | The Order Service cannot publish a `CancelRequested` event to Kafka for an `order_id` until the `OrderCreated` event for that same `order_id` has been committed and fully acknowledged by Kafka. This is a **producer-side causal guarantee**, not a Kafka ordering guarantee. | Order Service (upstream producer) |
| **2. Replay Strategy** | During crash recovery, `Replayer.replayTopic()` always drains `orders.submitted` to its high-water mark **first**, then starts `orders.cancel-requested`. This means the in-memory order book always contains the submitted order before its cancel is attempted during replay. | [`replayer.go:95`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go#L95) (`replayEngine`) |
| **3. Defensive Matcher** | `matcher.Cancel()` is idempotent by design. A cancel for an `order_id` that does not exist in the order book — whether it was already filled, already cancelled, or simply not yet received — returns `nil` without panicking or corrupting state. | [`matcher.go:120`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L120) (`Cancel`) |

> **In live mode**, the same two-goroutine consumer model applies — `orders.submitted` and `orders.cancel-requested` are consumed concurrently with no global ordering guarantee. The same three-layer defence applies: domain invariant prevents the cancel from arriving before the submit at the Kafka broker level, and the defensive matcher handles any race.

> **V2 Note:** If the domain invariant were ever violated (e.g., a bug in Order Service), the defensive matcher prevents corruption but the cancel is silently dropped. A V2 improvement would be to add a dead-letter queue or explicit re-routing for cancelled orders whose `order_id` is not found.

---

### Checkpoint Semantic Invariant

This is the most subtle aspect of the checkpoint model and must be understood precisely to reason about recovery correctness.

**What the checkpoint represents:**
```
kafka_checkpoints(topic, partition) → highest offset such that:
  - The MatchResult has been published to Kafka trades.executed (if fills existed)
  - The depth snapshot has been pushed to Redis (or failure logged)
  - This row has been committed to Postgres

It is NOT the offset of the last event received by a specific MarketEngine.
It is NOT per-market. It is per (topic, partition).
```

**Why this matters with multiple markets sharing one partition:**

With V1's single-partition topology, all markets share `(orders.submitted, partition=0)`. This means:

- `BTC-USDT` publisher processes offset 100 → writes checkpoint 100
- `ETH-USDT` publisher processes offset 101 → writes checkpoint 101  
- `BTC-USDT` publisher processes offset 102 → writes checkpoint 102
- `ETH-USDT` publisher (slow) tries to write checkpoint 99 → **monotonic guard rejects it**

The checkpoint `= 102` means: *"all events at or before offset 102 on this topic/partition have been successfully processed by at least one market engine and durably recorded."*

**On restart:**  
Recovery reads checkpoint `= 102` and replays from offset `103`. Events 99–102 are NOT replayed, even though some were processed by ETH engine and some by BTC engine. This is correct because the checkpoint is a **consumer-group progress marker** — it tracks the position of the entire group, not any individual market.

**The monotonic UPSERT is the critical safety mechanism:**
```sql
INSERT INTO kafka_checkpoints (topic, partition, offset, updated_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (topic, partition)
DO UPDATE SET
    offset = EXCLUDED.offset,
    updated_at = NOW()
WHERE kafka_checkpoints.offset < EXCLUDED.offset;  -- ← never regress
```
This ensures that a slow publisher finishing an earlier offset cannot overwrite a faster publisher's higher offset. The checkpoint is **monotonically non-decreasing**, which is the invariant that makes the recovery guarantee correct.

## 7. Data Contracts & Wire Formats

### 1. Ingestion: `orders.submitted`
```json
{
  "order_id": "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
  "user_id": "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
  "market_id": "BTC-USDT",
  "side": "BUY",
  "order_type": "LIMIT",
  "price": "65400.50",
  "quantity": "0.25000"
}
```

### 2. Ingestion: `orders.cancel-requested`
```json
{
  "order_id": "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
  "user_id": "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
  "market_id": "BTC-USDT"
}
```

### 3. Egress: Kafka `trades.executed`
```json
{
  "trade_id": "e3c7a72d-1144-4df6-857c-17937b2d5619",
  "market_id": "BTC-USDT",
  "maker_order_id": "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
  "taker_order_id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "buyer_user_id": "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
  "seller_user_id": "b1ffcd88-8d1a-4fe7-aa5c-5aa8ac270b22",
  "price": "65400.50",
  "quantity": "0.15000",
  "executed_at": "2026-08-18T10:30:00.123456789Z"
}
```

### 4. Egress: Redis `depth:{market_id}`

> **Durability Note:** Redis is a **non-critical projection/cache**, not a durable event boundary.
> A Redis write failure logs a warning and processing continues — the Postgres checkpoint still advances.
> The next successful event always overwrites the stale snapshot. Redis is never the source of truth.

```json
{
  "market_id": "BTC-USDT",
  "bids": [
    {"price": "65400.50", "quantity": "1.25000"},
    {"price": "65400.00", "quantity": "3.50000"}
  ],
  "asks": [
    {"price": "65401.00", "quantity": "0.80000"},
    {"price": "65402.50", "quantity": "2.10000"}
  ],
  "snapshot_at": "2026-08-18T10:30:00.123456789Z"
}
```

### 5. Durability: PostgreSQL `kafka_checkpoints`
```sql
CREATE TABLE IF NOT EXISTS kafka_checkpoints (
    topic      VARCHAR(255) NOT NULL,
    partition  INTEGER      NOT NULL,
    offset     BIGINT       NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (topic, partition)
);
```

---

## 8. Verification & Test Suite Summary

The system is guarded by **61 comprehensive automated unit and regression tests**:

| Subsystem | Test File | Test Count | Key Areas Covered |
| :--- | :--- | :---: | :--- |
| **Matcher** | `matcher_test.go` | **23** | Price-time priority, FIFO queue, partial fills, full fills, market order IOC sweeps, cancel order, invalid tick rejection |
| **Kafka Ingestion** | `consumer_test.go` | **12** | Payload unmarshalling, UUID parsing, decimal precision, routing by market ID, corrupt message handling |
| **Publisher** | `publisher_test.go` | **13** | Monotonic checkpoint regression guard, Redis non-blocking failure, pipeline failure propagation, sequential execution |
| **Projection** | `reader_test.go` | **13** | Empty-book semantics, negative price/quantity validation, missing Redis key, stale snapshot detection, malformed JSON |

**Run full test suite command:**
```powershell
go test ./internal/... -v
```

---

## 9. V2 Architectural Evolution Roadmap

1. **Multi-Partition Scale-Out**:
   - Upgrade `recovery.Replayer` to query `kafkago.LookupPartitions()` and replay partitions $0 \dots N$ concurrently.
2. **Dynamic Market Configuration**:
   - Replace hardcoded `marketConfigs()` in `main.go` with HTTP/gRPC discovery from the Market Management Service.
3. **Periodic Snapshot & WAL Compaction**:
   - Dump periodic in-memory B-Tree snapshots to MinIO/S3 every $N$ blocks so recovery can bootstrap from snapshot $+ \Delta$ Kafka offsets instead of replaying from genesis.
4. **Parallel Engine Recovery**:
   - Execute `replayEngine()` concurrently using `errgroup.Group` across all independent trading pairs at startup.
5. **Real-Time Projection Engine**:
   - Add specialized low-latency projection workers to feed live candle aggregations and WebSocket broadcast clusters.
