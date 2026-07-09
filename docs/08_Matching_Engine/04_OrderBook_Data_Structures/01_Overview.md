# TradeDrift Matching Engine — Data Structures

**Document:** 04_Data_Structures / 01_Overview.md
**Service:** Matching Engine
**Version:** V1.0
**Last Updated:** July 2026

---

# 1. Purpose

This document set defines the in-memory data structures used by the Matching Engine to represent and operate on the Order Book. Every decision is driven by the performance goals established in `03_Order_Book.md`. These structures are internal to the Matching Engine — no other service reads or writes them directly.

---

## 2. Design Constraints

| **Constraint** | **Why It Exists** | **Chosen Solution** |
|----------------|-------------------|---------------------|
| Low-latency matching | Database access is too slow for the matching path. | Execute all matching logic entirely in memory. |
| Accurate monetary calculations | Floating-point arithmetic introduces precision errors. | Use `decimal.Decimal` for all prices and quantities. |
| Fast order cancellation | Searching the order book is too expensive. | Store a direct pointer to each order's linked-list node. |
| Instant best price access | Matching always starts from the best bid/ask. | Maintain sorted price-level indexes for O(1) access. |
| High concurrency without locks | Mutexes increase latency and complexity. | Process events using a single goroutine per market. |

# 3. Design Philosophy

> Build the simplest architecture that satisfies today's requirements while preserving a clear path for future evolution.

Data structures were selected based on expected workload, implementation clarity, and maintainability — not solely on asymptotic complexity. Performance optimisations should be driven by measurement, not assumptions.

---

# 4. V1 Design Decision

TradeDrift V1 uses a **hybrid in-memory data structure** optimised for simplicity, correctness, and low latency.

The ordered price index is implemented using a **sorted slice** rather than a balanced tree, because:

- The expected number of active price levels per market is small.
- A sorted slice provides O(1) best-price lookup (read index 0).
- The implementation is significantly simpler to write, debug, and test.
- The sorted slice can be replaced by a B-Tree in a future version without changing any matching logic.

---

# 5. Decimal Type

All price and quantity values use `github.com/shopspring/decimal`. `float64` is explicitly rejected:

```
float64:  0.1 + 0.2  =  0.30000000000000004   ← wrong
decimal:  0.1 + 0.2  =  0.3                   ← correct
```

A matching engine that rounds incorrectly loses user funds or produces incorrect trade prices.

---

# 6. Hybrid Architecture

No single data structure satisfies all performance requirements of an exchange. TradeDrift combines three specialised structures per side, plus one shared index at book level:

```
┌─────────────────────────────────────────────┐
│                  OrderBook                  │
│  marketID   orderIndex (shared, book-level) │
│                    │                        │
│         ┌──────────┴──────────┐             │
│         │                     │             │
│      Bid Side             Ask Side          │
│      sortedPrices         sortedPrices      │
│      priceLevels          priceLevels       │
│         │                     │             │
│      PriceLevel           PriceLevel        │
│      orders (list)        orders (list)     │
│         │                     │             │
│      OrderNode            OrderNode         │
└─────────────────────────────────────────────┘
```

| Structure | Problem it solves |
| --- | --- |
| `orderIndex` (OrderBook-level) | O(1) cancel by order ID — single lookup, no side guessing |
| `sortedPrices` (per Side) | O(1) best price — read index 0 |
| `priceLevels` (per Side) | O(1) access to the queue at any specific price |
| `orders` (per PriceLevel) | O(1) FIFO execution, O(1) in-place cancel via `element` pointer |

---

# 7. Document Index

| File | Contents |
| --- | --- |
| `01_Overview.md` | This file |
| `02_Order_Book.md` | `OrderBook` struct — ownership, `orderIndex`, market isolation |
| `03_Order_Node.md` | `OrderNode` struct — every field, lifecycle, priority |
| `04_Price_Level.md` | `PriceLevel` struct — FIFO queue, `totalQty`, lifecycle |
| `05_Side.md` | `Side` struct — sorted slice, price map, bid/ask ordering |
| `06_Order_Index.md` | `orderIndex` map — O(1) cancel, pointer relationships |
| `07_Algorithms.md` | Pseudocode for every operation |
| `08_Memory_Model.md` | Ownership, pointers, GC, memory layout |
| `09_Complexity_Analysis.md` | All operations with complexity and reason |
| `10_Design_Decisions.md` | ADR — why sorted slice, rejected alternatives |
| `11_Future_Evolution.md` | Upgrade paths for future versions |
