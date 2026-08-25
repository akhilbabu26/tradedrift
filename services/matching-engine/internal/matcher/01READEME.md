# `internal/matcher` — Core Price-Time Matching Algorithm & Order Book Operations

**Package:** `matcher`  
**Service:** Matching Engine  
**Files Covered:** `matcher.go` (and `matcher_test.go`)  
**Documentation:** `02READEME.md`  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Purpose

The `internal/matcher` package contains the **pure, deterministic in-memory matching algorithm and state manipulation primitives** of the Matching Engine.

It implements the core exchange logic that governs how orders are entered, queued, matched, cancelled, and queried within an order book. Specifically, this package:
1. **Enforces Price-Time Priority (FIFO at Price Level)**: Highest bids and lowest asks match first; among identical prices, older orders execute first.
2. **Executes Multi-Level Sweeps & Fills**: Continuously matches incoming aggressive orders against opposing resting liquidity until the order is filled or price limits are reached.
3. **Guarantees Maker Pricing**: Trades always execute at the maker's (resting) price.
4. **Maintains Deterministic Trade IDs (UUID v5)**: Uses SHA-1 namespace hashing to ensure trade identifiers generated during live matching and crash-recovery replays are 100% identical.
5. **Provides Fast In-Place Partial Fills & $O(1)$ Cancellations**: Mutates order quantities in-place without queue demotion and removes cancelled orders via direct linked-list pointers.
6. **Produces Top-$N$ Depth Projections**: Extracts pre-aggregated book depths in $O(N)$ time for Redis publishing.

---

## 2. Core Problems Solved & Why This Package Is Needed

### 2.1 Preserving Strict Price-Time Priority Under High Frequency
In a financial exchange, fair matching requires that:
1. **Price Priority**: Better prices always execute before worse prices (Bids sorted descending, Asks sorted ascending).
2. **Time Priority**: When multiple orders exist at the same price, the order placed earlier must execute first.
- **How It Is Solved**:
  - `SortedPrices` slices maintain binary-search sorted price levels ($O(\log K)$).
  - Each `PriceLevel` contains a doubly linked list (`container/list.List`). New resting orders are appended to the back (`PushBack`), while matching always peeks the front (`Front`).

```
Price Level ($65,000.00) [Total Quantity: 4.5 BTC]
┌────────────────────────────────────────────────────────┐
│  Head (Oldest) ──► Order A (1.0 BTC, 10:00:01)         │  ◄── Matches First
│         │                                              │
│         ▼                                              │
│  Middle        ──► Order B (2.0 BTC, 10:00:02)         │
│         │                                              │
│         ▼                                              │
│  Tail (Newest) ──► Order C (1.5 BTC, 10:00:03)         │  ◄── Matches Last
└────────────────────────────────────────────────────────┘
```

### 2.2 In-Place Partial Fills Without Queue Demotion
When an incoming taker order only partially fills a resting maker order:
- **The Anti-Pattern**: Deleting the maker order and re-inserting the remaining quantity. This appends it to the back of the queue, unfairly penalizing the maker and violating time priority.
- **The Solution ([`PartialFill`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L110-L116))**: Subtracts `filledQty` directly from `node.RemainingQty` and `level.TotalQty` in-place. The node's `*list.Element` pointer and original arrival `Timestamp` remain completely unchanged.

### 2.3 $O(1)$ Order Cancellation via Linked-List Back-Pointers
When a cancellation request arrives for an order ID:
- Iterating through all price levels and list nodes to locate an order would be $O(N)$, creating a performance bottleneck.
- **The Solution ([`Cancel`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L60-L82))**: The `OrderBook.OrderIndex` map returns the `*OrderNode` in $O(1)$ time. Each `OrderNode` holds a direct `Element *list.Element` back-pointer. Calling `level.Orders.Remove(node.Element)` unlinks the node in $O(1)$ time with zero traversal.

### 2.4 Deterministic Trade ID Generation Across Crash Replay (Issue #2)
During startup recovery or audit replays, if the matching engine generated random UUIDs (`uuid.New()`), trade IDs would differ on every run, breaking reconciliation with downstream ledgers and order services.
- **The Solution ([`TradeID`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L162-L164))**: Generates deterministic RFC 4122 UUID v5 identifiers derived from:
  $$\text{TradeID} = \text{UUIDv5}(\text{NamespaceDNS}, \text{EventID} : \text{MakerOrderID} : \text{TakerOrderID} : \text{FillIndex})$$
