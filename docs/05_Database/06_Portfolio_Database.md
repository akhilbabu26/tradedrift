# TradeDrift — Portfolio Database Design

> **Status:** ✅ Frozen (V1.0)
> **Document:** 06_Portfolio_Database.md
> **Directory:** docs/05_Database/
> **Last Updated:** July 2026

---

## 1. Purpose

The Portfolio Database stores read-only user holdings, average entry costs, and trade logs to power dashboard revaluations and PnL metrics.

---

## 2. Table Schemas

### 2.1 Table: `holdings`
```sql
CREATE TABLE holdings (
    id                   UUID PRIMARY KEY,                      -- UUIDv7
    user_id              UUID NOT NULL,
    asset                VARCHAR(10) NOT NULL,
    total_quantity       DECIMAL(30,10) NOT NULL DEFAULT 0,
    average_entry_price  DECIMAL(30,10) NOT NULL DEFAULT 0,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_holdings_user_asset UNIQUE (user_id, asset)
);
```

### 2.2 Table: `processed_trades`
```sql
CREATE TABLE processed_trades (
    trade_id     UUID PRIMARY KEY,                      -- Match Trade ID
    user_id      UUID NOT NULL,                         -- Recipient of the trade event
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

---

## 3. Query Design & Expected Patterns

### 3.1 Fetch User Holdings (Query)
```sql
SELECT asset, total_quantity, average_entry_price 
FROM holdings 
WHERE user_id = $1;
```
*Index support:* Require an index on `user_id`.

### 3.2 Update Holding from UserTradeSettled (Transaction)
When a user trade settles, we update the holdings and track idempotency:
```sql
BEGIN;

-- 1. Check if trade already processed
SELECT 1 FROM processed_trades WHERE trade_id = $1;

-- 2. Lock holding row
SELECT id, total_quantity, average_entry_price 
FROM holdings 
WHERE user_id = $2 AND asset = $3 
FOR UPDATE;

-- 3. Recalculate average cost & quantity
-- ... perform cost calculation logic ...
UPDATE holdings 
SET total_quantity = $4,
    average_entry_price = $5,
    updated_at = NOW()
WHERE id = $6;

-- 4. Mark trade as processed
INSERT INTO processed_trades (trade_id, user_id) VALUES ($1, $2);

COMMIT;
```
*Index support:* Covered by unique constraint indices.
