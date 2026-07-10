# TradeDrift Matching Engine — Order Index

**Document:** 04_Data_Structures / 06_Order_Index.md
**Service:** Matching Engine
**Version:** V1.0
**Last Updated:** July 2026

---

# 1. Purpose

This document explains the `orderIndex` map — the structure that makes O(1) order cancellation possible.

---

# 2. Definition and Location

```go
type OrderBook struct {
    marketID   string
    bids       Side
    asks       Side
    orderIndex map[uuid.UUID]*OrderNode   // book-level, not Side-level
}
```

> `orderIndex` was moved from `Side` to `OrderBook`. Previously each side had its own index, requiring two hash lookups on every cancel (check bids, then asks). With a book-level index, cancel is a **single hash lookup** regardless of which side the order rests on. `node.side` on the returned node tells the algorithm which side to use for PriceLevel operations.

---

# 3. The Problem It Solves

**Without `orderIndex`:**

```
for each price level in bids:
    for each order in level.orders:
        if order.orderID == target → remove it
```

Worst case: O(p × q) where p = price levels, q = orders per level.

**With `orderIndex`:**

```
node = book.orderIndex[orderID]    O(1)
list.Remove(node.element)          O(1)
```

O(1) regardless of book depth.

---

# 4. Cancel Path

```
book.orderIndex[orderID]
        │  O(1)
        ▼
    node.side  ──▶  select bids or asks
    node.price ──▶  side.priceLevels[node.price]  ──▶  *PriceLevel
                                                              │
                                                              ▼
    level.totalQty -= node.remainingQty
    level.orders.Remove(node.element)   O(1) via element pointer
    delete(book.orderIndex, orderID)

    if level empty:
        delete(side.priceLevels, node.price)
        binary search + remove node.price from side.sortedPrices
```

---

# 5. Pointer Relationships

```
book.orderIndex[orderID]
        │  *OrderNode
        ▼
    OrderNode
        ├── side    ──▶  select bids or asks
        ├── price   ──▶  key in side.priceLevels
        └── element ──▶  *list.Element
                              │
                              ▼
                         list.Remove(element)  O(1)
```

---

# 6. Lifecycle

| Event | orderIndex change |
| --- | --- |
| Order inserted | `orderIndex[node.orderID] = node` |
| Partial fill | No change — node stays in book |
| Order cancelled | `delete(orderIndex, orderID)` |
| Order fully filled | `delete(orderIndex, orderID)` |

---

# 7. Invariants

- Every resting order is present in `orderIndex`.
- Every `orderIndex` entry corresponds to an order physically present in a PriceLevel linked list.
- No order is registered in `orderIndex` after removal from the book.
- `orderIndex` and linked lists are always consistent — updated together within the same Event Loop.

---

# 8. References

- `02_Order_Book.md` — where `orderIndex` lives
- `03_Order_Node.md` — the `element` field that enables O(1) removal
- `04_Price_Level.md` — the linked list that holds the nodes
- `07_Algorithms.md` — Cancel pseudocode
