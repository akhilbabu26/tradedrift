# TradeDrift Matching Engine — Algorithms

**Document:** 04_Data_Structures / 07_Algorithms.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document defines the pseudocode for every operation performed on the Order Book data structures.

This is the implementation blueprint for the Matching Core and Publisher Layer.

---

# 2. Insert

Adds a resting limit order to the correct side of the book.

Market orders are never inserted — they match immediately and any remainder is discarded.

```
Insert(book *OrderBook, order OrderNode):

    side = select side:
        if order.side == BUY:  side = &book.bids
        if order.side == SELL: side = &book.asks

    // Check if a price level already exists
    level = side.priceLevels[order.price.String()]

    if level == nil:
        // First order at this price -- create new level
        level = &PriceLevel{
            price:    order.price,
            orders:   list.New(),
            totalQty: 0,
        }
        side.priceLevels[order.price.String()] = level

        // Insert price into sorted index at correct position
        idx = binarySearchInsertIndex(side.sortedPrices, order.price, side)
        side.sortedPrices = insertAt(side.sortedPrices, idx, order.price)

    // Add order to back of queue (FIFO)
    order.element = level.orders.PushBack(&order)

    // Update aggregate quantity
    level.totalQty = level.totalQty.Add(order.remainingQty)

    // Register in order index for O(1) cancel
    side.orderIndex[order.orderID] = &order
```

**Complexity:**

| Case | Cost |
|---|---|
| Price level already exists | O(1) |
| New price level | O(log n) binary search + O(n) slice insert |

---

# 3. Cancel

Removes an order from the book by its ID.

Called when the Matching Engine receives an `OrderCancelRequested` event.

```
Cancel(book *OrderBook, orderID uuid.UUID):

    // Determine which side owns this order
    node = book.bids.orderIndex[orderID]
    if node == nil:
        node = book.asks.orderIndex[orderID]
    if node == nil:
        return  // order not in book -- already filled or unknown ID
                // idempotent: no-op if called twice

    side = selectSide(node.side, book)

    level = side.priceLevels[node.price.String()]

    // Update aggregate quantity
    level.totalQty = level.totalQty.Sub(node.remainingQty)

    // Remove from linked list using the back-pointer -- O(1)
    level.orders.Remove(node.element)

    // Deregister from index
    delete(side.orderIndex, orderID)

    // Destroy price level if now empty
    if level.orders.Len() == 0:
        delete(side.priceLevels, node.price.String())
        idx = binarySearch(side.sortedPrices, node.price)
        side.sortedPrices = removeAt(side.sortedPrices, idx)
```

**Complexity:**

| Step | Cost |
|---|---|
| orderIndex lookup | O(1) |
| priceLevels lookup | O(1) |
| totalQty update | O(1) |
| list.Remove | O(1) |
| orderIndex delete | O(1) |
| Remove empty level (if triggered) | O(log n) search + O(n) shift |

---

# 4. Execute Best Order

Returns the front order from the best price level without removing it.

Called by the Matching Core at the start of each fill iteration.

```
ExecuteBest(side *Side) -> *OrderNode:

    if len(side.sortedPrices) == 0:
        return nil  // book is empty on this side

    bestPrice = side.sortedPrices[0]
    level = side.priceLevels[bestPrice.String()]

    element = level.orders.Front()
    if element == nil:
        // Should never happen -- empty levels are destroyed immediately
        panic("invariant violated: empty price level in sortedPrices")

    return element.Value.(*OrderNode)
```

**Complexity:** O(1)

---

# 5. Partial Fill

Reduces the `remainingQty` of a resting order in-place.

The order stays in the book at its current queue position.

```
PartialFill(side *Side, node *OrderNode, filledQty decimal.Decimal):

    // Reduce the order's remaining quantity
    node.remainingQty = node.remainingQty.Sub(filledQty)

    // Reduce the price level's aggregate quantity
    level = side.priceLevels[node.price.String()]
    level.totalQty = level.totalQty.Sub(filledQty)

    // node.element is unchanged -- order stays at same queue position
    // node.timestamp is unchanged -- time priority preserved
    // orderIndex entry is unchanged -- node is still in the book
```

