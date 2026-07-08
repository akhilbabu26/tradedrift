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
Insert(book *OrderBook, order OrderNode):

    side = &book.bids  if order.side == BUY
           &book.asks  if order.side == SELL

    level = side.priceLevels[order.price.String()]

    if level == nil:
        level = &PriceLevel{price: order.price, orders: list.New(), totalQty: 0}
        side.priceLevels[order.price.String()] = level
        idx = binarySearchInsertIndex(side.sortedPrices, order.price)
        side.sortedPrices = insertAt(side.sortedPrices, idx, order.price)

    order.element = level.orders.PushBack(&order)
    level.totalQty += order.remainingQty
    book.orderIndex[order.orderID] = &order
```

| Case | Complexity |
| --- | --- |
| Price level already exists | O(1) |
| New price level | O(log n) binary search + O(n) slice insert |

---

# 3. Cancel

Removes an order by ID. Called when `OrderCancelRequested` is received.

```
Cancel(book *OrderBook, orderID uuid.UUID):

    node = book.orderIndex[orderID]    // single lookup — O(1)
    if node == nil:
        return  // not in book — idempotent, safe to call twice

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

The full matching loop the Matching Core runs on every incoming order.

```
Match(book *OrderBook, incoming OrderNode, mode Mode) -> []MatchResult:

    results = []MatchResult{}
    oppSide = &book.asks  if incoming.side == BUY
              &book.bids  if incoming.side == SELL

    for incoming.remainingQty > 0:

        best = ExecuteBest(oppSide)
        if best == nil:
            break  // no liquidity

        if not crossable(incoming, best):
            break  // prices do not overlap

        fillQty  = min(incoming.remainingQty, best.remainingQty)
        trade_id = newUUIDv7()    // generated in-memory — no DB round-trip

        results = append(results, MatchResult{
            tradeID:  trade_id,
            makerID:  best.orderID,
            takerID:  incoming.orderID,
            buyerID:  buyerOf(incoming, best),
            sellerID: sellerOf(incoming, best),
            price:    best.price,
            quantity: fillQty,
        })

        if fillQty == best.remainingQty:
            FullFill(book, oppSide, best)
        else:
            PartialFill(oppSide, best, fillQty)

        incoming.remainingQty -= fillQty

    if incoming.remainingQty > 0 and incoming.type == LIMIT:
        Insert(book, incoming)

    if mode == RECOVERY:
        return nil    // output suppressed — trades already settled

    return results
```

> **Recovery mode:** During Kafka replay on restart, the Match Loop runs in `RECOVERY` mode. All results are discarded — no `TradeExecuted` events published, no Redis snapshots pushed. The ME exits recovery mode when it reaches the checkpoint offset.

---

# 9. References

- `03_Order_Node.md` — OrderNode fields
- `04_Price_Level.md` — PriceLevel and linked list
- `05_Side.md` — sortedPrices, priceLevels
- `06_Order_Index.md` — orderIndex (book-level)
- `09_Complexity_Analysis.md` — full complexity table
