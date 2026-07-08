# TradeDrift Matching Engine — Memory Model

**Document:** 04_Data_Structures / 08_Memory_Model.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document describes how the Order Book structures are owned, laid out, and related to each other in memory.

Understanding the memory model prevents common mistakes such as:

- Accessing a node after it has been removed from the book.
- Holding a stale pointer to a cancelled order.
- Creating duplicate state that must be kept in sync manually.

---

# 2. Ownership Graph

Every piece of data has exactly one owner.

```
Market Engine  (goroutine)
      |
      | owns
      v
  OrderBook
      |
      +------ bids (Side) --+
      |                     |
      +------ asks (Side)   |
                            |
      +---------------------+
      |
      v
   Side
      |
      +-- priceLevels    map[price]*PriceLevel
      |         |
      |         +-- PriceLevel
      |                 |
      |                 +-- orders  *list.List
      |                         |
      |                         +-- *list.Element --> *OrderNode
      |
      +-- sortedPrices   []decimal.Decimal
      |         |
      |         (contains the same prices as keys of priceLevels)
      |
      +-- orderIndex     map[uuid.UUID]*OrderNode
                |
                (points to the same *OrderNode instances
                 that are stored in the linked lists)
```

`orderIndex` and the linked lists **do not duplicate the OrderNode data**.

Both structures hold pointers (`*OrderNode`) to the same heap-allocated node.

The OrderNode is allocated once and referenced from two places:
1. `PriceLevel.orders` (via `list.Element.Value`)
2. `Side.orderIndex` (via `map[uuid.UUID]*OrderNode`)

---

# 3. Memory Layout

```
Stack / Goroutine-local:
    Matching Core local variables (loop counters, incoming order, fill qty)

Heap:
    OrderBook struct
    Side struct (embedded in OrderBook)
    map[string]*PriceLevel    (priceLevels)
    []decimal.Decimal         (sortedPrices)
    map[uuid.UUID]*OrderNode  (orderIndex)

    For each active price level:
        PriceLevel struct
        list.List struct

    For each resting order:
        OrderNode struct
        list.Element struct   (wrapper used by Go's container/list)
```

---

# 4. Pointer Reference Graph

```
Side.orderIndex[orderID]
        |
        | *OrderNode pointer
        v
    OrderNode  <-- allocated once on heap
        |
        +-- element  *list.Element
                          |
                          | Value field
                          v
                      *OrderNode   (same pointer, back to the same node)
                      (list.Element.Value is interface{}, holds *OrderNode)
```

The doubly-linked list node (`list.Element`) holds a reference back to `*OrderNode` via its `Value` field.

`OrderNode.element` holds a pointer forward to the `list.Element`.

This circular reference is intentional — it is what enables O(1) removal in both directions.

Go's garbage collector handles circular references correctly. Both objects are collected together when neither is reachable from the book.

---

# 5. No Duplicated State

State exists in exactly one location.

| Data | Stored where | Updated where |
|---|---|---|
| Order queue position | PriceLevel.orders (linked list) | list.PushBack, list.Remove |
| Order lookup by ID | Side.orderIndex | insert, cancel, full fill |
| Best price | sortedPrices[0] | maintained on insert / level removal |
| Total qty at level | PriceLevel.totalQty | incremental update on every operation |

`sortedPrices` does not store the PriceLevel — it stores only the price key.

`priceLevels` stores the PriceLevel — accessed via the price key.

Both are kept consistent by always updating them together within the same operation.

---

# 6. Object Lifetime

```
Insert called
    |
    v
OrderNode allocated on heap
list.Element allocated on heap (by list.PushBack internally)
    |
    v
Both referenced by:
    - PriceLevel.orders (via list.Element)
    - Side.orderIndex   (via *OrderNode)
    |
    v
Order rests in book (both references held)
    |
    v
Cancel or FullFill called
    |
    v
list.Remove(node.element) -- list releases its reference
delete(orderIndex, orderID) -- map releases its reference
    |
    v
OrderNode and list.Element are now unreachable
Go GC collects them (not immediately, but on next GC cycle)
```

After removal, no code in the Matching Core holds a reference to the removed node.

The Matching Core returns a `MatchResult` (a value type, not a pointer to OrderNode) to the Publisher Layer.

---

# 7. Concurrency

The Order Book has **no locks**.

Correctness is guaranteed by ownership:

- Exactly one goroutine (the Market Engine's Event Loop) ever reads or writes the Order Book.
- No other goroutine touches the book directly.
- The Publisher Layer consumes output results from a channel — it never accesses the book.

Go's memory model guarantees that channel sends and receives establish a happens-before relationship. The Event Loop writes results to the output channel; the Publisher reads from it. No additional synchronisation is required.

---

# 8. GC Pressure

Each resting order allocates two heap objects: one `OrderNode` and one `list.Element`.

For a typical exchange with a few thousand resting orders, this is a negligible GC load.

The Go garbage collector handles short-lived allocations efficiently via its generational-style escape analysis and concurrent mark-and-sweep.

If GC pressure becomes a concern in a future high-throughput version, a pool of pre-allocated `OrderNode` objects (`sync.Pool`) can be introduced without changing the structure or algorithm.

---

# 9. References

- 03_Order_Node.md — OrderNode fields and lifecycle
- 04_Price_Level.md — list.List and list.Element
- 05_Side.md — priceLevels, sortedPrices, orderIndex
- 06_Order_Index.md — pointer relationships for O(1) cancel
- 07_Algorithms.md — how operations interact with these structures
