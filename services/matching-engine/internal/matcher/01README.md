# `internal/matcher` — Core Matching Algorithms

**Package:** `matcher`
**Service:** Matching Engine
**Last Updated:** August 2026

---

## 1. What This Package Does

This package contains every algorithm that operates on the Order Book.
It is the **execution engine** of TradeDrift — the code that decides who
trades with whom, at what price, and in what quantity.

It has zero infrastructure dependencies. No Kafka, no Redis, no Postgres,
no HTTP, no goroutines. It is a collection of pure functions that take an
OrderBook and a node as input, modify the book in memory, and return results.

---

## 2. Purpose

The `matcher` package answers three questions:

| Question | Function |
| :--- | :--- |
| Can this incoming order match against the resting book? | `Match()` |
| How do I add a resting order to the book? | `Insert()` |
| How do I remove an order from the book? | `Cancel()` |

Everything else (`PartialFill`, `FullFill`, `ExecuteBest`, `GetDepth`) is
support logic that makes those three operations correct and efficient.

---

## 3. Files In This Package

| File | Purpose |
| :--- | :--- |
| `matcher.go` | All 7 public functions + all helper functions |
| `matcher_test.go` | 23 unit tests covering every scenario |
| `README.md` | This file |

---

## 4. Public Functions

### `Insert(book, node)`

Adds a resting LIMIT order to the correct side of the Order Book.

**What it does:**
1. Checks for duplicate order ID (defensive — skips if already present)
2. Finds or creates the `PriceLevel` for `node.Price`
3. Appends the node to the back of the level's FIFO queue: `level.Orders.PushBack(node)`
4. Saves the returned `*list.Element` back onto the node: `node.Element = ...`
5. Updates `level.TotalQty += node.RemainingQty`
6. Registers node in `book.OrderIndex[node.OrderID]`

**Why the Element pointer matters:**
The `*list.Element` saved at step 4 is a back-pointer from the node into
the linked list. Without it, Cancel would need to scan the entire list to
find the node (O(n)). With it, `list.Remove(node.Element)` removes the
node directly (O(1)).

**Critical rule:** The caller MUST pass a heap-allocated `*OrderNode`.
Never pass `&localVar` — the pointer is stored in both the list and
OrderIndex and outlives this call.

**Complexity:**
- Existing price level: O(1)
- New price level: O(log n) binary search + O(n) slice insert

---

### `Cancel(book, orderID)`

Removes a resting order from the book by its ID.

**What it does:**
1. Looks up the node: `book.OrderIndex[orderID]` — O(1)
2. If not found: returns nil (idempotent no-op — safe to call twice)
3. Subtracts `node.RemainingQty` from `level.TotalQty`
4. Removes from linked list: `level.Orders.Remove(node.Element)` — O(1)
5. Deletes from OrderIndex
6. If level is now empty: removes it from `priceLevels` map and `sortedPrices` slice
7. Returns the node so the caller can build an `OrderCancelled` event payload

**Why it returns the node:**
The Event Loop needs the node's `UserID`, `MarketID`, and `RemainingQty`
to build the `OrderCancelled` Kafka event. Returning the node avoids
storing that data anywhere else.

**Why it is idempotent:**
If the order was already filled (removed from OrderIndex by FullFill),
the lookup returns nil and Cancel does nothing. This handles the
cancel-vs-fill race condition at the book level without any extra state.

**Complexity:** O(1) for the cancel itself. O(n) for slice removal if the
price level becomes empty (rare).

---

### `Match(book, incoming, mode)`

The core matching loop. Processes one incoming order against the opposite
side of the book. Returns a slice of `Fill` — one per individual trade.

**What it does:**

```
while incoming.RemainingQty > 0:
    best = ExecuteBest(opposite side)
    if best == nil: break          (opposite side empty)
    if not crossable: break        (prices do not overlap)

    fillQty = min(incoming.remaining, best.remaining)
    tradeID = newUUID()            (in-memory, no DB)

    append Fill{
        price: best.Price,         (ALWAYS maker price)
        quantity: fillQty,
        maker: best, taker: incoming,
        buyer/seller derived from sides
    }

    if fillQty == best.remaining: FullFill(best)
    else:                         PartialFill(best, fillQty)

    incoming.RemainingQty -= fillQty

if LIMIT and remaining > 0: Insert(incoming)    (rest on book)
if MARKET and remaining > 0: do NOT insert      (IOC — discard)

if mode == RECOVERY: return nil                 (suppress output)
return fills
```

**Price-Time Priority:**
- Better price always executes first (enforced by `SortedPrices[0]`)
- Within the same price, earlier timestamp executes first (enforced by FIFO linked list)

