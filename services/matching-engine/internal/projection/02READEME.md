# `internal/projection` — Read-Only Order Book Projections & Redis Depth Reader

**Package:** `projection`  
**Service:** Matching Engine / Market Data Projections  
**Files Covered:** `snapshot.go`, `reader.go`, `reader_test.go`  
**Documentation:** `02READEME.md`  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Purpose

The `internal/projection` package implements the **read projection and materialized view access layer** for Level-2 (L2) Order Book depth in the Matching Engine architecture.

In a high-frequency trading platform, thousands of external clients (API Gateways, WebSockets, Trading Bots, and User Interfaces) continuously query for the latest order book depth. Querying the Matching Engine directly would introduce lock contention, thread context switching, and latency jitter to the core matching loop.

The `projection` package resolves this by reading from **Redis materialized view projections** (`depth:{market_id}`) asynchronously emitted by [`internal/publisher`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/publisher/publisher.go). It provides:
1. **Decoupled Read Access**: External services read from Redis replicas without touching the matching engine's in-memory memory structures.
2. **Lossless Financial Precision**: Parses string-encoded decimal data into strongly typed fixed-point [`shopspring/decimal.Decimal`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/projection/snapshot.go#L7) structures.
3. **Rigorous Domain Validation**: Verifies payload integrity, positive price/quantity rules, and market ID alignment before returning data to consumers.
4. **Analytical Market Metrics**: Computes Top-of-Book Best Bid, Best Ask, Bid-Ask Spread, Mid-Price, and Book Emptiness with safe empty-book semantics.
5. **Batch Query Optimization**: Performs multi-market depth reads in a single network round-trip via Redis `MGET`.

---

## 2. Core Problems Solved & Why This Package Is Needed

### 2.1 Complete Isolation of Core Matching Engine from Read Traffic
If external REST API endpoints (`GET /api/v1/markets/{id}/orderbook`) or WebSocket clusters directly queried the Matching Engine:
- Read contention would stall the single-threaded Event Loop actor.
- A surge in external API traffic could degrade trade execution latency.
- **The Solution**: The Matching Engine publishes Top-20 depth snapshots to Redis (`depth:{market_id}`) after matching cycles. The `projection.Reader` queries Redis exclusively, completely insulating the Matching Engine from external query load.

```
┌───────────────────────────────────────────────────────────┐
│                      MATCHING ENGINE                      │
│                                                           │
│  Event Loop (Single Goroutine per Market)                 │
│       │                                                   │
│       ├── Executes matcher.Match() / Cancel()             │
│       └── Emits Top-20 DepthSnapshot to OutputQueue       │
└─────────────────────────┬─────────────────────────────────┘
                          │
                          ▼
                  publisher.Publisher
                          │ (Async Single-Key Overwrite)
                          ▼
┌───────────────────────────────────────────────────────────┐
│                 REDIS MATERIALIZED VIEW                   │
│                 Key: depth:{market_id}                    │
└─────────────────────────┬─────────────────────────────────┘
                          │
                          ▼ (Read-Only GET / MGET)
┌───────────────────────────────────────────────────────────┐
│             projection.Reader (This Package)              │
│                                                           │
│  • Validates Price > 0, Quantity > 0                      │
│  • Exact decimal.Decimal conversions                      │
│  • Computes Spread & MidPrice                             │
│  • Distinguishes ErrNotFound vs Empty Book                │
└─────────────────────────┬─────────────────────────────────┘
                          │
             ┌────────────┴────────────┐
             ▼                         ▼
        API Gateway            WebSocket Gateway
```

### 2.2 Strict Domain Validation & Corrupt Cache Defense
Cached data in Redis can become malformed due to network truncation, unmarshal errors, or erroneous writes. Passing invalid data to financial clients can cause catastrophic automated trading decisions.
- `parseAndValidateSnapshot()` acts as a strict validation gate:
  - **Positive Value Enforcement**: Asserts $\text{Price} > 0$ and $\text{Quantity} > 0$. Rejects negative prices (`"-100.00"`), zeroes (`"0.00"`), and non-numeric strings (`"abc"`).
  - **Market Key Guard**: Asserts `raw.MarketID == expectedMarketID` (prevents cross-market pollution).
  - **Timestamp Integrity**: Validates `snapshot_at` as a valid RFC3339 / RFC3339Nano timestamp.

### 2.3 Safe Empty-Book Semantics & Distinction from Missing Keys
In financial order books, an empty book (a market with no resting bids or asks) must **never** return a zero price ($0.00$) as a valid price level.
- `BestBid()`, `BestAsk()`, `Spread()`, and `MidPrice()` return boolean status flags: `(DepthLevel, false)` or `(decimal.Decimal, false)`.
- Distinguishes between:
  - **Market Not Found (`ErrNotFound`)**: Key does not exist in Redis (market uninitialized or invalid ID).
  - **Initialized But Empty Book (`IsEmpty() == true`)**: Key exists, but bids and asks slices have length 0.

### 2.4 High-Efficiency Batch Queries (`GetOrderBooks` via `MGET`)
Trading dashboards often require the status of all active trading pairs simultaneously. Iterating through markets with individual `GET` commands creates $N$ sequential network round-trips.
- `GetOrderBooks()` utilizes Redis `MGET` to fetch all requested keys in a **single round-trip**, automatically filtering missing keys without allocating bogus empty books.

---

## 3. External Packages & Dependencies

| External Package | Why It Is Used |
| :--- | :--- |
| `context` | Manages deadlines, cancellation signals, and request lifecycles during Redis network calls. |
| `encoding/json` | Unmarshals raw JSON wire format payloads fetched from Redis into domain structures. |
| `errors` | Defines exported sentinel errors (`ErrNotFound`, `ErrInvalidData`) for idiomatic `errors.Is` error checks. |
| `fmt` | Wraps errors with context (`%w`) and formats composite Redis keys (`"depth:" + marketID`). |
| `time` | Parses and validates RFC3339/RFC3339Nano snapshot timestamps (`time.Time`) to measure market data freshness. |
| `github.com/redis/go-redis/v9` | Modern, high-performance Go client for Redis. Supplies `*redis.Client`, `*redis.StringCmd`, `*redis.SliceCmd`, and `redis.Nil`. |
| `github.com/shopspring/decimal` | Arbitrary-precision fixed-point decimal arithmetic for financial prices, quantities, spreads, and mid-prices. |

---

## 4. Detailed Component & Function Breakdown

### 4.1 `snapshot.go` — Domain Models & Analytical Methods

#### Sentinel Errors
- **`ErrNotFound = errors.New("orderbook projection not found")`**: Returned when the requested market key does not exist in Redis (`redis.Nil`).
- **`ErrInvalidData = errors.New("invalid orderbook projection data")`**: Returned when the payload is malformed JSON, contains $\le 0$ prices/quantities, or fails market ID alignment.

#### Core Structs
1. **`DepthLevel`**:
   ```go
   type DepthLevel struct {
       Price    decimal.Decimal `json:"price"`
       Quantity decimal.Decimal `json:"quantity"`
   }
   ```
   - Represents an aggregated price tier carrying exact fixed-point price and quantity decimals.

2. **`OrderBookProjection`**:
   ```go
   type OrderBookProjection struct {
       MarketID   string       `json:"market_id"`
       Sequence   uint64       `json:"sequence"`
       Bids       []DepthLevel `json:"bids"` // sorted descending (best bid first)
       Asks       []DepthLevel `json:"asks"` // sorted ascending (best ask first)
       SnapshotAt time.Time    `json:"snapshot_at"`
   }
   ```
   - Master domain model representing the current state of a market's Top-20 depth.

#### Analytical Methods
- **`BestBid() (DepthLevel, bool)`**:
  - Returns `p.Bids[0]` (highest buying price).
  - Returns `(DepthLevel{}, false)` if `len(p.Bids) == 0`.
- **`BestAsk() (DepthLevel, bool)`**:
  - Returns `p.Asks[0]` (lowest selling price).
  - Returns `(DepthLevel{}, false)` if `len(p.Asks) == 0`.
- **`Spread() (decimal.Decimal, bool)`**:
  - Computes $\text{BestAsk.Price} - \text{BestBid.Price}$.
  - Returns `(decimal.Zero, false)` if either side is empty or if the book is crossed ($\text{Spread} < 0$).
- **`MidPrice() (decimal.Decimal, bool)`**:
  - Computes $(\text{BestBid.Price} + \text{BestAsk.Price}) / 2$.
  - Returns `(decimal.Zero, false)` if either side is empty.
- **`IsEmpty() bool`**:
  - Returns `true` if `len(p.Bids) == 0 && len(p.Asks) == 0`.

---

### 4.2 `reader.go` — Redis Retrieval & Validation Engine

#### Interfaces & Structs
1. **`RedisGetter`**:
   ```go
   type RedisGetter interface {
       Get(ctx context.Context, key string) *redis.StringCmd
       MGet(ctx context.Context, keys ...string) *redis.SliceCmd
   }
   ```
   - Abstracts Redis reading for unit test mocking (`fakeRedis`) without needing a live Redis server.

2. **`Reader`**:
   ```go
   type Reader struct {
       client RedisGetter
   }
   ```
   - Query client holding the abstracted Redis connection.

3. **`rawDepthMessage`**:
   - Internal wire-format decoding struct matching the JSON payload published by `internal/publisher`.

#### Constructors & Functions
- **`NewReader(client *redis.Client) *Reader`**:
  - Production constructor wrapping a concrete Redis client connection.
- **`NewCustomReader(client RedisGetter) *Reader`**:
  - Test constructor accepting mock interfaces.

- **`GetOrderBook(ctx context.Context, marketID string) (*OrderBookProjection, error)`**:
  - Validates `marketID != ""` (returns `ErrInvalidData` if empty).
  - Executes `r.client.Get(ctx, "depth:" + marketID)`.
  - If error is `redis.Nil`, returns `ErrNotFound`.
  - Calls `parseAndValidateSnapshot` on the raw payload.

- **`GetOrderBooks(ctx context.Context, marketIDs []string) (map[string]*OrderBookProjection, error)`**:
  - Builds slice of keys (`depth:{marketID}`).
  - Executes single-trip `r.client.MGet(ctx, keys...)`.
  - Loops over results: skips `nil` entries (missing keys) and corrupt entries.
  - Returns map of successfully parsed `map[string]*OrderBookProjection`.

- **`parseAndValidateSnapshot(data []byte, expectedMarketID string) (*OrderBookProjection, error)`**:
  - **Step 1**: Unmarshals JSON into `rawDepthMessage`.
  - **Step 2**: Asserts `raw.MarketID == expectedMarketID`.
  - **Step 3**: Validates and parses `raw.SnapshotAt` using `RFC3339Nano` or `RFC3339`.
  - **Step 4**: Iterates `raw.Bids`: parses `Price` and `Quantity`, asserting both are strictly $> 0$.
  - **Step 5**: Iterates `raw.Asks`: parses `Price` and `Quantity`, asserting both are strictly $> 0$.
  - **Step 6**: Returns sanitized `*OrderBookProjection`.

---

## 5. Usage Examples

### 5.1 Single-Market Query
```go
reader := projection.NewReader(redisClient)

proj, err := reader.GetOrderBook(ctx, "BTC-USDT")
if err != nil {
    if errors.Is(err, projection.ErrNotFound) {
        log.Printf("Market BTC-USDT not yet published to Redis")
        return
    }
    log.Fatalf("Failed to fetch order book: %v", err)
}

if bestBid, ok := proj.BestBid(); ok {
    fmt.Printf("Top Bid: %s @ %s\n", bestBid.Quantity, bestBid.Price)
}
if spread, ok := proj.Spread(); ok {
    fmt.Printf("Bid-Ask Spread: $%s\n", spread)
}
```

### 5.2 Multi-Market Batch Query
```go
markets := []string{"BTC-USDT", "ETH-USDT", "SOL-USDT"}
books, err := reader.GetOrderBooks(ctx, markets)
if err != nil {
    log.Fatalf("Batch depth query failed: %v", err)
}

for marketID, book := range books {
    if mid, ok := book.MidPrice(); ok {
        fmt.Printf("%s Mid Price: $%s\n", marketID, mid)
    }
}
```

---

## 6. Unit Test Suite Summary (`reader_test.go`)

| Test Function | Scenario / Invariant Validated |
| :--- | :--- |
| `TestReadSnapshot_Success` | Verifies end-to-end retrieval, JSON parsing, decimal conversions, and timestamp validation. |
| `TestMissingSnapshot` | Asserts that querying a non-existent market returns `ErrNotFound`. |
| `TestMalformedJSON` | Asserts that syntactically broken JSON returns `ErrInvalidData`. |
| `TestInvalidPrice` | Asserts that non-numeric price strings (e.g. `"not-a-number"`) return `ErrInvalidData`. |
| `TestInvalidQuantity` | Asserts that non-numeric quantity strings return `ErrInvalidData`. |
| `TestNegativePrice` | Asserts that prices $\le 0$ (e.g. `"-100.00"`) return `ErrInvalidData`. |
| `TestNegativeQuantity` | Asserts that quantities $\le 0$ (e.g. `"-0.5"`) return `ErrInvalidData`. |
| `TestZeroPrice` | Asserts that zero price levels (`"0.00"`) are rejected with `ErrInvalidData`. |
| `TestMarketIDMismatch` | Asserts that if Redis key payload market ID does not match expected ID, it is rejected. |
| `TestEmptyBids` | Verifies that a book with asks but empty bids returns `false` for `BestBid()`, `Spread()`, and `MidPrice()`. |
| `TestEmptyAsks` | Verifies that a book with bids but empty asks returns `false` for `BestAsk()`, `Spread()`, and `MidPrice()`. |
| `TestEmptyBook` | Verifies that `IsEmpty()` returns `true` when both sides have 0 levels. |
| `TestMultipleLevels_BestBidAskSpreadMidPrice` | Tests multi-tier depth calculations for `BestBid`, `BestAsk`, `Spread`, and `MidPrice`. |
