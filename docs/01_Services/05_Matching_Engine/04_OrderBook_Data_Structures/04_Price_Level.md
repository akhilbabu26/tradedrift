# TradeDrift Matching Engine — PriceLevel

**Document:** 04_Data_Structures / 04_Price_Level.md
**Service:** Matching Engine
**Version:** V1.0
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

### price

The exact price shared by all orders in this level. Used as the key in `Side.priceLevels`. Never changes after creation.

---

### orders

A doubly-linked list (`container/list` from Go stdlib). Each element holds a pointer to an `OrderNode`.

| Operation | Method | Complexity |
| --- | --- | --- |
| Insert new order (back of queue) | `list.PushBack(node)` | O(1) |
| Execute best order (front of queue) | `list.Front()` | O(1) |
| Cancel any order | `list.Remove(node.element)` | O(1) |
| Check if empty | `list.Len() == 0` | O(1) |

The doubly-linked list provides O(1) removal at any position — the critical property for O(1) cancel. A slice would require O(n) removal for arbitrary positions.

---

### totalQty

The sum of `remainingQty` across all orders at this level. Maintained **incrementally** — not recomputed from the list.

| Event | Update |
| --- | --- |
| Order inserted | `totalQty += order.remainingQty` |
| Partial fill of amount X | `totalQty -= X` |
| Order cancelled | `totalQty -= order.remainingQty` |
| Order fully filled | `totalQty -= order.remainingQty` |

Reading `totalQty` is O(1) because it is pre-aggregated. Without it, a depth snapshot would require O(orders_at_level) per level.

---

# 4. FIFO Queue Layout

```
Head (oldest — executes first)
  │
  ▼
OrderNode A  ←──▶  OrderNode B  ←──▶  OrderNode C
                                             │
                                            Tail (newest — executes last)
```

Orders at the same price always execute oldest-first (the "time" component of Price-Time Priority). A partial fill reduces `remainingQty` in-place — the node is never re-inserted, preserving its queue position.

---

# 5. Lifecycle

```
First order at price 101.00 arrives
        │
        ▼
PriceLevel created  {price: 101.00, orders: [A], totalQty: 1.5}
        │
        ▼
More orders arrive at 101.00
        │
        ▼
PriceLevel  {price: 101.00, orders: [A, B, C], totalQty: 4.0}
        │
        ▼
Orders matched or cancelled
        │
        ▼
Last order at 101.00 removed
        │
        ▼
PriceLevel destroyed
Side.priceLevels["101.00"] deleted
"101.00" removed from Side.sortedPrices
```

> **Invariant:** An empty PriceLevel must never remain in the book. An empty level with a price still in `sortedPrices` would cause the Matching Core to attempt matching against an empty queue.

---

# 6. Relationship to Side

```
Side.priceLevels
        │
        ├── "101.00"  ──▶  PriceLevel { orders: [A, B], totalQty: 2.0 }
        ├── "100.75"  ──▶  PriceLevel { orders: [C],    totalQty: 0.5 }
        └── "99.20"   ──▶  PriceLevel { orders: [D],    totalQty: 1.0 }

Side.sortedPrices  =  [101.00, 100.75, 99.20]  (bid side — descending)
```

---

# 7. References

- `03_Order_Node.md` — nodes stored in the list
- `05_Side.md` — the `priceLevels` map and `sortedPrices` index
- `07_Algorithms.md` — Insert, FullFill, PartialFill pseudocode
- `08_Memory_Model.md` — pointer ownership between Side, PriceLevel, and OrderNode
