# Notification Service — Internal Architecture & Package Guide

This document provides a comprehensive technical guide to the `internal/` packages of the **TradeDrift Notification Service**. It details the purpose of each directory, the problems solved, the implementation strategies, the key functions, and the end-to-end data flows across the system.

---

## Architecture Overview & Directory Hierarchy

```
services/notification/internal/
├── config/       # Environment configuration loading & validation
├── handler/      # gRPC transport boundary & request translation
├── kafka/        # Kafka consumer group & DLQ event ingestion
├── metrics/      # Prometheus observability & operational counters
├── model/        # Domain entities, outbox states & Redis envelopes
├── publisher/    # Transactional Outbox polling & Redis Pub/Sub worker
├── repository/   # Storage interfaces & PostgreSQL persistence adapter
│   └── postgres/ # Raw SQL implementations, transactions & outbox leases
└── service/      # Core business logic, counterparty privacy & idempotency
```

---

## 1. `config/` — Configuration Management

### Purpose
Centralizes all runtime configuration parameters required by the microservice, loading them from environment variables with sensible defaults and strict validation.

### Problem It Solves
- **Scattered magic strings**: Eliminates ad-hoc `os.Getenv` calls throughout the business logic.
- **Fail-Fast Startup**: Catches invalid timeouts, malformed addresses, or missing credentials during service boot before accepting traffic or connecting to infrastructure.

### Key Types & Functions
- `Config`: Root configuration struct holding database DSN, Redis address, Kafka broker list, consumer group IDs, gRPC port, HTTP metrics port, and publisher tuning options.
- `Load() (*Config, error)`: Reads environment variables, assigns production defaults, and validates required parameters.

---

## 2. `handler/` — gRPC Transport Layer

### Purpose
Implements the Protobuf-generated `NotificationServiceServer` interface, translating incoming RPCs into service-layer domain invocations and mapping domain results to Protobuf responses.

### Problem It Solves
- **Decoupled Transport**: Keeps gRPC/HTTP serialization details separate from core business logic.
- **Unified Error Mapping**: Translates internal Go domain errors (`ErrInvalidUserID`, `ErrNotificationNotFound`, etc.) into canonical gRPC status codes (`codes.InvalidArgument`, `codes.NotFound`, `codes.Internal`).

### Key Functions
- `CreateNotification(ctx, req)`: Unpacks user ID, title, message, reference IDs, and optional `idempotency_key`, forwarding them to `service.CreateNotification`.
- `GetNotifications(ctx, req)`: Handles keyset pagination parameters (`cursor_time`, `cursor_id`, `limit`) and maps database entities to `notificationv1.NotificationItem`. Slices the `limit + 1` query result to set `has_more` accurately.
- `MarkAsRead(ctx, req)`: Validates ownership and marks a specific notification as read.
- `MarkAllAsRead(ctx, req)`: Marks all unread notifications for a user as read and returns the updated count.
- `GetUnreadCount(ctx, req)`: Returns total unread notifications for badge rendering.

---

## 3. `kafka/` — Asynchronous Domain Event Ingestion

### Purpose
Manages the Kafka consumer group lifecycle, subscribing to internal exchange domain topics (`trades.settled.v1`, `orders.cancelled.v1`, `portfolios.updated.v1`).

### Problem It Solves
- **Dual-Write / Loss Avoidance**: Ensures Kafka offsets are committed **strictly after** the PostgreSQL transaction has committed to disk. If the database fails, the offset is not committed, ensuring at-least-once delivery.
- **Poison Message Deadlock**: Malformed payloads or messages missing required domain IDs (`event_id`) cannot be processed. Instead of crashing or blocking the consumer partition, they are routed to a Dead-Letter Queue (`notifications.dlq`) before committing the partition offset.

### Key Functions
- `Start(ctx)`: Boots the consumer loop and listens to assigned topic partitions.
- `consume()`: Reads batches of Kafka records and dispatches them sequentially.
- `routeMessage(ctx, msg)`: Decodes JSON event headers, verifies the schema, and calls the corresponding `service` event handler.
- `sendToDLQ(ctx, msg, err)`: Enriches poison records with diagnostic metadata (error reason, timestamp, source topic) and publishes them to the DLQ topic.

