# TradeDrift Matching Engine — Memory Model

**Document:** 04_Data_Structures / 08_Memory_Model.md
**Service:** Matching Engine
**Version:** V1.0
**Last Updated:** July 2026

---

# 1. Purpose

This document describes how the Order Book structures are owned, laid out, and related to each other in memory. Understanding this prevents stale pointer access, ownership confusion, and unnecessary state duplication.

---

# 2. Ownership Graph

```
Market Engine goroutine
        │  owns
        ▼
    OrderBook
        │
        ├── orderIndex  map[uuid.UUID]*OrderNode   (book-level)
        ├── bids (Side)
        │       ├── priceLevels  map[string]*PriceLevel
        │       └── sortedPrices []decimal.Decimal
        └── asks (Side)
                ├── priceLevels  map[string]*PriceLevel
                └── sortedPrices []decimal.Decimal

PriceLevel
        └── orders  *list.List
                        └── *list.Element  ──▶  *OrderNode (heap)

OrderNode (heap)
        └── element  *list.Element  (back-pointer for O(1) cancel)
```

`orderIndex` and the linked lists do not duplicate OrderNode data. Both hold pointers to the same heap-allocated node. A single OrderNode is allocated once and referenced from two places:
- `PriceLevel.orders` via `list.Element.Value`
- `OrderBook.orderIndex` via `map[uuid.UUID]*OrderNode`

---

# 3. Heap Layout

```
┌──────── Stack (goroutine-local) ──────────────────┐
│  Match loop variables: fillQty, incoming, best     │
└────────────────────────────────────────────────────┘

┌──────── Heap ──────────────────────────────────────┐
│  OrderBook struct                                  │
│    orderIndex map                                  │
│    bids Side                                       │
│      priceLevels map                               │
│      sortedPrices []decimal                        │
│    asks Side                                       │
│      priceLevels map                               │
│      sortedPrices []decimal                        │
│                                                    │
│  Per active price level:                           │
│    PriceLevel struct                               │
│    list.List struct                                │
│                                                    │
│  Per resting order:                                │
│    OrderNode struct                                │
│    list.Element struct                             │
└────────────────────────────────────────────────────┘
```

---

# 4. Pointer Reference Graph

```
OrderBook.orderIndex[orderID]
        │  *OrderNode pointer
        ▼
    OrderNode  ← allocated once on heap
        │
        └── element ──▶ *list.Element
                              │
                              └── Value ──▶ *OrderNode  (same pointer, back-reference)
```

The circular reference (`OrderNode → list.Element → OrderNode`) is intentional — it enables O(1) removal from both directions. Go's GC handles cycles correctly: both objects are collected together when neither is reachable from the book.

---

# 5. No Duplicated State

| Data | Stored where | How updated |
| --- | --- | --- |
| Order queue position | `PriceLevel.orders` (linked list) | `list.PushBack`, `list.Remove` |
| Order lookup by ID | `OrderBook.orderIndex` | Insert, Cancel, FullFill |
| Best price | `Side.sortedPrices[0]` | Maintained on insert / level removal |
| Total qty at level | `PriceLevel.totalQty` | Incremental update on every operation |

---

# 6. Object Lifetime

```
Insert(order) called
        │
        ▼
OrderNode allocated on heap
list.Element allocated (by list.PushBack)
        │
        ▼
Both referenced by:
    PriceLevel.orders  (via list.Element)
    OrderBook.orderIndex  (via *OrderNode)
        │
        ▼
Order rests in book
        │
        ▼
Cancel or FullFill called
        │
        ▼
list.Remove(node.element)      → list releases its reference
delete(orderIndex, orderID)    → map releases its reference
        │
        ▼
OrderNode and list.Element → unreachable → GC collects
```

After removal, no code in the Matching Core holds a pointer to the removed node. The Publisher Layer receives `MatchResult` (a value type, not a pointer to OrderNode).

---

# 7. Concurrency

The Order Book has **no locks**. Exactly one goroutine (the Market Engine's Event Loop) ever reads or writes the Order Book. The Publisher Layer reads from a channel — it never touches the book. Go's channel send/receive establishes a happens-before relationship — no additional synchronisation is required.

---

# 8. Recovery and Memory

On restart, the OrderBook starts empty. Recovery replays `OrderCreated` and `OrderCancelRequested` events through the same matching algorithm. The memory layout after recovery is identical to live state.

| Recovery event | Memory operation |
| --- | --- |
| `OrderCreated` | `Insert` — allocates OrderNode + list.Element |
| `OrderCancelRequested` | `Cancel` — releases both via GC |
| `TradeExecuted` | **Not replayed** — re-derived by the algorithm |

---

# 9. GC Pressure

Each resting order allocates two heap objects: one `OrderNode` and one `list.Element`. For a book with hundreds to a few thousand resting orders this is negligible GC load. If GC pressure becomes measurable, a `sync.Pool` of pre-allocated `OrderNode` objects can be introduced without changing any structure or algorithm.

---

# 10. References

- `02_Order_Book.md` — OrderBook struct and `orderIndex` placement
- `03_Order_Node.md` — OrderNode fields and lifecycle
- `04_Price_Level.md` — list.List and list.Element
- `06_Order_Index.md` — pointer relationships for O(1) cancel
- `07_Algorithms.md` — how operations interact with these structures
