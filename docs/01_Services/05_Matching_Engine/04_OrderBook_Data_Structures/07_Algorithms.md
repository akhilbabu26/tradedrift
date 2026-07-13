# TradeDrift Matching Engine — Algorithms

**Document:** 04_Data_Structures / 07_Algorithms.md
**Service:** Matching Engine
**Version:** V1.0
**Last Updated:** July 2026

---

# 1. Purpose

Pseudocode for every operation on the Order Book data structures. This is the implementation blueprint for the Matching Core and Publisher Layer.

---

# 2. Insert

Adds a resting limit order to the correct side. Market orders are never inserted — they match immediately and any remainder is discarded (IOC).

```
Insert(book *OrderBook, node *OrderNode):
    // IMPLEMENTATION NOTE: `node` MUST be heap-allocated by the caller.
    // Insert stores `node` in the linked list and in `orderIndex` — both
    // outlive this function call.  Do NOT pass the address of a local variable:
    //
    //   WRONG:  Insert(book, &localNode)   ← dangling pointer after caller returns
    //   RIGHT:  node := new(OrderNode)
    //           *node = incoming           ← copy fields to heap
    //           Insert(book, node)

    // Defensive check: prevent duplicate order ID insertion
    if book.orderIndex[node.orderID] != nil:
        log.Warn("duplicate order ID", "orderID", node.orderID)
        return

    side = &book.bids  if node.side == BUY
           &book.asks  if node.side == SELL

    level = side.priceLevels[node.price.String()]

    if level == nil:
        level = &PriceLevel{price: node.price, orders: list.New(), totalQty: 0}
        side.priceLevels[node.price.String()] = level
        idx = binarySearchInsertIndex(side.sortedPrices, node.price)
        side.sortedPrices = insertAt(side.sortedPrices, idx, node.price)

    node.element = level.orders.PushBack(node)
    level.totalQty += node.remainingQty
    book.orderIndex[node.orderID] = node
```

| Case | Complexity |
| --- | --- |
| Price level already exists | O(1) |
| New price level | O(log n) binary search + O(n) slice insert |

---

# 3. Cancel

Removes an order by ID. Called when `OrderCancelRequested` is received.

```
Cancel(book *OrderBook, orderID uuid.UUID) -> *OrderNode:
    // Returns the cancelled node so the caller can build an OrderCancelled payload.
    // Returns nil if the order is not in the book (already fully filled, or ID
    // was never seen). The nil return is the idempotent no-op path.

    node = book.orderIndex[orderID]    // single lookup — O(1)
    if node == nil:
        return nil  // not in book — idempotent, safe to call twice

    side = &book.bids  if node.side == BUY
           &book.asks  if node.side == SELL

    level = side.priceLevels[node.price.String()]
    level.totalQty -= node.remainingQty
    level.orders.Remove(node.element)    // O(1) via element pointer
    delete(book.orderIndex, orderID)

    if level.orders.Len() == 0:
        delete(side.priceLevels, node.price.String())
        idx = binarySearch(side.sortedPrices, node.price)
        side.sortedPrices = removeAt(side.sortedPrices, idx)

    return node   // caller reads node.remainingQty etc. to build OrderCancelled
```

| Step | Complexity |
| --- | --- |
| `orderIndex` lookup | O(1) |
| `priceLevels` lookup | O(1) |
| `list.Remove` | O(1) |
| `orderIndex` delete | O(1) |
| Remove empty level (if triggered) | O(log n) + O(n) shift |

---

# 4. Execute Best Order

Returns the front order from the best price level without removing it.

```
ExecuteBest(side *Side) -> *OrderNode:

    if len(side.sortedPrices) == 0:
        return nil

    bestPrice = side.sortedPrices[0]
    level     = side.priceLevels[bestPrice.String()]
    return level.orders.Front().Value.(*OrderNode)
```

**Complexity:** O(1)

---

# 5. Partial Fill

Reduces `remainingQty` in-place. Order stays in the book at its current position.

```
PartialFill(side *Side, node *OrderNode, filledQty decimal.Decimal):

    node.remainingQty -= filledQty
    level = side.priceLevels[node.price.String()]
    level.totalQty -= filledQty

    // node.element unchanged  — queue position preserved
    // node.timestamp unchanged — time priority preserved
```

**Complexity:** O(1)

> The node is never removed and re-inserted. Re-inserting would move it to the back of the queue, violating Price-Time Priority.

---

# 6. Full Fill

Removes a resting order that has been completely consumed.

```
FullFill(book *OrderBook, side *Side, node *OrderNode):

    level = side.priceLevels[node.price.String()]
    level.totalQty -= node.remainingQty
    level.orders.Remove(node.element)
    delete(book.orderIndex, node.orderID)

    if level.orders.Len() == 0:
        delete(side.priceLevels, node.price.String())
        idx = binarySearch(side.sortedPrices, node.price)
        side.sortedPrices = removeAt(side.sortedPrices, idx)
```

| Step | Complexity |
| --- | --- |
| `list.Remove` + `orderIndex` delete | O(1) |
| Remove empty level (if triggered) | O(log n) + O(n) shift |

---

# 7. Get Depth Snapshot

Reads the top N levels from each side for the Redis projection. Called after every match.

```
GetDepth(book *OrderBook, depth int) -> DepthSnapshot:

    bidLevels = []DepthLevel{}
    for i = 0; i < min(depth, len(book.bids.sortedPrices)); i++:
        price = book.bids.sortedPrices[i]
        level = book.bids.priceLevels[price.String()]
        bidLevels = append(bidLevels, {price, level.totalQty})

    askLevels = []DepthLevel{}
    for i = 0; i < min(depth, len(book.asks.sortedPrices)); i++:
        price = book.asks.sortedPrices[i]
        level = book.asks.priceLevels[price.String()]
        askLevels = append(askLevels, {price, level.totalQty})

    return DepthSnapshot{book.marketID, bidLevels, askLevels, time.Now()}
```