---

## 4. `metrics/` — Observability & Instrumentation

### Purpose
Exposes Prometheus operational metrics for latency, error rates, throughput, and queue backlog.

### Problem It Solves
- **Silent Failures & Backlog Blindspots**: Alerts operators if outbox processing lags, Redis encounters network drops, or poison messages spike in the DLQ.

### Key Metrics Defined
- `NotificationsCreatedTotal`: Counter labeled by notification `type` (`INFO`, `TRADE_FILL`, `SYSTEM`, `ACCOUNT`).
- `KafkaEventsProcessedTotal`: Counter labeled by `topic` and `status` (`success`, `duplicate_skipped`, `dlq_poison`, `error`).
- `OutboxEventsPublishedTotal`: Counter labeled by `target_channel`.
- `OutboxPublishErrorsTotal`: Counter tracking transient or persistent Redis publish failures.
- `OutboxPendingGauge`: Gauge monitoring pending records awaiting publication.

---

## 5. `model/` — Domain Models & Value Objects

### Purpose
Defines pure domain structures, database entities, and Redis transport envelopes without dependencies on transport protocols or external SDKs.

### Problem It Solves
- **Circular Imports**: Serves as a shared dependency layer between repository, service, publisher, and handler.
- **Clear Schema Boundaries**: Separates the database row representations from external API contracts.

### Key Types
- `Notification`: Represents the persistent user inbox record in PostgreSQL.
- `OutboxEvent`: Represents a staged outbox event with its lifecycle state (`PENDING`, `PROCESSING`, `PROCESSED`, `FAILED`), retry counter, claim token, and destination Redis channel.
- `RedisEnvelope`: The standardized JSON envelope broadcast over Redis Pub/Sub:
  - `event_id`: Domain event identity.
  - `notification_id`: Persistent inbox notification UUID (empty for ephemeral events).
  - `type`: Payload event type identifier.
  - `channel`: Logical destination.
  - `data`: Typed entity payload.
- `PaginationFilter`: Keyset pagination cursor parameters (`UserID`, `CursorTime`, `CursorID`, `Limit`, `TypeFilter`).

---

## 6. `publisher/` — Transactional Outbox Worker

### Purpose
An autonomous background worker that queries `notification_outbox` for `PENDING` records, publishes them to Redis Pub/Sub, and marks them `PROCESSED`.

### Problem It Solves
- **Distributed Two-Phase Commit**: Solves the problem of atomically writing to PostgreSQL and publishing to Redis without distributed 2PC transactions.
- **Redis Outage Resilience**: If Redis goes down, events safely accumulate in PostgreSQL as `PENDING`. When Redis recovers, the publisher drains the queue in strict chronological order with zero message loss.
- **Worker Crash Recovery**: If a worker node crashes mid-batch, its claimed rows are automatically recovered after a 60-second lease timeout.
- **Claim Token Ownership**: Protects against claim races when network latency exceeds the 60s lease timeout. Workers only update rows if their stamped `claim_token` still matches.

### Key Functions
- `Start(ctx)`: Initializes the polling loop and periodic stale claim recovery ticker.
- `ProcessBatch(ctx)`:
  1. Claims up to `BatchSize` rows using `SELECT ... FOR UPDATE SKIP LOCKED` and stamps a fresh `claim_token`.
  2. Iterates over rows and executes `publishWithRetry`.
  3. If publication fails after exponential backoff: records failure details via `IncrementOutboxRetry` (validating token), releases remaining batch items back to `PENDING` via `ReleaseOutboxClaims`, and halts the batch.
  4. On success: executes `MarkOutboxPublished` (validating token) to transition row to `PROCESSED`.
- `publishWithRetry(ctx, ev)`: Performs Redis `PUBLISH` with exponential backoff (100ms → 200ms → 400ms).

---

## 7. `repository/` — Data Access & PostgreSQL Persistence

