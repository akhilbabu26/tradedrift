# Major Problems Faced While Building the Notification Service

This document captures the distributed systems challenges, edge cases, failure recovery scenarios, and concurrency problems encountered and solved while building the Tradedrift **Notification Service**.

---

## Architecture Context

```
                             ┌────────────────────────────────────────────────────────┐
                             │                  Notification Service                  │
┌──────────────┐             │                                                        │             ┌──────────────┐
│ Kafka Broker │─(At-least)─►│ 1. Kafka Consumer (Manual Ack)                         │             │ PostgreSQL   │
│ Topics:      │   once      │    │                                                   │             │ Tables:      │
│ trade.settled│             │    ▼                                                   │             │ notifications│
│ order.events │             │ 2. PostgreSQL DB Transaction ─────────────────────────┼────────────►│ processed_ev │
│ portfolio... │             │    • processed_events (Idempotency check)              │             │ notif_outbox │
└──────────────┘             │    • notifications (Durable User Inbox)                │             └──────────────┘
                             │    • notification_outbox (Relay events)                │                    │
                             │    ▲                                                   │                    │
                             │    │ Polling Claim / Lease                             │                    │
                             │ 3. Outbox Publisher Worker ◄───────────────────────────┼────────────────────┘
                             │    │ (SKIP LOCKED + claim_token)                       │
                             │    ▼                                                   │             ┌──────────────┐
                             │ 4. Redis Publisher ────────────────────────────────────┼────────────►│ Redis Pub/Sub│
                             │    (user:notifications:<uid> / user:portfolio:<uid>)   │             │ (Live Push)  │
                             │                                                        │             └──────────────┘
                             └────────────────────────────────────────────────────────┘
```

---

## Table of Problems & Solutions

