# TradeDrift Matching Engine — OrderBook

**Document:** 04_Data_Structures / 02_Order_Book.md
**Service:** Matching Engine
**Version:** V1.0
**Last Updated:** July 2026

---

# 1. Purpose

This document defines the `OrderBook` struct — the top-level container for one trading pair's complete live state.

---

# 2. Struct Definition

```go
type OrderBook struct {
    marketID   string
    bids       Side
    asks       Side
    orderIndex map[uuid.UUID]*OrderNode
}
```

> **Architectural decision:** `orderIndex` lives on `OrderBook`, not on `Side`. This gives a single lookup for any cancel regardless of which side the order rests on. See `06_Order_Index.md`.

---

# 3. Fields

### marketID

The trading pair this book represents (e.g. `BTC-USDT`). Set at construction. Never changes.

Carried in all published events (`TradeExecuted`, `OrderCancelled`) and in Redis depth snapshots.

---

### bids

The buy side. Type: `Side`.

Contains all resting BUY limit orders, sorted highest-price-first.

`bids.sortedPrices[0]` is always the best (highest) bid.

---

### asks

The sell side. Type: `Side`.

Contains all resting SELL limit orders, sorted lowest-price-first.

`asks.sortedPrices[0]` is always the best (lowest) ask.

---

### orderIndex

```go
orderIndex map[uuid.UUID]*OrderNode
```

A single hash map covering **both sides** of the book.

- Key: `orderID` (UUIDv7)
- Value: pointer to the `OrderNode`

Used by every cancel operation. The `node.side` field on the retrieved node tells the algorithm which side to use for subsequent PriceLevel operations.

This replaces the previous design of one `orderIndex` per `Side`, which required checking both maps on every cancel.

---

# 4. Responsibilities

**OrderBook is responsible for:**
- Maintaining all resting BUY limit orders (bids).
- Maintaining all resting SELL limit orders (asks).
- Providing a single O(1) order lookup by ID via `orderIndex`.
- Providing the bid/ask spread at any moment.
- Providing a depth snapshot on demand.

**OrderBook is NOT responsible for:**
- Matching logic → belongs to the Matching Core.
- Publishing events → belongs to the Publisher Layer.
- Storing filled or cancelled orders → removed immediately.

---

# 5. Ownership

```
Market Engine (goroutine)
        │
        ▼
    OrderBook                ← one per trading pair
        │
        ├── bids (Side)
        ├── asks (Side)
        └── orderIndex       ← shared across both sides
```

One Market Engine owns exactly one OrderBook. No two Market Engines share an OrderBook. No goroutine other than the owning Market Engine's Event Loop reads or writes the OrderBook.

---

# 6. Market Isolation

```
BTC-USDT Engine  ──▶  BTC-USDT OrderBook
ETH-USDT Engine  ──▶  ETH-USDT OrderBook
SOL-USDT Engine  ──▶  SOL-USDT OrderBook
```

Events for one market never affect another market's book. Concurrent matching across markets requires no locks or coordination.

---

# 7. Spread

```
bestBid  =  bids.sortedPrices[0]
bestAsk  =  asks.sortedPrices[0]
spread   =  bestAsk - bestBid
```

When `spread <= 0`, the book is crossable — a match is available. The Matching Core checks this on every incoming order.

---

# 8. Lifecycle

```
Market Engine starts
        │
        ▼
OrderBook created  (empty bids, empty asks, empty orderIndex)
        │
        ▼
Recovery replay  (OrderCreated and OrderCancelRequested events replayed
                  through the matching algorithm in suppressed mode)
        │
        ▼
Live event processing
        │
        ▼
Market Engine stops
        │
        ▼
OrderBook discarded  (ephemeral — rebuilt from Kafka on next start)
```

The OrderBook is never persisted directly. See `08_Recovery_Strategy.md`.

---

# 9. References

- `01_Overview.md` — hybrid architecture overview
- `05_Side.md` — Side struct
- `06_Order_Index.md` — orderIndex detail
- `07_Algorithms.md` — Insert, Cancel, Match pseudocode
- `08_Memory_Model.md` — ownership and pointer graph

