# TradeDrift Matching Engine — Complexity Analysis

**Document:** 04_Data_Structures / 09_Complexity_Analysis.md
**Service:** Matching Engine
**Version:** V1.0
**Last Updated:** July 2026

---

# 1. Purpose

Complete complexity analysis of every Order Book operation with the reasoning behind each value.

---

# 2. Operation Table

| Operation | Complexity | Structure Used | Reason |
| --- | --- | --- | --- |
| Get Best Bid | O(1) | `bids.sortedPrices[0]` | First element of the sorted slice — no search |
| Get Best Ask | O(1) | `asks.sortedPrices[0]` | First element of the sorted slice — no search |
| Find Price Level | O(1) | `priceLevels[price]` | Hash map lookup by canonical price key |
| Find Order by ID | O(1) | `book.orderIndex[orderID]` | Single book-level hash map lookup |
| Insert (existing level) | O(1) | `list.PushBack` + map set | Level exists — append to list, register in index |
| Insert (new level) | O(log n) + O(n) | Binary search + slice insert | Find position + shift slice elements |
| Cancel Order | O(1) | `orderIndex` + `list.Remove` | Index gives node; `element` gives list position |
| Cancel (empties level) | O(1) + O(log n) + O(n) | + binary search + slice remove | Extra cost only when level becomes empty |
| Execute Best Order | O(1) | `list.Front()` | Front of the best price level's linked list |
| Partial Fill | O(1) | In-place field update | `remainingQty` and `totalQty` modified directly |
| Full Fill | O(1) | `list.Remove` + map delete | Same as cancel; O(n) shift only if level empties |
| Remove Empty Level | O(log n) + O(n) | Binary search + slice remove | Search for index + shift remaining elements |
| Get Depth Snapshot | O(d) | Iterate `sortedPrices[0..d]` | One pass over d levels; `totalQty` is pre-computed |
| Match Loop | O(k) | Repeated ExecuteBest + Fill | k = number of fill events produced |

---

# 3. The O(n) Shift

Two operations include an O(n) slice shift: **Insert new price level** and **Remove empty price level**.

`n` = number of distinct active price levels on one side — not the number of orders.

**Expected scale in V1:** tens to a few hundred price levels per market. An O(n) shift over 100 elements is ~100 pointer-sized copies on contiguous memory — nanoseconds on modern hardware.

**Frequency:** New-level inserts occur only when an order arrives at a previously unused price. Empty-level removals occur only when the last order at a price is consumed. Both are infrequent relative to fills within existing levels.

See `10_Design_Decisions.md` for why the sorted slice was chosen over a B-Tree.

---

# 4. Match Loop Analysis

| Per-iteration cost | Complexity |
| --- | --- |
| `ExecuteBest` | O(1) |
| `PartialFill` | O(1) |
| `FullFill` (level stays) | O(1) |
| `FullFill` (level empties) | O(log n) + O(n) shift |
| `incoming.remainingQty` update | O(1) |

- Single-fill match (most common): O(1)
- Large sweep consuming many levels: O(k) with occasional O(n) shifts

---

# 5. Depth Snapshot Analysis

`GetDepth(d)` is O(d). `totalQty` is maintained incrementally on every PriceLevel — reading it is O(1). Without pre-aggregation the snapshot would be O(d × orders_per_level).

---

# 6. Space Complexity

| Structure | Space | Notes |
| --- | --- | --- |
| `priceLevels` map | O(p) | One entry per distinct active price |
| `sortedPrices` slice | O(p) | One entry per distinct active price |
| `orderIndex` map | O(n) | One entry per resting order |
| `list.List` nodes | O(n) | One `list.Element` per resting order |
| `OrderNode` structs | O(n) | One per resting order |

**Total:** O(p + n) ≈ O(n), since p ≤ n always.

---

# 7. References

- `07_Algorithms.md` — pseudocode for each operation
- `10_Design_Decisions.md` — why O(n) slice was chosen over O(log n) tree
- `11_Future_Evolution.md` — upgrade path when O(n) becomes a bottleneck
