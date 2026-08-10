# Order Service — Database Migrations Package (`migration`)

> **Package:** SQL Migrations  
> **Directory:** `services/order/migration/`  
> **Target Database:** `tradedrift_order` (PostgreSQL)  
> **Migration Runner:** Goose (`tradedrift/platform/postgres`)

---

## 1. Purpose & Architectural Role

The `migration` package contains the DDL (Data Definition Language) SQL migration scripts for the Order Service. These scripts are automatically applied on service startup by `main.go` using the platform Goose migration engine.

It establishes the schema for:
1. **`orders` table**: Stores order domain records, statuses, quantities, prices, and client idempotency keys.
2. **`outbox` table**: Implements the **Transactional Outbox Pattern** for asynchronous Kafka event publishing.

---

## 2. Migration Files in This Directory

| File | Role |
| :--- | :--- |
| [`001_create_orders.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/migration/001_create_orders.sql) | Creates `orders` and `outbox` tables, constraint definitions, and performance indexes |

---

## 3. Schema & Column Specifications

### 3.1 `orders` Table

```sql
CREATE TABLE IF NOT EXISTS orders (
    id                  UUID            PRIMARY KEY,
    user_id             UUID            NOT NULL,
    market_id           VARCHAR(20)     NOT NULL,
    side                VARCHAR(4)      NOT NULL,
    order_type          VARCHAR(10)     NOT NULL,
    price               DECIMAL(30,10),
    quantity            DECIMAL(30,10)  NOT NULL,
    filled_quantity     DECIMAL(30,10)  NOT NULL DEFAULT 0,
    remaining_quantity  DECIMAL(30,10)  NOT NULL,
    status              VARCHAR(20)     NOT NULL,
    idempotency_key     UUID            UNIQUE,
    created_at          TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);
```

* **`price`**: `DECIMAL(30,10)` — Nullable for pure `MARKET` sell orders.
* **`idempotency_key`**: `UUID UNIQUE` — Prevents duplicate order placements via PostgreSQL constraint code `23505` (`orders_idempotency_key_key`).
* **`remaining_quantity`**: Regular column updated during partial execution fills.

---

### 3.2 `outbox` Table

```sql
CREATE TABLE IF NOT EXISTS outbox (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id    UUID        NOT NULL,
    event_type      VARCHAR(50) NOT NULL,
    payload         JSONB       NOT NULL,
    partition_key   VARCHAR(20) NOT NULL,
    published_at    TIMESTAMPTZ,
    processing_at   TIMESTAMPTZ,
    attempts        INT         NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

| Column | Data Type | Purpose |
| :--- | :--- | :--- |
| `id` | `UUID` | Primary key (`gen_random_uuid()`). |
| `aggregate_id` | `UUID` | Order ID associated with this event. |
| `event_type` | `VARCHAR(50)` | `"OrderCreated"` or `"OrderCancelRequested"`. |
| `payload` | `JSONB` | JSON event payload sent to Kafka. |
| `partition_key` | `VARCHAR(20)` | `market_id` (e.g. `"BTC-USDT"`) ensuring per-market partition ordering in Kafka. |
| `published_at` | `TIMESTAMPTZ` | Timestamp set when Kafka ACK is received (`NULL` while pending). |
| `processing_at` | `TIMESTAMPTZ` | Timestamp set during atomic claim lease to prevent worker collisions. |
| `attempts` | `INT` | Number of publish retry attempts. |
| `last_error` | `TEXT` | Failure error message for monitoring. |
| `created_at` | `TIMESTAMPTZ` | Record creation timestamp. |

---

## 4. Performance Indexes

```sql
-- Fast order lookups by user and status
CREATE INDEX IF NOT EXISTS idx_orders_user_id   ON orders(user_id);
CREATE INDEX IF NOT EXISTS idx_orders_market_id ON orders(market_id);
CREATE INDEX IF NOT EXISTS idx_orders_status    ON orders(status);

-- Keyset Cursor Pagination Index (user_id, market_id, created_at DESC, id DESC)
CREATE INDEX IF NOT EXISTS idx_orders_user_market_created
    ON orders(user_id, market_id, created_at DESC);

-- Partial Index for Unpublished Outbox Worker Queries
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished
    ON outbox(created_at) WHERE published_at IS NULL;
```
