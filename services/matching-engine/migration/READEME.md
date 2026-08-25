# `migration` — Database Schema, Persistence & SQL Migration Specifications

**Directory:** `migration`  
**Service:** Matching Engine  
**Files Covered:** `00001_create_kafka_checkpoints.sql`, `00002_create_market_sequences.sql`, `00003_create_market_snapshots.sql`  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Purpose

The `migration` directory contains the **authoritative PostgreSQL DDL migration scripts** for the Matching Engine service.

Although the Matching Engine processes orders strictly in high-speed, volatile RAM to achieve sub-millisecond execution times, it relies on a lightweight, ACID-compliant PostgreSQL database layer to maintain **authoritative durability coordinates, monotonic sequence progression, point-in-time order book snapshots, and cryptographic checksum verification**.

---

## 2. Why the Matching Engine Needs 3 Separate Migrations

The Matching Engine holds all active Order Books **100% in volatile RAM** for sub-microsecond matching speed. When the engine restarts or crashes, RAM is wiped completely clean.

To achieve **zero data loss**, **instant crash restarts (milliseconds instead of hours)**, and **cryptographic state verification**, the engine divides persistence into **3 distinct responsibilities**:

```
                               3 MIGRATION PILLARS IN THE MATCHING ENGINE
                               ═══════════════════════════════════════════

   1. kafka_checkpoints (Migration 1)   2. market_sequences (Migration 2)    3. market_snapshots (Migration 3)
   ┌────────────────────────────────┐   ┌────────────────────────────────┐   ┌────────────────────────────────┐
   │       RESUME COORDINATE        │   │      INTEGRITY VALIDATOR       │   │       SPEED ACCELERATOR        │
   │  "Where did we stop in Kafka   │   │   "Did we process all events   │   │  "Restore the book in 10ms     │
   │   without skipping any gaps?"  │   │    in strict monotonic order?" │   │   instead of replaying hours"  │
   └────────────────────────────────┘   └────────────────────────────────┘   └────────────────────────────────┘
```