### Purpose
Defines the storage abstraction (`repository.go`) and implements high-performance PostgreSQL persistence using `pgxpool` (`postgres/`).

### Problem It Solves
- **Mockability**: Allows unit testing of the service layer without spinning up PostgreSQL.
- **Transactional Atomicity**: Bundles inbox inserts, outbox records, and deduplication keys into a single database transaction.
- **Keyset Pagination Invariant**: Solves `OFFSET/LIMIT` page drift by ordering by `(created_at DESC, id DESC)`.

### Key Files & Functions

#### `postgres/notifications.go`
- `CreateWithDedupTx(ctx, notif, sourceEventID, outbox)`:
  1. Checks/inserts `sourceEventID` into `processed_events`. If duplicate, returns `ErrAlreadyProcessed`.
  2. Inserts persistent user notification into `notifications`.
  3. Inserts outbox event into `notification_outbox`.
- `CreateTradeSettledTx(ctx, buyerNotif, sellerNotif, tradeID, buyerOutbox, sellerOutbox)`:
  - Executes dual counterparty fan-out in a single atomic transaction: writes both buyer and seller inbox rows, both outbox records, and the deduplication record.
- `GetByUserID(ctx, filter)`: Keyset pagination query fetching `limit + 1` rows to detect whether additional pages exist without executing a separate `COUNT(*)` query.
- `GetNotificationByID(ctx, userID, notificationID)`: Retrieves a notification enforcing user ownership.
- `GetNotificationIDByEventID(ctx, eventID)`: Retrieves the original `notification_id` from `processed_events` for idempotency retries.

#### `postgres/outbox.go`
- `FetchPendingOutbox(ctx, limit)`:
  - Uses `SELECT ... FOR UPDATE SKIP LOCKED` inside a CTE to claim rows, sets `status = 'PROCESSING'`, `claimed_at = NOW()`, and stamps a unique `claim_token`.
- `MarkOutboxPublished(ctx, id, claimToken)`:
  - Updates row to `status = 'PROCESSED'`, verifying `WHERE id = $1 AND status = 'PROCESSING' AND claim_token = $2`. Returns `ErrOutboxClaimLost` if 0 rows were affected.
- `IncrementOutboxRetry(ctx, id, lastError, claimToken)`:
  - Bumps `retry_count` and records `last_error`, verifying `claim_token`.
- `ReleaseOutboxClaims(ctx, ids, claimToken)`:
  - Reverts in-flight claims back to `status = 'PENDING'` if batch processing encounters an error.
- `RecoverStaleOutboxClaims(ctx, timeout)`:
  - Resets abandoned `PROCESSING` records older than the lease threshold (60s) back to `PENDING` and clears `claim_token`.

---

## 8. `service/` — Core Business Logic

### Purpose
Contains the core business rules of the Notification domain, coordinating repository persistence, event transformations, input sanitization, and counterparty privacy enforcement.

### Problem It Solves
- **Counterparty Privacy Leakage**: In financial exchange trading, the Buyer must **never** see the Seller’s user ID, account details, or order ID, and vice-versa. `HandleTradeSettled` completely isolates and sanitizes buyer and seller views.
- **Idempotent Ingestion**: Handles duplicate Kafka events and duplicate gRPC calls cleanly without corrupting user state or duplicating notifications.
- **High-Frequency State Streams**: Distinguishes between persistent inbox alerts (trades, order cancellations) and ephemeral state snapshots (portfolio balances).

### Key Files & Functions

#### `service/events.go`
- `HandleTradeSettled(ctx, ev)`:
  - Validates trade event payload.
  - Constructs sanitized Buyer notification: `"Your BUY order of 0.05 BTC on BTC-USDT filled at 96500 USDT"`.
  - Constructs sanitized Seller notification: `"Your SELL order of 0.05 BTC on BTC-USDT filled at 96500 USDT"`.
  - Persists both atomically via `CreateTradeSettledTx`.
- `HandleOrderCancelled(ctx, ev)`:
  - Generates system alert informing user of order cancellation and reason.
  - Persists via `CreateWithDedupTx`.
