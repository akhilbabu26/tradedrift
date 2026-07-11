# TradeDrift — Database Indexing Strategy

> **Status:** ✅ Frozen (V1.0)
> **Document:** 10_Index_Strategy.md
> **Directory:** docs/05_Database/
> **Last Updated:** July 2026

---

## 1. Indexing Principles

Indexes must directly correspond to, and be justified by, expected application query patterns. Over-indexing degrades write (INSERT/UPDATE/DELETE) performance and increases buffer pool memory consumption.

All index selections in TradeDrift are driven by:
1. **Keyset Cursor Pagination:** Requiring multi-column indexes matching sorting direction.
2. **Transaction Locking Paths:** Supporting fast single-row index scans to minimize lock duration.
3. **Outbox Lease Loops:** Preventing sequence table scans during high-frequency poll intervals.

---

## 2. Core Index Catalog

### 2.1 Order Service (`orders` table)
* **Query Pattern:** Keyset-paginated retrieval of orders for a user:
  ```sql
  WHERE user_id = $1 AND (created_at, id) < ($2, $3) ORDER BY created_at DESC, id DESC
  ```
* **Index Configuration:**
  ```sql
  CREATE INDEX idx_orders_user_pagination ON orders (user_id, created_at DESC, id DESC);
  ```
  *Rationale:* Combines the filtering column (`user_id`) and sorting columns in order, allowing index-only pagination without a filesort.

### 2.2 Trade Service (`trades` table)
* **Query Pattern 1:** Market trade feed retrieval:
  ```sql
  WHERE market_id = $1 AND (executed_at, id) < ($2, $3) ORDER BY executed_at DESC, id DESC
  ```
* **Index Configuration 1:**
  ```sql
  CREATE INDEX idx_trades_market_pagination ON trades (market_id, executed_at DESC, id DESC);
  ```

* **Query Pattern 2:** User executions (where user is either buyer OR seller):
  ```sql
  WHERE (buyer_id = $1 OR seller_id = $1)
  ```
* **Index Configuration 2 (Splitting OR Conditions):**
  ```sql
  CREATE INDEX idx_trades_buyer_executed ON trades (buyer_id, executed_at DESC, id DESC);
  CREATE INDEX idx_trades_seller_executed ON trades (seller_id, executed_at DESC, id DESC);
  ```
  *Rationale:* Using standard OR logic bypasses single indexes. The service executes two distinct queries utilizing these indexes and merges them in memory.

### 2.3 Transactional Outbox (`outbox` table)
* **Query Pattern:** Polling for pending records:
  ```sql
  WHERE status = 'PENDING' ORDER BY created_at ASC
  ```
* **Index Configuration:**
  ```sql
  CREATE INDEX idx_outbox_leasing ON outbox (created_at) WHERE status = 'PENDING';
  ```
  *Rationale:* A partial index targeting only `'PENDING'` entries keeps index size extremely small and eliminates scans over published records.
