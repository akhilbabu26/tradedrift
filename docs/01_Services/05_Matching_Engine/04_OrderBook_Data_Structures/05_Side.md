# TradeDrift Matching Engine — Side

**Document:** 04_Data_Structures / 05_Side.md
**Service:** Matching Engine
**Version:** V1.0
**Last Updated:** July 2026

---

# 1. Purpose

This document defines the `Side` struct — one half of the Order Book (bid or ask side).

`Side` is the core hybrid data structure. It combines two specialised sub-structures to satisfy all per-side performance requirements.

---

# 2. Struct Definition

```go
type Side struct {
    priceLevels  map[string]*PriceLevel
    sortedPrices []decimal.Decimal
}
```

> `orderIndex` was moved from `Side` to `OrderBook`. `Side` now holds only the structures needed for price-level operations. See `06_Order_Index.md`.

---

# 3. Why Two Fields?

| Requirement | Best structure |
| --- | --- |
| O(1) access to the queue at a specific price | `priceLevels` hash map |
| O(1) best price without scanning the map | `sortedPrices` sorted slice |

No single structure solves both: a pure sorted map gives O(log n) best price; a pure hash map gives no sorted ordering.

---

# 4. priceLevels

```go
priceLevels map[string]*PriceLevel
```

A hash map keyed by `price.String()` mapping to the PriceLevel at that price. Provides O(1) access to the order queue at any specific price.

Key is `price.String()` (canonical string) to ensure reliable equality comparison across `decimal.Decimal` values.

| Operation | Usage |
| --- | --- |
| Insert | Check if level exists; create if not |
| Match | Access `sortedPrices[0]`'s level to consume orders |
| Cancel | Retrieve level to update `totalQty` |
| Full Fill | Retrieve level; destroy if empty after removal |

---

# 5. sortedPrices

```go
sortedPrices []decimal.Decimal
```

A slice of prices maintained in sorted order.

**Bid side — descending:**

```
sortedPrices  =  [101.50, 100.75, 99.20]
                    ▲
                 best bid (highest buy price)
```

**Ask side — ascending:**

```
sortedPrices  =  [102.00, 102.50, 103.10]
                    ▲
                 best ask (lowest sell price)
```

`sortedPrices[0]` is always the best price — O(1) read.

Insert new level: binary search to find index, then insert into slice.
Remove empty level: binary search to find index, then remove from slice.
The slice shift is O(n) worst case — acceptable for V1. See `10_Design_Decisions.md`.

**Invariants:**
- `sortedPrices` always contains exactly the same prices as the keys of `priceLevels`.
- No price appears in `sortedPrices` without a non-empty PriceLevel.

---

# 6. Bid vs Ask Ordering

| Side | Sort order | Best price | Rationale |
| --- | --- | --- | --- |
| Bid | Descending | Highest price | Buyer willing to pay more gets priority |
| Ask | Ascending | Lowest price | Seller willing to accept less gets priority |

---

# 7. Crossable Book

A match is possible when:

```
bids.sortedPrices[0]  >=  asks.sortedPrices[0]
```

The Matching Core checks this on every incoming order.

---

# 8. References

- `04_Price_Level.md` — the PriceLevel struct
- `06_Order_Index.md` — orderIndex (now at OrderBook level)
- `07_Algorithms.md` — how Side is used in each operation
- `10_Design_Decisions.md` — why sorted slice over B-Tree
