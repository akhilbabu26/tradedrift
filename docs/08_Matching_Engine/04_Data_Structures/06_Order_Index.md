# TradeDrift Matching Engine — Order Index

**Document:** 04_Data_Structures / 06_Order_Index.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document explains the `orderIndex` map — the structure that makes O(1) order cancellation possible.

---

# 2. Definition

```go
orderIndex map[uuid.UUID]*OrderNode
```

One `orderIndex` exists per `Side`.

The bid side has its own index; the ask side has its own index.

---

# 3. What It Solves

Without `orderIndex`, cancelling an order requires:

```
For each price level in the book:
    For each order in the level's queue:
        If order.orderID == target:
            remove it
```

This is O(p * q) where p = number of price levels, q = orders per level.

With `orderIndex`:

```
node = orderIndex[orderID]          O(1) map lookup
list.Remove(node.element)           O(1) linked list remove
```

Cancel is O(1) regardless of book depth.

---

# 4. Pointer Relationships

```
Side.orderIndex
        |
        |  orderID  -->  *OrderNode
        |
        v
    OrderNode
        |
        +-- price      -->  used to find PriceLevel in priceLevels map
        |
        +-- element    -->  *list.Element  (position in PriceLevel.orders)
                                |
                                v
                         list.Remove(element)   O(1)
```

The full cancel path:

```
1. orderIndex[orderID]            --> get *OrderNode           O(1)
2. priceLevels[node.price]        --> get *PriceLevel          O(1)
3. level.totalQty -= node.remainingQty                         O(1)
4. level.orders.Remove(node.element)                           O(1)
5. delete orderIndex[orderID]                                  O(1)
6. if level.orders.Len() == 0:
       delete priceLevels[node.price]                          O(1)
       remove node.price from sortedPrices     O(log n) search + O(n) shift
```

Steps 1–5 are all O(1). Step 6 only runs when the last order at a price is cancelled.

---

# 5. Lifecycle

**On Insert:**

```go
orderIndex[node.orderID] = node
```

The node is registered immediately after being placed in the linked list.

**On Cancel:**

```go
delete(orderIndex, orderID)
```

The node is deregistered before (or as part of) being removed from the linked list.

**On Full Fill:**

```go
delete(orderIndex, node.orderID)
```

Same as cancel — deregistered when removed from the book.

**On Partial Fill:**

The node stays in the book. `orderIndex` is not changed. The pointer remains valid.

---

# 6. Ownership

`orderIndex` is owned by `Side`.

An order that rests on the bid side is registered in `bids.orderIndex`.

An order that rests on the ask side is registered in `asks.orderIndex`.

An order is never registered in both indices simultaneously — an order rests on exactly one side.

---

# 7. Invariants

- Every order currently resting in the book is present in its side's `orderIndex`.
- Every entry in `orderIndex` corresponds to an order that is physically present in a PriceLevel's linked list.
- No order is registered in `orderIndex` after it has been removed from the book.
- `orderIndex` and the linked lists are always consistent — they are updated atomically within the same Event Loop.

---

# 8. References

- 03_Order_Node.md — the `element` field that enables O(1) removal
- 04_Price_Level.md — the linked list that holds the nodes
- 05_Side.md — where orderIndex lives
- 07_Algorithms.md — Cancel pseudocode