**Complexity:** O(d) where d = depth levels. `totalQty` is pre-aggregated — no inner loop.

---

# 8. Match Loop

Returns `[]Fill` — one element per individual trade. The Event Loop
(`07_Concurrency_Model.md §5`) wraps this slice into a `MatchResult` bundle
together with the `DepthSnapshot`, any IOC cancel, and the source Kafka offset.
The Match Loop itself never publishes events — it only returns data.

## Crossable Function

```
crossable(incoming *OrderNode, best *OrderNode) -> bool:

    if incoming.type == MARKET:
        return true                        // MARKET crosses any available liquidity;
                                           // `if best == nil: break` is the only stop
    if incoming.side == BUY:
        return incoming.price >= best.price
    else:                                  // SELL
        return incoming.price <= best.price
```

## Data Structures

### Fill (per-trade)

One `Fill` is produced for each individual trade within a sweep.

```go
type Fill struct {
    tradeID      uuid.UUID        // UUIDv7 — generated at match time, in-memory
    makerOrderID uuid.UUID        // resting order consumed by this fill
    takerOrderID uuid.UUID        // incoming order that triggered this fill
    buyOrderID   uuid.UUID        // order ID of the BUY-side party
    sellOrderID  uuid.UUID        // order ID of the SELL-side party
    buyerUserID  uuid.UUID        // user_id of the BUY-side party (from OrderNode.userID)
    sellerUserID uuid.UUID        // user_id of the SELL-side party (from OrderNode.userID)
    price        decimal.Decimal  // always the maker's price (05_Matching_Algorithm.md §9)
    quantity     decimal.Decimal  // min(incoming.remainingQty, best.remainingQty)
}
```

### MatchResult (per-event)

One `MatchResult` is sent to the Output Queue per processed input event.
Bundles all fills + depth snapshot + IOC cancel + source Kafka offset.

```go
type MatchResult struct {
    fills         []Fill           // 0..N fills; empty for resting-only events
    cancelResult  *OrderCancelled  // non-nil for cancels (user-requested, IOC expiry, or rejects)
    depthSnapshot DepthSnapshot    // book state after all fills (set by Event Loop)
    sourceOffset  int64            // Kafka input offset (set by Event Loop)
}
```

The `sourceOffset` field is what allows the Publisher to write exactly one
checkpoint per input event — no "isLast" flag needed, no per-fill counting.

## Fill Helper Functions

```
// Order IDs — which order is on the buy/sell side?
buyOrderOf(incoming, best *OrderNode) -> uuid.UUID:
    return incoming.orderID  if incoming.side == BUY  else  best.orderID

sellOrderOf(incoming, best *OrderNode) -> uuid.UUID:
    return incoming.orderID  if incoming.side == SELL  else  best.orderID

// User IDs — which user is on the buy/sell side?
buyUserOf(incoming, best *OrderNode) -> uuid.UUID:
    return incoming.userID   if incoming.side == BUY  else  best.userID

sellUserOf(incoming, best *OrderNode) -> uuid.UUID:
    return incoming.userID   if incoming.side == SELL  else  best.userID
```

## Match Loop Pseudocode

```
Match(book *OrderBook, incoming *OrderNode, mode Mode) -> []Fill:

    fills   = []Fill{}
    oppSide = &book.asks  if incoming.side == BUY
              &book.bids  if incoming.side == SELL

    for incoming.remainingQty > 0:

        best = ExecuteBest(oppSide)
        if best == nil:
            break  // opposite side empty

        if not crossable(incoming, best):
            break  // prices do not overlap

        fillQty  = min(incoming.remainingQty, best.remainingQty)
        trade_id = newUUIDv7()    // generated in-memory — no DB round-trip

        fills = append(fills, Fill{
            tradeID:      trade_id,
            makerOrderID: best.orderID,
            takerOrderID: incoming.orderID,
            buyOrderID:   buyOrderOf(incoming, best),
            sellOrderID:  sellOrderOf(incoming, best),
            buyerUserID:  buyUserOf(incoming, best),
            sellerUserID: sellUserOf(incoming, best),
            price:        best.price,
            quantity:     fillQty,
        })

        if fillQty == best.remainingQty:
            FullFill(book, oppSide, best)
        else:
            PartialFill(oppSide, best, fillQty)

        incoming.remainingQty -= fillQty

    // LIMIT: insert remainder as resting order.
    // `incoming` must already be heap-allocated by the caller (see §2 Insert note).
    if incoming.remainingQty > 0 and incoming.type == LIMIT:
        Insert(book, incoming)

    // MARKET IOC: remainder is NOT inserted.
    // The Event Loop detects incoming.remainingQty > 0 after Match returns
    // and builds an OrderCancelled {reason: "ioc_expired"} for the Publisher.
    // The Match Loop itself never publishes events.

    if mode == RECOVERY:
        return nil   // suppress output — fills already settled pre-crash

    return fills
```

> **Recovery mode:** During Kafka replay, the Match Loop runs identically but
> returns nil. No `TradeExecuted` events published, no Redis snapshots, no
> checkpoint writes. The market exits RECOVERY when it reaches the high-water
> mark recorded at consumer-group-join time.

---

# 9. References

- `03_Order_Node.md` — OrderNode fields
- `04_Price_Level.md` — PriceLevel and linked list
- `05_Side.md` — sortedPrices, priceLevels
- `06_Order_Index.md` — orderIndex (book-level)
- `09_Complexity_Analysis.md` — full complexity table
