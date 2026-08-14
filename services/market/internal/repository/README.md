# Market Service — Repository Layer Contracts (`internal/repository`)

> **Package:** `tradedrift/services/market/internal/repository`  
> **Directory:** `services/market/internal/repository/`  
> **Primary Design Patterns:** Repository Pattern, Interface Segregation, Domain Entity Modeling

---

## 1. 🎯 Purpose & Architectural Role

The `repository` package defines the **Data Access Layer Interfaces** and core **Domain Entity Structs** for the Market Service.

### Why Is This Layer Necessary?
1. **Separation of Concerns:** Isolates pure business rules (`internal/service`) from database query mechanics (`internal/repository/postgres`).
2. **Testability & Mocking:** By depending on interfaces (`MarketRepository`, `CandleRepository`), business logic can be tested using mock in-memory stores without spinning up a live PostgreSQL database.
3. **Domain Purity:** Domain entities (`Market`, `MarketTrade`, `Ticker24h`, `Candle`) use standard Go types (`time.Time`, `decimal.Decimal`, `uuid.UUID`) without any database driver or ORM tags.

---

## 2. 📂 Files in this Package

```
services/market/internal/repository/
├── market.go       <-- Core domain structs & repository interface contracts
├── errors.go       <-- Data access error constants (ErrNotFound, ErrDatabase)
├── README.md       <-- This comprehensive documentation
└── postgres/       <-- Concrete PostgreSQL driver implementation
    ├── market_repository.go
    ├── candle_repository.go
    └── README.md
```

---

## 3. 🔍 Deep-Dive: File 1 — `market.go` (Domain Models & Interfaces)

### 🧱 Core Domain Entities

```go
type MarketStatus string

const (
    MarketStatusActive      MarketStatus = "ACTIVE"
    MarketStatusHalted      MarketStatus = "HALTED"
    MarketStatusMaintenance MarketStatus = "MAINTENANCE"
)

type Market struct {
    ID          string
    BaseAsset   string
    QuoteAsset  string
    TickSize    decimal.Decimal
    LotSize     decimal.Decimal
    Status      MarketStatus
    MinQuantity decimal.Decimal
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type MarketTrade struct {
    ID          uuid.UUID
    MarketID    string
    Price       decimal.Decimal
    Quantity    decimal.Decimal
    ExecutedAt  time.Time
}

type Ticker24h struct {
    MarketID              string
    LastPrice             decimal.Decimal
    High24h               decimal.Decimal
    Low24h                decimal.Decimal
    Volume24h             decimal.Decimal
    QuoteVolume24h        decimal.Decimal
    PriceChange24hPercent decimal.Decimal
}

type Candle struct {
    MarketID     string
    Resolution   string
    StartTime    time.Time
    Open         decimal.Decimal
    High         decimal.Decimal
    Low          decimal.Decimal
    Close        decimal.Decimal
    Volume       decimal.Decimal
    QuoteVolume  decimal.Decimal
    OpenTradeAt  time.Time
    CloseTradeAt time.Time
}
```

---

### 📋 Interface Contracts

#### 1. `MarketRepository` Interface
```go
type MarketRepository interface {
    ListMarkets(ctx context.Context) ([]*Market, error)
    GetMarketByID(ctx context.Context, id string) (*Market, error)
    InsertTrade(ctx context.Context, tx pgx.Tx, trade *MarketTrade) (bool, error)
    GetTicker24h(ctx context.Context, marketID string) (*Ticker24h, error)
    WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}
```
* **`ListMarkets`**: Returns all configured trading pairs.
* **`GetMarketByID`**: Looks up a market pair by symbol (`"BTC-USDT"`). Returns `ErrNotFound` if missing.
* **`InsertTrade`**: Inserts an executed trade inside a database transaction (`pgx.Tx`). Returns `(inserted bool, err error)` — if the trade was already recorded, returns `false, nil` for duplicate deduplication.
* **`GetTicker24h`**: Calculates dynamic 24-hour price and volume statistics.
* **`WithTx`**: Wraps multiple repository operations in a single atomic database transaction. Automatically rolls back on error and commits on success.

#### 2. `CandleRepository` Interface
```go
type CandleRepository interface {
    UpsertCandles(ctx context.Context, tx pgx.Tx, candles []*Candle) error
    GetCandles(ctx context.Context, marketID, resolution string, limit int32, from, to *time.Time) ([]*Candle, error)
}
```
* **`UpsertCandles`**: Atomically updates or inserts candlestick buckets across multiple timeframes inside a database transaction.
* **`GetCandles`**: Fetches historical candlestick bars for chart engines with pagination and date range filtering.

---

## 4. 🔍 Deep-Dive: File 2 — `errors.go`

```go
var (
    ErrNotFound = errors.New("record not found")
    ErrDatabase = errors.New("database error")
)
```

* **Purpose:** Standardizes persistence errors so callers in the service layer do not need to import `pgx` or `sql` package errors directly.