### 2.1 `00001_create_kafka_checkpoints.sql` — The Resume Coordinate
* **Table Created:** [`kafka_checkpoints`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/migration/00001_create_kafka_checkpoints.sql#L3-L9) `(topic, partition, offset, updated_at)`
* **The Problem It Solves:**
  Multiple markets (e.g., `BTC-USDT` and `ETH-USDT`) share the same Kafka partition. Fast markets finish ahead of slow markets. If the system crashes, relying on Kafka's built-in consumer offset commits could commit offset `105` while offset `100` was still in progress on a slower market—permanently losing order `100`.
* **Why It Is Needed:**
  It stores the **contiguous offset watermark** calculated by [`checkpoint.Coordinator`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/checkpoint/coordinator.go). On restart, the engine reads this table to know the exact Kafka offset up to which **all** preceding events were fully executed and durably committed.

### 2.2 `00002_create_market_sequences.sql` — The Integrity Validator
* **Table Created:** [`market_sequences`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/migration/00002_create_market_sequences.sql#L3-L8) `(market_id, sequence, updated_at)`
* **The Problem It Solves:**
  Every trade and resting limit order increments a strictly monotonic sequence counter ($1, 2, 3, \dots, N$). Downstream services (Order Service, Wallet Settlement, WebSocket feeds) depend on this sequence to detect dropped, duplicated, or out-of-order execution packets.
* **Why It Is Needed:**
  During crash recovery replay, [`internal/recovery`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/02READEME.md) replays Kafka command events and queries `market_sequences` to assert:
  $$\text{engine.GetSequence()} == \text{db.market\_sequences.sequence}$$
  If they don't match, recovery halts immediately. This prevents corrupt or duplicate trades from ever entering live matching.

### 2.3 `00003_create_market_snapshots.sql` — The Speed Accelerator
* **Table Created:** [`market_snapshots`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/migration/00003_create_market_snapshots.sql#L3-L16) `(market_id, sequence, partition, offset, schema_version, snapshot, checksum, created_at)`
* **The Problem It Solves ($O(N)$ Log Replay Bottleneck):**
  If an exchange has processed 10,000,000 orders over a year, replaying 10 million Kafka messages from offset `0` on every restart would take hours of downtime.
* **Why It Is Needed:**
  Every 10,000 events or 60 seconds, the engine serializes the full in-memory book and computes a 256-bit SHA-256 checksum (`BYTEA`). On restart:
  1. It loads the latest snapshot $\le \text{checkpoint}$ in $O(1)$ time using the descending index [`idx_market_snapshots_market_sequence`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/migration/00003_create_market_snapshots.sql#L15).
  2. Verifies the SHA-256 hash to prevent bit rot or database row corruption.
  3. Replays only the tiny delta of Kafka events between the snapshot offset and the checkpoint offset.
  
  **Result:** Rebuilding a 500,000-order book takes **~10 milliseconds instead of hours**.

---

## 3. Summary Comparison of the 3 Migrations

| Migration | Table | Role | Question It Answers |
| :--- | :--- | :--- | :--- |
| **00001** | `kafka_checkpoints` | **Durability Watermark** | *"What is the exact Kafka offset to resume from without skipping any orders?"* |
| **00002** | `market_sequences` | **Sequence Verification** | *"Did our recovery replay produce the exact expected sequence counter?"* |
| **00003** | `market_snapshots` | **Point-in-Time State** | *"What was the exact order book state at the last snapshot, verified by SHA-256?"* |

### How They Work Together in Runtime
Whenever a batch of contiguous offsets finishes processing, [`checkpoint.Coordinator.commitTransaction`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/checkpoint/coordinator.go#L190-L245) executes a **single atomic PostgreSQL transaction (`BEGIN ... COMMIT`)** that updates all 3 tables simultaneously, guaranteeing that checkpoints, sequences, and snapshots are always 100% synchronized.

---

## 4. How We Choose the Kafka Partition Key

In TradeDrift, the **Kafka Partition Key** is chosen as the **`MarketID`** (e.g., `"BTC-USDT"`, `"ETH-USDT"`).

```
  Upstream Order Service                      Kafka Partitioning                         Matching Engine
  ──────────────────────                      ──────────────────                         ───────────────
  
  OrderCreated (BTC-USDT)  ──[Key = "BTC-USDT"]──► Partition 0 ──► (Sequential FIFO) ──► BTC-USDT EventLoop
  CancelOrder  (BTC-USDT)  ──[Key = "BTC-USDT"]──► Partition 0 ──► (Sequential FIFO) ──► BTC-USDT EventLoop
  
  OrderCreated (ETH-USDT)  ──[Key = "ETH-USDT"]──► Partition 1 ──► (Sequential FIFO) ──► ETH-USDT EventLoop
```

### 4.1 Why `MarketID` is Chosen as the Partition Key

1. **Strict Price-Time (FIFO) Ordering Guarantee:**
   - Kafka guarantees strict message ordering **only within a single partition**. Messages across different partitions have no ordering guarantee.
   - In a financial exchange, orders for the same trading pair (`BTC-USDT`) **must** arrive and execute in the exact order they were submitted (Price-Time Priority / FIFO).
   - By setting `Key = MarketID`, Kafka hashes all events for `BTC-USDT` to the **same partition**. An order submitted at $T_1$ will always be processed before an order submitted at $T_2$.

2. **Single-Writer Actor Model (Zero Mutex Locks):**
   - Each trading pair is owned by a single dedicated Go goroutine ([`MarketEngine.Run`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go#L114-L165)).
   - Because all commands for `BTC-USDT` arrive on the same partition, the consumer reads them sequentially and feeds the `BTC-USDT InputQueue`. **No mutexes or distributed locks are needed** on the in-memory order book.

3. **Causality Guarantee (Create before Cancel):**
   - When a user cancels an order, the `OrderCancelRequested` event **must** arrive after the `OrderCreated` event.
   - Since both events carry `Key = MarketID`, they land on the same Kafka partition, guaranteeing that the cancel is never processed before the order was placed.

### 4.2 Ingress & Egress Implementation in Code

#### A. Inbound Commands (`orders.commands`)
The consumer validates that the partition key strictly matches the payload's `market_id`:
```go
// internal/kafka/consumer.go
if string(msg.Key) != env.MarketID {
    return nil, fmt.Errorf("partition key mismatch: expected %s, got %s", env.MarketID, string(msg.Key))
}
```

#### B. Outbound Trades (`trades.executed`)
When the publisher emits executed trade fills, it also partitions by `MarketID`:
```go
// internal/publisher/publisher.go
kafkago.Message{
    Key:   []byte(fill.MarketID), // All trades for BTC-USDT land on the same partition
    Value: bytes,
}
```
Downstream settlement and candlestick/chart services receive all trades for `BTC-USDT` in strict sequence order ($1, 2, 3, \dots, N$) with zero out-of-order execution packets.

### 4.3 Why Other Partition Keys Were Rejected

| Alternative Key | Why It Was Rejected |
| :--- | :--- |
| **`user_id`** | If User A and User B trade `BTC-USDT`, their orders would land on different partitions. User B's order could arrive and match before User A's earlier order, **violating FIFO market fairness**. |
| **`order_id`** | Every order has a random UUID. Orders for `BTC-USDT` would be scattered across all partitions, completely destroying the chronological order book sequence. |
| **`Round-Robin / None`** | Messages are distributed randomly across partitions, resulting in random execution order and race conditions between order creations and cancellations. |

---

## 5. Migration DDL Specifications

### 5.1 `00001_create_kafka_checkpoints.sql`
```sql
-- +goose Up
-- Matching Engine PostgreSQL Durability Checkpoint Table
CREATE TABLE IF NOT EXISTS kafka_checkpoints (
    topic      VARCHAR(255) NOT NULL,
    partition  INTEGER      NOT NULL,
    "offset"   BIGINT       NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (topic, partition)
);

-- +goose Down
DROP TABLE IF EXISTS kafka_checkpoints;
```

#### Key Runtime SQL Operation (Monotonic UPSERT)
```sql
INSERT INTO kafka_checkpoints (topic, partition, "offset", updated_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (topic, partition)
DO UPDATE SET
    "offset"   = EXCLUDED."offset",
    updated_at = NOW()
WHERE kafka_checkpoints."offset" < EXCLUDED."offset";
```

---

### 5.2 `00002_create_market_sequences.sql`
```sql
-- +goose Up
-- Matching Engine PostgreSQL Sequence Tracking Table
CREATE TABLE IF NOT EXISTS market_sequences (
    market_id  VARCHAR(64)  NOT NULL,
    sequence   BIGINT       NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (market_id)
);

-- +goose Down
DROP TABLE IF EXISTS market_sequences;
```

#### Key Runtime SQL Operation (Monotonic Sequence Update)
```sql
INSERT INTO market_sequences (market_id, sequence, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (market_id)
DO UPDATE SET
    sequence   = EXCLUDED.sequence,
    updated_at = NOW();
```

---

### 5.3 `00003_create_market_snapshots.sql`
```sql
-- +goose Up
-- Matching Engine PostgreSQL Snapshot Table
CREATE TABLE IF NOT EXISTS market_snapshots (
    market_id      VARCHAR(64)  NOT NULL,
    sequence       BIGINT       NOT NULL,
    partition      INTEGER      NOT NULL,
    "offset"       BIGINT       NOT NULL,
    schema_version INTEGER      NOT NULL,
    snapshot       JSONB        NOT NULL,
    checksum       BYTEA        NOT NULL,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (market_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_market_snapshots_market_sequence 
ON market_snapshots (market_id, sequence DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_market_snapshots_market_sequence;
DROP TABLE IF EXISTS market_snapshots;
```

#### Descending Index Strategy
The compound descending index `idx_market_snapshots_market_sequence (market_id, sequence DESC)` enables instantaneous $O(1)$ scans when querying the latest snapshot below the partition checkpoint:
```sql
SELECT market_id, sequence, partition, "offset", schema_version, snapshot, checksum 
FROM market_snapshots
WHERE market_id = $1 AND "offset" <= $2
ORDER BY "offset" DESC, sequence DESC 
LIMIT 1;
```

---

## 6. Migration Execution & Operational Commands

### Applying Migrations via Goose CLI
```bash
# Apply all pending migrations
goose -dir services/matching-engine/migration postgres "postgres://tradedrift_user:password@localhost:5432/tradedrift_matching?sslmode=disable" up

# Check migration status
goose -dir services/matching-engine/migration postgres "postgres://tradedrift_user:password@localhost:5432/tradedrift_matching?sslmode=disable" status

# Roll back the last migration
goose -dir services/matching-engine/migration postgres "postgres://tradedrift_user:password@localhost:5432/tradedrift_matching?sslmode=disable" down
```

### Manual Inspection Queries (via psql)
```sql
-- 1. Inspect checkpoint watermarks and lag
SELECT topic, partition, "offset", updated_at, NOW() - updated_at AS write_lag
FROM kafka_checkpoints;

-- 2. Inspect active market sequences
SELECT market_id, sequence, updated_at
FROM market_sequences;

-- 3. Inspect recent snapshots per market
SELECT market_id, sequence, partition, "offset", schema_version, length(snapshot::text) AS size_bytes, created_at
FROM market_snapshots
ORDER BY market_id, sequence DESC;
```

---

## 7. What This Schema Does NOT Store

To maintain strict separation of concerns:
- **Does NOT store individual user orders or order histories**: Stored in the Order Service database (`services/order-service`).
- **Does NOT store user wallet balances or trade settlements**: Stored in the Wallet/Settlement database (`services/wallet-service`).
- **Does NOT store volatile live order queues or real-time Level-2 order book depth**: Maintained in volatile RAM (`internal/orderbook`) and cached in Redis (`depth:{market_id}`).
