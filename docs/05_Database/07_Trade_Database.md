# TradeDrift — Trade Database Design

> **Status:** ✅ Frozen (V1.0)
> **Document:** 07_Trade_Database.md
> **Directory:** docs/07_Database/
> **Last Updated:** July 2026

---

## 1. Purpose

The Trade Database stores all matching executions, providing a centralized ledger for historical trade records, auditing, and price history feeds.

---

## 2. Table Schema

### 2.1 Table: `trades`
```sql
CREATE TABLE trades (
    id            UUID PRIMARY KEY,                      -- Match Trade ID
    market_id     VARCHAR(20) NOT NULL,
    buyer_id      UUID NOT NULL,
    seller_id     UUID NOT NULL,
    buy_order_id  UUID NOT NULL,
    sell_order_id UUID NOT NULL,
    price         DECIMAL(30,10) NOT NULL,
    quantity      DECIMAL(30,10) NOT NULL,
    executed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

---

## 3. Query Design & Expected Patterns

### 3.1 List Historical Trades for a Market (Keyset Pagination)
Used to populate charts or market history components:
```sql
SELECT id, price, quantity, executed_at 
FROM trades 
WHERE market_id = $1 AND (executed_at, id) < ($2, $3)
ORDER BY executed_at DESC, id DESC
LIMIT $4;
```
*Index support:* Require a multi-column index on `(market_id, executed_at DESC, id DESC)`.

### 3.2 List Historical Trades for a Specific User (Keyset Pagination)
Used to display execution history:
```sql
SELECT id, market_id, buyer_id, seller_id, price, quantity, executed_at 
FROM trades 
WHERE (buyer_id = $1 OR seller_id = $1) AND (executed_at, id) < ($2, $3)
ORDER BY executed_at DESC, id DESC
LIMIT $4;
```
*Index support:* To optimize this query without slow OR-conditions, we split this index into:
- `idx_trades_buyer_executed_at` on `(buyer_id, executed_at DESC, id DESC)`
- `idx_trades_seller_executed_at` on `(seller_id, executed_at DESC, id DESC)`
The service executes two subqueries and merges them in memory to satisfy the index path.

---

## 4. Capacity & Growth Estimates

* **Expected Daily Throughput:** ~5,000,000 trades/day
* **Expected Annual Growth:** ~1,825,000,000 trades/year
* **Expected Storage Footprint:** 
  - Average row size (including indices): ~150 bytes per row.
  - Daily storage growth: ~750 MB/day.
  - Annual storage growth: ~270 GB/year.

---

## 5. Partitioning Strategy

* **V1 Setup:** Standard non-partitioned table. No partition overhead needed on day-one deployments.
* **Scale Trigger:** When table size exceeds **100,000,000 rows** (approximately 20 days of peak trading).
* **Target Migration Strategy:** Partition by range (monthly) using the `executed_at` timestamp.
  - Future tables will use Postgres declarative partitioning:
  ```sql
  CREATE TABLE trades (
      id            UUID,
      market_id     VARCHAR(20) NOT NULL,
      buyer_id      UUID NOT NULL,
      seller_id     UUID NOT NULL,
      buy_order_id  UUID NOT NULL,
      sell_order_id UUID NOT NULL,
      price         DECIMAL(30,10) NOT NULL,
      quantity      DECIMAL(30,10) NOT NULL,
      executed_at   TIMESTAMPTZ NOT NULL,
      PRIMARY KEY (id, executed_at) -- executed_at must be part of composite PK
  ) PARTITION BY RANGE (executed_at);
  ```

