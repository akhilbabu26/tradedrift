# Market Service — Database Schema & Migrations (`migration/`)

> **Directory:** `services/market/migration/`  
> **Database:** PostgreSQL (`tradedrift_market`)  
> **Migration Tool:** `golang-migrate` / `goose` compatible SQL DDL  
> **Primary File:** `00001_create_market_tables.sql`

---

## 1. Executive Summary & Purpose

The `migration/` directory defines the relational database schema, integrity constraints, indexing strategies, and default seed data for the **Market Service**.

In a financial exchange, the market database must support two opposing workloads:
1. **High-Frequency Ingestion (Writes):** Ingesting thousands of executed trade matches per second from the Matching Engine while atomically updating multi-resolution candlestick bars.
2. **Sub-Millisecond Read Queries (Reads):** Serving 24-hour ticker statistics and historical candlestick chart feeds (TradingView) to web/mobile users without database locks.

---

## 2. Entity Relationship Diagram (ERD)

```
       ┌────────────────────────────────────────────────────────┐
       │                        markets                         │
       ├────────────────────────────────────────────────────────┤
       │ id            VARCHAR(20) [PK] (e.g. 'BTC-USDT')       │
       │ base_asset    VARCHAR(10)      (e.g. 'BTC')            │
       │ quote_asset   VARCHAR(10)      (e.g. 'USDT')           │
       │ tick_size     DECIMAL(30,10)   (Price step increment)  │
       │ lot_size      DECIMAL(30,10)   (Qty step increment)    │
       │ status        VARCHAR(20)      ('ACTIVE','HALTED',...) │
       │ min_quantity  DECIMAL(30,10)   (Minimum order amount)  │
       │ created_at    TIMESTAMPTZ                              │
       │ updated_at    TIMESTAMPTZ                              │
       └───────────┬────────────────────────────────┬───────────┘
                   │ 1                              │ 1
                   │                                │
                   │ N (FK)                         │ N (FK)
                   ▼                                ▼
┌──────────────────────────────────────┐  ┌────────────────────────────────────────┐
│            market_trades             │  │              ohlc_candles              │
├──────────────────────────────────────┤  ├────────────────────────────────────────┤
│ id           UUID [PK] (trade_id)    │  │ market_id      VARCHAR(20) [PK, FK]    │
│ market_id    VARCHAR(20) [FK]        │  │ resolution     VARCHAR(5)  [PK] (1m,..)│
│ price        DECIMAL(30,10)          │  │ start_time     TIMESTAMPTZ [PK]        │
│ quantity     DECIMAL(30,10)          │  │ open_price     DECIMAL(30,10)          │
│ executed_at  TIMESTAMPTZ             │  │ high_price     DECIMAL(30,10)          │
└──────────────────────────────────────┘  │ low_price      DECIMAL(30,10)          │
                                          │ close_price    DECIMAL(30,10)          │
                                          │ volume         DECIMAL(30,10)          │
                                          │ quote_volume   DECIMAL(30,10)          │
                                          │ open_trade_at  TIMESTAMPTZ             │
                                          │ close_trade_at TIMESTAMPTZ             │
                                          └────────────────────────────────────────┘
```

---

## 3. Deep-Dive: Tables & Feature Mapping

---

### 🏛️ Table 1: `markets` — Trading Pair Authority
* **Feature Supported:** Exchange Product Catalog, Order Validation, Precision Constraints.
* **Why It Is Needed:** Every buy/sell order placed in the exchange must validate against strict market rules (e.g. Can I place an order for `0.0000001 BTC`? No, minimum is `0.0001 BTC`).
* **Column Breakdown:**

| Column | Data Type | Constraints | Purpose |
| :--- | :--- | :--- | :--- |
| `id` | `VARCHAR(20)` | `PRIMARY KEY` | Unique market symbol (e.g. `BTC-USDT`, `ETH-USDT`, `SOL-USDT`). |
| `base_asset` | `VARCHAR(10)` | `NOT NULL` | The commodity being traded (`BTC`, `ETH`). |
| `quote_asset` | `VARCHAR(10)` | `NOT NULL` | The pricing currency (`USDT`). |
| `tick_size` | `DECIMAL(30,10)`| `CHECK (tick_size > 0)` | Smallest allowable price movement (e.g. `$0.01`). |
| `lot_size` | `DECIMAL(30,10)` | `CHECK (lot_size > 0)` | Smallest allowable quantity increment (e.g. `0.0001`). |
| `status` | `VARCHAR(20)` | `CHECK (status IN ('ACTIVE','HALTED','MAINTENANCE'))` | Circuit breaker status for emergency exchange halts. |
| `min_quantity` | `DECIMAL(30,10)`| `CHECK (min_quantity > 0)` | Minimum order size to prevent micro-order spam. |
| `created_at` | `TIMESTAMPTZ` | `DEFAULT NOW()` | Market listing timestamp. |
| `updated_at` | `TIMESTAMPTZ` | `DEFAULT NOW()` | Configuration change timestamp. |

* **Default Seed Data:** Automatically seeds `BTC-USDT`, `ETH-USDT`, and `SOL-USDT` using `ON CONFLICT (id) DO NOTHING`.

---

### 📜 Table 2: `market_trades` — Immutable Execution Log
* **Feature Supported:** Rolling 24-hour Ticker Statistics (High, Low, Volume, % Change) & Deduplication.
* **Why It Is Needed:** Stores the historical record of every trade match executed by the Matching Engine.
* **Column Breakdown:**

