# TradeDrift — Settlement Database Design

> **Status:** ✅ Frozen (V1.0)
> **Document:** 05_Settlement_Database.md
> **Directory:** docs/05_Database/
> **Last Updated:** July 2026

---

## 1. Purpose

The Settlement Database tracks match settlements processed by the Settlement Service. It acts as the local transaction log to enforce idempotency when consuming `trades.executed.v1` events.

---

## 2. Table Schema

### 2.1 Table: `settled_trades`
```sql
CREATE TABLE settled_trades (
    trade_id     UUID PRIMARY KEY,                      -- Match execution Trade ID
    buyer_id     UUID NOT NULL,
    seller_id    UUID NOT NULL,
    market_id    VARCHAR(20) NOT NULL,
    price        NUMERIC(18, 8) NOT NULL,
    quantity     NUMERIC(18, 8) NOT NULL,
    status       VARCHAR(20) NOT NULL,                  -- 'PENDING', 'SETTLED', 'FAILED'
    sequence     BIGINT NOT NULL DEFAULT 0,             -- Matching engine execution sequence (migration 00002)
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_settled_trades_created_at_pending 
ON settled_trades(created_at) 
WHERE status = 'PENDING';
```

---

## 3. Query Design & Expected Patterns

### 3.1 Idempotency Check & Verification
When receiving a `TradeExecuted` message, the Settlement Service checks if it has already been recorded:
```sql
SELECT status 
FROM settled_trades 
WHERE trade_id = $1;
```
*Index support:* Covered by the primary key index on `trade_id`.

### 3.2 Two-Phase Settlement Record
1. **Record Pending Settlement (Pre-RPC):**
```sql
INSERT INTO settled_trades (trade_id, buyer_id, seller_id, market_id, price, quantity, status, sequence, created_at)
VALUES ($1, $2, $3, $4, $5, $6, 'PENDING', $7, $8);
```

2. **Commit Settlement Log (Post-RPC Success):**
Once the Wallet Service successfully acknowledges the balance mutations via the `SettleTrade` gRPC, the status is committed:
```sql
UPDATE settled_trades 
SET status = 'SETTLED', settled_at = $2 
WHERE trade_id = $1;
```
*Index support:* Covered by the primary key index on `trade_id`.

### 3.3 Recovery Polling
A background recovery worker queries unfinalized trades stuck in `PENDING` past the recovery timeout:
```sql
SELECT trade_id, buyer_id, seller_id, market_id, price, quantity, status, sequence, created_at, settled_at
FROM settled_trades
WHERE status = 'PENDING' AND created_at < $1
ORDER BY created_at ASC
LIMIT $2;
```
*Index support:* Covered by partial index `idx_settled_trades_created_at_pending`.
