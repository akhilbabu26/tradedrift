# `migration` — Matching Engine Database Schema & Migrations

**Directory:** `migration`  
**Service:** Matching Engine  
**Last Updated:** August 2026  

---

## 1. What This Folder Does

This folder contains the PostgreSQL database migration scripts for the **Matching Engine** service. 

While the Matching Engine keeps all active Order Books in volatile RAM for sub-millisecond execution speeds, it requires a single lightweight, ACID-compliant persistence table in PostgreSQL to store **Kafka offset checkpoints**.

---

## 2. Purpose & Why This Table Is Needed

The `kafka_checkpoints` table is the **single source of truth for crash recovery and durability**:

| Problem Without This Table | Solution With `kafka_checkpoints` |
| :--- | :--- |
| If the Matching Engine crashes, all in-memory Order Books are lost. | On restart, the engine queries this table to find the exact Kafka offset up to which events were fully processed and published. |
| Kafka consumer group offsets can drift if committed before trade events finish publishing. | We do NOT commit offsets to Kafka. The checkpoint is only written to Postgres **after** trades are acknowledged by Kafka and the depth snapshot is pushed to Redis. |
| Replaying the entire Kafka topic from offset 0 becomes too slow as event history grows. | The engine only needs to replay events from the recorded offset forward, achieving near-instant restarts. |

---

## 3. Migration Files

| File | Target Table | Description |
| :--- | :--- | :--- |
| `00001_create_kafka_checkpoints.sql` | `kafka_checkpoints` | Creates the primary checkpoint tracking table with composite primary key `(topic, partition)` |
| `00002_create_market_sequences.sql` | `market_sequences` | Creates the sequence tracking table for individual markets |
| `00003_create_market_snapshots.sql` | `market_snapshots` | Creates the order book state snapshot table and its sequence indexes |
| `README.md` | — | This documentation file |

---

## 4. Schema Breakdown

### `00001_create_kafka_checkpoints.sql`

```sql
CREATE TABLE IF NOT EXISTS kafka_checkpoints (
    topic      VARCHAR(255) NOT NULL,
    partition  INTEGER      NOT NULL,
    offset     BIGINT       NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (topic, partition)
);
```

### Column Specifications:

| Column | Type | Constraints | Description |
| :--- | :--- | :--- | :--- |
| `topic` | `VARCHAR(255)` | `NOT NULL` | The input Kafka topic name (e.g. `orders.submitted` or `orders.cancel-requested`). |
| `partition` | `INTEGER` | `NOT NULL` | The Kafka partition number (0, 1, 2, etc.). Offsets are partition-local. |
| `offset` | `BIGINT` | `NOT NULL` | The highest continuously processed Kafka message offset for this topic/partition. |
| `updated_at` | `TIMESTAMPTZ` | `NOT NULL DEFAULT NOW()` | Timestamp of the last successful checkpoint write. Used for operational monitoring and freshness alerts. |

### Key & Indexing:
- **Composite Primary Key `(topic, partition)`:** Ensures each partition within each topic has exactly one row. Lookups and UPSERTs run in $O(1)$ index time.

---

## 5. How It Works in Runtime (UPSERT Semantics)

During live operation, whenever `Publisher.process()` completes publishing trade executions to Kafka and updating the Redis depth snapshot, it executes an **idempotent UPSERT**:

```sql
INSERT INTO kafka_checkpoints (topic, partition, offset, updated_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (topic, partition)
DO UPDATE SET
    offset     = EXCLUDED.offset,
    updated_at = NOW();
```

- If no row exists for `(topic, partition)`: A new row is inserted.
- If a row exists: The `offset` is updated forward, and `updated_at` is set to current time.

---

## 6. How It Works in Recovery (Engine Restart)

When the Matching Engine starts up:

```
Matching Engine Boots
        │
        ▼
Query: SELECT topic, partition, offset FROM kafka_checkpoints
        │
        ├─ Row Found ──► Start Kafka reader from offset + 1
        │
        └─ No Row ─────► Start Kafka reader from offset 0 (beginning)
        │
        ▼
Replay events into OrderBook in ModeRecovery (suppress trade publication)
        │
        ▼
All historical events replayed ──► OrderBook is fully restored
        │
        ▼
Push fresh depth snapshot to Redis ──► Transition engine to ModeLive
```

---

## 7. Operational & Maintenance Guidelines

### How to apply manually (via psql):
```bash
psql -h localhost -U tradedrift_user -d tradedrift_matching -f services/matching-engine/migration/00001_create_kafka_checkpoints.sql
```

### How to inspect current checkpoints:
```sql
SELECT 
    topic, 
    partition, 
    offset, 
    updated_at, 
    NOW() - updated_at AS lag_duration 
FROM kafka_checkpoints;
```

---

## 8. What This Schema Does NOT Store

- Does NOT store individual orders or historical user transactions — stored in Order Service database (`services/order`).
- Does NOT store trade settlement or ledger records — stored in Wallet / Settlement database (`services/wallet`).
- Does NOT store order book depth or order queues — maintained purely in volatile memory (`internal/orderbook`) and cached in Redis.