- Replaying the same Kafka event produces the exact same trade ID every single time.

### 2.5 Immediate-or-Cancel (IOC) Execution for Market Orders
Market orders execute aggressively against available book depth. If there is insufficient liquidity to satisfy the full quantity:
- The matched portion produces fills.
- The unfilled remainder is **never** added to the book. Instead, `Match` leaves `incoming.RemainingQty > 0`, allowing the event loop to generate an immediate `ioc_expired` cancellation event.

---

## 3. Matching Algorithm Execution Flow (`Match`)

```
                           Incoming Order (*OrderNode)
                                        │
                         Opposite Side = getOppositeSide()
                                        │
                                        ▼
                         ┌─────────────────────────────┐
                         │   incoming.RemainingQty > 0? │ ◄──┐
                         └──────────────┬──────────────┘    │
                                        │ Yes               │
                                        ▼                   │
                         ┌─────────────────────────────┐    │
                         │   best = ExecuteBest(side)  │    │
                         └──────────────┬──────────────┘    │
                                        │                   │
                        ┌───────────────┴───────────────┐   │
                        │ best == nil OR !crossable()   │   │
                        └───────────────┬───────────────┘   │
                                        │ No (Crosses!)     │
                                        ▼                   │
                         ┌─────────────────────────────┐    │
                         │ fillQty = Min(incoming,best)│    │
                         │ tradeID = TradeID(...)      │    │
                         │ book.Sequence++             │    │
                         │ append Fill to fills slice  │    │
                         └──────────────┬──────────────┘    │
                                        │                   │
                        ┌───────────────┴───────────────┐   │
            fillQty == best.RemainingQty?               │   │
                        │                               │   │
                  Yes ──┴── No                          │   │
                   ▼        ▼                           │   │
             FullFill()   PartialFill()                 │   │
                   │        │                           │   │
                   └────────┴───────────────────────────┘   │
                                        │                   │
                     incoming.RemainingQty -= fillQty       │
                                        │                   │
                                        └───────────────────┘
                                        │
                                        ▼ No more matches / RemainingQty == 0
                        ┌───────────────────────────────┐
                        │ LIMIT order & RemainingQty > 0│
                        └───────────────┬───────────────┘
                                        │
                                  Yes ──┴── No (MARKET / Fully Filled)
                                   ▼        ▼
                              book.Sequence++
                              Insert(book, incoming)
                                   │        │
                                   └────────┘
                                        │
                                        ▼
                        mode == ModeRecovery ? return nil : return fills
```

---

## 4. External Packages & Dependencies

| Package | Why It Is Used |
| :--- | :--- |
| `container/list` | Standard Go doubly linked list (`*list.List`). Provides $O(1)$ insertion at tail (`PushBack`), $O(1)$ front peeking (`Front`), and $O(1)$ removal via pointer (`Remove`). |
| `fmt` | Formats compound seed strings for deterministic SHA-1 trade ID generation (`fmt.Sprintf("%s:%s:%s:%d", ...)`). |
| `sort` | Implements binary search (`sort.Search`) to find insertion indexes and search price levels in sorted decimal slices. |
| `time` | Captures depth snapshot timestamps (`time.Now()`). |
| `github.com/google/uuid` | Provides UUID structures, parsing, and cryptographic SHA-1 namespace hashing via `uuid.NewSHA1` for deterministic UUID v5 trade IDs. |
| `github.com/shopspring/decimal` | Exact arbitrary-precision fixed-point arithmetic for price-time matching, quantity subtraction, min/max calculations, and price comparisons without IEEE 754 precision loss. |
| `tradedrift/.../orderbook` | Core internal domain model supplying `OrderBook`, `Side`, `PriceLevel`, `OrderNode`, `Fill`, `DepthSnapshot`, and `DepthLevel`. |

---

## 5. Detailed Function & Logic Breakdown