| Column | Data Type | Constraints | Purpose |
| :--- | :--- | :--- | :--- |
| `id` | `UUID` | `PRIMARY KEY` | Canonical unique trade execution identifier produced by the Matching Engine. |
| `market_id` | `VARCHAR(20)` | `REFERENCES markets(id)` | Foreign key tying the trade to a valid market pair. |
| `price` | `DECIMAL(30,10)` | `CHECK (price > 0)` | Exact execution price. |
| `quantity` | `DECIMAL(30,10)` | `CHECK (quantity > 0)` | Exact base asset quantity traded. |
| `executed_at` | `TIMESTAMPTZ` | `NOT NULL` | The exact millisecond timestamp the trade was matched in the engine. |

* **Indexing & Performance Strategy:**
  ```sql
  CREATE INDEX idx_market_trades_rolling ON market_trades(market_id, executed_at DESC);
  ```
  * **Why:** The 24h ticker query searches `WHERE market_id = $1 AND executed_at >= NOW() - INTERVAL '24 hours'`. This composite B-Tree index enables instant index range scans without scanning entire table partitions.

---

### 📊 Table 3: `ohlc_candles` — Multi-Timeframe Candlestick Bars
* **Feature Supported:** Real-Time TradingView Charts, Technical Indicators (MACD, RSI, Moving Averages).
* **Why It Is Needed:** Computing candlestick charts on the fly from millions of raw trades is too slow for frontend UIs. Pre-aggregating into `ohlc_candles` enables sub-10ms chart load times.
* **Column Breakdown:**

| Column | Data Type | Constraints | Purpose |
| :--- | :--- | :--- | :--- |
| `market_id` | `VARCHAR(20)` | `PRIMARY KEY (Col 1)` | Market symbol (`BTC-USDT`). |
| `resolution` | `VARCHAR(5)` | `PRIMARY KEY (Col 2), CHECK IN ('1m','5m','15m','1h','1d')` | Timeframe bar bucket resolution. |
| `start_time` | `TIMESTAMPTZ` | `PRIMARY KEY (Col 3)` | Normalized bucket start boundary (e.g. `12:00:00`, `12:05:00`). |
| `open_price` | `DECIMAL(30,10)` | `CHECK (open_price > 0)` | First trade price in the time window. |
| `high_price` | `DECIMAL(30,10)` | `CHECK (high_price > 0)` | Highest trade price in the time window. |
| `low_price` | `DECIMAL(30,10)` | `CHECK (low_price > 0)` | Lowest trade price in the time window. |
| `close_price` | `DECIMAL(30,10)` | `CHECK (close_price > 0)` | Latest trade price in the time window. |
| `volume` | `DECIMAL(30,10)` | `DEFAULT 0` | Total base asset volume traded (`SUM(quantity)`). |
| `quote_volume` | `DECIMAL(30,10)` | `DEFAULT 0` | Total quote currency volume traded (`SUM(price * quantity)`). |
| `open_trade_at`| `TIMESTAMPTZ` | `NOT NULL` | Timestamp of the specific trade that set the `open_price`. |
| `close_trade_at`| `TIMESTAMPTZ` | `NOT NULL` | Timestamp of the specific trade that set the `close_price`. |

* **Composite Primary Key (`market_id`, `resolution`, `start_time`):**
  * Guarantees there is only ever **one** candle record per market per timeframe per time window.
* **Why `open_trade_at` and `close_trade_at` Exist:**
  * In distributed systems, Kafka messages can occasionally arrive out-of-order due to network retries.
  * When a trade arrives, PostgreSQL compares its `executed_at` against `open_trade_at` and `close_trade_at`. It only replaces `open_price` if the trade is strictly earlier, and only replaces `close_price` if the trade is strictly later. This guarantees 100% chart accuracy regardless of message arrival sequence.

* **Indexing for Chart Feeds:**
  ```sql
  CREATE INDEX idx_candles_time ON ohlc_candles(market_id, resolution, start_time DESC);
  ```
  * Allows the gateway to fetch the latest `N` candles (`ORDER BY start_time DESC LIMIT 100`) via a backward index scan in under 2 milliseconds.

---

## 4. Architectural Data Type Decisions

### 1. `DECIMAL(30,10)` vs `FLOAT / DOUBLE`
* Binary floating-point numbers (`FLOAT8`) suffer from precision rounding errors (e.g. `0.1 + 0.2 = 0.30000000000000004`).
* In financial systems, even a $0.00000001 discrepancy breaks financial ledgers.
* `DECIMAL(30,10)` guarantees exact fixed-point base-10 arithmetic up to 20 integer digits and 10 decimal fractional digits.

### 2. `TIMESTAMPTZ` vs `TIMESTAMP`
* `TIMESTAMPTZ` (Timestamp with Time Zone) converts all stored dates to UTC internally.
* Eliminates timezone drift bugs across servers running in different cloud regions.

### 3. `UUID` vs `BIGSERIAL` for Trades
* The Matching Engine generates unique UUIDs across distributed partitions without locking a centralized database auto-increment sequence.

---

## 5. Migration Execution

The migrations are automatically executed at application boot time via `postgres.RunMigrations(...)` in `cmd/server/main.go`.

To run or rollback manually via goose:
```powershell
# Apply migration
goose -dir services/market/migration postgres "$MARKET_DB_URL" up

# Rollback migration
goose -dir services/market/migration postgres "$MARKET_DB_URL" down
```
