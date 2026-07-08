# TradeDrift Matching Engine — PriceLevel

**Document:** 04_Data_Structures / 04_Price_Level.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document defines the `PriceLevel` struct — the container that groups all resting orders at the same price into a FIFO queue.

---

# 2. Struct Definition

```go
type PriceLevel struct {
    price    decimal.Decimal
    orders   *list.List
    totalQty decimal.Decimal
}
```

---

# 3. Fields

---

## price

Type: `decimal.Decimal`

The exact price all orders in this level share.

Used as the key in `Side.priceLevels` (the hash map from price to PriceLevel).

Never changes after the level is created.

---

## orders

Type: `*list.List` (Go stdlib `container/list`)

A doubly-linked list of `*OrderNode` values.

This is the FIFO queue at this price level.

```
Head (oldest, executes first)
  |
  v
OrderNode A  <-->  OrderNode B  <-->  OrderNode C
                                          |
                                         Tail (newest, executes last)
```

Operations:

| Operation | How | Complexity |
|---|---|---|
| Insert new order | `list.PushBack(node)` | O(1) |
| Execute best order | `list.Front()` | O(1) |
| Cancel any order | `list.Remove(node.element)` | O(1) |
| Check if empty | `list.Len() == 0` | O(1) |

The doubly-linked list is chosen because it provides O(1) removal at any position — the critical property for O(1) cancel.

An array or slice would require O(n) removal when cancelling an order in the middle.

---

## totalQty

Type: `decimal.Decimal`

The sum of `remainingQty` across all orders currently at this price level.

Maintained **incrementally** — updated on every insert, fill, and cancel rather than recomputed from the list.

```
On insert:       totalQty += order.remainingQty
On partial fill: totalQty -= filledAmount
On cancel:       totalQty -= order.remainingQty
On full fill:    totalQty -= order.remainingQty  (then order removed)
```

Reading `totalQty` is O(1).

Recomputing from the list on every read would be O(n).

**Used for:**

Depth snapshots pushed to Redis. The snapshot includes `{price, totalQty}` for each level — the quantity visible in the order book at that price. `totalQty` makes this O(d) instead of O(n * d).

---

# 4. FIFO Ordering

Orders within a price level always execute oldest-first.

This is the "time" component of Price-Time Priority.

When two orders share the same price, the one that arrived earlier has a smaller timestamp and sits closer to the head of the list.

```
Order A  arrived 09:00   --> Head  (executes first)
Order B  arrived 09:05   --> Middle
Order C  arrived 09:10   --> Tail  (executes last)
```

Partial fills do not change queue position. `remainingQty` is reduced in-place. The node is never re-inserted.

---

# 5. Lifecycle

A PriceLevel is created dynamically when the first order at a price arrives.

A PriceLevel is destroyed when the last order at that price is removed.

```
First order at price 101.00 inserted
        |
        v
PriceLevel created { price: 101.00, orders: [A], totalQty: 1.5 }
        |
        v
More orders arrive at 101.00
        |
        v
PriceLevel { price: 101.00, orders: [A, B, C], totalQty: 4.0 }
        |
        v
Orders are matched or cancelled
        |
        v
Last order at 101.00 removed
        |
        v
PriceLevel destroyed
Side.priceLevels[101.00] deleted
101.00 removed from Side.sortedPrices
```

**An empty PriceLevel must never remain in the book.**

An empty level with a price in `sortedPrices` would make `sortedPrices[0]` point to a level with no orders — causing the Matching Core to attempt matching against nothing.

---

# 6. Relationship to Side

```
Side
  |
  +-- priceLevels  map[price]*PriceLevel
  |       |
  |       +-- 101.00  -->  PriceLevel { orders: [A, B] }
  |       +-- 100.75  -->  PriceLevel { orders: [C] }
  |       +-- 99.20   -->  PriceLevel { orders: [D] }
  |
  +-- sortedPrices  [101.00, 100.75, 99.20]
```

`priceLevels` gives O(1) access to the PriceLevel for any price.

`sortedPrices` gives O(1) access to the best price without scanning the map.

---

# 7. References

- 03_Order_Node.md — the nodes stored in the list
- 05_Side.md — the priceLevels map and sortedPrices index
- 07_Algorithms.md — Insert, FullFill, PartialFill pseudocode
- 08_Memory_Model.md — pointer ownership between Side, PriceLevel, and OrderNode