### 5.1 `Insert(book *orderbook.OrderBook, node *orderbook.OrderNode)`
- **Purpose**: Inserts a resting LIMIT order into the appropriate side (`Bids` or `Asks`).
- **Memory Invariant**: `node` **must** be heap-allocated because its pointer is stored in `OrderIndex` and the linked list `Element`.
- **Logic**:
  1. Checks `book.OrderIndex[node.OrderID] != nil` (defensive duplicate prevention).
  2. Resolves price level from `side.PriceLevels[priceKey]`.
  3. If level does not exist:
     - Allocates new `PriceLevel` with an empty `list.New()`.
     - Performs binary search via `binarySearchInsertIndex` to find sorted position.
     - Inserts price into `side.SortedPrices`.
  4. Appends node to queue tail: `node.Element = level.Orders.PushBack(node)`.
  5. Updates `level.TotalQty = level.TotalQty.Add(node.RemainingQty)`.
  6. Stores node in `book.OrderIndex[node.OrderID]`.

---

### 5.2 `Cancel(book *orderbook.OrderBook, orderID uuid.UUID) *orderbook.OrderNode`
- **Purpose**: Cancels and removes an active order by ID.
- **Idempotency**: Safe to call multiple times. Returns `nil` if the order is already filled or not found.
- **Logic**:
  1. Looks up `node := book.OrderIndex[orderID]`. If `nil`, returns `nil`.
  2. Deducts `node.RemainingQty` from `level.TotalQty`.
  3. Unlinks node in $O(1)$: `level.Orders.Remove(node.Element)`.
  4. Deletes entry from `book.OrderIndex`.
  5. If `level.Orders.Len() == 0`: deletes level from `PriceLevels` map and removes price from `side.SortedPrices`.
  6. Returns cancelled `node`.

---

### 5.3 `ExecuteBest(side *orderbook.Side) *orderbook.OrderNode`
- **Purpose**: Peeks the best-priced, oldest order at the top of the book.
- **Safety**:
  - Returns `nil` if `SortedPrices` is empty.
  - Returns `nil` if `side.PriceLevels[bestPrice]` is nil or empty.
  - Safely type-asserts `front.Value.(*orderbook.OrderNode)`.
- **Note**: This is a read-only peek; it does not mutate book state.

---

### 5.4 `PartialFill(side *orderbook.Side, node *orderbook.OrderNode, filledQty decimal.Decimal)`
- **Purpose**: Deducts filled quantity from a resting maker order in-place.
- **Invariants Preserved**:
  - `node.Element` remains unchanged (preserves FIFO queue position).
  - `node.Timestamp` remains unchanged (preserves time priority).
  - `level.TotalQty` is updated.

---

### 5.5 `FullFill(book *orderbook.OrderBook, side *orderbook.Side, node *orderbook.OrderNode)`
- **Purpose**: Cleans up and removes a completely matched maker order.
- **Logic**: Unlinks `node.Element`, removes from `book.OrderIndex`, and prunes empty price levels.

---

### 5.6 `GetDepth(book *orderbook.OrderBook, depth int) orderbook.DepthSnapshot`
- **Purpose**: Reads Top-$N$ price levels for bids and asks for Redis caching.
- **Performance**: $O(N)$ runtime. Uses pre-aggregated `level.TotalQty` directly without iterating through individual orders.

---

### 5.7 `TradeID(eventID, makerID, takerID uuid.UUID, fillIndex int) uuid.UUID`
- **Formula**: `uuid.NewSHA1(uuid.NameSpaceDNS, []byte(fmt.Sprintf("%s:%s:%s:%d", ...)))`
- **Purpose**: Cryptographically deterministic UUID v5 trade ID generation.

---

### 5.8 `Match(book *orderbook.OrderBook, incoming *orderbook.OrderNode, mode Mode, eventID uuid.UUID) []orderbook.Fill`
- **Purpose**: The primary matching loop executing trades against opposing liquidity.
- **Step-by-step Execution**:
  1. Identifies opposite side (`getOppositeSide`).
  2. While `incoming.RemainingQty > 0`:
     - Peeks `best := ExecuteBest(oppSide)`. If nil, breaks.
     - Tests `crossable(incoming, best)`. If prices do not cross, breaks.
     - Calculates fill quantity: `fillQty = min(incoming.RemainingQty, best.RemainingQty)`.
     - Generates deterministic `tradeID`.
     - Increments authoritative `book.Sequence++`.
     - Appends `orderbook.Fill` with `Price: best.Price` (maker price).
     - If maker is fully filled $\to$ calls `FullFill()`. Otherwise $\to$ calls `PartialFill()`.
     - Decrements `incoming.RemainingQty -= fillQty`.
  3. **Resting LIMIT Remainder**: If `incoming.RemainingQty > 0` and `OrderType == OrderTypeLimit`, increments `book.Sequence++` and inserts into book via `Insert(book, incoming)`.
  4. **Mode Check**: In `ModeRecovery`, returns `nil` (suppresses output). In `ModeLive`, returns `fills`.

