# TradeDrift — PostgreSQL Database Design Specification

> **Status:** ✅ Designed (V1)
> **Document:** 18_PostgreSQL_Design.md
> **Service:** Platform Architecture
> **Version:** V1.0
> **Last Updated:** July 2026

---

## 1. Database Topology & High Availability

To support reliable transactional writes and separate reporting queries, the TradeDrift platform implements a dedicated PostgreSQL cluster deployment model:

```
                              [ API / Transactional Traffic ]
                                             │
                                     (Writes / Updates)
                                             ▼
                                     [ pgBouncer Pooler ]
                                             │
                                             ▼
                                    [ Primary Master DB ]
                                             │
                                   (Streaming Replication)
                                             ▼
                                   [ Read-Only Replica ]
                                             │
                                             ▲
                               (Queries: Portfolio/Trade/Admin)
```

### 1.1 Deployment Topology
* **Primary-Replica Cluster:** Services perform mutations on a single **Primary Master** instance. Transactional logs are streamed asynchronously using PostgreSQL Streaming Replication to **one or more Read-Only Replicas** used for heavy queries (e.g., historical trade lookups, portfolio valuation calculations).
* **Connection Pooling:** All microservices connect to PostgreSQL through **pgBouncer** instances deployed in **Transaction Pooling** mode. Transaction pooling optimizes connection reuse, allowing the cluster to handle thousands of concurrent client connections with minimal backend memory overhead.
* **Storage Standard:** Solid State Drives (SSDs) in a RAID-10 configuration, with write-ahead log (WAL) archiving enabled for point-in-time recovery (PITR).

---

## 2. Core Schema Conventions

All databases across the microservices ecosystem enforce standardized column types and precision constraints:

* **Primary Keys:** Exclusively UUIDs generated as **UUIDv7** by the owning service before ingestion (see `docs/03_Standards/ID_Correlation_Standard.md`). Generating keys in application code prevents round-trip sequencing latencies and ensures database compatibility.
* **Numeric Representation:** All balance values, order quantities, execution prices, and transaction amounts must be stored as **`DECIMAL(30,10)`**. This ensures exact representation up to 10 decimal places, preventing floating-point inaccuracies.
* **Timezone Safety:** All timestamp columns must use the **`TIMESTAMPTZ`** (timestamp with time zone) format. Applications write timestamps in UTC, and formatting transitions are handled at the presentation layer.

---

## 3. Transactional Outbox Schema & Leasing Mechanics

To ensure atomic state mutations and message delivery guarantees across microservices, every write-side service implements a **Transactional Outbox** table within its database schema.

### 3.1 Outbox Schema
```sql
CREATE TABLE outbox (
    id             UUID PRIMARY KEY,                      -- UUIDv7 event identifier
    aggregate_id   UUID NOT NULL,                         -- Target aggregate UUID (e.g. order_id, user_id)
    event_type     VARCHAR(50) NOT NULL,                  -- Versioned type, e.g. 'orders.created.v1'
    payload        JSONB NOT NULL,                        -- Complete JSON payload content
    partition_key  VARCHAR(50) NOT NULL,                  -- Kafka partition routing key (e.g., market_id)
    status         VARCHAR(20) NOT NULL DEFAULT 'PENDING',-- 'PENDING', 'PUBLISHED', 'FAILED'
    failed_reason  TEXT,                                  -- Failure context/error traceback
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at   TIMESTAMPTZ                            -- NULL until sent successfully
);

CREATE INDEX idx_outbox_pending ON outbox(created_at) WHERE status = 'PENDING';
```

### 3.2 High-Performance Concurrent Leasing
To scale outbox publishing, multiple publisher daemons (running within service replicas) poll the database concurrently. To prevent race conditions and lock wait states, daemons lease batches using the **`FOR UPDATE SKIP LOCKED`** row-locking clause:

```sql
-- Daemon leases a batch of 50 pending records atomically
BEGIN;

SELECT id, event_type, payload, partition_key 
FROM outbox 
WHERE status = 'PENDING' 
ORDER BY created_at ASC 
LIMIT 50 
FOR UPDATE SKIP LOCKED;

-- [Daemon publishes events to Kafka and awaits acks=all broker confirmation]

-- On success:
UPDATE outbox 
SET status = 'PUBLISHED', published_at = NOW() 
WHERE id = ANY($1);

COMMIT;
```

This mechanism ensures:
1. No two worker daemons lock the same outbox rows, preventing serial blockages.
2. High throughput by fanning out writes to Kafka in parallel partitions.
3. Event delivery is guaranteed even if a daemon crashes immediately after fetching database records.

---

## 4. Transaction Isolation & Deadlock Prevention

### 4.1 Isolation Levels
All database mutations execute under the default **Read Committed** isolation level. To prevent lost updates during concurrent mutations (e.g., balance updates or portfolio recalculations), application transactions lock rows using explicit row-level locks:

```sql
SELECT available_balance, reserved_balance 
FROM wallets 
WHERE user_id = $1 AND asset = $2 
FOR UPDATE;
```

### 4.2 Lock Ordering Standard
Deadlocks can occur if concurrent transactions lock the same resources in opposing order (e.g., Trade Settlement locking Buyer → Seller vs. Seller → Buyer). 

To prevent this, any transaction modifying multiple rows within the same table must **sort row lock acquisitions by ascending key order** in application code:

```go
// Example: Lock two counterparty wallets in ascending UUID order
var firstLockUUID, secondLockUUID string
if buyerUUID < sellerUUID {
    firstLockUUID = buyerUUID
    secondLockUUID = sellerUUID
} else {
    firstLockUUID = sellerUUID
    secondLockUUID = buyerUUID
}

// Execute queries sequentially
db.Exec("SELECT id FROM wallets WHERE id = $1 FOR UPDATE", firstLockUUID)
db.Exec("SELECT id FROM wallets WHERE id = $2 FOR UPDATE", secondLockUUID)
```

---

## 5. Schema Migration & Zero-Downtime Evolution

All databases use migration frameworks (such as `golang-migrate` or `dbmate`) to track schema versions. Schema changes must adhere to the **expand-and-contract** design pattern to support rolling service updates without downtime.

```
                  [ Phase 1: Expand ]
            (Add new columns / Dual-write)
                          │
                          ▼
               [ Phase 2: Deploy Code ]
         (Point services to read new fields)
                          │
                          ▼
                  [ Phase 3: Contract ]
         (Drop deprecated columns / Remove constraints)
```

### Zero-Downtime Rules:
1. **No Destructive Operations:** Drop columns, rename columns, or constraint deletions must never occur in the same release cycle as the application code deploy.
2. **Nullable New Columns:** Newly added columns must be declared as `NULL` or contain a default value.
3. **Rename Pattern:** To rename `col_old` to `col_new`:
   - Phase 1: Add `col_new` as nullable.
   - Phase 2: Update application code to write to both `col_old` and `col_new`, reading from `col_old` as fallback. Run a migration script to populate existing values.
   - Phase 3: Update application code to read/write exclusively from `col_new`.
   - Phase 4: Issue database migration to drop `col_old`.

---

## 6. Service Invariants

- **PGD-1 (Exact Precision):** Float or double types must not be used for balances, prices, or quantities. These values must use `DECIMAL(30,10)`.
- **PGD-2 (Deterministic Lock Order):** Transactions acquiring multiple row locks within a single transaction must sort their keys in ascending lexical order before executing locks.
- **PGD-3 (Outbox Lock Safety):** Outbox polling daemons must utilize `FOR UPDATE SKIP LOCKED` to prevent lock contention between parallel processing instances.
