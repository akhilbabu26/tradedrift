# TradeDrift Matching Engine

**Document:** 04_Data_Structures.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document defines the in-memory data structures used by the Matching Engine to represent and operate on the Order Book.

Every data structure decision is driven by the performance goals defined in 03_Order_Book.md.

The structures defined here are internal to the Matching Engine.

No other service reads or writes these structures directly.

---

# 2. Design Constraints

- All operations occur in-memory. No database access during matching.
- Price and quantity values must use decimal arithmetic. Float64 is not permitted.
- Cancel must be O(1). This drives the linked list and pointer design.
- Best price lookup must be O(1). This drives the sorted slice design.
- Structures must support lock-free operation within a single goroutine.

---

# 3. Design Philosophy

TradeDrift follows the principle:

> Build the simplest architecture that satisfies today's requirements while preserving a clear path for future evolution.

The data structures were selected based on expected workload, implementation clarity, and maintainability -- not solely on asymptotic complexity.

Performance optimizations should be driven by measurement rather than assumptions.

---

# 4. V1 Design Decision

TradeDrift V1 uses a hybrid in-memory data structure optimized for simplicity, correctness, and low latency.

The ordered price index is implemented using a **sorted slice** rather than a balanced tree.

Reasons for this decision:

- The expected number of active price levels per market is relatively small.
- A sorted slice provides O(1) best-price lookup.
- The implementation is significantly simpler than maintaining a balanced tree.
- It is easier to debug, test, and explain.
- The architecture allows the price index to be replaced by a B-Tree in future versions without changing the Matching Engine logic.

This decision was made after evaluating the expected workload rather than choosing the most complex data structure available.

---

# 5. Decimal Type

All price and quantity values use:

```
github.com/shopspring/decimal
```

Float64 is explicitly rejected.

Reason:

```
float64:   0.1 + 0.2 = 0.30000000000000004
decimal:   0.1 + 0.2 = 0.3
```

Float64 arithmetic loses precision in financial calculations.

A matching engine that rounds incorrectly loses user funds or produces incorrect trade prices.

Every price, quantity, and total in this system is a `decimal.Decimal`.

---

# 6. Hybrid Data Structure Design

No single data structure satisfies all performance requirements of an exchange.

TradeDrift combines three specialized structures per side, working together as a hybrid design.

```
                 Order Book

                      |

        +-------------+-------------+

        v                           v

     Bid Side                   Ask Side

        |                           |

        +-- Price Level Map          +-- Price Level Map

        +-- Sorted Price Index       +-- Sorted Price Index

        +-- Order Index              +-- Order Index

                      |

                      v

                 Price Level

                      |

                      v

           FIFO Doubly Linked List
```

Each structure is optimized for a different operation.

---

## Why Three Structures?

| Structure | Purpose |
|---|---|
| Sorted Price Index | Maintain price ordering and provide O(1) best-price lookup |
| Price Level Map | Locate a price level in constant time by exact price |
| Order Index | Locate and cancel any order in constant time by order ID |

No single structure solves all three:

- A pure sorted map gives O(log n) best price and O(log n) lookup, but no O(1) cancel by ID.
- A pure hash map gives O(1) price lookup but no sorted best-price access.
- A sorted slice alone has no O(1) cancel by ID.

The three-field hybrid combines them so each operation hits the right structure.

---

## Full Structure Overview

```
OrderBook
    |
    +-- BidSide  (Side)
    |       |
    |       +-- priceLevels   map[Decimal]*PriceLevel
    |       +-- sortedPrices  []Decimal  (descending)
    |       +-- orderIndex    map[UUID]*OrderNode
    |
    +-- AskSide  (Side)
            |
            +-- priceLevels   map[Decimal]*PriceLevel
            +-- sortedPrices  []Decimal  (ascending)
            +-- orderIndex    map[UUID]*OrderNode


PriceLevel
    |
    +-- price     Decimal
    +-- orders    *list.List  (doubly-linked list of *OrderNode)
    +-- totalQty  Decimal


OrderNode
    |
    +-- orderID      UUID
    +-- userID       UUID
    +-- marketID     string
    +-- side         SideType
    +-- price        Decimal
    +-- originalQty  Decimal
    +-- remainingQty Decimal
    +-- timestamp    time.Time
    +-- element      *list.Element
```

---

# 7. OrderNode

An OrderNode represents one resting order inside the Order Book.

```go
type OrderNode struct {
    orderID      uuid.UUID
    userID       uuid.UUID
    marketID     string
    side         SideType        // BUY or SELL
    price        decimal.Decimal
    originalQty  decimal.Decimal
    remainingQty decimal.Decimal
    timestamp    time.Time
    element      *list.Element   // pointer to position in PriceLevel.orders
}
```

---

## Fields

### orderID

UUIDv7. Generated by the Order Service before the order is persisted.

Used as the key in `Side.orderIndex` for O(1) lookup.

---

### userID

UUIDv7. Identifies the user who placed the order.

Included in TradeExecuted and OrderCancelled events.

---

### marketID

The trading pair. Example: `BTC-USDT`

---

### side

