# 🧠 TradeDrift Matching Engine Concepts & Architecture Guide

A comprehensive, deep-dive reference manual explaining the core financial concepts, algorithmic mechanics, and low-level architectural patterns powering high-performance matching engines and the **TradeDrift Matching Engine**.

---

## 📑 Table of Contents

1. [Executive Summary & Concept Map](#1-executive-summary--concept-map)
2. [Core Concept 1: The Order Book](#2-core-concept-1-the-order-book)
3. [Core Concept 2: Price-Time Priority](#3-core-concept-2-price-time-priority)
4. [Core Concept 3: FIFO (First-In, First-Out)](#4-core-concept-3-fifo-first-in-first-out)
5. [Core Concept 4: Partial Fills](#5-core-concept-4-partial-fills)
6. [Core Concept 5: Market Orders](#6-core-concept-5-market-orders)
7. [Core Concept 6: Limit Orders](#7-core-concept-6-limit-orders)
8. [Core Concept 7: IOC (Immediate-Or-Cancel)](#8-core-concept-7-ioc-immediate-or-cancel)
9. [Core Concept 8: GTC (Good-Til-Cancelled)](#9-core-concept-8-gtc-good-til-cancelled)
10. [Core Concept 9: Single-Threaded Matching](#10-core-concept-9-single-threaded-matching)
11. [Deep Dive: Why Single Goroutine per Symbol?](#11-deep-dive-why-single-goroutine-per-symbol)
12. [Deep Dive: Why Deterministic Execution is Critical?](#12-deep-dive-why-deterministic-execution-is-critical)
13. [Deep Dive: Data Structures & Why B-Trees / Trees are Useful](#13-deep-dive-data-structures--why-b-trees--trees-are-useful)
14. [Deep Dive: How End-to-End Ordering Guarantees are Maintained](#14-deep-dive-how-end-to-end-ordering-guarantees-are-maintained)
15. [Codebase Cross-Reference Map](#15-codebase-cross-reference-map)

---

## 1. Executive Summary & Concept Map

A **Matching Engine (ME)** is the computational core of a financial exchange (e.g., NASDAQ, NYSE, CME, Binance, Coinbase). Its sole responsibility is to maintain an electronic ledger of resting trading intentions (the **Order Book**) and execute buy and sell orders deterministically at sub-millisecond latencies.

```
                           Incoming Trading Events
                  (Kafka Topic: orders.created, Partitioned by Market)
                                     │
                                     ▼
                  ┌─────────────────────────────────────┐
                  │      Kafka Consumer Goroutine       │
                  └──────────────────┬──────────────────┘
                                     │ (InputQueue Channel)
                                     ▼
        ┌─────────────────────────────────────────────────────────────┐
        │            MarketEngine Event Loop Goroutine                │
        │           (Single-Threaded per Trading Pair)                │
        │                                                             │
        │  ┌───────────────────────────────────────────────────────┐  │
        │  │                Deterministic Matcher                  │  │
        │  │  • Price-Time Priority (FIFO)                         │  │
        │  │  • Limit Orders (GTC) / Market Orders (IOC)           │  │
        │  │  • In-place Partial & Full Fills                      │  │
        │  └──────────────────────────┬────────────────────────────┘  │
        │                             │ Mutates in-memory             │
        │                             ▼                               │
        │  ┌───────────────────────────────────────────────────────┐  │
        │  │                  In-Memory OrderBook                  │  │
        │  │  • Bids Side (Sorted Descending)                      │  │
        │  │  • Asks Side (Sorted Ascending)                       │  │
        │  │  • PriceLevels: FIFO Linked Lists (container/list)    │  │
        │  │  • OrderIndex: O(1) UUID -> OrderNode Map             │  │
        │  └───────────────────────────────────────────────────────┘  │
        └─────────────────────────────┬───────────────────────────────┘
                                      │ (OutputQueue Channel)
                                      ▼
                  ┌─────────────────────────────────────┐
                  │         Publisher Goroutine         │
                  │   • trades.executed -> Kafka        │
                  │   • depth:{market_id} -> Redis      │
                  │   • kafka_checkpoints -> Postgres   │
                  └─────────────────────────────────────┘
```

---

## 2. Core Concept 1: The Order Book

### What is it?
An **Order Book** is a real-time, in-memory continuous ledger recording all unexecuted buy intentions (**Bids**) and sell intentions (**Asks**) for a specific financial asset pair (e.g., `BTC-USDT`).

```
                    ASK SIDE (Sellers) - Ascending Price
           Price        Quantity       Total Quantity      Orders
          $60,003        1.50 BTC         1.50 BTC        [O_8, O_9]
          $60,002        3.20 BTC         4.70 BTC        [O_7]
          $60,001        0.80 BTC         5.50 BTC        [O_6] ◄── Best Ask
   ───────────────────────────────────────────────────────────────
                     SPREAD = $60,001 - $60,000 = $1.00
   ───────────────────────────────────────────────────────────────
          $60,000        2.10 BTC         2.10 BTC        [O_1, O_2] ◄── Best Bid
          $59,999        4.50 BTC         6.60 BTC        [O_3, O_4]
          $59,998        5.00 BTC        11.60 BTC        [O_5]
                    BID SIDE (Buyers) - Descending Price
```

### Key Components:
1. **Bids (Buy Side):** Traders willing to buy at or below specified prices. Sorted in **descending** order (highest price at the top).
2. **Asks (Sell Side):** Traders willing to sell at or above specified prices. Sorted in **ascending** order (lowest price at the top).
3. **Best Bid / Best Ask (Top of Book / BBO):** The highest bid price and the lowest ask price currently available.
4. **Spread:** The difference between Best Ask and Best Bid (`Spread = BestAsk - BestBid`). If `Spread <= 0`, a match condition exists.
5. **Market Depth (Level 2 / L2):** Aggregated total quantity available at each distinct price point.
6. **Order Queue (Level 3 / L3):** The individual order entities waiting at each price level.

### Why & Where it is used:
* **Where:** Centralized exchanges (TradFi & Crypto), electronic communication networks (ECNs), and automated market makers with off-chain books.
* **Why:** In-memory order books allow ultra-low latency price discovery, zero-disk-IO matching, and instantaneous liquidity evaluation.

---

## 3. Core Concept 2: Price-Time Priority

### What is it?
**Price-Time Priority** (also called Price/Time or FIFO allocation) is the foundational rule that dictates the exact order in which resting orders are chosen to be filled against an incoming aggressive order.

```
                          EVALUATION HIERARCHY

                      1. PRICE PRIORITY (Primary)
                 "Best price ALWAYS matches first"
                 • Buy Side: Highest Price Wins
                 • Sell Side: Lowest Price Wins
                                │
                                ▼
                       (If Prices are Equal)
                                │
                                ▼
                      2. TIME PRIORITY (Secondary)
                     "Oldest order matches first"
                 • First In, First Out (FIFO) Queue
                 • Timestamp assigned on ME arrival
```

### How it works:
1. **Price Priority (Primary):**
   * An incoming buyer offering $101 will always be matched before a buyer offering $100.
   * An incoming seller offering $99 will always be matched before a seller offering $100.
2. **Time Priority (Secondary / Tie-Breaker):**
   * If two traders both offer to sell at $100, the trader whose order arrived at `10:00:01` is matched before the trader whose order arrived at `10:00:02`.

### Why & Where it is used:
* **Where:** Used by NASDAQ, NYSE, CME, Binance, OKX, Coinbase, and TradeDrift.
* **Why:** 
  * **Price Priority** incentivizes aggressive pricing, narrowing the bid-ask spread for all participants.
  * **Time Priority** rewards liquidity providers who take the risk of posting quotes early and eliminates ambiguity in matching.

---

## 4. Core Concept 3: FIFO (First-In, First-Out)

### What is it?
**FIFO** is the concrete data structure mechanism used at each individual price level to enforce Time Priority. All orders at the exact same price are organized in a **Doubly Linked List** (Queue).

```
 Price Level: $60,000
 ┌────────────────────────────────────────────────────────────────────────┐
 │ Head (Front)                                                Tail (Back)│
 │                                                                        │
 │ ┌──────────────┐     ┌──────────────┐     ┌──────────────┐             │
 │ │   Order A    │◄───►│   Order B    │◄───►│   Order C    │             │
 │ │ Arrival: .001│     │ Arrival: .002│     │ Arrival: .003│             │
 │ │ Qty: 5 BTC   │     │ Qty: 3 BTC   │     │ Qty: 10 BTC  │             │
 │ └──────┬───────┘     └──────────────┘     └──────┬───────┘             │
 └────────┼─────────────────────────────────────────┼─────────────────────┘
          ▲                                         ▲
          │ Matches FIRST                           │ Appended LAST
   (level.Orders.Front())                     (level.Orders.PushBack())
```

### Mechanical Rules:
1. **New resting order arrives:** Appended to the tail via `list.PushBack()`.
2. **Aggressive order matches:** Peeks the front of the list via `list.Front()`.
3. **Order cancellation / full fill:** Removed directly via back-pointer `list.Remove(node.Element)` in **$O(1)$** time.

### Why & Where it is used:
* **Where:** Inside every `PriceLevel` struct in the matching engine.
* **Why:** Doubly linked lists provide **$O(1)$ push-to-back**, **$O(1)$ peek-front**, and **$O(1)$ removal from anywhere in the queue** using node pointers.

---

## 5. Core Concept 4: Partial Fills

### What is it?
A **Partial Fill** occurs when an order matches against opposing liquidity whose available volume is smaller than the order's remaining quantity.

### The 3 Execution Scenarios:

```
Scenario 1: Maker Partial Fill (Taker fully filled)
Resting Maker: SELL 10 @ $100   ──► Subtracted in-place ──► Resting Maker: SELL 6 @ $100 (Still at front!)
Incoming Taker: BUY 4 @ $100    ──► Fully executed      ──► Completed (0 remaining)

Scenario 2: Taker Partial Fill across Multiple Levels (Book Sweep)
Incoming Taker: BUY 10 @ $102
Level 1 ($100): 3 available     ──► Matched (Fill 1: 3 @ $100) ──► Level 1 deleted
Level 2 ($101): 4 available     ──► Matched (Fill 2: 4 @ $101) ──► Level 2 deleted
Level 3 ($102): 10 available    ──► Matched (Fill 3: 3 @ $102) ──► Level 3 decremented to 7

Scenario 3: Taker Partial Fill + Remainder Resting (Limit Order)
Incoming Limit: BUY 10 @ $100
Available Asks: 4 @ $100        ──► Matched (Fill: 4 @ $100)
Leftover (6):                   ──► Inserted into Bid side @ $100 (Rests at back of $100 queue)
```

### The Invariant: In-Place Mutation
```go
// node.RemainingQty is mutated in-place.
// The node NEVER leaves its position in the linked list.
node.RemainingQty = node.RemainingQty.Sub(filledQty)
level.TotalQty = level.TotalQty.Sub(filledQty)
```
> **CRITICAL RULE:** A resting order that is partially filled **must never be removed and re-inserted**. Re-inserting would place it at the back of the queue, stripping the trader of their earned time priority.

---

## 6. Core Concept 5: Market Orders

### What is it?
A **Market Order** is an unpriced order instructing the engine to execute immediately against the best available prices on the opposite side of the book, regardless of price, until filled or liquidity is exhausted.

```
                              Market Order Flow
                           [Incoming Market Buy 5 BTC]
                                       │
                                       ▼
                       Check Opposite Side (Asks) Empty?
                         ├── YES ──► Reject / Expire Remainder
                         └── NO
                                       │
                                       ▼
                       Match Against Asks (Top-to-Bottom)
                                 Level 1 ($60,000)
                                 Level 2 ($60,001)
                                       │
                                       ▼
                    Is any quantity leftover after sweep?
                         ├── NO  ──► Fully Filled
                         └── YES ──► CANCEL Remaining (IOC Expired)
                                     (NEVER rest in order book)
```

### Key Characteristics:
* **Taker Only:** Market orders remove liquidity; they never provide liquidity.
* **Immediate Execution:** They execute instantly upon ingestion.
* **Slippage Risk:** In a thin market, a large market order will sweep across multiple price levels, resulting in an average execution price worse than the top-of-book quote.
* **No Resting:** Market orders never sit in the order book. Any unfilled portion is automatically expired under IOC rules.

---

## 7. Core Concept 6: Limit Orders

### What is it?
A **Limit Order** is an order to buy or sell a specified quantity at a **specified price or better**.
* **Limit Buy:** Executes at $\le \text{LimitPrice}$.
* **Limit Sell:** Executes at $\ge \text{LimitPrice}$.

```
                             Limit Order Execution
                         [Incoming Limit Buy 10 @ $100]
                                       │
                                       ▼
                    Opposing Asks available with Price <= $100?
                         │
        ┌────────────────┴────────────────┐
        ▼ YES                             ▼ NO
  Match immediately                 Cannot match immediately
  as TAKER (Fills generated)        Rests in book as MAKER
        │                                 │
        ▼                                 ▼
  Is RemainingQty > 0?              Insert into Bids @ $100
        ├── NO  ──► Complete        (Appended to FIFO queue tail)
        └── YES ──► Rest remainder
```

### Key Characteristics:
* **Price Protection:** Guaranteed price ceiling for buyers and price floor for sellers.
* **Can be Maker or Taker:**
  * **Taker:** If the price crosses the current opposite book immediately upon arrival.
  * **Maker:** If the price does not cross immediately, it rests in the book and provides liquidity.
* **Default Time-In-Force:** Generally defaults to **GTC** (Good-Til-Cancelled).

---

## 8. Core Concept 7: IOC (Immediate-Or-Cancel)

### What is it?
**Immediate-Or-Cancel (IOC)** is a Time-In-Force (TIF) instruction requiring that all or part of an order be executed **immediately** upon arrival in the engine. Any portion of the order that cannot be filled immediately is **instantly cancelled and never enters the resting order book**.

```
 Example: Limit IOC Order -> BUY 10 BTC @ $60,000 (IOC)
 Order Book Ask Depth: 4 BTC @ $60,000 available.

 Execution:
   1. Match 4 BTC @ $60,000 (Fill generated).
   2. Remaining 6 BTC cannot match immediately at <= $60,000.
   3. Engine generates OrderCancelled { RemainingQty: 6, Reason: "ioc_expired" }.
   4. ZERO quantity rests in the Order Book.
```

### Where & Why it is used:
* **Where:** Market orders in modern crypto/FX engines (including TradeDrift) are implemented under the hood as IOC orders. High-Frequency Trading (HFT) firms also use Limit IOC orders to ping liquidity without leaving resting orders exposed.
* **Why:** Guarantees that the trader is never left with unintended resting exposure if market conditions move.

---

## 9. Core Concept 8: GTC (Good-Til-Cancelled)

### What is it?
**Good-Til-Cancelled (GTC)** is the standard Time-In-Force instruction for limit orders. An order with GTC remains active and resting in the order book indefinitely until:
1. It is completely filled by incoming opposing taker orders.
2. The user explicitly requests a cancellation.
3. The market/instrument is halted or delisted.

### Order Cancellation via `OrderIndex` ($O(1)$)
To support GTC cancellations efficiently among millions of resting orders, the engine maintains an `OrderIndex` map (`map[uuid.UUID]*OrderNode`).

```
 [Cancel Request for OrderID: 550e8400-e29b-41d4-a716-446655440000]
                               │
                               ▼
            Look up OrderNode in book.OrderIndex -> O(1)
                               │
                               ▼
     Retrieve back-pointer: node.Element (*list.Element)
                               │
                               ▼
            level.Orders.Remove(node.Element) -> O(1)
            level.TotalQty -= node.RemainingQty
            delete(book.OrderIndex, OrderID)
```

---

## 10. Core Concept 9: Single-Threaded Matching

### What is it?
**Single-Threaded Matching** is an architectural pattern (popularized by the **LMAX Disruptor** and **Actor Model**) where the matching core of a financial market is executed sequentially on **exactly one dedicated thread/goroutine**, without using locks, mutexes, or atomic synchronization inside the matching loop.

```
       TRADITIONAL (Multi-Threaded + Mutex)             ACTOR / DISRUPTOR (Single-Threaded Loop)
┌───────────────────────────────────────────────┐     ┌───────────────────────────────────────────────┐
│ Thread 1 ──► [Lock Mutex] ──► Mutates Book    │     │ Thread 1 (Producer) ──┐                       │
│ Thread 2 ──► [STALLS...]  ──► Waiting...      │     │ Thread 2 (Producer) ──┼──► [ Lock-Free Queue] │
│ Thread 3 ──► [STALLS...]  ──► Waiting...      │     │ Thread 3 (Producer) ──┘           │           │
│                                               │     │                                   ▼           │
│ Problems: Cache bouncing, Context switching,  │     │                       Single Event Loop       │
│ lock contention, non-deterministic race bugs. │     │                       (Zero locks, Max speed) │
└───────────────────────────────────────────────┘     └───────────────────────────────────────────────┘
```

---

## 11. Deep Dive: Why Single Goroutine per Symbol?

In TradeDrift, each active market (`BTC-USDT`, `ETH-USDT`, `SOL-USDT`) is assigned **its own dedicated single goroutine** running `MarketEngine.Run()`.

### 1. Zero Lock Contention
In a multi-threaded matching engine with mutexes (`sync.RWMutex`), every incoming order must acquire a lock on the order book. Under high throughput (e.g., 100,000 orders/sec):
* CPU threads spend up to **70% of their cycles waiting on kernel futexes / spinlocks**.
* A single-threaded event loop processes orders sequentially from a buffered memory channel, executing each match in **under 500 nanoseconds** without a single lock.

### 2. Elimination of Cache-Line Bouncing
In multi-core processors, when multiple CPU cores attempt to write to the same memory addresses (the Order Book), the CPU cache coherency protocol (MESI) repeatedly invalidates L1/L2 caches across cores.
* Single-threaded execution binds the hot memory structures to the CPU cache of the executing core, maintaining maximum memory prefetch efficiency.

### 3. Independence Across Trading Pairs
Orders for `BTC-USDT` have zero operational overlap with `ETH-USDT`. By isolating each symbol to its own goroutine:
* `BTC-USDT` and `ETH-USDT` execute in true parallel across distinct physical CPU cores.
* A sudden surge in meme-coin volume cannot block or degrade the latency of Bitcoin trading.

---

## 12. Deep Dive: Why Deterministic Execution is Critical?

### What is Determinism?
A matching engine is **deterministic** if:
$$\text{State}_{T+1} = f(\text{State}_T, \text{Event})$$
Given the exact same initial state and the exact same ordered sequence of input events, the engine will **always produce the exact same final order book state and identical trade executions**, regardless of when, where, or how many times it is run.

### Why It Matters:

#### 1. Zero-Data-Loss Crash Recovery via Event Replay
If the matching engine process crashes or its server loses power:
1. The engine reads the last committed checkpoint offset from PostgreSQL (e.g., offset `1,000,000`).
2. It fetches historical Kafka messages from offset `1,000,001` up to the current High-Water Mark (`1,005,000`).
3. It replays those 5,000 events through the `Matcher` in `ModeRecovery`.
4. Because the algorithm is 100% deterministic, the in-memory order book is reconstructed to the exact microsecond state it had prior to the crash.

#### 2. Auditability & Regulatory Compliance
In regulated financial markets, exchanges must be able to prove why Trader A was filled instead of Trader B at `10:00:00.000152`. Deterministic logs provide verifiable mathematical proof of fair execution.

#### 3. Eliminating Non-Deterministic Flaws
In multi-threaded locking engines, thread scheduling by the OS kernel introduces non-deterministic execution order. If two orders arrive simultaneously, which one gets the lock first depends on OS thread slicing. A single-threaded sequencer eliminates this ambiguity completely.

---

## 13. Deep Dive: Data Structures & Why B-Trees / Trees are Useful

### Comparative Evaluation of Order Book Indexing

| Data Structure | Best Price Lookup | Price Insert / Delete | Memory Layout / CPU Cache | Implementation Complexity |
| :--- | :--- | :--- | :--- | :--- |
| **Sorted Array / Slice (TradeDrift)** | **$O(1)$** (`[0]`) | $O(N)$ (Shift memory) | **Maximum** (Contiguous cache lines) | Very Simple |
| **Red-Black / AVL Tree** | $O(1)$ (Cached min/max) | $O(\log N)$ | **Poor** (Scattered heap node pointers) | Moderate |
| **B-Tree / B+Tree** | $O(1)$ (Cached leaf) | $O(\log N)$ | **Excellent** (Nodes packed in cache lines) | High |
| **Radix Tree / Price Bucket Array** | **$O(1)$** | **$O(1)$** | **Good** (Fixed price index table) | Complex (Memory bound) |

```
                                  B-Tree Node Memory Layout
               ┌─────────────────────────────────────────────────────────────┐
               │ Keys:   [ $59,998 | $59,999 | $60,000 | $60,001 | $60,002 ] │ ◄── Fits in 1 Cache Line (64B)
               │ Pointers: [ Ptr0  |  Ptr1   |  Ptr2   |  Ptr3   |  Ptr4   ] │     High L1 hit rate!
               └─────────────────────────────────────────────────────────────┘
```

### Why B-Trees are Favored in High-Volume Engines:
1. **Cache Locality:** Unlike Binary Search Trees (where every traversal step dereferences an arbitrary memory address, causing L3 cache misses), a B-Tree node contains multiple price keys packed contiguously in a single 64-byte CPU cache line.
2. **Logarithmic Scaling with Large Depths:** When an order book contains tens of thousands of active price levels (e.g., during volatile market events), binary searching and shifting a contiguous slice becomes expensive ($O(N)$). A B-Tree provides stable **$O(\log N)$** insertions and deletions while retaining the CPU cache friendliness of contiguous memory.
3. **TradeDrift’s Pragmatic Choice:** For standard crypto pairs where active price levels rarely exceed a few hundred active ticks, TradeDrift uses a **Sorted Slice + Binary Search (`sort.Search`) + Hash Map (`PriceLevels`)**, combining $O(1)$ top-of-book access with minimal code complexity.

---

## 14. Deep Dive: How End-to-End Ordering Guarantees are Maintained

To guarantee strict Price-Time Priority from the moment a user clicks "Submit" to the moment the trade settles, the system enforces a 5-stage sequential pipeline:

```
 [User Device]
       │ (1) HTTP / gRPC Request
       ▼
 [Order Service]
       │ (2) Writes to Kafka with Key = MarketID ("BTC-USDT")
       ▼
 [Kafka Topic: orders.created]
   Partition 0: Key "BTC-USDT" ──► Monotonic Offsets: [ 101, 102, 103, 104, 105 ]
       │
       │ (3) Kafka Consumer (1 partition reader per market)
       ▼
 [Engine InputQueue] (Go buffered channel: chan InputEvent)
       │
       │ (4) MarketEngine Event Loop (Sequential Pop)
       ▼
 [Matcher.Match()]
   • Evaluates Order 101 -> Mutates Book -> Emits Fill 101
   • Evaluates Order 102 -> Mutates Book -> Emits Fill 102
       │
       │ (5) OutputQueue (chan MatchResult)
       ▼
 [Publisher Goroutine]
   • Kafka trades.executed
   • Postgres Checkpoint: Offset = 102
```

### The 5 Architectural Guarantees:
1. **Kafka Partition Keying:** All orders for a specific `MarketID` are hashed to the **same Kafka partition**. Kafka guarantees strict FIFO message ordering within a single partition.
2. **Single Consumer Stream:** The engine consumes the partition with a single reader, preserving the exact sequential offset order.
3. **Authoritative Timestamping:** The arrival timestamp (`node.Timestamp = time.Now()`) is assigned by the matching engine upon receiving the event, preventing client clock manipulation.
4. **FIFO Channel Pipeline:** Go buffered channels (`InputQueue` and `OutputQueue`) operate as strict FIFO queues.
5. **Monotonic Checkpointing:** Database checkpoints only advance monotonically (`offset = MAX(current_offset, new_offset)`), guaranteeing that replayed logs match live execution exactly.

---

## 15. Codebase Cross-Reference Map

| Concept | File / Function Location | Description |
| :--- | :--- | :--- |
| **OrderBook & Indexes** | [`orderbook/book.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/book.go) | Defines `OrderBook`, `Side`, `Bids`, `Asks`, and `OrderIndex`. |
| **FIFO Price Level** | [`orderbook/level.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/level.go) | Implements `PriceLevel` with `container/list.List` FIFO queue. |
| **Order Node & Invariants** | [`orderbook/node.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/node.go) | Defines `OrderNode`, `RemainingQty`, `Timestamp`, and `*list.Element`. |
| **Price-Time Match Algorithm**| [`matcher/matcher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L158-L221) | Core `Match()` function, multi-level sweeping, and price crossing. |
| **In-place Partial Fill** | [`matcher/matcher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L108-L114) | `PartialFill()` updates quantity in-place, preserving queue spot. |
| **O(1) Cancellation** | [`matcher/matcher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L58-L80) | `Cancel()` removes node from doubly linked list in $O(1)$ time. |
| **Single-Threaded Loop** | [`market/event_loop.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go#L20-L29) | `MarketEngine.Run()` dedicated goroutine event loop. |
| **Deterministic Recovery** | [`recovery/replayer.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer.go) | Replays Kafka history into memory to recover state after restarts. |
