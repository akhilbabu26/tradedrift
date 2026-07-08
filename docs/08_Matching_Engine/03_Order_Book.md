# TradeDrift Matching Engine

**Document:** 03_Order_Book.md  
**Service:** Matching Engine  
**Version:** V1.0  
**Status:** ✅ Architecture Finalized (Pre-Implementation)  
**Last Updated:** July 2026

---

# 1. Purpose

The Order Book is the core in-memory data structure used by the Matching Engine to maintain all active (resting) orders for a single trading pair.

Its primary responsibility is to maintain executable market liquidity while enforcing deterministic **Price-Time Priority**.

Each trading pair owns exactly one Order Book.

The Order Book is exclusively owned by one Market Engine and is modified only by that Market Engine's Event Loop.

---

# 2. Design Goals

The Order Book is designed to achieve the following goals.

- Extremely low latency
- Deterministic execution
- Price-Time Priority
- Lock-free operation
- Memory-first processing
- Fast order cancellation
- Efficient best price lookup
- Simple recovery after restart

---

# 3. Core Concepts

## Trading Pair

Each trading pair owns its own independent Order Book.

Examples:

- BTC-USDT
- ETH-USDT
- SOL-USDT

Order Books never share state.

---

## Buy Side (Bid Book)

The Buy Side contains all active BUY limit orders.

Orders are sorted by:

1. Highest price first
2. Earliest order first

Example

```

101.50

├── Order A

├── Order B

↓

100.75

├── Order C

↓

99.20

├── Order D

```

The highest bid is always the best bid.

---

## Sell Side (Ask Book)

The Sell Side contains all active SELL limit orders.

Orders are sorted by:

1. Lowest price first
2. Earliest order first

Example

```

102.00

├── Order X

↓

102.50

├── Order Y

├── Order Z

↓

103.10

```

The lowest ask is always the best ask.

---

# 4. Order Book Architecture

Every Market Engine owns exactly one Order Book.

```

                    Order Book

                 BTC-USDT Market

        ┌─────────────────────────────┐

        │                             │

        ▼                             ▼

    Buy Side                      Sell Side

 (Highest → Lowest)          (Lowest → Highest)

        │                             │

        ▼                             ▼

   Price Levels                 Price Levels

        │                             │

        ▼                             ▼

    FIFO Orders                 FIFO Orders

```

The Order Book never communicates directly with Kafka, Redis, PostgreSQL, or other services.

It exists purely in memory.

---

# 5. Price Levels

Orders are grouped by price.

Example

```

Price 101.00

├── Order 1

├── Order 2

├── Order 3

```

Every price exists only once.

Orders at the same price are stored in arrival order.

---

# 6. Order Queues

Each Price Level maintains its own FIFO queue.

Example

```

Price = 101.00

Head

↓

Order A

↓

Order B

↓

Order C

↓

Tail

```

The oldest order always executes first.

This guarantees Price-Time Priority.

---

# 7. Order Lifecycle (Order Book view)

The Order Book has its own view of an order — simpler than the Order Service state machine, because the book only cares about what is resting and what has been consumed.

> **Note:** `Created` and `Accepted` are **Order Service states**, not Order Book states. By the time an `OrderCreated` event reaches the Matching Engine, the Order Service has already validated the order, reserved funds, and set status to `OPEN`. The ME never tracks those earlier states.

```
OrderCreated event received
         |
         v
  Added to book  (resting, full remaining_qty)
         |
         |
   +-----+------------------------+
   |                              |
   v                              v
Partially matched          Cancel requested
(remaining_qty reduced,    (removed from book immediately)
 queue position unchanged,
 timestamp unchanged)
   |
   v
Fully matched
(removed from book)
```

Market Orders never enter the resting state.

They are matched immediately on arrival and never placed in the book.

---

# 8. Supported Order Types

## Limit Order

A Limit Order specifies the maximum buy price or minimum sell price.

Possible outcomes:

- Fully filled
- Partially filled
- Added to the Order Book
- Cancelled by user

Limit Orders use **Good Till Cancelled (GTC)**.

---

## Market Order

A Market Order executes immediately against the best available liquidity.

