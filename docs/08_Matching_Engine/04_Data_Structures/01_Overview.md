# TradeDrift Matching Engine — Data Structures

**Document:** 04_Data_Structures / 01_Overview.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document set defines the in-memory data structures used by the Matching Engine to represent and operate on the Order Book.

Every decision is driven by the performance goals established in 03_Order_Book.md.

These structures are internal to the Matching Engine.

No other service reads or writes these structures directly.

---

# 2. Design Constraints

- All operations occur in-memory. No database access during matching.
- Price and quantity values must use decimal arithmetic. `float64` is not permitted.
- Cancel must be O(1). This drives the linked-list-and-pointer design.
- Best price lookup must be O(1). This drives the sorted-slice design.
- Structures must support lock-free operation within a single goroutine.

---

# 3. Design Philosophy

TradeDrift follows the principle:

> Build the simplest architecture that satisfies today's requirements while preserving a clear path for future evolution.

Data structures were selected based on expected workload, implementation clarity, and maintainability — not solely on asymptotic complexity.

Performance optimizations should be driven by measurement rather than assumptions.

---

# 4. V1 Design Decision

TradeDrift V1 uses a hybrid in-memory data structure optimized for simplicity, correctness, and low latency.

The ordered price index is implemented using a **sorted slice** rather than a balanced tree.

Reasons for this decision:

- The expected number of active price levels per market is relatively small.
- A sorted slice provides O(1) best-price lookup (read index 0).
- The implementation is significantly simpler than maintaining a balanced tree.
- It is easier to debug, test, and explain.
- The architecture allows the price index to be replaced by a B-Tree in future versions without changing any Matching Engine logic.

This decision was made after evaluating the expected workload — not by choosing the most complex structure available.

---

# 5. Decimal Type

All price and quantity values use:

```
github.com/shopspring/decimal
```

`float64` is explicitly rejected.

```
float64:   0.1 + 0.2 = 0.30000000000000004
decimal:   0.1 + 0.2 = 0.3
```

Float arithmetic loses precision in financial calculations.

A matching engine that rounds incorrectly loses user funds or produces incorrect trade prices.

Every price, quantity, and running total in this system is a `decimal.Decimal`.

---

# 6. Hybrid Architecture

No single data structure satisfies all performance requirements of an exchange.

TradeDrift combines three specialised structures per side:

```
                   Order Book
                        |
         +--------------+--------------+
         v                             v
      Bid Side                     Ask Side
         |                             |
         +-- Price Level Map           +-- Price Level Map
         +-- Sorted Price Index        +-- Sorted Price Index
         +-- Order Index               +-- Order Index
                        |
                        v
                   Price Level
                        |
                        v
            FIFO Doubly-Linked List
                        |
                        v
                   OrderNode
                        |
                        v
                   *list.Element  (back-pointer for O(1) cancel)
```

| Structure | Problem it solves |
|---|---|
| Sorted Price Index | O(1) best-price lookup without scanning the map |
| Price Level Map | O(1) access to the queue at a specific price |
| Order Index | O(1) cancel by order ID without scanning the book |

No single structure solves all three. See 10_Design_Decisions.md for why each alternative was rejected.

---

# 7. Document Index

| File | Contents |
|---|---|
| 01_Overview.md | This file — purpose, constraints, philosophy, high-level architecture |
| 02_Order_Book.md | `OrderBook` struct — ownership, market isolation |
| 03_Order_Node.md | `OrderNode` struct — every field, lifecycle, priority |
| 04_Price_Level.md | `PriceLevel` struct — FIFO queue, totalQty, lifecycle |
| 05_Side.md | `Side` struct — sorted slice, price map, ordering rules |
| 06_Order_Index.md | `orderIndex` map — O(1) cancel, pointer relationships |
| 07_Algorithms.md | Pseudocode for every operation |
| 08_Memory_Model.md | Ownership, pointers, GC, memory layout |
| 09_Complexity_Analysis.md | All operations with complexity and reason |
| 10_Design_Decisions.md | ADR — why sorted slice, rejected alternatives |
| 11_Future_Evolution.md | Upgrade paths, future data structures |
