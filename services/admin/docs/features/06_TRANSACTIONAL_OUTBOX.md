# Feature 06: Transactional Outbox Pattern

## 1. What This Feature Does
The **Transactional Outbox Pattern** eliminates dual-write inconsistencies between the Admin Service's relational database (PostgreSQL) and the messaging backbone (Apache Kafka):
* **Atomic Staging**: Every domain event (`market-halted`, `market-resumed`, `wallet-frozen`, `wallet-unfrozen`, `user-suspended`, `user-unsuspended`) is written into an `admin_outbox` table in the exact same SQL transaction that mutates the operational state.
* **Guaranteed Delivery Worker**: A dedicated background process (`OutboxPublisher`) periodically polls unpublished events, dispatches them to Kafka with `RequiredAcks: kafka.RequireAll`, and marks them as published only after Kafka acknowledges receipt.

---

## 2. Why We Need It
Directly calling `kafkaWriter.WriteMessages()` from an HTTP request handler creates the fatal **Dual-Write Inconsistency Problem**:
1. **Scenario A (Database Succeeds, Kafka Fails)**: PostgreSQL commits the market halt, but Kafka is down or network-partitioned. The publish call errors out. Downstream services (e.g. Notification, Liquidity) never receive the event, causing severe operational blind spots.
2. **Scenario B (Kafka Succeeds, Database Fails)**: Kafka receives the event and consumers act on it, but PostgreSQL subsequently rolls back due to a constraint violation. Downstream systems react to an action that technically never happened in the system of record.
3. **At-Least-Once Guarantee**: The outbox table guarantees that no event is lost, even if Kafka brokers remain offline for hours.

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **Outbox Publisher** | [`services/admin/internal/service/outbox_publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/outbox_publisher.go#L30-L160) | Periodic ticker (1s), parallel topic batching, Kafka producer |
| **Outbox Repository** | [`services/admin/internal/repository/postgres/outbox_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/outbox_repo.go) | `GetUnpublishedEvents`, `MarkEventsPublished` |
| **Transaction Manager**| [`services/admin/internal/repository/postgres/tx_manager.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/tx_manager.go) | Injects event into `admin_outbox` inside atomic transaction |
| **Database Schema** | [`services/admin/migrations/00003_create_admin_outbox.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00003_create_admin_outbox.sql) | DDL with indexes on `(published_at, created_at)` |
| **Metrics** | [`services/admin/internal/metrics/metrics.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/metrics/metrics.go#L127-L135) | `RecordOutboxBacklogDepth`, `RecordOutboxOldestAge` |

---

## 4. How We Achieve This Feature

1. **Step 1: Synchronous Enqueue inside DB Transaction**:
   ```sql
   INSERT INTO admin_outbox (id, event_type, topic, payload, created_at)
   VALUES ($1, 'MARKET_HALTED', 'admin.market-halted.v1', $4, NOW());
   ```
   If the transaction fails, the event is automatically rolled back.
2. **Step 2: Non-Blocking Polling (`OUTBOX_INTERVAL = 1s`)**:
   `OutboxPublisher` fetches up to 100 unpublished rows using concurrency-safe row locking:
   ```sql
   SELECT id, topic, payload FROM admin_outbox
   WHERE published_at IS NULL
   ORDER BY created_at ASC
   LIMIT 100
   FOR UPDATE SKIP LOCKED;
   ```
3. **Step 3: Grouping & Parallel Dispatch**:
   Events are grouped by Kafka topic. Parallel goroutines write to each topic with:
   - `kafka.RequireAll`: Guarantees full broker replica quorum acknowledgement.
   - Hash partitioning by key (e.g. `market_id` or `user_id`) to preserve strict chronological ordering per entity.
4. **Step 4: Atomic Acknowledgment**:
   Upon successful broker response:
   ```sql
   UPDATE admin_outbox 
   SET published_at = NOW() 
   WHERE id = ANY($1);
   ```

---

## 5. Execution Flow

```
                 TICK EVENT (Every 1 Second)
                             │
                             ▼
  ┌──────────────────────────────────────────────────────────┐
  │ 1. Poll Unpublished Events from PostgreSQL               │
  │    SELECT * FROM admin_outbox WHERE published_at IS NULL │
  │    ORDER BY created_at LIMIT 100 FOR UPDATE SKIP LOCKED  │
  └───────────────────────────┬──────────────────────────────┘
                              │
                   Are there any events?
                              ├── NO ──► Sleep until next tick
                              │
                             └── YES
                              │
                              ▼
  ┌──────────────────────────────────────────────────────────┐
  │ 2. Group Events by Topic & Dispatch Parallel Goroutines  │
  │    - admin.market-halted.v1                              │
  │    - admin.market-resumed.v1                             │
  │    - admin.wallet-frozen.v1                              │
  │    - admin.user-suspended.v1                             │
  └───────────────────────────┬──────────────────────────────┘
                              │
                              ▼
  ┌──────────────────────────────────────────────────────────┐
  │ 3. Publish to Apache Kafka via kafka.Writer              │
  │    - Balance: Hash partitioning by entity ID             │
  │    - RequiredAcks: RequireAll                            │
  └───────────────────────────┬──────────────────────────────┘
                              │
                     Did Kafka Acknowledge?
                              ├── NO ──► Log error, back off with jitter, retry next tick
                              │
                             └── YES
                              │
                              ▼
  ┌──────────────────────────────────────────────────────────┐
  │ 4. Mark Outbox Events Published in PostgreSQL            │
  │    UPDATE admin_outbox SET published_at = NOW()          │
  └──────────────────────────────────────────────────────────┘
```