- [1. Guaranteeing Reliable Kafka Event Processing](#1-guaranteeing-reliable-kafka-event-processing)
- [2. Achieving Atomic Notification Creation](#2-achieving-atomic-notification-creation)
- [3. Preventing Duplicate Processing Across Rebalances and Restarts](#3-preventing-duplicate-processing-across-rebalances-and-restarts)
- [4. Designing a Reliable Transactional Outbox Pattern](#4-designing-a-reliable-transactional-outbox-pattern)
- [5. Handling Concurrent Outbox Workers with Leases and Claim Tokens](#5-handling-concurrent-outbox-workers-with-leases-and-claim-tokens)
- [6. Recovering from Outbox Worker Crashes](#6-recovering-from-outbox-worker-crashes)
- [7. Handling Redis Publishing Failures](#7-handling-redis-publishing-failures)
- [8. Handling Redis Publish + Database Update Failure (Double Publish Window)](#8-handling-redis-publish--database-update-failure-double-publish-window)
- [9. Separating Durable Notifications from Ephemeral Portfolio Updates](#9-separating-durable-notifications-from-ephemeral-portfolio-updates)
- [10. Preventing Unbounded Outbox Table Growth (Tiered Retention)](#10-preventing-unbounded-outbox-table-growth-tiered-retention)
- [11. Avoiding Full Table Scans During Retention Cleanup](#11-avoiding-full-table-scans-during-retention-cleanup)
- [12. Dead-Letter Queue (DLQ) Handling for Malformed Events](#12-dead-letter-queue-dlq-handling-for-malformed-events)
- [13. Strict Financial Decimal Validation at the Ingestion Boundary](#13-strict-financial-decimal-validation-at-the-ingestion-boundary)
- [14. Counterparty Privacy and Notification Sanitization](#14-counterparty-privacy-and-notification-sanitization)
- [15. Multi-Tenant Notification Ownership Enforcement](#15-multi-tenant-notification-ownership-enforcement)
- [16. Deterministic Keyset (Cursor-Based) Pagination](#16-deterministic-keyset-cursor-based-pagination)
- [17. Preserving First-Read Timestamps (`read_at`)](#17-preserving-first-read-timestamps-read_at)
- [18. Handling Kafka Offset Commit Failures Without Event Skips](#18-handling-kafka-offset-commit-failures-without-event-skips)
- [19. Zero-Lock Contention Between Outbox Publisher and Retention Worker](#19-zero-lock-contention-between-outbox-publisher-and-retention-worker)
- [20. End-to-End Observability of the Asynchronous Outbox Lifecycle](#20-end-to-end-observability-of-the-asynchronous-outbox-lifecycle)
- [21. Graceful Shutdown and In-Flight Outbox Lease Draining](#21-graceful-shutdown-and-in-flight-outbox-lease-draining)
- [22. Atomic Unread Count Aggregation vs. Counter Drift](#22-atomic-unread-count-aggregation-vs-counter-drift)
- [23. User-Isolated Redis Pub/Sub Channel Namespacing](#23-user-isolated-redis-pubsub-channel-namespacing)
- [⭐ The Top 8 Distributed Systems Challenges for Interviews & Portfolio](#-the-top-8-distributed-systems-challenges-for-interviews--portfolio)

---

### 1. Guaranteeing Reliable Kafka Event Processing

* **The Problem:** Kafka uses at-least-once delivery, meaning network blips, rebalances, and broker heartbeats can deliver the same message multiple times. Committing the Kafka offset *before* database processing risks permanent message loss if the service crashes mid-processing; committing it *after* without deduplication risks duplicate database state.
* **The Fix:** Manual offset management coupled with an atomic idempotency check in PostgreSQL. The consumer only commits the Kafka offset synchronously *after* the database transaction commits successfully. If the database transaction fails, the offset is not committed, allowing Kafka to redeliver the event.

---

### 2. Achieving Atomic Notification Creation

* **The Problem:** A single `TradeSettled` event affects two distinct users (Buyer and Seller) and generates multiple downstream writes:
  ```
  TradeSettled
       │
       ├── Buyer notification (notifications table)
       ├── Seller notification (notifications table)
       ├── Buyer outbox event (notification_outbox table)
       ├── Seller outbox event (notification_outbox table)
       └── Processed event log (processed_events table)
  ```
  If any write failed or if the service crashed halfway through, the database would be left in an inconsistent partial state (e.g., buyer notified, seller completely missing).
* **The Fix:** Wrap all five operations inside a single, atomic PostgreSQL transaction (`BEGIN ... COMMIT`). Either every record is committed, or the transaction rolls back cleanly with zero side-effects.

---

### 3. Preventing Duplicate Processing Across Rebalances and Restarts

* **The Problem:** When consumer groups rebalance or nodes restart, messages already written to the database can be redelivered. Without protection, users would receive duplicate notifications in their inboxes.
* **The Fix:** A dedicated `processed_events` table indexed by `event_id PRIMARY KEY`. Before executing any business logic, the service executes:
  ```sql
  INSERT INTO processed_events (event_id, event_type, processed_at)
  VALUES ($1, $2, NOW())
  ON CONFLICT (event_id) DO NOTHING;
  ```
  If `RowsAffected == 0`, the transaction is skipped immediately as an idempotent no-op and the Kafka offset is acknowledged.

---

### 4. Designing a Reliable Transactional Outbox Pattern

* **The Problem:** Delivering notifications to users requires publishing to Redis Pub/Sub so API Gateways can push WebSocket messages. However, PostgreSQL and Redis cannot participate in a distributed two-phase commit (2PC). If PostgreSQL commits but Redis is down, the push is lost forever. If Redis publishes first but PostgreSQL aborts, users receive phantom notifications for trades that never persisted.
* **The Fix:** The **Transactional Outbox Pattern**:
  ```
  Kafka Consumer ──► PostgreSQL Transaction:
                         ├── INSERT notifications
                         ├── INSERT notification_outbox (status = 'PENDING')
                         └── COMMIT
  
  Outbox Worker  ──► Polls notification_outbox
                 ──► PUBLISH to Redis
                 ──► UPDATE notification_outbox SET status = 'PROCESSED'
  ```
  The message is guaranteed to be durably saved before any network transmission to Redis is attempted.

---

### 5. Handling Concurrent Outbox Workers with Leases and Claim Tokens

* **The Problem:** When running multiple instances of the Notification Service, multiple background workers poll `notification_outbox` concurrently. Two workers could pick up the same `PENDING` record and send duplicate WebSocket messages to Redis.
* **The Fix:** **Lease-based claims with UUID claim tokens**:
  1. Worker generates a random `claim_token = uuid.New()`.
  2. Worker atomically claims a batch of rows:
     ```sql
     UPDATE notification_outbox
     SET status = 'PROCESSING',
         claim_token = $1,
         lease_expires_at = NOW() + INTERVAL '30 seconds'
     WHERE id IN (
         SELECT id FROM notification_outbox
         WHERE status = 'PENDING' 
            OR (status = 'PROCESSING' AND lease_expires_at < NOW())
         ORDER BY created_at ASC
         LIMIT 50
         FOR UPDATE SKIP LOCKED
     );
     ```
  3. When marking complete, the worker checks:
     ```sql
     UPDATE notification_outbox
     SET status = 'PROCESSED', published_at = NOW()
     WHERE id = $1 AND claim_token = $2;
     ```
  If the lease expired and another worker took over, the original worker's write is rejected because the token no longer matches.

---

### 6. Recovering from Outbox Worker Crashes

* **The Problem:** If a worker claims 50 events, marks them `PROCESSING`, and immediately encounters an Out-Of-Memory (OOM) panic or gets killed by Kubernetes before publishing to Redis, those events could be stuck in `PROCESSING` forever.
* **The Fix:** **Time-based lease expiration (`lease_expires_at`)**:
  The claim query explicitly includes `(status = 'PROCESSING' AND lease_expires_at < NOW())`. Dead workers' claims naturally expire after 30 seconds, allowing healthy workers to automatically adopt and publish them without manual intervention.

---

### 7. Handling Redis Publishing Failures

* **The Problem:** Redis is used for real-time WebSocket delivery, but Redis is an in-memory pub/sub broker without durable topic queues. If Redis restarts or drops the connection mid-publish, notifications could fail.
* **The Fix:** PostgreSQL remains the source of truth. If `redis.Publish()` fails, the outbox record is not marked `PROCESSED`. Instead, its `retry_count` is incremented and its `lease_expires_at` is set to the past, making it immediately available for the next polling cycle. If Redis stays down, notifications remain safely stored in user inboxes in PostgreSQL.

---

### 8. Handling Redis Publish + Database Update Failure (Double Publish Window)

* **The Problem:** Consider this edge case:
  1. Worker publishes notification to Redis successfully.
  2. Database crashes before the worker can update status to `PROCESSED`.
  3. Worker crashes or lease expires.
  4. Another worker re-publishes the event to Redis.
* **The Fix:** Distributed at-least-once delivery makes this edge case inevitable. We solved this by designing **downstream idempotency**: Every outbox payload contains both the `event_id` and the `notification_id`. The API Gateway WebSocket client maintains a small LRU cache of recently pushed IDs and discards duplicates within a 60-second window.

---

### 9. Separating Durable Notifications from Ephemeral Portfolio Updates

* **The Problem:** The service ingests both `TradeSettled` events (which users want to see in their notification center indefinitely) and `PortfolioUpdated` events (which fire dozens of times per second just to update equity graphs on the UI). Storing every portfolio tick in `notifications` would cause table bloat and flood the user's notification list with noise.
* **The Fix:** Dual ingestion paths:
  - **Durable Events (`TradeSettled`, `OrderCancelled`):** Written to `notifications` (user inbox) **AND** `notification_outbox` (WebSocket push).
  - **Ephemeral Events (`PortfolioUpdated`):** Written **ONLY** to `notification_outbox` for live WebSocket pushing, bypassing the `notifications` inbox table entirely.

---

### 10. Preventing Unbounded Outbox Table Growth (Tiered Retention)

* **The Problem:** Under continuous trading, the `notification_outbox` table accumulates millions of `PROCESSED` rows. Without pruning, query performance on `FOR UPDATE` degrades, index size explodes, and disk storage runs out.
* **The Fix:** **Tiered Retention Background Worker** with chunked deletion:
  - `user:portfolio:*` events: Retention = **24 Hours** (high-frequency, short shelf-life).
  - `user:notifications:*` events: Retention = **7 Days** (user notification history).
  - Cleanup runs every hour in batches of 1,000 using `FOR UPDATE SKIP LOCKED` to prevent blocking active publishers.

---

### 11. Avoiding Full Table Scans During Retention Cleanup

* **The Problem:** Running a periodic query like `DELETE FROM notification_outbox WHERE status = 'PROCESSED' AND published_at < $1` causes a full sequential scan on large tables, causing high CPU usage and lock contention.
* **The Fix:** A specialized **Partial Index** (`00004_create_outbox_retention_index.sql`):
  ```sql
  CREATE INDEX idx_outbox_retention 
  ON notification_outbox (published_at, target_channel)
  WHERE status = 'PROCESSED';
  ```
  Because the index only indexes `PROCESSED` rows, it remains small, fast, and allows the retention cleanup query to perform an instant index range scan.

---

### 12. Dead-Letter Queue (DLQ) Handling for Malformed Events

* **The Problem:** If a publisher writes corrupt JSON or missing schema fields to Kafka, the notification consumer will crash or fail to parse. If it retries forever, it blocks the Kafka partition, halting all notifications behind it. If it silently skips it, critical audit data is lost.
* **The Fix:** **Dead-Letter Queue (DLQ) Pattern**:
  ```
  Malformed Event ──► Attempt Parse ──► Fail ──► Produce to DLQ Topic (notification.dlq)
                                                           │
                                                (Kafka Offset Committed)
  ```
  If DLQ production succeeds, the offset is committed and the consumer moves forward. If DLQ production fails, the offset is *not* committed, ensuring nothing is lost.

---

### 13. Strict Financial Decimal Validation at the Ingestion Boundary

* **The Problem:** Using standard JSON unmarshaling into Go `float64` for prices, amounts, or quantities introduces IEEE-754 binary floating-point rounding errors (e.g., `96411.43 * 0.1 = 9641.143000000001`), corrupting balance and trade display values.
* **The Fix:** All financial fields are ingested strictly as strings and validated using arbitrary-precision decimal libraries (`shopspring/decimal`). Any value that fails decimal validation is rejected before hitting the database.

---

### 14. Counterparty Privacy and Notification Sanitization

* **The Problem:** A single `TradeSettled` event contains details for both the Buyer and Seller:
  - Buyer User ID & Buyer Order ID
  - Seller User ID & Seller Order ID
  If this raw payload were broadcast, User A would see User B's internal database UUID and order ID, creating a major security and privacy violation.
* **The Fix:** **Payload Sanitization**: The service constructs two distinct payloads:
  - Buyer Payload: Includes only Buyer Order ID, execution price, quantity, and asset. Zero counterparty identifiers.
  - Seller Payload: Includes only Seller Order ID, execution price, quantity, and asset. Zero counterparty identifiers.

---

### 15. Multi-Tenant Notification Ownership Enforcement

* **The Problem:** In an API supporting `PATCH /api/v1/notifications/{id}/read`, a malicious user could guess or brute-force another user's notification UUID and mark it read or delete it.
* **The Fix:** Mandatory composite ownership queries. Every repository query filters on both `id` AND `user_id`:
  ```sql
  UPDATE notifications
  SET read_at = NOW()
  WHERE id = $1 AND user_id = $2;
  ```
  If a user attempts to modify an ID belonging to someone else, `RowsAffected == 0` and the API returns `404 Not Found`.

---

### 16. Deterministic Keyset (Cursor-Based) Pagination

* **The Problem:** Traditional `OFFSET / LIMIT` pagination suffers from two flaws:
  1. *Performance:* High offsets (`OFFSET 10000`) force the database to scan and discard 10,000 rows.
  2. *Drift:* When new notifications arrive while a user is scrolling, offsets shift, causing duplicate or skipped notifications.
* **The Fix:** **Keyset Pagination** on composite cursor `(created_at, id)`:
  ```sql
  SELECT * FROM notifications
  WHERE user_id = $1 
    AND (created_at, id) < ($2, $3)
  ORDER BY created_at DESC, id DESC
  LIMIT $4;
  ```
  This query is backed by index `idx_notifications_user_created (user_id, created_at DESC, id DESC)`, guaranteeing O(1) performance regardless of how deep the user paginates.

---

### 17. Preserving First-Read Timestamps (`read_at`)

* **The Problem:** If a user clicks "Mark as Read" multiple times (or multiple client apps sync simultaneously), an unconditional `UPDATE SET read_at = NOW()` overwrites the historical timestamp of when the user originally opened the notification.
* **The Fix:** Safe timestamp assignment using PostgreSQL `COALESCE`:
  ```sql
  UPDATE notifications
  SET read_at = COALESCE(read_at, NOW()),
      updated_at = NOW()
  WHERE id = $1 AND user_id = $2;
  ```
  If `read_at` is already populated, it is preserved unchanged.

---

### 18. Handling Kafka Offset Commit Failures Without Event Skips

* **The Problem:** A subtle distributed race:
  1. The database transaction commits successfully.
  2. The network disconnects during `kafkaConsumer.CommitOffset()`.
  3. The consumer panics or restarts.
  If the application fails to handle this, restarting could cause consumer rebalance loops.
* **The Fix:** The consumer catches commit errors and retries the commit up to 3 times with exponential backoff before allowing the worker to crash. If the process dies and redelivers the event later, Step 3 (`processed_events`) safely drops the duplicate with zero side effects.

---

### 19. Zero-Lock Contention Between Outbox Publisher and Retention Worker

* **The Problem:** Both the Outbox Publisher worker and the Retention Cleaner worker query `notification_outbox` concurrently. If both used standard table scans or naive `FOR UPDATE`, they would block each other, stalling real-time notifications during cleanup cycles.
* **The Fix:** Strict query segregation:
  - **Publisher:** Targets `status = 'PENDING'` with `ORDER BY created_at ASC FOR UPDATE SKIP LOCKED`.
  - **Cleaner:** Targets `status = 'PROCESSED'` using the partial index `idx_outbox_retention` with `FOR UPDATE SKIP LOCKED`.
  Because the two workers target mutually exclusive `status` values and use `SKIP LOCKED`, they never block one another.

---

### 20. End-to-End Observability of the Asynchronous Outbox Lifecycle

* **The Problem:** Because notifications flow asynchronously across Kafka, PostgreSQL, and Redis, simple HTTP health checks are insufficient to know if notifications are actually being delivered.
* **The Fix:** Prometheus metrics and structured Zap logging instrumentation across the full pipeline:
  - `notifications_processed_total{event_type="trade.settled"}`
  - `notifications_outbox_published_total{status="success|failure"}`
  - `notifications_outbox_purged_total{tier="portfolio|notification"}`
  - `notifications_outbox_latency_seconds` (time from database insert to Redis publish).

---

### 21. Graceful Shutdown and In-Flight Outbox Lease Draining

* **The Problem:** During a deployment or rolling restart (`SIGTERM`), terminating the service immediately could interrupt an active Redis publish or leave claimed rows in `PROCESSING` status until their 30-second lease expired, unnecessarily delaying user pushes.
* **The Fix:** Coordinated graceful shutdown:
  1. Stop Kafka consumer from receiving new messages (`consumer.Close()`).
  2. Signal the outbox worker loop context cancellation.
  3. Wait with a `sync.WaitGroup` for all in-flight Redis publish operations to complete and mark their rows `PROCESSED`.
  4. Close database connection pools cleanly.

---

### 22. Atomic Unread Count Aggregation vs. Counter Drift

* **The Problem:** Maintaining an explicit `unread_notifications_count` counter column on a user table quickly becomes inaccurate when notifications are deleted, batched as read, or inserted during concurrent device connections.
* **The Fix:** Pure indexed query aggregation:
  ```sql
  SELECT COUNT(*) FROM notifications
  WHERE user_id = $1 AND read_at IS NULL;
  ```
  Backed by a partial index:
  ```sql
  CREATE INDEX idx_notifications_unread 
  ON notifications (user_id) 
  WHERE read_at IS NULL;
  ```
  This index only tracks unread rows, resulting in sub-millisecond count queries without risk of counter desynchronization.

---

### 23. User-Isolated Redis Pub/Sub Channel Namespacing

* **The Problem:** If Redis pub/sub channel names were broad (e.g. `trades`, `notifications`), API Gateways would have to subscribe to a global firehose and filter messages in memory, wasting gateway CPU and risking message leaks across tenants.
* **The Fix:** Strict user-scoped channel namespacing:
  - Notification Push: `user:notifications:<user_uuid>`
  - Portfolio Live Push: `user:portfolio:<user_uuid>`
  API Gateways only subscribe to the specific channels of currently connected WebSockets for authenticated sessions.

---

## ⭐ The Top 8 Distributed Systems Challenges for Interviews & Portfolio

When explaining the engineering depth of this service on a resume, GitHub portfolio, or technical interview, focus on these **8 core challenges**:

```
 1. Kafka At-Least-Once & Idempotency  ──────────► PostgreSQL processed_events boundary
 2. Multi-Record Atomic Consistency   ──────────► Single-transaction Buyer/Seller/Outbox writes
 3. Transactional Outbox Pattern       ──────────► PostgreSQL ──(durability)──► Redis Pub/Sub
 4. Distributed Concurrency & Leases   ──────────► claim_token + lease_expires_at + SKIP LOCKED
 5. Fault Tolerance & Self-Healing     ──────────► Automatic lease adoption after worker OOM
 6. Durable vs. Ephemeral Data Paths   ──────────► Inbox persistence vs. WebSocket-only stream
 7. Non-Blocking Tiered Retention      ──────────► Partial indexing + SKIP LOCKED chunked purge
 8. Dead-Letter Queue (DLQ) Resilience ──────────► Corrupt payload isolation without head-of-line blocking
```

### 1. Kafka At-Least-Once Delivery & Idempotent Processing
> **The Story:** "Kafka guarantees at-least-once delivery, so network timeouts, rebalances, and service restarts deliver duplicate events. We decoupled message consumption from state mutations by establishing an idempotency boundary in PostgreSQL using `processed_events`. We only commit Kafka offsets after the database transaction succeeds, guaranteeing zero message loss and zero duplicate notifications."

### 2. Atomic Multi-Record Creation with PostgreSQL Transactions
> **The Story:** "A single `TradeSettled` event must generate distinct buyer and seller notifications, separate outbox records for real-time WebSocket delivery, and an idempotency marker. To prevent partial state inconsistencies where one party is notified but the other is not, all five database operations execute inside a single atomic PostgreSQL transaction."

### 3. Transactional Outbox Pattern for Reliable Redis Delivery
> **The Story:** "To bridge durable PostgreSQL storage with ephemeral Redis Pub/Sub without distributed 2PC transactions, we implemented the Transactional Outbox pattern. Notifications are committed to `notification_outbox` in the same transaction as user data, guaranteeing that real-time WebSocket pushes are never lost even if Redis experiences a temporary outage."

### 4. Concurrent Outbox Workers with Leases & Claim Tokens
> **The Story:** "When multiple worker replicas poll the outbox table concurrently, preventing duplicate pushes is critical. We designed a lease-based claim mechanism using UUID claim tokens and `FOR UPDATE SKIP LOCKED`. Workers acquire exclusive 30-second leases; when marking a row processed, the worker validates its token, preventing race conditions if a previous lease expired."

### 5. Automatic Crash Recovery for Asynchronous Pipelines
> **The Story:** "If an outbox worker claims a batch of notifications and suddenly crashes (e.g. OOM), those rows cannot remain stuck in `PROCESSING`. We implemented self-healing leases: healthy workers automatically query for expired leases (`lease_expires_at < NOW()`) and reclaim orphaned events without human intervention."

### 6. Durable vs. Ephemeral Dual-Path Architecture
> **The Story:** "A notification service handles high-value notifications (Trade Executions, Cancellations) and high-volume ephemeral telemetry (Portfolio Graph Updates). We segregated the pipeline: durable events persist to the user's notification inbox table, while high-frequency portfolio ticks bypass the inbox and route directly through the outbox to Redis, preventing table bloat."

### 7. Non-Blocking Tiered Retention with Partial Indexes
> **The Story:** "With millions of events passing through the outbox, unbounded table growth causes severe database degradation. We implemented tiered retention (24h for portfolio events, 7d for notifications). To eliminate lock contention and table scans, cleanup runs in chunked 1,000-row batches using a partial index on `(published_at, target_channel) WHERE status = 'PROCESSED'` and `FOR UPDATE SKIP LOCKED`."

### 8. Dead-Letter Queue (DLQ) and Strict Financial Validation
> **The Story:** "To prevent corrupt financial payloads from halting Kafka consumers (head-of-line blocking), we implemented a dead-letter queue. Events failing strict decimal parsing or schema validation are routed to a `notification.dlq` topic and their offsets acknowledged, preserving the bad records for offline inspection while keeping the main trading pipeline flowing."