**Maker price rule:**
The trade price is ALWAYS the resting order's (maker's) price — never
the incoming order's (taker's) price. A BUY @ 105 that matches a SELL @ 100
produces a trade at 100. The buyer gets a better price than asked; the
seller gets their exact asking price.

**RECOVERY mode:**
The algorithm runs identically in RECOVERY mode — the book is fully updated.
The only difference is `return nil` instead of `return fills`. This suppresses
output to the Publisher, preventing duplicate TradeExecuted events from being
published for fills that were already settled before the crash.

**One incoming order → multiple fills:**
If the incoming order sweeps through multiple price levels (e.g. BUY @ 105
matches SELL @ 100, then SELL @ 101, then SELL @ 102), Match produces
one `Fill` per level consumed. The entire sweep is returned in a single
`[]Fill` slice, which becomes one `MatchResult` on the Output Queue.

**Complexity:** O(number of fills produced)

---

### `ExecuteBest(side)`

Peeks at the front order of the best price level. Does NOT remove it.

```go
side.SortedPrices[0]           // best price — O(1)
side.PriceLevels[bestPrice]    // level — O(1)
level.Orders.Front()           // oldest order — O(1)
```

Returns nil if the side is empty. Used only inside `Match`.

---

### `PartialFill(side, node, filledQty)`

Reduces a resting order's remaining quantity in-place.

**Critical:** The node is NEVER removed and re-inserted.
Re-inserting would move it to the back of the queue — losing its
time priority. `RemainingQty` shrinks, `Element` and `Timestamp` never change.

Updates `level.TotalQty -= filledQty` to keep the depth snapshot accurate.

---

### `FullFill(book, side, node)`

Removes a resting order that has been completely consumed.

- `level.TotalQty -= node.RemainingQty`
- `list.Remove(node.Element)` — O(1)
- `delete(book.OrderIndex, node.OrderID)`
- If level empty: remove from map + sorted slice

---

### `GetDepth(book, depth)`

Reads the top-N price levels from both sides for the Redis depth snapshot.

```go
for i := 0; i < depth; i++:
    price = side.SortedPrices[i]        // O(1)
    qty   = side.PriceLevels[price].TotalQty  // O(1) — pre-aggregated
    append DepthLevel{price, qty}
```

Complexity: O(depth) — never iterates individual orders.
`TotalQty` is pre-aggregated on `PriceLevel` by Insert/PartialFill/FullFill/Cancel.

---

## 5. Helper Functions (internal)

| Function | Purpose |
| :--- | :--- |
| `crossable(incoming, best)` | Returns true if the two orders can trade |
| `getSide(book, side)` | Returns pointer to `book.Bids` or `book.Asks` |
| `getOppositeSide(book, side)` | Returns the opposite side for matching |
| `buyOrderOf(incoming, best)` | Returns the order ID of whichever is BUY side |
| `sellOrderOf(incoming, best)` | Returns the order ID of whichever is SELL side |
| `buyUserOf(incoming, best)` | Returns the user ID of the BUY side |
| `sellUserOf(incoming, best)` | Returns the user ID of the SELL side |
| `binarySearchInsertIndex(side, price)` | Finds insert position in sorted slice |
| `findPriceIndex(side, price)` | Finds removal position in sorted slice |
| `insertAt(prices, idx, price)` | Inserts price into sorted slice at index |
| `removeAt(prices, idx)` | Removes price from sorted slice at index |

---

## 6. The `crossable` Rule

```
MARKET order        → always crossable (crosses any available liquidity)
LIMIT BUY  @ price  → crossable when price >= best ask
LIMIT SELL @ price  → crossable when price <= best bid
```

A MARKET order always crosses because it has no price constraint — it
takes whatever is available until either fully filled or the opposite side
is empty (IOC). A LIMIT order only crosses if the prices actually overlap.

---

## 7. Mode — RECOVERY vs LIVE

The `Mode` type controls whether `Match` suppresses its output:

```go
type Mode int

const (
    ModeRecovery Mode = iota  // return nil
    ModeLive                  // return fills
)
```

**Why this exists:**
When the Matching Engine restarts, it must replay all historical Kafka
events through the same `Match()` function to rebuild the in-memory book.
But those fills were already settled before the crash — publishing them
again would double-settle trades.

`ModeRecovery` lets the exact same algorithm be used for both:
- **Startup replay** (rebuild book, discard fills)
- **Live processing** (rebuild book AND emit fills)

No separate "recovery algorithm" needed. No code duplication.

---

## 8. How It Works — Full Flow

