# `internal/orderbook` — In-Memory Order Book Data Structures & Snapshot Persistence

**Package:** `orderbook`  
**Service:** Matching Engine  
**Files Covered:** `book.go`, `side.go`, `level.go`, `node.go`, `result.go`, `snapshot.go`, `snapshot_test.go`  
**Documentation:** `02READEME.md`  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Purpose

The `internal/orderbook` package defines the **foundational in-memory domain models, composite data structures, event outcome wrappers, and cryptographic snapshot serialization mechanisms** for the Matching Engine.

It provides the data representation and state contracts utilized across all engine subsystems:
- **`market` package**: Owns and drives the order book instance within a single-threaded Event Loop actor.
- **`matcher` package**: Traverses and mutates the order book structures during price-time matching and cancellation.
- **`recovery` package**: Reconstructs pre-crash order book states using deterministic snapshots and Kafka log replay.
- **`publisher` & `projection` packages**: Consumes matching outcome models ([`MatchResult`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/result.go#L67-L75), [`DepthSnapshot`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/result.go#L49-L55)) to publish trade events to Kafka, broadcast Level-2 market depth to Redis, and commit checkpoints to PostgreSQL.

---

## 2. Core Problems Solved & Why This Package Is Needed

### 2.1 Composite Data Structure for High-Performance Order Operations
An exchange order book must support four high-frequency operations simultaneously:
1. **Best Price Lookup**: Must be $O(1)$ to evaluate top-of-book prices instantly.
2. **Price Level Lookup**: Must be $O(1)$ to find or aggregate resting quantity at a given price.
3. **Queue Insertion & FIFO Traversal**: Must be $O(1)$ to append incoming resting orders and peek the oldest maker order.
4. **Order Cancellation & Removal by ID**: Must be $O(1)$ without linear scans across thousands of orders.

The package combines three synergistic data structures to achieve this:
- **Sorted Price Slices (`SortedPrices []decimal.Decimal`)**: Index `0` is always the best price ($O(1)$ best price lookup).
- **Price Level Hash Map (`PriceLevels map[string]*PriceLevel`)**: Stringified price keys map directly to level structures ($O(1)$ level access).
- **FIFO Doubly Linked List (`Orders *list.List`) + Global Hash Map (`OrderIndex map[uuid.UUID]*OrderNode`)**: Each `OrderNode` stores an `Element *list.Element` back-pointer. Cancellation by ID is a direct map lookup followed by an $O(1)$ linked list unlinking.

```
                           OrderBook ("BTC-USDT")
                                     │
         ┌───────────────────────────┴───────────────────────────┐
         ▼                                                       ▼
    Bids (Side)                                             Asks (Side)
┌──────────────────────────────────────────────┐   ┌──────────────────────────────────────────────┐
│ SortedPrices: [65000.00, 64990.00, 64980.00] │   │ SortedPrices: [65010.00, 65020.00, 65030.00] │
│ PriceLevels:                                 │   │ PriceLevels:                                 │
│   "65000.00" ──► PriceLevel (Total: 3.5 BTC) │   │   "65010.00" ──► PriceLevel (Total: 1.2 BTC) │
│                    │ (FIFO List)             │   │                    │ (FIFO List)             │
│                    ├──► OrderNode 1 (1.5 BTC)│   │                    └──► OrderNode 3 (1.2 BTC)│
│                    └──► OrderNode 2 (2.0 BTC)│   └──────────────────────────────────────────────┘
└──────────────────────────────────────────────┘
                                     ▲
                                     │
                           OrderIndex (map[UUID]*OrderNode)
                           ├── UUID_1 ──► OrderNode 1 (Element back-pointer)
                           ├── UUID_2 ──► OrderNode 2 (Element back-pointer)
                           └── UUID_3 ──► OrderNode 3 (Element back-pointer)
```

### 2.2 Pre-Aggregated Level Quantities for $O(\text{depth})$ Depth Projections
Emitting real-time market depth to Redis after every trade requires calculating the total volume across top price levels. Iterating through individual orders would be $O(M)$ (where $M$ is total order count).
- **The Solution ([`PriceLevel.TotalQty`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/level.go#L17))**: Every insert, fill, and cancel keeps `TotalQty` in sync in-place. Extracting Top-$N$ depth levels (`GetDepth`) is strictly $O(N)$ with zero per-order iteration.

### 2.3 Cryptographic, Deterministic Snapshot Durability (Issue #8 & v9.4)
Periodic snapshots allow the engine to recover within milliseconds rather than replaying millions of historical Kafka messages from offset 0.
- **Canonical Serialization ([`Serialize`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go#L61-L112))**: Serializes resting orders in deterministic order (Bids descending by price, Asks ascending by price; FIFO order within each level).
- **SHA-256 Integrity Checksum ([`Checksum`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go#L51-L58))**: Calculates a 256-bit SHA-256 hash over the canonical JSON representation.
- **Strict Pre-Restoration Validation ([`Restore`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go#L115-L231))**:
  - Schema version compatibility check (`SchemaVersion == 1`).
  - Market ID and Partition ID verification.
  - Asserting `Offset <= Checkpoint` (prevents restoring future/uncommitted snapshots).
  - SHA-256 checksum match verification.
  - Ensuring no resting orders have `OrderType == MARKET` (market orders are IOC and must never rest in book).
  - Validating `RemainingQty > 0`, `RemainingQty <= OriginalQty`, `Price > 0`.
  - Asserting strict compliance with market `TickSize` (`Price % TickSize == 0`) and `LotSize` (`Quantity % LotSize == 0`).
  - Duplicate order ID prevention across snapshot orders.
- **Side-Effect-Free Restoration ([`InsertRestoredOrder`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/snapshot.go#L234-L257))**: Reconstructs book memory structures directly without triggering sequence increments or publisher events.

---

## 3. External Packages & Dependencies

| Package | Purpose & Justification |
| :--- | :--- |
| `container/list` | Standard Go doubly linked list (`*list.List`). Provides $O(1)$ FIFO queue operations (`PushBack`, `Front`, `Remove`) and back-pointers via `*list.Element`. |
| `crypto/sha256` | Cryptographic SHA-256 hashing (`sha256.Sum256`) used to generate and verify snapshot checksums. |
| `encoding/json` | Marshals and unmarshals canonical snapshot JSON payloads for database storage and checksum validation. |
| `errors` | Defines exported sentinel errors (`ErrSnapshotMarketMismatch`, `ErrSnapshotChecksumMismatch`, etc.). |
| `fmt` | Formats descriptive errors during snapshot parsing and restoration. |
| `time` | Captures order arrival timestamps (`time.Time`, `time.RFC3339Nano`) and snapshot creation times. |
| `github.com/google/uuid` | Provides 128-bit RFC 4122 UUID types for `OrderID`, `UserID`, `TradeID`, and map indexing in `OrderIndex`. |
| `github.com/shopspring/decimal` | Exact fixed-point decimal arithmetic for prices, quantities, and modulo compliance checks without floating-point drift. |

---

## 4. File-by-File Detailed Breakdown

### 4.1 `book.go` — The Master OrderBook Struct

```go
type OrderBook struct {
    MarketID   string
    Sequence   uint64                   // Monotonically increasing sequence per market
    Bids       Side                     // buy side — sorted highest → lowest
    Asks       Side                     // sell side — sorted lowest → highest
    OrderIndex map[uuid.UUID]*OrderNode // O(1) cancel lookup
}
```

- **`NewOrderBook(marketID string) *OrderBook`**:
  - Allocates and initializes an empty `OrderBook`.
  - Explicitly initializes empty `SortedPrices` slices and `PriceLevels` maps for `Bids` (`IsBid = true`) and `Asks` (`IsBid = false`).
  - Allocates `OrderIndex` map.

---

### 4.2 `side.go` — Order Book Side (Bids / Asks)

```go
type Side struct {
    SortedPrices []decimal.Decimal         // index 0 = best price
    PriceLevels  map[string]*PriceLevel    // price.String() → level
    IsBid        bool                      // true = bids (desc), false = asks (asc)
}
```

- Encapsulates one side of the order book.
- `SortedPrices[0]` is always top-of-book (best bid or best ask).
- `PriceLevels` allows $O(1)$ access to any price level via `price.String()`.

---

### 4.3 `level.go` — Single Price Level & Order Queue

```go
type PriceLevel struct {
    Price    decimal.Decimal
    Orders   *list.List      // FIFO queue of *OrderNode
    TotalQty decimal.Decimal // sum of all RemainingQty at this level
}
```

- Holds all resting orders at an exact price point in FIFO sequence.
- `TotalQty` is maintained in-place on every mutation, enabling $O(N)$ depth snapshots.

---

### 4.4 `node.go` — Order Node & Enums

#### Enums
- `SideType`: `"BUY"` (`SideBuy`) or `"SELL"` (`SideSell`).
- `OrderType`: `"LIMIT"` (`OrderTypeLimit`) or `"MARKET"` (`OrderTypeMarket`).

#### `OrderNode` Struct
```go
type OrderNode struct {
    OrderID      uuid.UUID
    UserID       uuid.UUID
    MarketID     string
    Side         SideType
    OrderType    OrderType
    Price        decimal.Decimal // zero for MARKET orders
    OriginalQty  decimal.Decimal // never changes
    RemainingQty decimal.Decimal // reduced on every partial fill
    Timestamp    time.Time       // ME arrival time — determines time priority
    Element      *list.Element   // back-pointer: list.Remove(node.Element) = O(1)
}
```

- **Heap Allocation Invariant**: `OrderNode` must be heap-allocated because its pointer is stored in `OrderIndex` and the doubly linked list.
- `RemainingQty` is mutated in-place on partial fills to preserve queue position and time priority.
- `Element` enables $O(1)$ unlinking from `PriceLevel.Orders`.

---

### 4.5 `result.go` — Event Outcomes, Positions & Telemetry

1. **`Fill`**:
   - Represents an individual matched trade.
   - Contains `TradeID` (deterministic UUID v5), `MarketID`, `Sequence`, `MakerOrderID`, `TakerOrderID`, `BuyOrderID`, `SellOrderID`, `BuyerUserID`, `SellerUserID`, `Price` (maker price), and `Quantity`.

2. **`CancelledOrder`**:
   - Emitted when an order is removed from the book.
   - `Reason` values: `"user_requested"`, `"ioc_expired"`, or `"invalid_order_parameters"`.

3. **`DepthLevel` & `DepthSnapshot`**:
   - `DepthLevel`: Pairs `Price` with pre-aggregated `Quantity`.
   - `DepthSnapshot`: Carries `MarketID`, `Sequence`, `Bids` slice, `Asks` slice, and `SnapshotAt` timestamp for Redis depth cache projection.

4. **`KafkaPosition`**:
   - Identifies exact Kafka coordinates (`Topic`, `Partition`, `Offset`).
   - Represents the global durability checkpoint coordinate across partitions.

5. **`MatchResult`**:
   - Container emitted for **every single** input event (one-in one-out).
   - Holds `Fills`, `CancelResult`, `DepthSnapshot`, `SourcePosition`, optional `Snapshot`, and recovery barrier markers (`BarrierReached`, `BarrierOffset`).

---

### 4.6 `snapshot.go` — Serialization, Checksum & Safe Restoration

#### Constants & Errors
- `CurrentSchemaVersion = 1`
- Exported sentinel errors: `ErrSnapshotMarketMismatch`, `ErrSnapshotPartitionMismatch`, `ErrSnapshotBeyondCheckpoint`, `ErrSnapshotChecksumMismatch`, `ErrSnapshotSchemaMismatch`.

#### Structures
- `BookSnapshot`: Serialized snapshot containing `SchemaVersion`, `MarketID`, `Partition`, `Offset`, `Sequence`, and `Orders []SnapshotOrder`.
- `SnapshotOrder`: String-serialized representation of an active resting order.

#### Core Functions

- **`Checksum(snap BookSnapshot) ([]byte, error)`**:
  - Marshals `snap` to canonical JSON and returns its SHA-256 byte slice.

- **`Serialize(book *OrderBook, partition int, offset int64) BookSnapshot`**:
  - Traverses `book.Bids` (descending prices) and `book.Asks` (ascending prices).
  - Iterates through `level.Orders` in FIFO order from `Front()` to `Back()`.
  - Serializes order fields to strings (prices, quantities, RFC3339Nano timestamps).
  - Returns structured `BookSnapshot`.

- **`Restore(...) error`**:
  - Comprehensive pre-flight validation gate:
    1. Validates `SchemaVersion == 1`.
    2. Validates `MarketID` and `Partition` match expected engine configuration.
    3. Asserts `snap.Offset <= checkpoint`.
    4. Computes SHA-256 checksum and compares against `expectedChecksum`.
    5. Resets book `Bids`, `Asks`, `OrderIndex`, and restores `book.Sequence = snap.Sequence`.
    6. Iterates over `snap.Orders`, parses UUIDs, timestamps, and decimals.
    7. Enforces data integrity: `Side` is BUY/SELL, `OrderType` is LIMIT, `RemainingQty > 0`, `RemainingQty <= OriginalQty`, `Price % TickSize == 0`, `RemainingQty % LotSize == 0`, and no duplicate order IDs.
    8. Calls `InsertRestoredOrder` for each valid order.

- **`InsertRestoredOrder(book *OrderBook, node *OrderNode)`**:
  - Directly populates price levels, sorted price slices, and `OrderIndex` without triggering side-effects, sequence changes, or fill emissions.

- **Helper Functions**:
  - `binarySearchInsertIndex(side, price)`: Binary search for sorted price insertion.
  - `insertAt(prices, idx, price)`: In-place slice expansion and insertion.

---

## 5. Unit Test Suite Summary (`snapshot_test.go`)

| Test Function | Invariant Verified |
| :--- | :--- |
| `TestRestore_ValidSnapshot` | Validates successful end-to-end restoration of a valid snapshot, verifying sequence counter and order index reconstruction. |
| `TestRestore_MarketOrderResting_Fails` | Verifies that snapshots containing resting `MARKET` orders fail validation with an error (market orders must be IOC). |
| `TestRestore_TickSizeViolation_Fails` | Verifies that resting limit orders whose prices violate the market's `TickSize` are rejected during restoration. |
| `TestRestore_LotSizeViolation_Fails` | Verifies that resting limit orders whose quantities violate the market's `LotSize` are rejected during restoration. |