**Complexity:** O(1)

**Critical rule:** The order is never removed and re-inserted. Re-inserting would place it at the back of the queue, losing its time priority. This violates Price-Time Priority.

---

# 6. Full Fill

Removes a resting order that has been completely consumed.

```
FullFill(side *Side, node *OrderNode):

    level = side.priceLevels[node.price.String()]

    // Update aggregate quantity
    level.totalQty = level.totalQty.Sub(node.remainingQty)

    // Remove from linked list -- O(1)
    level.orders.Remove(node.element)

    // Deregister from index
    delete(side.orderIndex, node.orderID)

    // Destroy price level if now empty
    if level.orders.Len() == 0:
        delete(side.priceLevels, node.price.String())
        idx = binarySearch(side.sortedPrices, node.price)
        side.sortedPrices = removeAt(side.sortedPrices, idx)
```

**Complexity:**

| Step | Cost |
|---|---|
| priceLevels lookup | O(1) |
| totalQty update | O(1) |
| list.Remove | O(1) |
| orderIndex delete | O(1) |
| Remove empty level (if triggered) | O(log n) search + O(n) shift |

---

# 7. Get Depth Snapshot

Reads the top N price levels from each side for the Redis projection.

Called by the Publisher Layer after every match.

```
GetDepth(book *OrderBook, depth int) -> DepthSnapshot:

    bidLevels = []DepthLevel{}
    limit = min(depth, len(book.bids.sortedPrices))
    for i = 0 to limit:
        price = book.bids.sortedPrices[i]
        level = book.bids.priceLevels[price.String()]
        bidLevels = append(bidLevels, DepthLevel{price, level.totalQty})

    askLevels = []DepthLevel{}
    limit = min(depth, len(book.asks.sortedPrices))
    for i = 0 to limit:
        price = book.asks.sortedPrices[i]
        level = book.asks.priceLevels[price.String()]
        askLevels = append(askLevels, DepthLevel{price, level.totalQty})

    return DepthSnapshot{
        marketID:  book.marketID,
        bids:      bidLevels,
        asks:      askLevels,
        timestamp: time.Now(),
    }
```

**Complexity:** O(d) where d = depth levels requested

`totalQty` is pre-aggregated on the PriceLevel — no inner loop over orders.

---

# 8. Match Loop (overview)

The full matching loop that the Matching Core runs on every incoming order.

```
Match(book *OrderBook, incoming OrderNode) -> []MatchResult:

    results = []

    oppositeSide = selectOppositeSide(incoming.side, book)

    for incoming.remainingQty > 0:

        best = ExecuteBest(oppositeSide)

        if best == nil:
            break  // no liquidity on opposite side

        if not crossable(incoming, best):
            break  // prices do not overlap

        fillQty = min(incoming.remainingQty, best.remainingQty)

        trade_id = newUUIDv7()  // generated here, in memory, at match time

        result = MatchResult{
            tradeID:   trade_id,
            makerID:   best.orderID,
            takerID:   incoming.orderID,
            buyerID:   buyerOf(incoming, best),
            sellerID:  sellerOf(incoming, best),
            price:     best.price,
            quantity:  fillQty,
        }
        results = append(results, result)

        // Update resting order
        if fillQty == best.remainingQty:
            FullFill(oppositeSide, best)
        else:
            PartialFill(oppositeSide, best, fillQty)

        // Update incoming order
        incoming.remainingQty -= fillQty

    // If incoming has remaining qty and is a limit order: Insert it
    if incoming.remainingQty > 0 and incoming.type == LIMIT:
        Insert(book, incoming)

    // If incoming is a market order with remaining qty: discard (IOC)

    return results
```

**Note:** `trade_id = newUUIDv7()` — generated in application code in memory at match time. No database round-trip. Consistent with the ID Correlation Standard.

---

# 9. References

- 03_Order_Node.md — OrderNode fields
- 04_Price_Level.md — PriceLevel and linked list
- 05_Side.md — sortedPrices, priceLevels, orderIndex
- 06_Order_Index.md — O(1) cancel detail
- 09_Complexity_Analysis.md — full complexity table
