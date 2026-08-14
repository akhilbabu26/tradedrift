# Market Service — Business Domain Service (`internal/service`)

> **Package:** `tradedrift/services/market/internal/service`  
> **Directory:** `services/market/internal/service/`  
> **Role:** Core business domain rules, multi-timeframe candlestick bucket math, transaction boundary management, and input validation.

---

## 1. 🎯 Purpose & Architectural Role

In Clean Architecture / Domain-Driven Design (DDD), the **Service Layer** is the heart of the microservice. It contains the business rules, invariants, and calculations of the domain.

### Key Architectural Principles:
1. **Framework-Agnostic:** The service layer has **zero dependencies** on gRPC, HTTP, Fiber, or Kafka. It communicates strictly via standard Go primitives, domain structs, and repository interfaces.
2. **Atomic Consistency:** Orchestrates database transactions so that recording a trade and updating 5 candlestick timeframes either succeed together or fail together.
3. **Idempotency & Precision:** Enforces arbitrary-precision math on financial numbers (`Price`, `Quantity`, `QuoteVolume`) using `shopspring/decimal`.

---

## 2. 📂 Files in this Package

```
services/market/internal/service/
├── service.go    <-- Business logic implementation & trade event processor
├── errors.go     <-- Domain business error sentinel values
└── README.md     <-- This comprehensive documentation
```

---

## 3. 🔍 Deep-Dive: File 1 — `service.go` (Business Logic Engine)

### 🏗️ Interface & Struct Definition
```go
type MarketService interface {
    ListMarkets(ctx context.Context) ([]*domain.Market, error)
    GetMarket(ctx context.Context, marketID string) (*domain.Market, error)
    GetTicker(ctx context.Context, marketID string) (*domain.Ticker24h, error)
    GetCandles(ctx context.Context, marketID, resolution string, limit int32, from, to *time.Time) ([]*domain.Candle, error)
    ProcessTradeEvent(ctx context.Context, payload *domain.TradeEventPayload) error
}

type marketService struct {
    marketRepo repository.MarketRepository
    candleRepo repository.CandleRepository
}
```

* **Constructor:** `NewMarketService(marketRepo, candleRepo) MarketService`
* **Dependency Inversion:** Operates against interface abstractions (`MarketRepository`, `CandleRepository`), enabling seamless mocking in unit tests.

---

### 🛠️ Method-by-Method Detailed Breakdown

#### 1. `ProcessTradeEvent(ctx context.Context, payload *domain.TradeEventPayload) error`
* **Triggered By:** Kafka Consumer (`internal/kafka/consumer.go`) upon receiving an executed trade match from the Matching Engine.
* **Execution Flow:**
  1. **Market Verification:** Checks if `payload.MarketID` exists in database via `s.marketRepo.GetMarketByID(ctx, payload.MarketID)`. If unknown, rejects with `ErrNotFound`.
  2. **Transaction Demarcation:** Opens an atomic PostgreSQL transaction using `s.marketRepo.WithTx(ctx, func(tx pgx.Tx) error { ... })`.
  3. **Trade Log Persistence:** Inserts the raw trade into `market_trades` (`ON CONFLICT (id) DO NOTHING`).
  4. **Quote Volume Calculation:** Computes total cash value traded:
     $$\text{QuoteVolume} = \text{Price} \times \text{Quantity}$$
     Executed with arbitrary precision (`payload.Price.Mul(payload.Quantity)`).
  5. **Multi-Timeframe Candlestick Bucket Computation:**
     Calculates bucket start times for all 5 supported resolutions:
     ```go
     resolutions := []struct {
         name     string
         duration time.Duration
     }{
         {"1m", 1 * time.Minute},
         {"5m", 5 * time.Minute},
         {"15m", 15 * time.Minute},
         {"1h", 1 * time.Hour},
         {"1d", 24 * time.Hour},
     }
     ```
     For each resolution, the bucket start time is calculated using integer truncation:
     $$\text{StartTime} = \text{Truncate}(\text{ExecutedAt}, \text{ResolutionDuration})$$
  6. **Candle Upsert:** Passes the 5 prepared `domain.Candle` structs to `s.candleRepo.UpsertCandles(ctx, tx, candles)`.
  7. **Atomic Commit:** Commits the transaction. If any step fails, the entire transaction rolls back cleanly.

---

#### 2. `GetCandles(ctx context.Context, marketID, resolution string, limit int32, from, to *time.Time) ([]*domain.Candle, error)`
* **Purpose:** Queries historical OHLC candlestick bars for chart rendering.
* **Validation & Safety Rules:**
  1. **Resolution Whitelist:** Checks if `resolution` is one of `{"1m", "5m", "15m", "1h", "1d"}`. If invalid, returns `ErrInvalidResolution`.
  2. **Limit Defaulting & Bounds:**
     * If `limit == 0` ➔ Defaults to `100`.
     * If `limit < 0` or `limit > 500` ➔ Returns `ErrInvalidLimit` (prevents memory exhaustion attacks).
  3. **Market Existence:** Validates `marketID` exists in the database.
  4. **Repository Fetch:** Calls `s.candleRepo.GetCandles(ctx, marketID, resolution, limit, from, to)`.

---

#### 3. `GetTicker(ctx context.Context, marketID string) (*domain.Ticker24h, error)`
* **Purpose:** Fetches rolling 24-hour statistics for a single market pair.
* **Execution Flow:**
  1. Validates that `marketID` exists.
  2. Calls `s.marketRepo.GetTicker24h(ctx, marketID)` to execute the high-speed SQL CTE query.
  3. Returns `*domain.Ticker24h`.

---

## 4. 🔍 Deep-Dive: File 2 — `errors.go` (Domain Business Errors)

```go
var (
    ErrNotFound          = errors.New("market not found")
    ErrInvalidResolution = errors.New("invalid candle resolution: supported resolutions are 1m, 5m, 15m, 1h, 1d")
    ErrInvalidLimit      = errors.New("invalid limit: limit must be between 1 and 500")
)
```

* **Why Sentinel Errors?**
  Using sentinel error variables (`var Err... = errors.New(...)`) enables upper layers (like `handler/mapper.go`) to use `errors.Is(err, service.ErrNotFound)` reliably without fragile string matching.

---

## 5. 🛠️ Tools & Packages Used

| Tool / Package | Why Used in Service Layer |
| :--- | :--- |
| **`github.com/shopspring/decimal`** | Financial decimal math (`.Mul()`, `.Div()`, `.Sub()`) with zero floating-point rounding error. |
| **`github.com/google/uuid`** | UUID validation and domain identification. |
| **`time`** | Standard Go time utilities for calculating UTC candlestick bucket boundaries (`time.Truncate`). |
| **`github.com/jackc/pgx/v5`** | `pgx.Tx` interface for transaction demarcation. |
