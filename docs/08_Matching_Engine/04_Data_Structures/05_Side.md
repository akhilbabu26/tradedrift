# TradeDrift Matching Engine — Side

**Document:** 04_Data_Structures / 05_Side.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document defines the `Side` struct — one half of the Order Book, representing either the bid (buy) side or the ask (sell) side.

`Side` is the core hybrid data structure of the Matching Engine. It combines three specialised sub-structures to satisfy all performance requirements simultaneously.

---

# 2. Struct Definition

```go
type Side struct {
    priceLevels  map[string]*PriceLevel
    sortedPrices []decimal.Decimal
    orderIndex   map[uuid.UUID]*OrderNode
}
```

---

# 3. Why Three Fields?

No single data structure satisfies all three requirements:

| Requirement | Best structure |
|---|---|
| O(1) access to queue at a specific price | Hash map |
| O(1) best price without scanning | Sorted index |
| O(1) cancel any order by ID | Hash map with pointer |

The three-field hybrid gives each requirement its own optimal structure:

```
priceLevels   -->  O(1) "give me the queue at price 101.00"
sortedPrices  -->  O(1) "give me the best price right now"
orderIndex    -->  O(1) "cancel order abc-123"
```

---

# 4. priceLevels

Type: `map[string]*PriceLevel`

A hash map keyed by price string (`price.String()`) mapping to the PriceLevel at that price.

**Purpose:** O(1) access to the order queue at any specific price.

**Used during:**

- **Insert:** Check if a level for this price already exists. Create one if not.
- **Match:** Get the PriceLevel at `sortedPrices[0]` to consume orders from its front.
- **Cancel:** Get the PriceLevel for the node's price to update `totalQty`.
- **Full Fill:** Get the PriceLevel, remove the order, destroy the level if empty.

**Key choice:** `price.String()` rather than `decimal.Decimal` directly, because map keys in Go must be comparable and `decimal.Decimal` is a struct that may not compare correctly as a map key without a canonical string representation.

---

# 5. sortedPrices

Type: `[]decimal.Decimal`

A slice of prices, maintained in sorted order at all times.

**Bid side:** descending — `[101.50, 100.75, 99.20]`

Best bid is always `sortedPrices[0]`.

```
sortedPrices[0]  = 101.50  ← best bid (highest buy price)
sortedPrices[1]  = 100.75
sortedPrices[2]  = 99.20
```

**Ask side:** ascending — `[102.00, 102.50, 103.10]`

Best ask is always `sortedPrices[0]`.

```
sortedPrices[0]  = 102.00  ← best ask (lowest sell price)
sortedPrices[1]  = 102.50
sortedPrices[2]  = 103.10
```

**Operations:**

Insert new price level: binary search to find the correct insertion index, then insert.

```
binary search O(log n)  +  slice insert O(n) shift  =  O(n) worst case
```

Remove empty price level: binary search to find the index, then remove.

```
binary search O(log n)  +  slice remove O(n) shift  =  O(n) worst case
```

Read best price: `sortedPrices[0]` — O(1).

**V1 rationale:** The O(n) shift is acceptable for V1 because the number of active price levels per market is expected to be small (tens to low hundreds). The slice provides better cache locality than a tree and is simpler to implement, debug, and test.

**Invariants:**

- `sortedPrices` always contains the same prices as the keys of `priceLevels`.
- No price appears in `sortedPrices` unless a non-empty PriceLevel exists for it.
- An empty PriceLevel is never represented in `sortedPrices`.

---

# 6. orderIndex

Type: `map[uuid.UUID]*OrderNode`

A hash map keyed by `orderID` mapping to a pointer to the `OrderNode`.

**Purpose:** O(1) lookup of any order in the book by its ID.

**Used during:**

- **Cancel:** `orderIndex[orderID]` gives the node directly. The node's `element` field gives the linked-list position. `list.Remove(node.element)` removes it in O(1).
- **Find by ID:** Direct lookup for debugging or admin purposes.

The `orderIndex` is the reason cancel is O(1) instead of O(n).

Without it, cancel would require scanning every PriceLevel's linked list to find the order.

Detailed coverage in `06_Order_Index.md`.

---

# 7. Bid Ordering

The bid side sorts prices from highest to lowest.

A buyer placing an order at a higher price is more willing to trade — they get priority.

```
101.50  <-- best bid (executes first)
100.75
99.20   <-- worst bid (executes last)
```

Matching against the bid side starts at `sortedPrices[0]` and proceeds downward.

---

# 8. Ask Ordering

The ask side sorts prices from lowest to highest.

A seller placing an order at a lower price is more willing to trade — they get priority.

```
102.00  <-- best ask (executes first)
102.50
103.10  <-- worst ask (executes last)
```

Matching against the ask side starts at `sortedPrices[0]` and proceeds upward.

---

# 9. Crossable Book

A match is possible when:

```
bids.sortedPrices[0] >= asks.sortedPrices[0]
```

That is: the best buy price is at least as high as the best sell price.

The Matching Core checks this condition on every incoming order.

---

# 10. References

- 04_Price_Level.md — the PriceLevel struct
- 06_Order_Index.md — orderIndex detail
- 07_Algorithms.md — how Side is used in each operation
- 10_Design_Decisions.md — why sorted slice over B-Tree
