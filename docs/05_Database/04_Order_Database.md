# TradeDrift — Order Database Design

> **Status:** ✅ Frozen (V1.0)
> **Document:** 04_Order_Database.md
> **Directory:** docs/05_Database/
> **Last Updated:** July 2026

---

## 1. Purpose

The Order Database stores user orders, execution volumes, tracking states (open, filled, cancelling, cancelled), and transactional outbox queues.

---

## 2. Table Schemas

### 2.1 Table: `orders`
```sql
CREATE TABLE orders (
    id                  UUID PRIMARY KEY,                      -- UUIDv7
    user_id             UUID NOT NULL,                         -- Logical reference
    market_id           VARCHAR(20) NOT NULL,                  -- Logical reference
    side                VARCHAR(10) NOT NULL,                  -- 'BUY', 'SELL'
    order_type          VARCHAR(10) NOT NULL,                  -- 'LIMIT', 'MARKET'
    price               DECIMAL(30,10) NOT NULL,
    quantity            DECIMAL(30,10) NOT NULL,
    filled_quantity     DECIMAL(30,10) NOT NULL DEFAULT 0,
    remaining_quantity  DECIMAL(30,10) GENERATED ALWAYS AS (quantity - filled_quantity) STORED,
    status              VARCHAR(20) NOT NULL DEFAULT 'OPEN',   -- 'OPEN', 'PARTIALLY_FILLED', 'FILLED', 'CANCELLING', 'CANCELLED'
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 2.2 Table: `outbox`
```sql
CREATE TABLE outbox (
    id             UUID PRIMARY KEY,                      -- UUIDv7
    aggregate_id   UUID NOT NULL,                         -- e.g. order_id
    event_type     VARCHAR(50) NOT NULL,                  -- 'orders.created.v1', 'orders.cancel-requested.v1'
    payload        JSONB NOT NULL,
    partition_key  VARCHAR(100) NOT NULL,                 -- market_id (to route to matching partition)
    status         VARCHAR(20) NOT NULL DEFAULT 'PENDING',-- 'PENDING', 'PUBLISHED', 'FAILED'
    failed_reason  TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at   TIMESTAMPTZ
);
```

---

## 3. Query Design & Expected Patterns

### 3.1 Create Order (Transaction)
```sql
BEGIN;

INSERT INTO orders (id, user_id, market_id, side, order_type, price, quantity) 
VALUES ($1, $2, $3, $4, $5, $6, $7);

INSERT INTO outbox (id, aggregate_id, event_type, payload, partition_key) 
VALUES ($8, $1, 'orders.created.v1', $9, $3);

COMMIT;
```

### 3.2 List Orders for User (Keyset Pagination)
```sql
SELECT id, market_id, side, order_type, price, quantity, filled_quantity, status, created_at 
FROM orders 
WHERE user_id = $1 AND (created_at, id) < ($2, $3)
ORDER BY created_at DESC, id DESC
LIMIT $4;
```
*Index support:* Require a multi-column index on `(user_id, created_at DESC, id DESC)` to satisfy the keyset pagination without sorting.

### 3.3 Update Order Executed Match
```sql
UPDATE orders 
SET filled_quantity = filled_quantity + $1,
    status = CASE WHEN filled_quantity + $1 >= quantity THEN 'FILLED' ELSE 'PARTIALLY_FILLED' END,
    updated_at = NOW()
WHERE id = $2;
```
*Index support:* Primary key lookup `WHERE id = $2`.
