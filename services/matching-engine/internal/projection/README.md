# `internal/projection` — Read-Only Order Book Projections

**Package:** `projection`  
**Service:** Matching Engine / Market Data Readers  
**Last Updated:** August 2026  

---

## 1. What This Package Does

This package is the **read projection and client access layer** for order book depth. It provides strongly typed data models and Redis query helpers so that external services can inspect the live market depth without ever querying the Matching Engine directly or touching in-memory order books.

Consumers of this package include:
- **API Gateway**: Serves `GET /api/v1/markets/{id}/orderbook` queries.
- **WebSocket Broadcast Cluster**: Streams real-time depth updates to connected trading interfaces.
- **Trading Desks & Dashboards**: Read-only monitoring of liquidity across all pairs.

---

## 2. Architecture & Design Principles

```
  ┌───────────────────────────────────────────────────────────┐
  │                      MATCHING ENGINE                      │
  │                                                           │
  │  Event Loop (owns OrderBook)                              │
  │       │                                                   │
  │       ├── matcher.Match() / Cancel()                      │
  │       └── matcher.GetDepth(book, 20)                      │
  │                 │                                         │
  │                 ▼ (OutputQueue)                           │
  │          publisher.Publisher                              │
  │                 │                                         │
  │                 └── pushDepth()                           │
  └─────────────────┼─────────────────────────────────────────┘
                    │
                    ▼  (Async Overwrite)
  ┌───────────────────────────────────────────────────────────┐
  │                 REDIS (Projection Cache)                  │
  │                 Key: depth:{market_id}                    │
  └─────────────────┬─────────────────────────────────────────┘
                    │
                    ▼  (Read-Only GET / MGET)
  ┌───────────────────────────────────────────────────────────┐
  │             projection.Reader (This Package)              │
  │                                                           │
  │ • Strict Validation (Price > 0, Qty > 0)                  │
  │ • Parse strongly typed decimals                           │
  │ • Safe Empty-Book Semantics (BestBid/Ask/Spread/Mid)      │
  │ • Missing Key Protection (ErrNotFound)                    │
  └─────────────────┬─────────────────────────────────────────┘
                    │
       ┌────────────┴────────────┐
       ▼                         ▼
  API Gateway            WebSocket Service
```

### Core Invariants

1. **Redis is a Projection, Not Source of Truth**:
   The in-memory `OrderBook` inside the Matching Engine is the sole authoritative state. Redis is an asynchronously updated read-only replica.
2. **Single-Key Atomic Overwrites**:
   Snapshots are stored under `depth:{market_id}` with no TTL. Every new match overwrites the previous snapshot.
3. **No Lock Contention on Matching Path**:
   Because readers query Redis, the Matching Engine's single-goroutine matching loop remains completely uninhibited by external query traffic.

---

## 3. Files In This Package

| File | Purpose |
| :--- | :--- |
| `snapshot.go` | Domain models (`OrderBookProjection`, `DepthLevel`), analytical helpers (`BestBid`, `BestAsk`, `Spread`, `MidPrice`, `IsEmpty`), and sentinel errors |
| `reader.go` | `Reader` struct, `RedisGetter` interface, `GetOrderBook` (single), `GetOrderBooks` (batch MGET), and strict payload validator `parseAndValidateSnapshot` |
| `reader_test.go` | 13 comprehensive unit tests covering parsing, missing keys, malformed data, decimal conversions, negative values, empty books, and multi-level depth |
| `README.md` | This documentation file |

---

## 4. Data Models & API Reference

### `DepthLevel`

Represents an aggregated price level with fixed-point decimal precision:

```go
type DepthLevel struct {
    Price    decimal.Decimal `json:"price"`
    Quantity decimal.Decimal `json:"quantity"`
}
```

---

### `OrderBookProjection`

Domain representation of the live book depth:

```go
type OrderBookProjection struct {
    MarketID   string       `json:"market_id"`
    Bids       []DepthLevel `json:"bids"` // sorted descending (best bid first)
    Asks       []DepthLevel `json:"asks"` // sorted ascending (best ask first)
    SnapshotAt time.Time    `json:"snapshot_at"`
}
```

#### Analytical Helper Methods

