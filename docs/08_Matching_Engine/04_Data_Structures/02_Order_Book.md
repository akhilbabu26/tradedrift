# TradeDrift Matching Engine — OrderBook

**Document:** 04_Data_Structures / 02_Order_Book.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document defines the `OrderBook` struct — the top-level container that holds the complete live state of one trading pair.

---

# 2. Struct Definition

```go
type OrderBook struct {
    marketID string
    bids     Side
    asks     Side
}
```

---

# 3. Fields

## marketID

The trading pair this book represents.

Example: `BTC-USDT`

Set at construction time. Never changes during the lifetime of the book.

Used in published events (`TradeExecuted`, `OrderCancelled`) and in depth snapshots pushed to Redis.

---

## bids

The buy side of the book.

Type: `Side`

Contains all resting BUY limit orders.

Sorted in **descending** price order — the highest bid is always at index 0.

---

## asks

The sell side of the book.

Type: `Side`

Contains all resting SELL limit orders.

Sorted in **ascending** price order — the lowest ask is always at index 0.

---

# 4. Responsibilities

The OrderBook is responsible for:

- Maintaining all resting BUY limit orders (bids).
- Maintaining all resting SELL limit orders (asks).
- Providing the bid/ask spread at any moment.
- Providing a depth snapshot on demand.

The OrderBook is NOT responsible for:

- Matching logic. That belongs to the Matching Core (`matcher/`).
- Publishing events. That belongs to the Publisher Layer.
- Storing filled or cancelled orders. Those are removed immediately.

---

# 5. Ownership

```
Market Engine
      |
      v
   OrderBook        <-- one per trading pair
      |
      +-- bids (Side)
      +-- asks (Side)
```

One Market Engine owns exactly one OrderBook.

One OrderBook belongs to exactly one Market Engine.

No two Market Engines share an OrderBook.

No goroutine other than the owning Market Engine's Event Loop reads or writes the OrderBook.

---

# 6. Market Isolation

Each trading pair has its own independent OrderBook.

State never crosses between books.

```
BTC-USDT Engine  -->  BTC-USDT OrderBook
ETH-USDT Engine  -->  ETH-USDT OrderBook
SOL-USDT Engine  -->  SOL-USDT OrderBook
```

This means:

- An event for BTC-USDT never affects the ETH-USDT book.
- Concurrent matching across markets requires no locks or coordination.
- A crash in one market's engine does not corrupt another market's book.

---

# 7. Spread

The current spread is derived from bids and asks at any moment:

```
bestBid = bids.sortedPrices[0]
bestAsk = asks.sortedPrices[0]
spread  = bestAsk - bestBid
```

A negative spread means the book is crossable — there is a match available.

The Matching Core checks for a crossable book on every incoming order.

---

# 8. Lifecycle

```
Market Engine starts
      |
      v
OrderBook created (empty bids, empty asks)
      |
      v
Kafka replay (recovery) or live event processing
      |
      v
Orders accumulate, matches occur
      |
      v
Market Engine stops
      |
      v
OrderBook discarded (ephemeral state)
```

The OrderBook is never persisted directly.

Recovery rebuilds it from Kafka events. See `08_Recovery_Strategy.md`.

---

# 9. References

- 01_Overview.md — hybrid architecture
- 05_Side.md — Side struct detail
- 07_Algorithms.md — Insert, Cancel, Match pseudocode
- 08_Memory_Model.md — ownership and pointer graph