```
Kafka Consumer
    │
    ▼ InputEvent{OrderCreated | OrderCancelRequested}
    │
MarketEngine.processEvent()  (event_loop.go)
    │
    ├─ OrderCreated
    │       │
    │       ├─ Build *OrderNode (heap-allocated)
    │       │
    │       ├─ validTickAndLot?
    │       │       No  → Cancel{reason: invalid_order_parameters}
    │       │       Yes ↓
    │       │
    │       └─ Match(book, node, mode)
    │               │
    │               ├─ Loop: ExecuteBest → crossable → Fill → PartialFill/FullFill
    │               │
    │               ├─ LIMIT remainder → Insert(book, node)
    │               │
    │               ├─ MARKET remainder → IOC (not inserted)
    │               │         ↓
    │               │   Cancel{reason: ioc_expired}
    │               │
    │               └─ returns []Fill (nil in RECOVERY)
    │
    ├─ OrderCancelRequested
    │       │
    │       └─ Cancel(book, orderID)
    │               │
    │               ├─ found  → Cancel{reason: user_requested}
    │               └─ not found → silent no-op (already filled)
    │
    ▼
MatchResult{fills, cancelResult, depthSnapshot, sourceOffset}
    │
    ▼
OutputQueue → Publisher
```

---

## 9. Test Coverage

23 unit tests in `matcher_test.go`:

| Category | Tests |
| :--- | :--- |
| Insert | Order index, price levels, bid sort (desc), ask sort (asc), duplicate prevention |
| Cancel | Full removal, level cleanup, nil for unknown ID, idempotency, level retained when others exist |
| Match — LIMIT | Full fill (BUY→SELL), full fill (SELL→BUY), no match (gap), partial maker, partial taker rests |
| Match — MARKET | Full fill, IOC partial fill, no liquidity (empty book) |
| Match — Special | Multi-level sweep, time priority, maker price rule, fill ID correctness |
| Match — Recovery | Returns nil but book state still correct |
| GetDepth | Empty book, top-N limit, TotalQty aggregation |

All 23 tests pass.

---

## 10. V1 Limitations and V2 Upgrade Path

### Current V1

| Aspect | V1 Behaviour |
| :--- | :--- |
| Price index | Sorted slice — O(1) best price, O(n) insert/remove for new levels |
| Supported order types | LIMIT and MARKET (IOC) only |
| Trade ID generation | `uuid.New()` (v4) — TODO replace with UUIDv7 |
| Fee calculation | Not present — Settlement Service handles fees |
| Stop orders | Not implemented |
| Iceberg orders | Not implemented |

### Future V2 Upgrades

**1. Replace sorted slice with B-Tree**

The sorted slice (`SortedPrices []decimal.Decimal`) is simple and fast
for small numbers of price levels. When markets have thousands of active
levels, slice insert/remove becomes O(n).

Upgrade: replace with a B-Tree (e.g. `github.com/google/btree`).
The `Match()` loop, `Insert()`, and `Cancel()` logic do NOT change —
only the two binary search helpers and the `Side` struct change.

**2. UUIDv7 for trade_id**

The current `uuid.New()` generates v4 (random). TradeDrift's ID standard
requires v7 (time-ordered). Once the platform/uuid package is available
as a dependency of the matching-engine module, replace:

```go
// Before (V1)
tradeID := uuid.New()

// After (V2)
tradeID := platformuuid.NewV7()
```

**3. Stop-Limit and Stop-Market orders**

Stop orders require a "trigger price" concept. When the last trade price
crosses the stop price, the stop order activates and enters the book as
a LIMIT or MARKET order. This requires:
- A stop order queue per market (separate from the active book)
- A trigger check after every fill: `if lastTradePrice >= stopPrice: activate`
- Adding `EventType = EventStopActivated` to the Event Loop

This does not change any existing matching logic.

**4. Iceberg (Reserve) orders**

Iceberg orders show only a partial quantity publicly (the "tip") and
replenish from a hidden reserve after the tip is filled. Requires:
- A `ReserveQty` field on `OrderNode`
- After `FullFill(best)`: if `best.ReserveQty > 0`, re-insert with qty = next tip
- Re-insertion gives it a NEW timestamp (back of queue for the new tip only)

---

## 11. What This Package Does NOT Do

- Does NOT read from Kafka — that is `../kafka/`
- Does NOT publish events — that is `../publisher/`
- Does NOT write to Redis — that is `../projection/`
- Does NOT write to Postgres — that is `../recovery/`
- Does NOT manage goroutines — that is `../market/`
- Does NOT validate auth or ownership — that is Order Service
- Does NOT calculate fees — that is Settlement Service
- Does NOT track order status (OPEN, FILLED) — that is Order Service