- `HandlePortfolioUpdated(ctx, ev)`:
  - Ephemeral state stream. Writes directly to outbox for Redis streaming (`user:portfolio:{uid}`) without writing to `notifications` table or `processed_events`, avoiding database write amplification.

#### `service/inbox.go`
- `CreateNotification(ctx, input)`:
  - Direct gRPC entrypoint.
  - Validates `user_id`, `reference_id` (UUID), `reference_type` (`TRADE`, `ORDER`, `DEPOSIT`), and `type` (`INFO`, `TRADE_FILL`, `SYSTEM`, `ACCOUNT`).
  - Idempotency support: When `IdempotencyKey` is provided, checks `processed_events`. On duplicate retry, queries `GetNotificationIDByEventID` and returns the existing notification instead of creating a duplicate.
- `GetNotifications(ctx, filter)`: Validates pagination UUIDs and executes keyset inbox search.
- `MarkAsRead(ctx, userID, notifID)`: Enforces user ownership and toggles `is_read = true`.
- `MarkAllAsRead(ctx, userID)`: Bulk-updates all unread user notifications.
- `GetUnreadCount(ctx, userID)`: Returns unread badge count.

---

## End-to-End System Flows

### Flow 1: Kafka Domain Ingestion & Dual Fan-Out (TradeSettled)
```
Kafka Broker
   │
   ▼
kafka.Consumer (trades.settled.v1)
   │ (reads message, validates JSON schema)
   ▼
service.HandleTradeSettled()
   │ 1. Validate UUIDs
   │ 2. Build sanitized Buyer notification (BUY side only)
   │ 3. Build sanitized Seller notification (SELL side only)
   ▼
repository.CreateTradeSettledTx()
   │ BEGIN TX
   │   ├── INSERT INTO processed_events (event_id)
   │   ├── INSERT INTO notifications (buyer)
   │   ├── INSERT INTO notifications (seller)
   │   ├── INSERT INTO notification_outbox (channel: user:notifications:buyer)
   │   └── INSERT INTO notification_outbox (channel: user:notifications:seller)
   │ COMMIT TX
   ▼
kafka.Consumer.commitOffset() (strictly after DB commit)
```

---

### Flow 2: Transactional Outbox Publishing to Redis
```
publisher.Publisher (Loop: 500ms active / 2s idle)
   │
   ▼
repository.FetchPendingOutbox()
   │ SELECT ... FOR UPDATE SKIP LOCKED
   │ SET status = 'PROCESSING', claim_token = $token, claimed_at = NOW()
   ▼
For each OutboxEvent:
   │
   ├── publishWithRetry()
   │      └── Redis PUBLISH to target_channel (e.g. user:notifications:{uid})
   │
   ├── Success:
   │      └── repository.MarkOutboxPublished(id, claim_token)
   │             SET status = 'PROCESSED', claim_token = NULL
   │
   └── Failure (Redis down / timeout):
          ├── repository.IncrementOutboxRetry(id, error, claim_token)
          ├── repository.ReleaseOutboxClaims(remaining_ids, claim_token)
          │      SET status = 'PENDING', claim_token = NULL
          └── Halts current batch (waits for backoff / recovery)
```

---

### Flow 3: gRPC Direct Ingestion with Retry Idempotency
```
Wallet / Auth / Admin Service
   │
   ▼
gRPC handler.CreateNotification(CreateNotificationRequest)
   │ (extracts parameters, validates UUIDs & enums)
   ▼
service.CreateNotification()
   │
   ├── Case A: First attempt (IdempotencyKey = "uuid-123")
   │      ├── repo.CreateWithDedupTx() inserts into processed_events & notifications
   │      └── Returns new Notification
   │
   └── Case B: Network timeout, caller retries with same IdempotencyKey
          ├── repo.CreateWithDedupTx() returns ErrAlreadyProcessed
          ├── service detects IdempotencyKey was provided
          ├── service.lookupByIdempotencyKey() queries processed_events & notifications
          └── Returns original Notification (zero duplicates created)
```
