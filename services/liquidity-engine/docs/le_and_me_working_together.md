# Engineering the Core: How the Liquidity Engine (LE) & Matching Engine (ME) Work Together Reliably

Building a high-throughput, low-latency crypto exchange requires solving two fundamentally different engineering problems:
1. **The Matching Engine (ME):** A mathematically pure, deterministic, single-threaded in-memory execution core.
2. **The Liquidity Engine (LE):** An asynchronous distributed state coordinator and automated market maker.

Getting these two engines to work together across Kafka, PostgreSQL, Redis, and gRPC without race conditions, dropped fills, or phantom orders is the cornerstone of TradeDrift's architecture.

---

## 1. High-Level System Architecture

```text
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                                 TradeDrift Core Loop                                   │
│                                                                                        │
│   ┌──────────────────────────┐   orders.commands (Kafka)   ┌───────────────────────┐   │
│   │                          │ ──────────────────────────> │                       │   │
│   │     Liquidity Engine     │                             │    Matching Engine    │   │
│   │                          │ <────────────────────────── │                       │   │
│   └──────────────────────────┘   trades.executed (Kafka)   └───────────────────────┘   │
│         │               ▲                                       │             │        │
│         │ gRPC          │ GET /status (HTTP)                    │ Redis Depth │ SQL    │
│         ▼               │                                       ▼             ▼        │
│   ┌───────────┐   ┌───────────────┐                        ┌─────────┐  ┌───────────┐  │
│   │   Order   │   │ Direct Health │                        │  Redis  │  │ PostgreSQL│  │
│   │  Service  │   │     Probe     │                        │  Cache  │  │ Checkpoint│  │
│   └───────────┘   └───────────────┘                        └─────────┘  └───────────┘  │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. The Matching Engine: Computational & Deterministic Correctness

The Matching Engine is the hardest part from a **correctness and determinism** perspective. It is designed around the following core principles:

### A. Deterministic State & Event Sourcing
- The order book is not backed by traditional database transactions during execution; SQL locks are far too slow.
- The engine runs as an event-sourced state machine per market partition.
- **Guarantee:** Replaying the exact same Kafka message stream from offset $0$ to offset $N$ will **always produce the exact same order book depth, sequence numbers, and trade executions**.

### B. Price-Time Priority (FIFO) & Partial Fills
- Price levels are arranged in an ordered price ladder (B-Tree / Red-Black Tree).
- Within each price level, orders are stored in strict FIFO queues.
- When an aggressive order arrives, it matches against resting orders at the best available price. If the order size exceeds resting quantity, the resting order is filled, removed, and matching cascades to the next order in line.

### C. Fixed-Point Decimal Precision
- Standard IEEE-754 floating-point arithmetic introduces rounding errors (e.g., `0.1 + 0.2 = 0.30000000000000004`).
- In financial exchanges, even a $10^{-8}$ rounding error violates accounting balance invariants.
- The Matching Engine uses arbitrary-precision fixed-point math (`shopspring/decimal`) for all prices, quantities, and lot-size truncations.

### D. Snapshotting & Kafka Replay Recovery
- Periodically (or on clean shutdown), the Matching Engine takes a serialized snapshot of the order book and saves it to PostgreSQL (`market_snapshots`) with its exact Kafka offset.
- On startup/crash recovery:
  1. Loads the latest snapshot from PostgreSQL at offset $S$.
  2. Finds the Kafka High Water Mark (HWM) at offset $H$.
  3. Enters `ModeRecovery`: replays events from offset $S \to H$ with side-effects (external trade emissions) **suppressed**.
  4. Pushes fresh order book depth to Redis (`depth:{market_id}`).
  5. Transitions to `ModeLive` and starts accepting live orders.

---

## 3. The Liquidity Engine: Distributed State Coordination

While the Matching Engine is mathematically deterministic, the Liquidity Engine faces a different challenge: **distributed state coordination across multiple asynchronous boundaries**.

The LE continuously reconciles:
$$\text{Order Service DB} \iff \text{Matching Engine} \iff \text{In-Flight Local Tracker}$$

```text
               ┌───────────────────────────┐
               │    Desired Price Ladder   │
               │   (12 Bids / 12 Asks)     │
               └─────────────┬─────────────┘
                             │
                             ▼
               ┌───────────────────────────┐
               │        order.Diff()       │
               │   (Compares Desired vs    │
               │      Tracker Known)       │
               └─────────────┬─────────────┘
                             │
            ┌────────────────┼────────────────┐
            ▼                ▼                ▼
       DiffCreate       DiffCorrect       DiffCancel
            │                │                │
            │          Cancel Old +           │
            │        Queue Replacement        │
            │                │                │
            ▼                ▼                ▼
     1. OS Register    1. ME Cancel     1. ME Cancel
     2. SetPending     2. Confirm Wait  2. SetCancelling
     3. Kafka Pub      3. OS Register
                       4. Kafka Pub
