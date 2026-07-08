# TradeDrift Matching Engine — Design Decisions

**Document:** 04_Data_Structures / 10_Design_Decisions.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document is an Architecture Decision Record (ADR) for the Order Book data structure choices.

It captures what was decided, why, and what was rejected — so that future contributors understand the reasoning without having to reconstruct it.

---

# 2. Decision: Sorted Slice for the Price Index

**Decision:** Use a sorted `[]decimal.Decimal` slice as the ordered price index inside `Side`.

**Status:** Accepted

---

## Context

The Order Book needs to answer "what is the best price right now?" on every incoming order.

This requires a data structure that maintains prices in sorted order and provides fast access to the best (minimum or maximum) price.

Several alternatives were evaluated.

---

## Alternatives Considered

---

### AVL Tree / Red-Black Tree

A self-balancing binary search tree.

| Property | Value |
|---|---|
| Best price lookup | O(log n) |
| Insert | O(log n) |
| Remove | O(log n) |

**Rejected because:**

- O(log n) best-price lookup is worse than O(1) from a sorted slice.
- Requires implementing or importing a balanced tree. No equivalent is in the Go stdlib.
- Significantly more complex to implement, debug, and test.
- Offers no measurable benefit for the expected number of price levels (tens to low hundreds).

---

### B-Tree (`github.com/google/btree`)

A B-Tree with high branching factor, optimised for cache locality.

| Property | Value |
|---|---|
| Best price lookup | O(log n) |
| Insert | O(log n) |
| Remove | O(log n) |

**Considered. Rejected for V1 because:**

- O(log n) best-price lookup is still worse than O(1) from a sorted slice.
- Introduces an external dependency.
- More complex than a slice for the expected workload.
- The expected number of active price levels does not justify the additional complexity.

**Future path:** If profiling shows the sorted slice is a bottleneck, the B-Tree is the preferred upgrade. See `11_Future_Evolution.md`.

---

### Skip List

A probabilistic data structure.

| Property | Value |
|---|---|
| Best price lookup | O(log n) average |
| Insert | O(log n) average |
| Remove | O(log n) average |

**Rejected because:**

- Probabilistic — behaviour is not fully deterministic.
- More complex than both the sorted slice and B-Tree.
- O(log n) best-price lookup.
- No implementation in Go stdlib.

---

### Min/Max Heap

A binary heap tracking only the best price.

| Property | Value |
|---|---|
| Best price lookup | O(1) |
| Insert | O(log n) |
| Remove (arbitrary element) | O(n) |

**Rejected because:**

- Removing an arbitrary price level (when it becomes empty) is O(n) with lazy deletion or requires complex bookkeeping.
- Does not give direct access to all sorted prices (needed for depth snapshot).

---

### Redis Sorted Set

An external sorted set backed by Redis.

| Property | Value |
|---|---|
| Best price lookup | O(1) |
| Insert | O(log n) |
| Remove | O(log n) |

**Rejected because:**

- Every operation requires a network round-trip to Redis.
- Network I/O on the matching hot path introduces unacceptable latency.
- Redis is used only as a read replica for the UI — not as a matching data structure.

---

## Decision Rationale: Sorted Slice

| Property | Sorted Slice |
|---|---|
| Best price lookup | O(1) — `sortedPrices[0]` |
| Insert new level | O(log n) search + O(n) shift |
| Remove empty level | O(log n) search + O(n) shift |
| Depth snapshot | O(d) — iterate first d elements |
| Implementation complexity | Low |
| Go stdlib dependency | None (only `sort.Search`) |
| Cache locality | Excellent — contiguous memory |

The O(n) shift on insert and remove is acceptable because:

1. The number of active price levels is small in V1 (tens to low hundreds).
2. New level inserts and empty level removals are infrequent relative to fills and partial fills.
3. The O(n) shift operates on a small, cache-friendly slice, making it fast in practice despite the asymptotic label.
4. The design allows replacing the slice with a B-Tree in one place (`Side.sortedPrices`) without changing any other structure or algorithm.

---

# 3. Decision: Doubly-Linked List for Order Queues

**Decision:** Use Go stdlib `container/list` (doubly-linked list) as the FIFO queue at each price level.

**Status:** Accepted

---

## Context

Each price level maintains a FIFO queue of resting orders. The queue needs:

- O(1) append (new order arrives).
- O(1) front access (execute best order).
- O(1) arbitrary removal (cancel order).

---

## Alternatives Considered

### Slice / Array

| Property | Value |
|---|---|
| Append | O(1) amortised |
| Front access | O(1) |
| Arbitrary removal | O(n) |

**Rejected because:** Cancel requires finding and removing an order from the middle of the queue. O(n) is unacceptable.

### Ring Buffer / Circular Queue

| Property | Value |
|---|---|
| Append | O(1) |
| Front access | O(1) |
| Arbitrary removal | O(n) |

**Rejected:** Same reason as slice.

### Doubly-Linked List

| Property | Value |
|---|---|
| Append | O(1) |
| Front access | O(1) |
| Arbitrary removal | O(1) with pointer |

**Chosen.** O(1) arbitrary removal is achieved by storing a back-pointer (`node.element`) in each `OrderNode`. This pointer allows `list.Remove(node.element)` without scanning the list.

---

# 4. Decision: Hash Map for Price Level Lookup

**Decision:** Use `map[string]*PriceLevel` keyed by `price.String()` for O(1) price level access.

**Status:** Accepted

A hash map provides O(1) average-case lookup, insert, and delete by key.

The key is `decimal.Decimal.String()` — a canonical string representation — to ensure consistent hashing across equal decimal values with different internal representations.

---

# 5. Decision: Hash Map for Order Lookup (orderIndex)

**Decision:** Use `map[uuid.UUID]*OrderNode` keyed by `orderID` for O(1) cancel by order ID.

**Status:** Accepted

Without this map, cancel would require scanning all price levels and their queues.

With this map, cancel is a constant-time lookup followed by an O(1) linked-list removal.

---

# 6. Decision: decimal.Decimal for All Financial Values

**Decision:** Use `github.com/shopspring/decimal` for all price and quantity values. Reject `float64`.

**Status:** Accepted

```
float64:   0.1 + 0.2 = 0.30000000000000004
decimal:   0.1 + 0.2 = 0.3
```

Float arithmetic loses precision. In a financial system, imprecise arithmetic causes incorrect trade prices, incorrect fill quantities, and potential fund loss.

`shopspring/decimal` provides arbitrary-precision decimal arithmetic with correct rounding semantics.

---

# 7. References

- 05_Side.md — the sortedPrices and priceLevels implementation
- 09_Complexity_Analysis.md — full complexity table
- 11_Future_Evolution.md — upgrade path to B-Tree
