# `internal/orderbook` — In-Memory Order Book

**Package:** `orderbook`
**Service:** Matching Engine
**Last Updated:** August 2026

---

## 1. What This Package Does

This package defines the **core in-memory data structures** of the Matching Engine.
It has zero infrastructure dependencies — no Kafka, no Redis, no PostgreSQL, no HTTP.

Its only job is to represent the live state of a single trading pair''s order book in memory:
- Which buy orders are resting, at what price, in what order
- Which sell orders are resting, at what price, in what order
- A fast lookup map so any order can be found by ID in O(1)

The actual matching logic (Insert, Cancel, Match loop) lives in `../matcher/`.
This package only defines **what the data looks like**, not how it is manipulated.

---

## 2. Why Pure In-Memory?

Three alternatives were considered and rejected:

| Alternative | Problem |
| :--- | :--- |
| Database-backed order book | Database I/O is too slow. Even a 1ms round-trip is unacceptable for sub-millisecond matching. |
| Shared order book across goroutines | Concurrent modification requires mutexes, adding latency and non-determinism. |
| In-memory + write-through to Redis | Adds I/O to the hot matching path. Redis is only used for depth snapshots *after* matching. |

**Chosen approach:** Fully in-memory, exclusively owned by one goroutine per market. No locks needed.

---

## 3. External Packages Used

### `github.com/shopspring/decimal`

Used for **all price and quantity values**. `float64` is explicitly rejected:

```
float64:  0.1 + 0.2 = 0.30000000000000004   ? WRONG
decimal:  0.1 + 0.2 = 0.3                   ? CORRECT
```

A matching engine that rounds incorrectly loses user funds or produces wrong trade prices.

---

### `container/list` (Go stdlib)

Used for the **FIFO order queue inside each price level**.

Go''s `container/list` is a **doubly linked list**. Each `*list.Element` internally holds:

```go
type Element struct {
    next, prev *Element  // BOTH directions
    list       *List
    Value      any
}
```

Why we use it:
- `Orders.Front()` — O(1) access to the oldest (highest-priority) order
- `Orders.PushBack(node)` — O(1) insertion at the back
- `list.Remove(node.Element)` — O(1) removal at **any** position

#### How O(1) cancel works with a doubly linked list

When orders A, B, C are at the same price level:

```
[sentinel] <-> [OrderA.Element] <-> [OrderB.Element] <-> [OrderC.Element] <-> [sentinel]
                      ^                    ^                    ^
                      |                   |                    |
                stored on           stored on             stored on
                OrderA node         OrderB node           OrderC node
```

To cancel OrderB:
```go
list.Remove(orderB.Element)
// Internally:
// orderB.Element.prev.next = orderB.Element.next  -> OrderA points to OrderC
// orderB.Element.next.prev = orderB.Element.prev  -> OrderC points back to OrderA
// Result: O(1), no scanning
```

> **Q: "Is Element a back-pointer? Does a doubly linked list need a front pointer too?"**
>
> A: `*list.Element` already contains BOTH `prev` and `next` internally.
> The "front" of a price level is accessed via `level.Orders.Front()` on the PriceLevel,
> not on the OrderNode. Each node only needs its own position pointer (Element),
> not the position of the front of the whole queue.

---

### `github.com/google/uuid`

All IDs in TradeDrift are UUIDv7 — time-ordered UUIDs. The ME never generates `order_id`
(that belongs to Order Service). The ME only generates `trade_id` inside `../matcher/`.

---

### `time` (Go stdlib)

Used for `Timestamp` on `OrderNode`. This is the **ME arrival time** — the exact moment
the ME inserts the order into the book. NOT the Order Service `created_at`.

This determines time priority: when two orders share the same price, earlier timestamp wins (FIFO).

---

## 4. Files In This Package

### `node.go` — OrderNode

One `OrderNode` = one resting order in the book.

```go
type OrderNode struct {
    OrderID      uuid.UUID
    UserID       uuid.UUID
    MarketID     string
    Side         SideType        // BUY | SELL
    OrderType    OrderType       // LIMIT | MARKET
    Price        decimal.Decimal // limit price; zero for MARKET
    OriginalQty  decimal.Decimal // never changes
    RemainingQty decimal.Decimal // reduced in-place on partial fill
    Timestamp    time.Time       // ME arrival time — time priority
    Element      *list.Element   // position pointer for O(1) self-removal
}
```

**Key design decisions:**

- `RemainingQty` is reduced **in-place** — NEVER re-insert the node on partial fill.
  Re-inserting would move it to the back of the queue, breaking Price-Time Priority.

- `Timestamp` is set by the ME at insertion, not copied from Order Service.
  This makes time priority deterministic regardless of Kafka lag.

- `Element` is a pointer from the node back to its slot in the linked list.
  Without it, cancel = O(n) scan. With it, cancel = O(1) direct removal.

- `MarketID` is stored on the node so `TradeExecuted`/`OrderCancelled` events
  can be built without any external lookup.

- Both `OriginalQty` and `RemainingQty` exist because:
  - `OriginalQty` is for event payloads and monitoring (fill %)
  - `RemainingQty` is what Wallet Service uses to calculate how much to release on cancel

---

### `level.go` — PriceLevel

Groups all resting orders at exactly one price point.

```go
type PriceLevel struct {
    Price    decimal.Decimal
    Orders   *list.List      // FIFO linked list of *OrderNode
    TotalQty decimal.Decimal // pre-aggregated sum of all RemainingQty
}
```

**Why `TotalQty` is pre-aggregated:**