---

### 5.9 Internal Helper Functions

- `crossable(incoming, best *orderbook.OrderNode) bool`:
  - MARKET orders $\to$ always crossable (`true`).
  - BUY LIMIT orders $\to$ `incoming.Price >= best.Price`.
  - SELL LIMIT orders $\to$ `incoming.Price <= best.Price`.
- `getSide(book, side)` / `getOppositeSide(book, side)`: Returns references to `&book.Bids` or `&book.Asks`.
- `buyOrderOf`, `sellOrderOf`, `buyUserOf`, `sellUserOf`: Resolves buyer/seller IDs based on whether the taker was BUY or SELL.
- `binarySearchInsertIndex`: Uses `sort.Search` to maintain descending bids or ascending asks.
- `findPriceIndex`: Binary search locating an exact price index for deletion.
- `insertAt` / `removeAt`: Efficient slice manipulation helpers for sorted prices.

---

## 6. Unit Test Suite Summary (`matcher_test.go`)

| Test Function | Scenario Validated |
| :--- | :--- |
| `TestInsert_AddsToOrderIndex` | Confirms orders are indexed in `book.OrderIndex` upon insertion. |
| `TestInsert_AddsToPriceLevels` | Confirms price levels are created with accurate `TotalQty`. |
| `TestInsert_BidsSortedDescending` | Confirms bids sort descending (e.g. 101, 100, 99). |
| `TestInsert_AsksSortedAscending` | Confirms asks sort ascending (e.g. 99, 100, 101). |
| `TestInsert_Duplicate_Ignored` | Confirms duplicate insertions of the same order ID are ignored. |
| `TestCancel_RemovesFromBook` | Confirms cancelling an order removes it from index, level, and sorted prices. |
| `TestCancel_NotFound_ReturnsNil` | Confirms cancelling unknown order IDs is a safe no-op returning `nil`. |
| `TestCancel_Idempotent` | Confirms secondary cancel calls return `nil`. |
| `TestCancel_LevelRetainedIfOtherOrdersExist` | Confirms price level remains active if other orders still exist at that price. |
| `TestMatch_LimitBuyVsSellLimit_FullFill` | Tests full match between crossing limit buy and sell orders. |
| `TestMatch_NoMatch_PricesDoNotOverlap` | Confirms non-crossing orders produce zero fills and rest in book. |
| `TestMatch_PartialFill_MakerPartiallyConsumed` | Tests partial fill where maker remains in book at same queue position. |
| `TestMatch_PartialFill_TakerPartiallyFills_RemainsRests` | Tests partial fill where taker remainder rests in book. |
| `TestMatch_MultiLevelSweep` | Tests sweeping multiple price levels in a single aggressive order. |
| `TestMatch_Market_IOC_PartialFill` | Tests IOC market orders where unfilled remainder is dropped without resting. |
| `TestMatch_RecoveryMode_ReturnsNil` | Confirms `ModeRecovery` updates book state while returning `nil` fills. |
| `TestMatch_TimePriority_EarlierOrderFillsFirst` | Confirms orders placed earlier execute before newer orders at the same price. |
| `TestMatch_MakerPrice_AlwaysUsed` | Confirms trades execute at the maker's price, not the taker's price. |
| `TestMatch_FillIDs_BuyerSeller_Correct` | Confirms buyer/seller user IDs and order IDs are correctly attributed in fills. |
| `TestGetDepth_ReturnsTopN` | Confirms `GetDepth` returns requested top $N$ levels with correct aggregated volume. |
| `TestMatch_DeterministicTradeIDs` | Confirms replaying identical matching inputs produces 100% identical Trade IDs (UUID v5). |
