# Market Service — PostgreSQL Implementation (`internal/repository/postgres`)

> **Package:** `tradedrift/services/market/internal/repository/postgres`  
> **Directory:** `services/market/internal/repository/postgres/`  
> **Driver:** `jackc/pgx/v5` (Pure Go PostgreSQL Driver)  
> **Primary Design Patterns:** Data Mapper, Unit of Work (pgx.Tx), CTE Query Engine

---

## 1. 🎯 Purpose & Engineering Philosophy

The `postgres` package is the **Persistence Adapter** that implements the `MarketRepository` and `CandleRepository` interfaces.

Financial exchange databases require specialized query engineering:
1. **Zero-Lock Ticker Calculations:** 24-hour high/low/volume statistics must be calculated in real-time without taking table-level write locks.
2. **Out-of-Order Execution Resilience:** If Kafka message retries deliver trade events out of sequence, candlestick open and close prices must remain mathematically accurate.
3. **High-Throughput Batch Upserts:** Updating 5 candlestick timeframes (`1m`, `5m`, `15m`, `1h`, `1d`) must execute inside a single atomic database transaction using batch statements.

---

## 2. 📂 Files in this Package

```
services/market/internal/repository/postgres/
├── market_repository.go   <-- Markets, idempotent trades & 24h ticker CTE query
├── candle_repository.go   <-- Out-of-order OHLC candle upserts & time-series range queries
└── README.md              <-- This comprehensive documentation
```

---

## 3. 🔍 Deep-Dive: File 1 — `market_repository.go`

### 🏗️ Struct Definition & Pool Dependency
```go
type MarketRepository struct {
    pool *pgxpool.Pool
}

func NewMarketRepository(pool *pgxpool.Pool) *MarketRepository {
    return &MarketRepository{pool: pool}
}
```

---

### ⚙️ Method-by-Method Breakdown & SQL Engineering

#### 1. `WithTx(ctx, fn) error` — Atomic Transaction Boundary
```go
func (r *MarketRepository) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
    tx, err := r.pool.Begin(ctx)
    if err != nil {
        return err
    }
    defer tx.Rollback(ctx) // Safe no-op if committed

    if err := fn(tx); err != nil {
        return err
    }
    return tx.Commit(ctx)
}
```
* **Why:** Enforces the **Unit of Work pattern**. When a trade arrives, both the `market_trades` record and the 5 `ohlc_candles` updates share the exact same database transaction. If any write fails, everything is rolled back.

---

#### 2. `InsertTrade(ctx, tx, trade) (bool, error)` — Idempotent Ingestion
```sql
INSERT INTO market_trades (id, market_id, price, quantity, executed_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO NOTHING;
```
* **Why:** If Kafka delivers a duplicate trade ID, PostgreSQL quietly ignores the duplicate (`ON CONFLICT (id) DO NOTHING`). The function checks `cmdTag.RowsAffected() == 1` to report whether the trade was newly inserted or already existed.

---

#### 3. `GetTicker24h(ctx, marketID) (*Ticker24h, error)` — The 24-Hour Rolling CTE
Instead of running a cron job that updates a stale ticker table every few minutes, `GetTicker24h` computes the exact 24-hour metrics dynamically in a single SQL query using **Common Table Expressions (CTEs)**:

```sql
WITH market_check AS (
    SELECT id FROM markets WHERE id = $1
),
stats AS (
    SELECT 
        MAX(price) AS high_24h,
        MIN(price) AS low_24h,
        COALESCE(SUM(quantity), 0) AS volume_24h,
        COALESCE(SUM(price * quantity), 0) AS quote_volume_24h
    FROM market_trades
    WHERE market_id = $1 AND executed_at >= NOW() - INTERVAL '24 hours'
),
last_trade AS (
    SELECT price FROM market_trades
    WHERE market_id = $1
    ORDER BY executed_at DESC LIMIT 1
),
first_24h_trade AS (
    SELECT price FROM market_trades
    WHERE market_id = $1 AND executed_at >= NOW() - INTERVAL '24 hours'
    ORDER BY executed_at ASC LIMIT 1
)
SELECT 
    mc.id,
    COALESCE(lt.price, 0) AS last_price,
    COALESCE(s.high_24h, 0) AS high_24h,
    COALESCE(s.low_24h, 0) AS low_24h,
    s.volume_24h,
    s.quote_volume_24h,
    COALESCE(f24.price, 0) AS first_24h_price
FROM market_check mc
CROSS JOIN stats s
LEFT JOIN last_trade lt ON true
LEFT JOIN first_24h_trade f24 ON true;
```

