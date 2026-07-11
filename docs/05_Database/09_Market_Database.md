# TradeDrift — Market Database Design

> **Status:** ✅ Frozen (V1.0)
> **Document:** 09_Market_Database.md
> **Directory:** docs/07_Database/
> **Last Updated:** July 2026

---

## 1. Purpose

The Market Database stores active trading pair parameters (prices, tick sizing, lot sizes) and aggregated daily trading statistics.

---

## 2. Table Schemas

### 2.1 Table: `markets`
```sql
CREATE TABLE markets (
    id           VARCHAR(20) PRIMARY KEY,               -- e.g. 'BTC-USDT'
    base_asset   VARCHAR(10) NOT NULL,
    quote_asset  VARCHAR(10) NOT NULL,
    tick_size    DECIMAL(30,10) NOT NULL,               -- e.g. 0.0100000000
    lot_size     DECIMAL(30,10) NOT NULL,               -- e.g. 0.0001000000
    is_enabled   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 2.2 Table: `market_stats_daily`
```sql
CREATE TABLE market_stats_daily (
    market_id   VARCHAR(20) NOT NULL REFERENCES markets(id),
    window_date DATE NOT NULL,
    open_price  DECIMAL(30,10) NOT NULL,
    high_price  DECIMAL(30,10) NOT NULL,
    low_price   DECIMAL(30,10) NOT NULL,
    close_price DECIMAL(30,10) NOT NULL,
    volume      DECIMAL(30,10) NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (market_id, window_date)
);
```

---

## 3. Query Design & Expected Patterns

### 3.1 Fetch Enabled Markets (Query)
```sql
SELECT id, base_asset, quote_asset, tick_size, lot_size 
FROM markets 
WHERE is_enabled = TRUE;
```
*Index support:* Require an index on `is_enabled` (or a partial index `WHERE is_enabled = TRUE`).

### 3.2 Update Market 24h Statistics
```sql
INSERT INTO market_stats_daily (market_id, window_date, open_price, high_price, low_price, close_price, volume) 
VALUES ($1, CURRENT_DATE, $2, $2, $2, $2, $3)
ON CONFLICT (market_id, window_date) 
DO UPDATE SET 
    high_price = GREATEST(market_stats_daily.high_price, EXCLUDED.high_price),
    low_price = LEAST(market_stats_daily.low_price, EXCLUDED.low_price),
    close_price = EXCLUDED.close_price,
    volume = market_stats_daily.volume + EXCLUDED.volume,
    updated_at = NOW();
```
*Index support:* Handled by the composite primary key index on `(market_id, window_date)`.