After every match, Publisher calls `GetDepth()` to push a Redis snapshot.
Without pre-aggregation, `GetDepth` would iterate every order at every level.
With `TotalQty`, it just reads one field per level — O(1) per level, O(depth) total.

The `matcher` package keeps it in sync:
- Insert:      `TotalQty += node.RemainingQty`
- PartialFill: `TotalQty -= filledQty`
- FullFill:    `TotalQty -= node.RemainingQty`
- Cancel:      `TotalQty -= node.RemainingQty`

**Why `container/list` and not a slice:**

Slices require O(n) shift on front removal and O(n) scan on mid-removal.
The linked list gives O(1) for all three access patterns we need:
PushBack (new order), Front (execute oldest), Remove (cancel any).

---

### `side.go` — Side

Represents either the bid book (BUY orders) or the ask book (SELL orders).

```go
type Side struct {
    SortedPrices []decimal.Decimal        // index 0 = best price always
    PriceLevels  map[string]*PriceLevel   // price.String() -> level
    IsBid        bool                     // true=bids(desc), false=asks(asc)
}
```

**Two complementary structures:**

| Structure | What it solves | Complexity |
| :--- | :--- | :--- |
| `SortedPrices` | Best price lookup — read index 0 | O(1) |
| `PriceLevels` | Access the queue at any specific price | O(1) |

Neither alone is enough:
- `SortedPrices` alone: you know the best price but cannot reach the orders
- `PriceLevels` alone: you can reach any level but need a scan to find the best price

**Why a sorted slice instead of a balanced tree:**

- The number of active price levels per market is small in practice
- Sorted slice gives O(1) best-price lookup (read index 0)
- Much simpler to implement, debug, and test than a red-black tree
- Can be upgraded to a B-Tree in a future version with no changes to matching logic

**`IsBid` controls sort direction:**
- Bids (`IsBid=true`): SortedPrices is descending — highest price at index 0
- Asks (`IsBid=false`): SortedPrices is ascending — lowest price at index 0
- In both cases, `SortedPrices[0]` is the **best** price

---

### `book.go` — OrderBook

The top-level struct. One per market, one owner goroutine.

```go
type OrderBook struct {
    MarketID   string
    Bids       Side
    Asks       Side
    OrderIndex map[uuid.UUID]*OrderNode
}
```

**Why `OrderIndex` is book-level (not per-side):**

Cancel requests carry only `order_id` — no side information. With a book-level
index, one O(1) lookup gives the node, then `node.Side` tells us which side to use.
If OrderIndex were per-side, we would need two lookups and conditional logic.

**One book per market, one goroutine per book:**

Each trading pair gets its own `OrderBook`. Books never share state.
Each book is exclusively owned by its market's Event Loop goroutine.
No mutex is ever needed — only one goroutine ever touches a given book.

---

### `result.go` — Output Types

These types represent what the matching algorithm produces. Consumed by the Publisher.

```
Fill           — one individual trade
MatchResult    — output of processing ONE Kafka input event (one-in one-out)
DepthSnapshot  — top-N price levels for Redis
CancelledOrder — an order removed from the book
DepthLevel     — one row in a depth snapshot (price + qty)
```

**`Fill.Price` is always the maker''s price:**

The maker is the resting order. The taker is the incoming order.
Trade price is always the maker''s price — standard exchange behaviour.

**`MatchResult.SourceOffset`:**

Every `MatchResult` carries the Kafka offset of the input event that produced it.
The Publisher uses this to write exactly one checkpoint per result.

One-in one-out invariant: **one Kafka event ? one MatchResult ? one checkpoint write**.

---

## 5. Architecture Diagram

```
+-----------------------------------------------+
|                 OrderBook                     |
|  MarketID   OrderIndex (book-level, O(1) cancel)|
|                   |                           |
|         +---------+---------+                 |
|         |                   |                 |
|      Bid Side            Ask Side             |
|    (IsBid=true)         (IsBid=false)         |
|    SortedPrices[0]      SortedPrices[0]       |
|    = highest bid        = lowest ask          |
|    PriceLevels map      PriceLevels map        |
|         |                   |                 |
|      PriceLevel          PriceLevel           |
|      Orders (list)       Orders (list)        |
|      TotalQty            TotalQty             |
|         |                   |                 |
|      OrderNode           OrderNode            |
|      (Element ptr)       (Element ptr)        |
+-----------------------------------------------+
```

---

## 6. Complexity Summary

| Operation | Complexity | How |
| :--- | :--- | :--- |
| Best price lookup | O(1) | `side.SortedPrices[0]` |
| Get level by price | O(1) | `side.PriceLevels[price.String()]` |
| Get oldest order at level | O(1) | `level.Orders.Front()` |
| Insert new order (existing level) | O(1) | `level.Orders.PushBack(node)` |
| Insert new order (new level) | O(log n + n) | Binary search + slice insert |
| Cancel order by ID | O(1) | `OrderIndex` lookup + `list.Remove(node.Element)` |
| Partial fill | O(1) | Reduce `RemainingQty` in-place |
| Full fill | O(1) | `list.Remove` + `delete(OrderIndex, id)` |
| Get depth snapshot | O(depth) | Read `SortedPrices[0..d]` + `TotalQty` |

---

## 7. What This Package Does NOT Do

- Does NOT match orders — that is `../matcher/`
- Does NOT publish Kafka events — that is `../publisher/`
- Does NOT write to Redis — that is `../projection/`
- Does NOT read from Postgres — that is `../recovery/`
- Does NOT manage goroutines — that is `../market/`
- Does NOT validate tick/lot sizes — that is `../market/event_loop.go`