Possible outcomes:

- Fully filled
- Partially filled
- Remaining quantity cancelled

Market Orders use **Immediate-Or-Cancel (IOC)**.

They never become resting orders.

---

# 9. Matching Rules

The Order Book follows strict Price-Time Priority.

## Rule 1

Better price always executes first.

**Buy side** — higher price is better (buyer is willing to pay more):

```
BUY at 101  executes before  BUY at 100
```

**Sell side** — lower price is better (seller is willing to accept less):

```
SELL at 99  executes before  SELL at 100
```

---

## Rule 2

If prices are equal,

the oldest order executes first.

Example

```

Price 101

09:00

↓

09:05

↓

09:10

```

Execution order is identical.

---

## Rule 3

Matching continues until:

- Incoming order is fully executed, or
- No executable liquidity remains.

---

# 10. Resting Orders

Only Limit Orders may remain inside the Order Book.

Market Orders never rest.

Partially filled Limit Orders remain at their original position.

Specifically: the order's **queue position and arrival timestamp are unchanged** — only `remaining_qty` is reduced. The order is modified in-place; it is never removed and re-inserted, which would cause it to lose its time priority.

Their priority never changes.

---

# 11. Order Book Invariants

The following rules must always hold.

## Invariant 1

Each Market Engine owns exactly one Order Book.

---

## Invariant 2

Only one Event Loop may modify an Order Book.

---

## Invariant 3

Buy prices are sorted in descending order.

---

## Invariant 4

Sell prices are sorted in ascending order.

---

## Invariant 5

Orders inside one Price Level are always FIFO.

---

## Invariant 6

Each Order ID exists at most once.

---

## Invariant 7

Market Orders never remain inside the Order Book.

---

## Invariant 8

Cancelled orders are removed immediately.

---

## Invariant 9

A partially filled order retains its original priority.

---

## Invariant 10

Matching results are deterministic for identical event sequences.

---

# 12. Expected Performance

| Operation | Expected Complexity |
|------------|--------------------|
| Best Bid Lookup | O(1) |
| Best Ask Lookup | O(1) |
| Add Order | Defined in 04_Data_Structures.md |
| Cancel Order | Defined in 04_Data_Structures.md |
| Match Order | O(number of executed trades) |

This document intentionally focuses on logical behavior rather than implementation details.

---

# 13. Memory Model

The Order Book exists entirely in memory.

Persistent storage is **not** consulted during matching.

Persistence exists only for:

- Kafka offset checkpoints  (stored in Postgres, one row per partition: `{topic, partition, offset}`)

V1 has no snapshot mechanism. Snapshots are a possible future optimisation documented in **08_Recovery_Strategy.md**.

This minimizes matching latency.

---

# 14. Failure & Recovery

The Order Book is considered ephemeral state.

If the Matching Engine restarts:

1. Read the checkpoint row from Postgres  (`{topic, partition, offset}`).
2. Replay Kafka events from that offset.
3. Reconstruct the in-memory Order Book by processing each replayed event.
4. Resume live matching.

V1 has no snapshot to load. The checkpoint row tells the ME exactly where to start replaying — avoiding a full replay from offset 0 on every restart.

Recovery procedures are documented in **08_Recovery_Strategy.md**.

---

# 15. Why This Design?

Several alternative approaches were considered.

## Database-backed Order Book

Rejected.

Reason:

Database I/O introduces unacceptable latency for real-time matching.

---

## Shared Order Book Across Workers

Rejected.

Reason:

Concurrent modification would require synchronization and violate deterministic Price-Time Priority.

---

## In-Memory Order Book

Chosen.

Reason:

- Lowest latency
- Exclusive ownership
- Lock-free matching
- Simple recovery
- Predictable performance

---

# 16. References

Implementation details are described in:

- 04_Data_Structures.md
- 05_Matching_Algorithm.md
- 07_Concurrency_Model.md
- 08_Recovery_Strategy.md
- 09_Redis_Projection.md
- 10_Failure_Handling.md
- 11_Monitoring.md
- 12_Sequence_Diagrams.md
- 13_Future_Enhancements.md