`BUY` or `SELL`.

---

### price

The limit price for this order. Must be a positive decimal.

---

### originalQty

The quantity requested at order creation. Never modified after insertion.

---

### remainingQty

The quantity not yet filled. Starts equal to `originalQty`.

Reduced on partial fill. When it reaches zero, the order is fully filled and removed.

---

### timestamp

The time the order was added to the book.

Determines time priority when two orders share the same price. Earlier timestamp wins.

---

### element

A pointer to this node's position inside `PriceLevel.orders` (a `*list.List`).

This pointer is what makes cancel O(1).

Without it, cancel requires scanning the linked list to find the node: O(n).

With it, the node removes itself directly: O(1).

---

## Why Not Store Balance or User Details?

The Order Book is a matching structure, not a financial ledger.

It contains only what is needed to determine execution priority and produce trade events.

Balance checks, fund reservations, and user validation are handled by the Order Service and Wallet Service before the order reaches the Matching Engine.

---

# 8. PriceLevel

A PriceLevel groups all resting orders at the same price into a FIFO queue.

```go
type PriceLevel struct {
    price    decimal.Decimal
    orders   *list.List      // Go stdlib doubly-linked list
    totalQty decimal.Decimal // sum of remainingQty across all orders at this level
}
```

---

## Fields

### price

The exact price of this level. Used as the key in `Side.priceLevels`.

---

### orders

A doubly-linked list (`container/list` from Go stdlib).

Each element is a pointer to an `OrderNode`.

- Orders are appended to the back on insertion (FIFO).
- Orders are consumed from the front during matching.
- Orders are removed by pointer during cancellation (O(1)).

---

### totalQty

The sum of `remainingQty` across all orders at this price level.

Maintained incrementally:

- On insert: `totalQty += order.remainingQty`
- On partial fill: `totalQty -= filledAmount`
- On cancel: `totalQty -= order.remainingQty`

Reading `totalQty` is O(1) because it is maintained incrementally, not recomputed on read.

Used for order book depth snapshots pushed to Redis after each match.

---

## Lifecycle

A PriceLevel is created when the first order at a price is inserted.

A PriceLevel is destroyed when the last order at a price is removed.

An empty PriceLevel must never remain in the book.

---

# 9. Side

A Side represents one half of the Order Book.

Bid side and ask side are both instances of the same structure.

```go
type Side struct {
    priceLevels  map[string]*PriceLevel  // price.String() -> PriceLevel
    sortedPrices []decimal.Decimal       // bid: descending, ask: ascending
    orderIndex   map[uuid.UUID]*OrderNode
}
```

---

## priceLevels

A hash map from price to PriceLevel.

Provides O(1) access to the price level for a specific price.

Used during insert (check if a level exists), cancel (update totalQty), and match (access the best level).

---

## sortedPrices

A slice of prices maintained in sorted order.

- Bid side: descending. `sortedPrices[0]` is always the best bid.
- Ask side: ascending. `sortedPrices[0]` is always the best ask.

Best price lookup is O(1): read index 0.

Insert a new price level: binary search to find insertion point, then insert into slice.

Remove an empty price level: binary search to find, then remove from slice.

The slice shift on insert and remove is O(n) in the worst case. For V1 this is acceptable -- see Section 4.

---

## orderIndex

A hash map from `orderID` to `*OrderNode`.

Provides O(1) lookup of any order in the book by ID.

Used for cancel (look up node, then use `node.element` to remove from linked list) and find-by-ID.

---

# 10. OrderBook

The top-level container for a single trading pair.

```go
type OrderBook struct {
    marketID string
    bids     Side
    asks     Side
}
```

One OrderBook exists per trading pair.

One Market Engine owns exactly one OrderBook.

No two Market Engines share an OrderBook.

---

# 11. Operation Pseudocode

---

## Insert Order

```
Insert(order OrderNode):

    side = order.side == BUY ? bids : asks

    level = side.priceLevels[order.price]
    if level == nil:
        level = new PriceLevel(price = order.price)
        side.priceLevels[order.price] = level
        binary insert order.price into side.sortedPrices

    order.element = level.orders.PushBack(&order)
    level.totalQty += order.remainingQty
    side.orderIndex[order.orderID] = &order
```

Complexity: O(log n) for new price level. O(1) for existing price level.

---

## Cancel Order

```
Cancel(orderID UUID):

    node = side.orderIndex[orderID]
    if node == nil:
        return  // already removed or unknown

    level = side.priceLevels[node.price]
    level.totalQty -= node.remainingQty
    level.orders.Remove(node.element)
    delete side.orderIndex[orderID]

    if level.orders.Len() == 0:
        delete side.priceLevels[node.price]
        binary search and remove node.price from side.sortedPrices
```

Complexity: O(1) for the cancel. O(n) slice shift if the price level becomes empty.

---

## Execute Best Order (during matching)

```
ExecuteBest(side Side) -> *OrderNode:

    if len(side.sortedPrices) == 0:
        return nil

    bestPrice = side.sortedPrices[0]
    level = side.priceLevels[bestPrice]
    return level.orders.Front().Value.(*OrderNode)
```