#### 🧮 How 24-Hour Price Change % Is Calculated:
$$\text{PriceChange\%} = \frac{\text{LastPrice} - \text{FirstPrice24h}}{\text{FirstPrice24h}} \times 100$$
* **Zero Division Protection:** If `first_24h_price == 0` (no trades in 24 hours), the code sets `PriceChange24hPercent = 0.00` to prevent division-by-zero crashes.

---

## 4. 🔍 Deep-Dive: File 2 — `candle_repository.go`

### 🏗️ Struct Definition
```go
type CandleRepository struct {
    pool *pgxpool.Pool
}

func NewCandleRepository(pool *pgxpool.Pool) *CandleRepository {
    return &CandleRepository{pool: pool}
}
```

---

### 🛡️ Out-of-Order Candlestick Resolution (`UpsertCandles`)

When network retries cause trade messages to arrive out of chronological sequence, naive `UPDATE` queries corrupt the **Open** (first trade) and **Close** (latest trade) prices of the candlestick.

The `UpsertCandles` query solves this with conditional `CASE` logic:

```sql
INSERT INTO ohlc_candles (
    market_id, resolution, start_time, open_price, high_price, low_price, close_price,
    volume, quote_volume, open_trade_at, close_trade_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (market_id, resolution, start_time) DO UPDATE SET
    open_price = CASE 
        WHEN EXCLUDED.open_trade_at < ohlc_candles.open_trade_at THEN EXCLUDED.open_price 
        ELSE ohlc_candles.open_price 
    END,
    close_price = CASE 
        WHEN EXCLUDED.close_trade_at >= ohlc_candles.close_trade_at THEN EXCLUDED.close_price 
        ELSE ohlc_candles.close_price 
    END,
    high_price = GREATEST(ohlc_candles.high_price, EXCLUDED.high_price),
    low_price = LEAST(ohlc_candles.low_price, EXCLUDED.low_price),
    volume = ohlc_candles.volume + EXCLUDED.volume,
    quote_volume = ohlc_candles.quote_volume + EXCLUDED.quote_volume,
    open_trade_at = LEAST(ohlc_candles.open_trade_at, EXCLUDED.open_trade_at),
    close_trade_at = GREATEST(ohlc_candles.close_trade_at, EXCLUDED.close_trade_at);
```

#### 📊 Visual Rule Table for Candlestick Updates:

| Field | Update Rule | Explanation |
| :--- | :--- | :--- |
| **`open_price`** | `CASE WHEN EXCLUDED.open_trade_at < current.open_trade_at` | Only replaced if the new trade occurred **earlier** in time than the trade that originally set the open price. |
| **`close_price`** | `CASE WHEN EXCLUDED.close_trade_at >= current.close_trade_at`| Only replaced if the new trade occurred **later** in time than the trade that set the close price. |
| **`high_price`** | `GREATEST(current.high_price, EXCLUDED.high_price)` | Expands the high boundary if the new price is higher. |
| **`low_price`** | `LEAST(current.low_price, EXCLUDED.low_price)` | Expands the low boundary if the new price is lower. |
| **`volume`** | `current.volume + EXCLUDED.volume` | Atomically adds the base asset quantity. |
| **`quote_volume`**| `current.quote_volume + EXCLUDED.quote_volume` | Atomically adds the quote currency value. |

---

### 📈 Historical Chart Range Queries (`GetCandles`)

```sql
SELECT 
    market_id, resolution, start_time, open_price, high_price, low_price, close_price,
    volume, quote_volume, open_trade_at, close_trade_at
FROM ohlc_candles
WHERE market_id = $1 AND resolution = $2
  AND ($3::timestamptz IS NULL OR start_time >= $3)
  AND ($4::timestamptz IS NULL OR start_time <= $4)
ORDER BY start_time DESC
LIMIT $5;
```

* **Index Utilized:** `idx_candles_time ON ohlc_candles(market_id, resolution, start_time DESC)`.
* **Chronological Sorting:** The query scans backward using the index to get the most recent `N` candles, and the Go code reverses the slice before returning so chart libraries receive bars in natural left-to-right (chronological) order.

---

## 5. 🛠️ Tools & Packages Used

| Tool / Package | Why Used in PostgreSQL Layer |
| :--- | :--- |
| **`github.com/jackc/pgx/v5`** | The highest-performing pure-Go PostgreSQL driver with native binary format encoding. |
| **`github.com/jackc/pgx/v5/pgxpool`** | Thread-safe, non-blocking connection pool manager. |
| **`github.com/shopspring/decimal`** | Scans PostgreSQL `DECIMAL(30,10)` columns directly without conversion to float64. |
| **`github.com/google/uuid`** | Scans PostgreSQL native `UUID` types directly into 16-byte UUID structs. |