| Method | Signature | Behavior & Empty-Book Semantics |
| :--- | :--- | :--- |
| `BestBid()` | `(DepthLevel, bool)` | Returns highest buy level. Returns `false` if bids are empty (prevents accidental zero-price levels). |
| `BestAsk()` | `(DepthLevel, bool)` | Returns lowest sell level. Returns `false` if asks are empty. |
| `Spread()` | `(decimal.Decimal, bool)` | Computes $\text{BestAsk} - \text{BestBid}$. Returns `false` if either side is empty or if book is crossed. |
| `MidPrice()` | `(decimal.Decimal, bool)` | Computes $(\text{BestBid} + \text{BestAsk}) / 2$. Returns `false` if either side is empty. |
| `IsEmpty()` | `bool` | Returns `true` if both bids and asks contain zero levels. |

---

### `Reader`

```go
type Reader struct {
    client RedisGetter
}
```

Constructors:
- `NewReader(client *redis.Client)`: Production constructor using a real Redis client.
- `NewCustomReader(client RedisGetter)`: Testable constructor accepting any implementation of `RedisGetter`.

#### Operations

- **`GetOrderBook(ctx, marketID) (*OrderBookProjection, error)`**:  
  Retrieves and validates the depth snapshot for `depth:{marketID}`.  
  Returns `ErrNotFound` if the key does not exist in Redis.  
  Returns `ErrInvalidData` if the payload fails validation.

- **`GetOrderBooks(ctx, marketIDs) (map[string]*OrderBookProjection, error)`**:  
  Fetches multiple order book projections in a single round-trip using Redis `MGET`.  
  Missing markets are omitted from the map and **never** confused with empty books.

---

## 5. Strict Validation Rules

Every raw payload read from Redis is passed through `parseAndValidateSnapshot()` before being returned to callers:

1. **MarketID Matching**:
   Payload `market_id` must match the requested key (e.g. `BTC-USDT` cannot be returned for `depth:ETH-USDT`).
2. **Timestamp Mandatory**:
   `snapshot_at` must be a non-empty RFC3339 / RFC3339Nano string.
3. **Strictly Positive Prices and Quantities**:
   Every price level must satisfy:
   $$\text{Price} > 0 \quad \text{and} \quad \text{Quantity} > 0$$
   Negative numbers (`-100.00`), zeroes (`0.00`), or non-numeric tokens (`"abc"`) are rejected immediately with `ErrInvalidData`.
4. **JSON Schema Integrity**:
   Malformed or truncated JSON produces `ErrInvalidData` without crashing the reader.

---

## 6. Usage Examples

### Single Market Query

```go
reader := projection.NewReader(rdb)

proj, err := reader.GetOrderBook(ctx, "BTC-USDT")
if err != nil {
    if errors.Is(err, projection.ErrNotFound) {
        log.Println("Market not yet initialized in Redis")
        return
    }
    log.Fatalf("Failed to fetch projection: %v", err)
}

if bestBid, ok := proj.BestBid(); ok {
    fmt.Printf("Best Bid: %s @ %s\n", bestBid.Quantity, bestBid.Price)
}

if spread, ok := proj.Spread(); ok {
    fmt.Printf("Market Spread: $%s\n", spread)
}
```

### Batch Multi-Market Query

```go
markets := []string{"BTC-USDT", "ETH-USDT", "SOL-USDT"}
projections, err := reader.GetOrderBooks(ctx, markets)
if err != nil {
    log.Fatalf("Batch query failed: %v", err)
}

for marketID, book := range projections {
    if mid, ok := book.MidPrice(); ok {
        fmt.Printf("%s Mid Price: $%s\n", marketID, mid)
    }
}
```

---

## 7. Test Suite Summary

The package is thoroughly tested in `reader_test.go` with 13 distinct test cases:

- `TestReadSnapshot_Success`: Valid payload parsing and decimal verification.
- `TestMissingSnapshot`: Missing Redis key returns `ErrNotFound`.
- `TestMalformedJSON`: Syntax errors return `ErrInvalidData`.
- `TestInvalidPrice` & `TestInvalidQuantity`: Non-numeric string rejection.
- `TestNegativePrice` & `TestNegativeQuantity`: Rejection of values $\le 0$.
- `TestZeroPrice`: Rejection of zero prices.
- `TestMarketIDMismatch`: Detection of mismatched market IDs.
- `TestEmptyBids` & `TestEmptyAsks`: Verification that helper methods return `false` on one-sided books.
- `TestEmptyBook`: `IsEmpty()` validation.
- `TestMultipleLevels_BestBidAskSpreadMidPrice`: Analytical calculations across multi-tier depth.
