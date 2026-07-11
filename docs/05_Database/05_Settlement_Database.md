# TradeDrift — Settlement Database Design

> **Status:** ✅ Frozen (V1.0)
> **Document:** 05_Settlement_Database.md
> **Directory:** docs/07_Database/
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
    price        DECIMAL(30,10) NOT NULL,
    quantity     DECIMAL(30,10) NOT NULL,
    status       VARCHAR(20) NOT NULL DEFAULT 'SETTLED', -- 'SETTLED', 'FAILED'
    settled_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

---

## 3. Query Design & Expected Patterns

### 3.1 Idempotency Check & Verification
When receiving a `TradeExecuted` message, the Settlement Service checks if it has already been processed:
```sql
SELECT status 
FROM settled_trades 
WHERE trade_id = $1;
```
*Index support:* Covered by the primary key index on `trade_id`.

### 3.2 Commit Settlement Log
Once the Wallet Service successfully acknowledges the balance mutations via the `SettleTrade` gRPC, the status is committed:
```sql
INSERT INTO settled_trades (trade_id, buyer_id, seller_id, market_id, price, quantity) 
VALUES ($1, $2, $3, $4, $5, $6);
```
*Index support:* Covered by the primary key index on `trade_id`.
