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
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              UUID NOT NULL,
    asset                VARCHAR(20) NOT NULL,
    quantity             NUMERIC(28, 8) NOT NULL DEFAULT 0,
    total_cost           NUMERIC(28, 8) NOT NULL DEFAULT 0,
    average_entry_price  NUMERIC(28, 8) NOT NULL DEFAULT 0,
    realized_pnl         NUMERIC(28, 8) NOT NULL DEFAULT 0,
    version              BIGINT NOT NULL DEFAULT 1,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_holdings_user_asset UNIQUE (user_id, asset)
);

CREATE INDEX IF NOT EXISTS idx_holdings_user_id ON holdings(user_id);
```

### 2.2 Table: `processed_user_trades`
```sql
CREATE TABLE processed_user_trades (
    trade_id     UUID NOT NULL,
    user_id      UUID NOT NULL,                         -- Recipient of the user trade event
    market_id    VARCHAR(20) NOT NULL,
    sequence     BIGINT NOT NULL,
    order_id     UUID NOT NULL,
    role         VARCHAR(10) NOT NULL,
    price        NUMERIC(28, 8) NOT NULL,
    quantity     NUMERIC(28, 8) NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (trade_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_processed_user_trades_user_id ON processed_user_trades(user_id);
```

### 2.3 Table: `processed_market_sequences`
```sql
CREATE TABLE processed_market_sequences (
    market_id    VARCHAR(20) NOT NULL,
    sequence     BIGINT NOT NULL,
    trade_id     UUID NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (market_id, sequence)
);
```

### 2.4 Table: `portfolio_outbox`
```sql
CREATE TABLE portfolio_outbox (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type    VARCHAR(100) NOT NULL,
    aggregate_id  VARCHAR(100) NOT NULL,
    payload       JSONB NOT NULL,
    status        VARCHAR(20) NOT NULL DEFAULT 'PENDING', -- 'PENDING', 'PROCESSING', 'PUBLISHED', 'FAILED'
    retry_count   INT NOT NULL DEFAULT 0,
    last_error    TEXT,
    claimed_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_portfolio_outbox_pending 
ON portfolio_outbox(created_at) 
WHERE status IN ('PENDING', 'PROCESSING');
```

---

## 3. Query Design & Expected Patterns

### 3.1 Fetch User Holdings (Query)
```sql
SELECT id, user_id, asset, quantity, total_cost, average_entry_price, realized_pnl, version, created_at, updated_at 
FROM holdings 
WHERE user_id = $1;
```
*Index support:* Covered by `idx_holdings_user_id`.

### 3.2 Update Holding from PortfolioUserTrade (Atomic Transaction)
When a user trade settles, the portfolio service processes the event within an atomic database transaction:
```sql
BEGIN;

-- 1. Check user-level idempotency
SELECT trade_id, user_id, market_id, sequence, order_id, role, price, quantity, processed_at
FROM processed_user_trades
WHERE trade_id = $1 AND user_id = $2;

-- 2. Verify market sequence consistency
INSERT INTO processed_market_sequences (market_id, sequence, trade_id)
VALUES ($1, $2, $3)
ON CONFLICT (market_id, sequence) DO UPDATE SET sequence = EXCLUDED.sequence
RETURNING trade_id;

-- 3. Lock or initialize holding row
SELECT id, user_id, asset, quantity, total_cost, average_entry_price, realized_pnl, version, created_at, updated_at
FROM holdings
WHERE user_id = $1 AND asset = $2
FOR UPDATE;

-- 4. Apply weighted-average cost update
UPDATE holdings 
SET quantity = $3,
    total_cost = $4,
    average_entry_price = $5,
    realized_pnl = $6,
    version = version + 1,
    updated_at = NOW()
WHERE user_id = $1 AND asset = $2;

-- 5. Record processed user trade
INSERT INTO processed_user_trades (trade_id, user_id, market_id, sequence, order_id, role, price, quantity)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- 6. Stage outbox event for portfolio update
INSERT INTO portfolio_outbox (event_type, aggregate_id, payload)
VALUES ('PortfolioUpdated', $1, $2);

COMMIT;
```
*Index support:* Covered by unique constraint indices and primary keys.