```

### Core Responsibilities of the Liquidity Engine:
1. **Dynamic Price Laddering:** Computes geometric spreads around market reference prices (`pricing.GenerateLadder`) to supply continuous two-sided liquidity (72 total orders across BTC, ETH, and SOL).
2. **Crash-Safe 3-Step Creation:** Registers orders in the Order Service before publishing to Kafka. If the engine crashes midway, the order is recovered dynamically on restart via `ListMMOrders()`.
3. **Monotonic Generation Tracking:** Uses deterministic client order IDs (`MM-BTC-USDT-ASK-01-G007`) so price updates increment to `G008` without resetting to `G001`.
4. **Inventory Projection & Skew:** Projects available capital after deducting in-flight committed orders, automatically trimming ladder depth when inventory drops into `Low` or `Critical` tiers.

---

## 4. How the Two Engines Communicate

To achieve high throughput without compromising data integrity, the LE and ME interact through four specialized protocols:

| Interaction Channel | Protocol / Medium | Purpose | Critical Guarantees |
| :--- | :--- | :--- | :--- |
| **Command Ingress** | Kafka `orders.commands` | LE publishes `OrderCreated` and `OrderCancelRequested` | Market partition keying, deterministic ordering. |
| **Fill Notifications** | Kafka `trades.executed` | ME publishes trade execution fills back to LE | Synchronous `TradeEnvelope` Ack + ring buffer deduplication. |
| **Health Handshake** | HTTP `/status` | LE probes ME health every 5s | Direct probe independent of trade volume; 3-failure threshold. |
| **Recovery Ingress** | gRPC `OrderService` | LE queries persistent state on startup | Reconstructs in-memory tracker without maintaining an LE database. |

---

## 5. End-to-End Execution Flow (Lifecycle of a Trade)

Here is what happens when an external user (taker) matches against an MM order placed by the Liquidity Engine:

```text
1. [LE] Places MM Ask at $96,450 (MM-BTC-USDT-ASK-01-G001) via orders.commands.
   ↓
2. [ME] Ingests command, validates tick/lot size, and rests order in live book.
   ↓
3. [Taker] Submits a Market BUY order for 0.05 BTC.
   ↓
4. [ME] Matches Taker against MM Ask:
   • Deducts 0.05 BTC from resting MM order.
   • Generates Trade ID (e.g., T-90812).
   • Emits TradeExecuted event to trades.executed.
   • Updates Redis depth cache.
   ↓
5. [LE Consumer] Reads TradeExecuted event:
   • Wraps event in TradeEnvelope{Event, AckChan}.
   • Forwards envelope to the single-threaded Engine Event Loop.
   ↓
6. [LE Event Loop] Processes Fill:
   • Deduplicates TradeID.
   • Deducts 0.05 BTC from projected base balance.
   • Adds (0.05 × 96450) USDT to projected quote balance.
   • Updates remaining quantity in local tracker.
   • Closes AckChan (Kafka consumer commits offset).
   • Debounces and triggers targeted reconcile to restore filled liquidity.
```

---

## 6. Summary: Why This Architecture Works

1. **Separation of Concerns:** The Matching Engine focuses 100% on **deterministic execution and raw throughput**, while the Liquidity Engine handles **market risk, price laddering, and distributed state recovery**.
2. **Zero-Lock Concurrency:** The Matching Engine uses single-partition event loops, and the Liquidity Engine uses a single-goroutine event loop with atomic snapshot publishing—**eliminating mutex deadlocks entirely**.
3. **Resilience to Crashes:** If the Matching Engine crashes, it replays from PostgreSQL snapshots and Kafka offsets. If the Liquidity Engine crashes, it reconstructs its entire state from the Order Service and resumes quoting seamlessly.