Complexity: O(1).

---

## Partial Fill

```
PartialFill(node *OrderNode, filledQty Decimal):

    node.remainingQty -= filledQty
    level = side.priceLevels[node.price]
    level.totalQty -= filledQty

    // node stays in place
    // queue position and timestamp are unchanged
    // no re-insert
```

Complexity: O(1).

---

## Full Fill

```
FullFill(node *OrderNode):

    level = side.priceLevels[node.price]
    level.totalQty -= node.remainingQty
    level.orders.Remove(node.element)
    delete side.orderIndex[node.orderID]

    if level.orders.Len() == 0:
        delete side.priceLevels[node.price]
        binary search and remove node.price from side.sortedPrices
```

Complexity: O(1) for the fill. O(n) slice shift if the price level becomes empty.

---

## Get Depth Snapshot

```
GetDepth(depthLevels int) -> DepthSnapshot:

    bids = []DepthLevel{}
    for i = 0 to min(depthLevels, len(bids.sortedPrices)):
        price = bids.sortedPrices[i]
        level = bids.priceLevels[price]
        append { price, level.totalQty } to bids

    asks = []DepthLevel{}
    for i = 0 to min(depthLevels, len(asks.sortedPrices)):
        price = asks.sortedPrices[i]
        level = asks.priceLevels[price]
        append { price, level.totalQty } to asks

    return DepthSnapshot{ marketID, bids, asks, timestamp }
```

Complexity: O(d) where d = depth levels requested.

Called by the Publisher Layer after every match to push to Redis.

---

# 12. Operation Complexity

| Operation | Complexity | Reason |
|---|---|---|
| Get Best Bid | O(1) | First element of the sorted slice |
| Get Best Ask | O(1) | First element of the sorted slice |
| Find Price Level | O(1) | HashMap lookup by exact price |
| Find Order by ID | O(1) | Order Index lookup |
| Insert (existing price level) | O(1) | HashMap lookup + linked list append |
| Insert (new price level) | O(log n) search + O(n) shift | Binary search into sorted slice + slice element shift |
| Cancel Order | O(1) | Order Index lookup + linked list pointer remove |
| Remove Empty Price Level | O(log n) search + O(n) shift | Binary search + slice element shift |
| Execute Best Order | O(1) | Front of best price level linked list |
| Partial Fill | O(1) | Modify remainingQty in-place, no re-insert |
| Full Fill | O(1) + O(n) if level empty | Linked list remove; O(n) only if level becomes empty |
| Get Depth Snapshot | O(d) | Iterate top d price levels |

> **Note:** Insert and Remove include an O(n) slice shift in the worst case. The number of active price levels per market in V1 is expected to remain small (tens to low hundreds). This cost is acceptable. Profiling should guide future optimizations rather than premature complexity.

---

# 13. Future Evolution

The ordered price index is an internal implementation detail of the `Side` struct.

If performance measurements show that maintaining the sorted slice becomes a bottleneck, it can be replaced with a B-Tree or another ordered index without changing:

- Matching algorithms
- Order Book behavior
- Event processing
- Market Engine architecture

This keeps the implementation flexible while avoiding unnecessary complexity in V1.

---

# 14. Alternatives Considered

---

## Balanced Trees (AVL / Red-Black Tree)

Rejected for V1.

Reason:

- More complex implementation.
- Additional maintenance overhead.
- No measurable benefit for the expected workload.

---

## B-Tree

Considered.

Advantages:

- O(log n) insertion and deletion.
- Scales to large numbers of price levels.
- Good cache locality.

Rejected for V1 because:

- Introduces additional implementation complexity.
- The expected number of active price levels does not justify the overhead.

The design allows replacing the Sorted Price Index with a B-Tree in future versions if profiling indicates a bottleneck.

---

## Redis as Primary Order Book

Rejected.

Reason:

- Network round-trip on every match introduces unacceptable latency.
- Redis is used only as a read replica for the UI. See 09_Redis_Projection.md.

---

# 15. What the Order Book Does Not Store

The Order Book is not a ledger.

It does not store:

- User balances
- Reserved funds
- Wallet information
- Order history (filled orders are removed immediately)
- Trade records (published as events, not stored here)

These belong to:

- Wallet Service: balances and reservations
- Order Service: order history and status
- Settlement Service: trade records

---

# 16. Recovery Behaviour

On restart, the Order Book starts empty.

It is reconstructed by replaying Kafka events from the stored checkpoint offset.

| Event | Operation |
|---|---|
| `OrderCreated` | `Insert(order)` |
| `OrderCancelRequested` | `Cancel(orderID)` |
| `TradeExecuted` (partial fill) | `PartialFill(node, filledQty)` |
| `TradeExecuted` (full fill) | `FullFill(node)` |

After replay, the book state is identical to what it was before the restart.

See `08_Recovery_Strategy.md` for full recovery sequencing.

---

# 17. References

- 03_Order_Book.md
- 05_Matching_Algorithm.md
- 07_Concurrency_Model.md
- 08_Recovery_Strategy.md
- 09_Redis_Projection.md
