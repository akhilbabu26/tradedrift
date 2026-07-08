# TradeDrift Matching Engine — Complexity Analysis

**Document:** 04_Data_Structures / 09_Complexity_Analysis.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document provides a complete complexity analysis of every Order Book operation, with the reasoning behind each value.

---

# 2. Operation Table

| Operation | Complexity | Structure Used | Reason |
|---|---|---|---|
| Get Best Bid | O(1) | `bids.sortedPrices[0]` | First element of the sorted slice — no search |
| Get Best Ask | O(1) | `asks.sortedPrices[0]` | First element of the sorted slice — no search |
| Find Price Level | O(1) | `priceLevels[price]` | Hash map lookup by price key |
| Find Order by ID | O(1) | `orderIndex[orderID]` | Hash map lookup by order ID |
| Insert (existing price level) | O(1) | `list.PushBack` + map set | Level already exists — just append to list and update index |
| Insert (new price level) | O(log n) + O(n) | Binary search + slice insert | Binary search to find position + slice element shift |
| Cancel Order | O(1) | `orderIndex` + `list.Remove` | Index lookup gives node directly; element pointer gives list position |
| Cancel (last order at level) | O(1) + O(log n) + O(n) | + binary search + slice remove | Extra cost only when price level becomes empty |
| Execute Best Order | O(1) | `list.Front()` | Front of the best price level's linked list |
| Partial Fill | O(1) | In-place field update | Modify `remainingQty` and `totalQty` directly, no structural change |
| Full Fill | O(1) | `list.Remove` + map delete | Same as cancel; O(n) only if level becomes empty |
| Remove Empty Price Level | O(log n) + O(n) | Binary search + slice remove | Search for position + shift remaining elements |
| Get Depth Snapshot | O(d) | Iterate `sortedPrices[0..d]` | One pass over d levels; `totalQty` is pre-computed per level |
| Match Loop | O(k) | Repeated ExecuteBest + Fill | k = number of individual fill events produced |

---

# 3. Notes on O(n) Operations

Two operations include an O(n) slice shift: **Insert new price level** and **Remove empty price level**.

This O(n) refers to shifting elements in `sortedPrices` — the sorted slice of price keys.

The n here is the number of **distinct active price levels** on one side of the book, not the number of orders.

**Expected behaviour in V1:**

The number of active price levels per market in TradeDrift V1 is expected to remain in the range of tens to a few hundred.

An O(n) shift over 100 elements is approximately 100 integer copies. On modern hardware this takes nanoseconds and is cache-friendly.

**When does Insert new level trigger?**

Only when an order arrives at a price that has no existing resting orders. For well-used markets, many orders cluster at existing price levels, making new-level creation relatively infrequent.

**When does Remove empty level trigger?**

Only when the last order at a price is consumed or cancelled. Again, infrequent in practice.

**Conclusion:**

The amortised cost of these operations is acceptable for V1. Profiling on real workloads should determine whether a B-Tree is needed. See `10_Design_Decisions.md`.

---

# 4. Match Loop Analysis

The match loop runs O(k) where k = number of fills produced.

Each iteration of the loop:

- `ExecuteBest`: O(1)
- `PartialFill` or `FullFill`: O(1) or O(n) if level empties
- `remainingQty` update on incoming order: O(1)

The total cost of a single match operation that produces k fills is O(k) in the common case.

For a single-fill match (most common), the cost is O(1).

For a large sweep (market order consuming many levels), the cost is O(k) fills — each is O(1) unless it empties a level.

---

# 5. Depth Snapshot Analysis

`GetDepth(d)` is O(d) where d = the number of price levels requested.

`totalQty` is maintained incrementally on every PriceLevel — reading it is O(1).

Without pre-aggregated `totalQty`, the snapshot would require iterating all orders at each level: O(d * orders_per_level).

Pre-aggregation trades a small constant update cost on every fill/cancel for O(1) aggregate reads. This is correct for the snapshot use case.

---

# 6. Space Complexity

| Structure | Space |
|---|---|
| `priceLevels` map | O(p) — one entry per distinct active price |
| `sortedPrices` slice | O(p) — one entry per distinct active price |
| `orderIndex` map | O(n) — one entry per resting order |
| `list.List` nodes | O(n) — one `list.Element` per resting order |
| `OrderNode` structs | O(n) — one per resting order |

Total: O(p + n) where p = active price levels, n = resting orders.

p <= n always (each price level has at least one order).

Effective space: O(n).

---

# 7. References

- 07_Algorithms.md — pseudocode for each operation
- 10_Design_Decisions.md — why O(n) slice was chosen over O(log n) tree
- 11_Future_Evolution.md — upgrade path when O(n) becomes a bottleneck
