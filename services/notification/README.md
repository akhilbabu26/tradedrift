# Notification Service (`services/notification`) — Architectural & Engineering Guide

This document provides a comprehensive technical breakdown of the **TradeDrift Notification Service**. It details the purpose of every folder, the specific problems solved by each file and function, the engineering mechanisms employed, and all end-to-end system flows.

---

## Table of Contents

1. [Executive Summary & System Role](#1-executive-summary--system-role)
2. [Directory Tree & File Map](#2-directory-tree--file-map)
3. [Folder-by-Folder, File-by-File & Function-by-Function Breakdown](#3-folder-by-folder-file-by-file--function-by-function-breakdown)
   - [3.1 `cmd/` — Application Entrypoint & Composition Root](#31-cmd--application-entrypoint--composition-root)
   - [3.2 `internal/config/` — Environment Configuration & Validation](#32-internalconfig--environment-configuration--validation)
   - [3.3 `internal/handler/` — Transport Layer & gRPC Server](#33-internalhandler--transport-layer--grpc-server)
   - [3.4 `internal/kafka/` — Asynchronous Ingestion & DLQ Protection](#34-internalkafka--asynchronous-ingestion--dlq-protection)
   - [3.5 `internal/metrics/` — Prometheus Telemetry Registry](#35-internalmetrics--prometheus-telemetry-registry)
   - [3.6 `internal/model/` — Pure Domain Entities & Envelope Contracts](#36-internalmodel--pure-domain-entities--envelope-contracts)
   - [3.7 `internal/publisher/` — Transactional Outbox Background Worker](#37-internalpublisher--transactional-outbox-background-worker)
   - [3.8 `internal/repository/` — Data Access & PostgreSQL Engine](#38-internalrepository--data-access--postgresql-engine)
   - [3.9 `internal/service/` — Core Business Logic & Privacy Rules](#39-internalservice--core-business-logic--privacy-rules)
   - [3.10 `migration/` — Database Schema & Distributed Lease DDL](#310-migration--database-schema--distributed-lease-ddl)
4. [End-to-End System Flows](#4-end-to-end-system-flows)
   - [Flow 1: Bootstrap & Graceful Teardown Flow](#flow-1-bootstrap--graceful-teardown-flow)
   - [Flow 2: Trade Settled Flow (Counterparty Privacy Dual Fan-Out)](#flow-2-trade-settled-flow-counterparty-privacy-dual-fan-out)
   - [Flow 3: Order Cancelled Flow (Idempotent Inbox Persistence)](#flow-3-order-cancelled-flow-idempotent-inbox-persistence)
   - [Flow 4: High-Frequency Ephemeral Portfolio Streaming](#flow-4-high-frequency-ephemeral-portfolio-streaming)
   - [Flow 5: Kafka Poison Message DLQ Isolation Flow](#flow-5-kafka-poison-message-dlq-isolation-flow)
   - [Flow 6: Transactional Outbox Background Relay & Token Verification](#flow-6-transactional-outbox-background-relay--token-verification)
   - [Flow 7: Real-Time WebSocket Delivery Flow (Gateway Hub)](#flow-7-real-time-websocket-delivery-flow-gateway-hub)
   - [Flow 8: gRPC CreateNotification Idempotency Recovery Flow](#flow-8-grpc-createnotification-idempotency-recovery-flow)
   - [Flow 9: Keyset-Paginated Inbox Query Flow (LIMIT + 1 Slicing)](#flow-9-keyset-paginated-inbox-query-flow-limit--1-slicing)
   - [Flow 10: Anti-Tenant Enumeration MarkAsRead Flow](#flow-10-anti-tenant-enumeration-markasread-flow)
5. [Summary Matrix of System Invariants & Protections](#5-summary-matrix-of-system-invariants--protections)

---

## 1. Executive Summary & System Role

The **Notification Service** is the central notification and real-time event streaming hub for TradeDrift. It bridges asynchronous exchange events from Apache Kafka and synchronous internal alerts via gRPC into persistent user inboxes and low-latency Redis Pub/Sub channels for WebSocket streaming.

### Core Architectural Responsibilities
1. **Financial Privacy Enforcement**: Strips counterparty data on trade fills so buyers and sellers never see each other's identities or order IDs.
2. **Transactional Outbox Guarantee**: Commits inbox records and outbox events within the same atomic PostgreSQL transaction, guaranteeing at-least-once delivery to Redis Pub/Sub.
3. **Write Amplification Prevention**: Routes high-frequency ephemeral portfolio updates directly to the outbox without storing dead rows in the `notifications` table.
4. **Idempotency & Safe Retries**: Uses `processed_events` to deduplicate redelivered Kafka messages and recover original notifications on retried gRPC requests.
5. **Distributed Lease Safety**: Employs unique `claim_token` UUIDs to eliminate race conditions and double-publishes during outbox batch processing.

```
                  ┌────────────────────────┐       ┌────────────────────────┐
                  │      Trade Engine      │       │     Wallet Service     │
                  └───────────┬────────────┘       └───────────┬────────────┘
                              │ Kafka                          │ gRPC
                              │ ("trades.settled.v1", etc.)    │ (CreateNotification)
                              ▼                                ▼
                 ┌──────────────────────────────────────────────────┐
                 │          TradeDrift Notification Service         │
                 │                                                  │
                 │  ┌──────────────┐              ┌──────────────┐  │
                 │  │Kafka Consumer│              │ gRPC Server  │  │
                 │  └──────┬───────┘              └──────┬───────┘  │
                 │         │                             │          │
                 │         ▼                             ▼          │
                 │  ┌────────────────────────────────────────────┐  │
                 │  │             Domain Service Layer           │  │
                 │  │  - Privacy Isolation   - Idempotency Dedup │  │
                 │  │  - Enum Validation     - PII Suppression   │  │
                 │  └──────────────────────┬─────────────────────┘  │
                 │                         │                        │
                 │         ┌───────────────┴───────────────┐        │
                 │         ▼                               ▼        │
                 │  ┌──────────────┐              ┌──────────────┐  │
                 │  │PostgreSQL DB │              │Outbox Worker │  │
                 │  │  Repository  │              │  (Publisher) │  │
                 │  └──────────────┘              └──────┬───────┘  │
                 └───────────────────────────────────────┼──────────┘
                                                         │ Redis Pub/Sub
                                                         ▼
                                            ┌────────────────────────┐
                                            │      Gateway / WS      │
                                            └────────────┬───────────┘
                                                         │ WebSocket
                                                         ▼
                                            ┌────────────────────────┐
                                            │  Client Browser / App  │
                                            └────────────────────────┘
```

---

## 2. Directory Tree & File Map

```
services/notification/
├── cmd/
│   ├── README.md                 # Composition root & lifecycle documentation
│   └── server/
│       └── main.go               # 11-stage bootstrapper & 10-second graceful teardown
├── internal/
│   ├── README.md                 # Internal package architecture guide
│   ├── config/
│   │   ├── README.md             # Configuration parsing & validation guide
│   │   └── config.go             # Typed configuration struct & broker parsing
│   ├── handler/
│   │   ├── README.md             # gRPC transport adapter documentation
│   │   └── grpc.go               # Implementation of notificationv1.NotificationServiceServer
│   ├── kafka/
│   │   ├── README.md             # Consumer loop & DLQ isolation guide
│   │   ├── consumer.go           # Asynchronous ingestion, DLQ fallback, PII logging suppression
│   │   └── consumer_test.go      # Unit test suite for Kafka consumer
│   ├── metrics/
│   │   ├── README.md             # Telemetry dictionary & alerting PromQL queries
│   │   └── metrics.go            # Prometheus CounterVecs, Histograms, Gauges (promauto)
│   ├── model/
│   │   ├── README.md             # Pure Go domain models & envelope contract guide
│   │   └── model.go              # Notification, OutboxEvent, RedisEnvelope, closed enums
│   ├── publisher/
│   │   ├── README.md             # Outbox worker & lease management documentation
│   │   ├── publisher.go          # SELECT FOR UPDATE SKIP LOCKED, claim tokens, orphan sweeper
│   │   └── publisher_test.go     # Test suite for outbox publisher & concurrency guards
│   ├── repository/
│   │   ├── README.md             # Repository interface & transaction semantics guide
│   │   ├── repository.go         # Domain interfaces & sentinel errors (ErrOutboxClaimLost)
│   │   └── postgres/
│   │       ├── notifications.go  # Inbox queries, dual fan-out Tx, keyset pagination
│   │       ├── outbox.go         # Outbox batch claim, lease token update, retry backoff
│   │       ├── repository.go     # pgx connection pool wrapper & ping methods
│   │       └── repository_test.go# Database repository unit tests
│   └── service/
│       ├── README.md             # Core business logic & privacy isolation guide
│       ├── events.go             # Kafka handlers (TradeSettled, OrderCancelled, PortfolioUpdated)
│       ├── inbox.go              # Synchronous gRPC inbox methods & idempotency recovery
│       ├── service.go            # Service struct, sentinel errors, domain event validation
│       └── service_test.go       # Mock repository test suite for service layer
├── migration/
│   ├── README.md                 # Schema migration history & index justification
│   ├── 00001_create_notifications.sql                  # Base schema (notifications, outbox, dedup)
│   ├── 00002_outbox_pending_index.sql                  # Partial index on pending outbox events
│   └── 00003_add_notification_id_and_claim_token.sql   # Claim tokens, idempotency ID, check constraints
├── Dockerfile                    # Multi-stage production container build
├── ENTIRE_FLOW.md                # Exhaustive end-to-end system flows document
├── go.mod                        # Go module definition
├── go.sum                        # Cryptographic checksums
└── README.md                     # Master engineering guide
```

---

## 3. Folder-by-Folder, File-by-File & Function-by-Function Breakdown

---

### 3.1 `cmd/` — Application Entrypoint & Composition Root

#### Folder Purpose
The `cmd/` directory owns application lifecycle management, dependency assembly, connection pool initialization, background worker coordination, and graceful shutdown orchestration.

#### File: `cmd/server/main.go`
* **Purpose**: Top-level composition root executing the 11-stage boot sequence and 10-second graceful teardown.
* **What Problem It Solves**: Prevents boot-time race conditions, unmigrated database startup crashes, message loss during deployment restarts, and dangling outbox claims.
* **How It Solves It**:
  1. Validates configuration and DLQ topic definitions upfront.
  2. Runs Goose migrations synchronously before connecting the pgx connection pool.
  3. Registers background workers with a `sync.WaitGroup`.
  4. Intercepts `SIGINT`/`SIGTERM` to coordinate an orderly shutdown: stops incoming RPCs, stops consumer loops, flushes outbox workers, closes Redis, and drains database connections.

#### Functions in `cmd/server/main.go`:
1. **`main()`**:
   * **Signature**: `func main()`
   * **Purpose**: Composition root of the Notification Service.
   * **Execution Steps**:
     1. Calls `config.Load()` and panics if mandatory variables are missing.
     2. Initializes structured logger (`logger.New(cfg.LogLevel)`).
     3. Binds Prometheus telemetry collectors (`metrics.New()`).
     4. Executes Goose migrations synchronously via `platformpg.RunMigrations`.
     5. Connects PostgreSQL connection pool (`platformpg.NewPool`).
     6. Connects Redis client and validates connectivity via `redis.Ping()`.
     7. Creates Kafka reader consumer group and DLQ writer producer.
     8. Wires dependency graph: Repo $\rightarrow$ Service $\rightarrow$ Publisher $\rightarrow$ Handler $\rightarrow$ Consumer.
     9. Launches Outbox Publisher, Kafka Consumer, gRPC Server (`:50051`), and HTTP Server (`:8081`).
     10. Traps OS signals and executes 10-second graceful shutdown.
2. **`setupHealthRoutes(router, db, redisClient)`**:
   * **Signature**: `func setupHealthRoutes(...)`
   * **Purpose**: Binds `/healthz` (liveness), `/ready` (dependency check: DB + Redis ping), and `/metrics` (Prometheus scrape).

---

### 3.2 `internal/config/` — Environment Configuration & Validation

#### Folder Purpose
The `internal/config/` directory provides strongly-typed configuration structures, parsing logic, and fail-fast validation.

#### File: `internal/config/config.go`
* **Purpose**: Defines the `Config` struct, parses environment variables, and validates mandatory parameters.
* **What Problem It Solves**: Prevents runtime panics and connection failures caused by missing environment variables, whitespace in broker lists, or invalid port strings.
* **How It Solves It**: Checks mandatory environment variables (`NOTIFICATION_POSTGRES_DSN`, `REDIS_ADDR`, `KAFKA_BROKERS`) and sanitizes broker lists.

#### Functions in `internal/config/config.go`:
1. **`Load()`**:
   * **Signature**: `func Load() (Config, error)`
   * **Purpose**: Reads environment variables, validates mandatory parameters, and injects production defaults.
2. **`parseBrokers(raw string)`**:
   * **Signature**: `func parseBrokers(raw string) []string`
   * **Purpose**: Splits comma-delimited broker strings, stripping surrounding whitespace and omitting empty elements.
3. **`getEnvOrDefault(key, fallback string)`**:
   * **Signature**: `func getEnvOrDefault(key, fallback string) string`
   * **Purpose**: Safely reads an environment variable with a fallback default.

---

### 3.3 `internal/handler/` — Transport Layer & gRPC Server

#### Folder Purpose
The `internal/handler/` directory implements the gRPC transport boundary defined in `notification.proto`. It handles request validation, error mapping, and response serialization.

#### File: `internal/handler/grpc.go`
* **Purpose**: Implements `notificationv1.NotificationServiceServer`.
* **What Problem It Solves**:
  - Prevents invalid UUIDs and out-of-range limits from reaching the database.
  - Prevents tenant ID enumeration by masking missing notifications as generic `NotFound`.
  - Implements `LIMIT + 1` sentinel row slicing for keyset cursor pagination.
* **How It Solves It**: Validates UUID formats on all requests, checks user ownership, and slices the extra row to calculate `has_more` without running slow `COUNT(*)` queries.

#### Functions in `internal/handler/grpc.go`:
1. **`NewGRPCHandler(svc *service.Service, log *zap.Logger)`**:
   * **Signature**: `func NewGRPCHandler(...) *GRPCHandler`
   * **Purpose**: Constructor injecting domain service and logger.
2. **`CreateNotification(ctx context.Context, req *notificationv1.CreateNotificationRequest)`**:
   * **Signature**: `func (h *GRPCHandler) CreateNotification(...) (*notificationv1.CreateNotificationResponse, error)`
   * **Purpose**: gRPC entrypoint for internal microservices creating alerts with idempotency support.
3. **`GetNotifications(ctx context.Context, req *notificationv1.GetNotificationsRequest)`**:
   * **Signature**: `func (h *GRPCHandler) GetNotifications(...) (*notificationv1.GetNotificationsResponse, error)`
   * **Purpose**: Retrieves paginated user inbox notifications using `(created_at, id)` composite cursor pairs.
4. **`MarkAsRead(ctx context.Context, req *notificationv1.MarkAsReadRequest)`**:
   * **Signature**: `func (h *GRPCHandler) MarkAsRead(...) (*notificationv1.MarkAsReadResponse, error)`
   * **Purpose**: Marks a single notification as read, enforcing strict user ownership.
5. **`MarkAllAsRead(ctx context.Context, req *notificationv1.MarkAllAsReadRequest)`**:
   * **Signature**: `func (h *GRPCHandler) MarkAllAsRead(...) (*notificationv1.MarkAllAsReadResponse, error)`
   * **Purpose**: Bulk-marks all unread alerts as read for a given user.
6. **`GetUnreadCount(ctx context.Context, req *notificationv1.GetUnreadCountRequest)`**:
   * **Signature**: `func (h *GRPCHandler) GetUnreadCount(...) (*notificationv1.GetUnreadCountResponse, error)`
   * **Purpose**: Returns the count of unread notifications for badge rendering.
7. **`toProtoNotification(n *model.Notification)`**:
   * **Signature**: `func toProtoNotification(...) *notificationv1.Notification`
   * **Purpose**: Converts internal domain `Notification` struct into the Protobuf message format.

---

### 3.4 `internal/kafka/` — Asynchronous Ingestion & DLQ Protection

#### Folder Purpose
The `internal/kafka/` directory manages asynchronous message ingestion from Kafka topics (`trades.settled.v1`, `orders.cancelled.v1`, `portfolios.updated.v1`).

#### File: `internal/kafka/consumer.go`
* **Purpose**: Consumer group event loop, poison pill DLQ isolation, inner retries, and offset management.
* **What Problem It Solves**:
  - Prevents poison pills (malformed JSON, broken schemas) from permanently stalling partition consumption.
  - Prevents PII/financial data leakage in error logs.
  - Prevents data loss by never committing an offset if a DLQ publish fails.
* **How It Solves It**:
  - Catches unmarshal/validation errors, attaches diagnostic headers (`x-error-reason`, `x-failed-at`), and publishes to the DLQ.
  - Suppresses message payloads in logs, logging only metadata (topic, partition, offset).
  - Commits offsets strictly *after* successful database transactions or DLQ offloads.

#### Functions in `internal/kafka/consumer.go`:
1. **`NewConsumer(...)`**:
   * **Signature**: `func NewConsumer(...) *Consumer`
   * **Purpose**: Instantiates Kafka reader and DLQ producer.
2. **`Start(ctx context.Context)`**:
   * **Signature**: `func (c *Consumer) Start(ctx context.Context)`
   * **Purpose**: Sequential consumption loop reading messages and routing by topic.
3. **`processMessage(ctx context.Context, msg kafkago.Message)`**:
   * **Signature**: `func (c *Consumer) processMessage(...) error`
   * **Purpose**: Deserializes JSON, validates event payload, and invokes domain service handlers.
4. **`sendToDLQ(ctx context.Context, msg kafkago.Message, reason string)`**:
   * **Signature**: `func (c *Consumer) sendToDLQ(...) error`
   * **Purpose**: Routes corrupted messages to the Dead-Letter Queue preserving raw bytes and context headers.
5. **`Close()`**:
   * **Signature**: `func (c *Consumer) Close() error`
   * **Purpose**: Closes the Kafka consumer reader and DLQ writer cleanly.

---

### 3.5 `internal/metrics/` — Prometheus Telemetry Registry

#### Folder Purpose
The `internal/metrics/` directory defines and registers Prometheus telemetry instruments using `promauto`.

#### File: `internal/metrics/metrics.go`
* **Purpose**: Central metric registry tracking Kafka ingestion, outbox relayer latency, database performance, and gRPC status codes.
* **What Problem It Solves**: Eliminates operational blindness and prevents memory leaks from high-cardinality label values.
* **Key Telemetry Instruments**:
  - `EventsConsumedTotal`: Counter tracking events processed by topic and status (`success`, `duplicate`, `dlq`).
  - `DLQMessagesTotal`: Counter tracking poison messages routed to the DLQ.
  - `OutboxPublishedTotal`: Counter tracking outbox events published to Redis.
  - `OutboxPublishDuration`: Histogram measuring outbox processing latency.
  - `DBDurationSeconds`: Histogram measuring PostgreSQL query latency.
  - `GRPCRequestsTotal` & `GRPCDurationSeconds`: RPC call counts and duration histograms.

---

### 3.6 `internal/model/` — Pure Domain Entities & Envelope Contracts

#### Folder Purpose
The `internal/model/` directory defines pure Go domain models, database entity mappings, and Redis Pub/Sub envelope contracts with zero external dependencies.

#### File: `internal/model/model.go`
* **Purpose**: Contains domain representations for notifications, outbox events, deduplication records, and pagination filters.
* **What Problem It Solves**: Prevents circular dependencies and establishes an unambiguous serialization contract for WebSocket clients.

#### Key Types & Constants:
1. **`Notification`**: Primary inbox entity (`ID`, `UserID`, `Type`, `Title`, `Message`, `ReferenceID`, `ReferenceType`, `ReadAt`, `CreatedAt`).
2. **`OutboxEvent`**: Transactional outbox row (`ID`, `TargetChannel`, `Payload`, `Status`, `RetryCount`, `ClaimToken`, `LeaseTimeout`).
3. **`RedisEnvelope`**: Serialization wrapper delivered to Redis Pub/Sub:
   ```go
   type RedisEnvelope struct {
       EventID        string `json:"event_id"`
       NotificationID string `json:"notification_id,omitempty"`
       Data           any    `json:"data"`
   }
   ```
4. **Closed Enums**:
   - Types: `INFO`, `TRADE_FILL`, `SYSTEM`, `ACCOUNT`.
   - Reference Types: `TRADE`, `ORDER`, `DEPOSIT`.
   - Outbox Statuses: `PENDING`, `PROCESSING`, `PROCESSED`, `FAILED`.

---

### 3.7 `internal/publisher/` — Transactional Outbox Background Worker

#### Folder Purpose
The `internal/publisher/` directory implements the background worker that reliably streams staged outbox events from PostgreSQL to Redis Pub/Sub.

#### File: `internal/publisher/publisher.go`
* **Purpose**: Polling loop, distributed lease claiming, Redis publishing, concurrency verification, and orphan recovery.
* **What Problem It Solves**:
  - Prevents race conditions and duplicate publishing when multiple service pods run concurrently.
  - Prevents abandoned events from stalling when a pod crashes mid-batch.
  - Implements exponential backoff retry on transient Redis outages.
* **How It Solves It**:
  - Fetches events using `SELECT ... FOR UPDATE SKIP LOCKED`, stamping a 60-second lease and unique `claim_token` UUID.
  - Updates status to `PROCESSED` only if the database row still matches the stamped `claim_token`.
  - Runs a 15-second orphan recovery ticker resetting expired leases (`lease_timeout < NOW()`) back to `PENDING`.

#### Functions in `internal/publisher/publisher.go`:
1. **`New(repo, redisClient, cfg, log)`**:
   * **Signature**: `func New(...) *Publisher`
   * **Purpose**: Constructor injecting repository, Redis client, and configuration.
2. **`Start(ctx context.Context)`**:
   * **Signature**: `func (p *Publisher) Start(ctx context.Context)`
   * **Purpose**: Runs background polling loop (100ms publish ticker, 15s sweeper ticker).
3. **`publishBatch(ctx context.Context)`**:
   * **Signature**: `func (p *Publisher) publishBatch(ctx context.Context)`
   * **Purpose**: Claims up to 50 pending events and iterates through publishing each one.
4. **`publishOne(ctx context.Context, ev model.OutboxEvent)`**:
   * **Signature**: `func (p *Publisher) publishOne(...) error`
   * **Purpose**: Executes `redisClient.Publish`, calls `MarkOutboxPublished(id, claim_token)`, and handles retry backoff.
5. **`recoverOrphans(ctx context.Context)`**:
   * **Signature**: `func (p *Publisher) recoverOrphans(ctx context.Context)`
   * **Purpose**: Calls `repo.ReleaseOutboxClaims(ctx, 60*time.Second)` to reset expired leases.
6. **`Stop()`**:
   * **Signature**: `func (p *Publisher) Stop()`
   * **Purpose**: Releases active claims and terminates background tickers cleanly.

---

### 3.8 `internal/repository/` — Data Access & PostgreSQL Engine

#### Folder Purpose
The `internal/repository/` directory provides data access abstractions and implements PostgreSQL operations using `jackc/pgx/v5`.

#### File: `internal/repository/repository.go`
* **Purpose**: Declares the `Repository` interface and sentinel errors (`ErrNotFound`, `ErrAlreadyProcessed`, `ErrOutboxClaimLost`).

#### File: `internal/repository/postgres/notifications.go`
* **Purpose**: Manages user notifications, deduplication tracking, and atomic multi-row transactions.
* **What Problem It Solves**: Guarantees database atomicity between inbox inserts, deduplication entries, and outbox staging.
* **Key Functions**:
  1. `CreateWithDedupTx`: Inserts into `processed_events`, `notifications`, and `notification_outbox` in a single transaction. Returns `ErrAlreadyProcessed` on duplicate.
  2. `CreateTradeSettledTx`: Executes dual fan-out transaction for buyer and seller simultaneously.
  3. `GetByUserID`: Keyset-paginated query using `(created_at, id) < ($2, $3) ORDER BY created_at DESC, id DESC LIMIT $limit + 1`.
  4. `MarkAsRead`, `MarkAllAsRead`, `GetUnreadCount`: User-isolated notification mutations.
  5. `GetNotificationIDByEventID` & `GetNotificationByID`: Idempotency recovery helpers.

#### File: `internal/repository/postgres/outbox.go`
* **Purpose**: Outbox table lifecycle operations.
* **What Problem It Solves**: Enforces distributed locking and lease validation at the database layer.
* **Key Functions**:
  1. `StageOutboxEvent`: Inserts raw outbox record (used for ephemeral portfolio updates).
  2. `FetchPendingOutbox`: `SELECT ... FOR UPDATE SKIP LOCKED`, stamping `claim_token = UUID()` and `lease_timeout = NOW() + INTERVAL '60s'`.
  3. `MarkOutboxPublished`: Marks event `PROCESSED` where `id = $1 AND claim_token = $2`. Returns `ErrOutboxClaimLost` if rows affected is 0.
  4. `IncrementOutboxRetry`: Schedules backoff retry where `id = $1 AND claim_token = $2`. Marks `FAILED` if retry limit reached.
  5. `ReleaseOutboxClaims`: Resets stalled events with expired leases back to `PENDING`.

---

### 3.9 `internal/service/` — Core Business Logic & Privacy Rules

#### Folder Purpose
The `internal/service/` directory encapsulates pure business rules, privacy isolation, input validation, and gRPC retry idempotency recovery.

#### File: `internal/service/service.go`
* **Purpose**: Defines `Service` struct, sentinel domain errors, and shared UUID validator `validateUUID`.

#### File: `internal/service/events.go`
* **Purpose**: Domain event handlers for Kafka ingestion.
* **Key Functions**:
  1. **`HandleTradeSettled`**: Splits trade execution into two isolated notifications. Buyer sees only BUY parameters; seller sees only SELL parameters. Counterparty identities and order IDs are strictly suppressed.
  2. **`HandleOrderCancelled`**: Generates `SYSTEM` alert with cancellation reason and persists via `CreateWithDedupTx`.
  3. **`HandlePortfolioUpdated`**: Routes ephemeral balance update directly to `StageOutboxEvent`, bypassing `notifications` to eliminate database write amplification.

#### File: `internal/service/inbox.go`
* **Purpose**: Synchronous gRPC inbox operations and idempotency recovery.
* **Key Functions**:
  1. **`CreateNotification`**: Enforces closed-set enum validation. If `CreateWithDedupTx` returns `ErrAlreadyProcessed` and an `IdempotencyKey` is provided, calls `lookupByIdempotencyKey` to recover and return the original notification.
  2. **`lookupByIdempotencyKey`**: Queries `GetNotificationIDByEventID` followed by `GetNotificationByID` to retrieve the original alert.
  3. **`GetNotifications`**, **`MarkAsRead`**, **`MarkAllAsRead`**, **`GetUnreadCount`**: Coordinates with repository enforcing UUID validation.

---

### 3.10 `migration/` — Database Schema & Distributed Lease DDL

#### Folder Purpose
The `migration/` directory contains versioned SQL migrations executed via Goose.

#### Migration Files & Design Rationale:
1. **`00001_create_notifications.sql`**:
   - Creates `notifications` table (`id`, `user_id`, `type`, `title`, `message`, `reference_id`, `reference_type`, `read_at`, `created_at`).
   - Creates `notification_outbox` table (`id`, `target_channel`, `payload`, `status`, `retry_count`, `created_at`, `processed_at`).
   - Creates `processed_events` table (`event_id`, `user_id`, `processed_at`) with composite primary key `(event_id, user_id)`.
   - Creates compound index `idx_notifications_user_created` on `(user_id, created_at DESC, id DESC)` for keyset pagination.
2. **`00002_outbox_pending_index.sql`**:
   - Creates partial index `idx_outbox_pending` on `notification_outbox (created_at ASC) WHERE status = 'PENDING'` for fast outbox polling.
3. **`00003_add_notification_id_and_claim_token.sql`**:
   - Adds `claim_token UUID` and `lease_timeout TIMESTAMPTZ` to `notification_outbox` for safe distributed leasing.
   - Adds `notification_id UUID` to `processed_events` for gRPC idempotency recovery.
   - Adds closed-enum `CHECK` constraints (`chk_outbox_status`, `chk_notification_type`, `chk_notification_reference_type`).

---

## 4. End-to-End System Flows

---

### Flow 1: Bootstrap & Graceful Teardown Flow

```
[OS Boot / Container Start]
      │
      ▼
1. Load Environment Configuration (config.Load)
      │  ├─ Validates NOTIFICATION_POSTGRES_DSN, REDIS_ADDR, KAFKA_BROKERS
      │  └─ Rejects missing/malformed settings immediately (Fail-Fast)
      ▼
2. Initialize Prometheus Telemetry (metrics.New)
      ▼
3. Connect PostgreSQL Pool & Execute Goose Migrations
      │  ├─ Runs migrations synchronously (00001, 00002, 00003)
      │  └─ Establishes pgx connection pool
      ▼
4. Connect Redis Client (go-redis)
      │  └─ Verifies connection via redisClient.Ping()
      ▼
5. Initialize Kafka Consumer Group & DLQ Producer
      ▼
6. Dependency Injection Assembly
      │  ├─ Repo:      tradepg.New(dbPool)
      │  ├─ Service:   tradesvc.New(repo, logger)
      │  ├─ Publisher: publisher.New(repo, redisClient, cfg, logger)
      │  ├─ Handler:   tradehandler.NewGRPCHandler(svc, logger)
      │  └─ Consumer:  tradekafka.NewConsumer(svc, reader, dlqWriter, logger)
      ▼
7. Launch Background Goroutines
      │  ├─ go publisher.Start(ctx)          (Outbox Relayer & Sweeper)
      │  ├─ go consumer.Start(ctx)            (Kafka Consumer Event Loop)
      │  ├─ go startGRPCServer(:50051)        (gRPC API)
      │  └─ go startHTTPServer(:8081)         (Metrics & Probes)
      ▼
[SERVICE OPERATIONAL - SERVING TRAFFIC]
      │
[OS Signal: SIGINT / SIGTERM Intercepted]
      │
      ▼
8. Graceful Shutdown Phase (10-Second Deadline)
      │  ├─ 1. HTTP Server Shutdown (Stop healthz/ready traffic)
      │  ├─ 2. gRPC Server GracefulStop() (Finish in-flight RPCs)
      │  ├─ 3. Stop Kafka Consumer (Cancel context, finish batch, close reader)
      │  ├─ 4. Stop Outbox Publisher (Release active claims, close worker)
      │  ├─ 5. Close Kafka DLQ Producer
      │  ├─ 6. Close Redis Client
      │  └─ 7. Close PostgreSQL Connection Pool
      ▼
[Process Exits Cleanly with Code 0]
```

---

### Flow 2: Trade Settled Flow (Counterparty Privacy Dual Fan-Out)

```
                         Kafka Topic: "trades.settled.v1"
                                       │
                                       ▼
                             kafka.Consumer.Start()
                                       │
                                       ▼
                         service.HandleTradeSettled()
                                       │
             ┌─────────────────────────┴─────────────────────────┐
             ▼                                                   ▼
1. Buyer Notification Record                        2. Seller Notification Record
   - UserID:    BuyerUserID                            - UserID:    SellerUserID
   - Type:      TRADE_FILL                             - Type:      TRADE_FILL
   - Reference: TradeID (TRADE)                        - Reference: TradeID (TRADE)
   - Title:     "Trade Executed"                       - Title:     "Trade Executed"
   - Message:   "Your BUY order of 1 BTC               - Message:   "Your SELL order of 1 BTC
                 filled at 95,000 USDT"                             filled at 95,000 USDT"
   *(Seller ID & Sell Order ID OMITTED)*              *(Buyer ID & Buy Order ID OMITTED)*
             │                                                   │
             ▼                                                   ▼
   Outbox TargetChannel:                               Outbox TargetChannel:
   "user:notifications:<BuyerUserID>"                  "user:notifications:<SellerUserID>"
             │                                                   │
             └─────────────────────────┬─────────────────────────┘
                                       │
                                       ▼
                  repo.CreateTradeSettledTx(ctx, params)
                                       │
                 ┌─────────────────────┴─────────────────────┐
                 │         BEGIN PostgreSQL TRANSACTION      │
                 │                                           │
                 │ 1. INSERT INTO processed_events           │
                 │    (event_id, user_id, notification_id)   │
                 │    ON CONFLICT DO NOTHING                 │
                 │    --> If rows = 0: ROLLBACK & return     │
                 │        ErrAlreadyProcessed                │
                 │                                           │
                 │ 2. INSERT INTO notifications (Buyer)      │
                 │ 3. INSERT INTO notifications (Seller)     │
                 │ 4. INSERT INTO notification_outbox (Buyer)│
                 │ 5. INSERT INTO notification_outbox(Seller)│
                 │                                           │
                 │                  COMMIT                   │
                 └─────────────────────┬─────────────────────┘
                                       │
                     ┌─────────────────┴─────────────────┐
                     │                                   │
              [Transaction Success]             [ErrAlreadyProcessed]
                     │                                   │
                     ▼                                   ▼
          Commit Kafka Offset                Log Debug "already processed"
                     │                                   │
                     └─────────────────┬─────────────────┘
                                       ▼
                            Process Next Message
```

---

### Flow 3: Order Cancelled Flow (Idempotent Inbox Persistence)

```
Kafka: "orders.cancelled.v1"
          │
          ▼
service.HandleOrderCancelled()
          │
          ├─ Validates event_id, order_id, user_id are valid UUIDs
          │
          ├─ Constructs Notification:
          │    UserID:    ev.UserID
          │    Type:      "SYSTEM"
          │    Title:     "Order Cancelled"
          │    Message:   "Your order on BTC-USDT was cancelled: <reason>"
          │    RefType:   "ORDER"
          │    RefID:     ev.OrderID
          │
          ▼
repo.CreateWithDedupTx(ctx, notif, outbox, ev.EventID)
          │
          ├─ BEGIN TX
          ├─ 1. INSERT INTO processed_events (event_id, user_id, notification_id)
          │     If conflict -> ROLLBACK, return ErrAlreadyProcessed
          ├─ 2. INSERT INTO notifications (...)
          ├─ 3. INSERT INTO notification_outbox (TargetChannel: "user:notifications:<user_id>", ...)
          ├─ COMMIT TX
          │
          ▼
Commit Kafka Offset strictly after successful DB transaction
```

---

### Flow 4: High-Frequency Ephemeral Portfolio Streaming

```
Kafka: "portfolios.updated.v1"
          │
          ▼
service.HandlePortfolioUpdated()
          │
          ├─ Validates event_id, user_id
          │
          ├─ Constructs Redis Envelope:
          │    EventID:        ev.EventID
          │    NotificationID: "" (EMPTY: Indicates ephemeral state, not inbox row)
          │    Data:           PortfolioUpdatedPayload
          │
          ├─ Constructs OutboxEvent:
          │    TargetChannel: "user:portfolio:<user_id>"
          │    Payload:       JSON(RedisEnvelope)
          │    Status:        "PENDING"
          │
          ▼
repo.StageOutboxEvent(ctx, outboxEvent)
          │
          ├─ INSERT INTO notification_outbox (...)
          │  *(BYPASSES "notifications" table: Zero dead inbox rows)*
          │  *(BYPASSES "processed_events" table: High-throughput state snapshot)*
          │
          ▼
Commit Kafka Offset
```

---

### Flow 5: Kafka Poison Message DLQ Isolation Flow

```
Kafka Message Received
          │
          ▼
json.Unmarshal() OR event.Validate() Fails
          │
          ├── 1. Log Error (Sanitizes PII, logs message offset & partition)
          │
          ├── 2. Build DLQ Payload:
          │      Headers:
          │        x-original-topic:     "trades.settled.v1"
          │        x-original-partition: "2"
          │        x-original-offset:    "104928"
          │        x-error-reason:       "invalid UUID format in buyer_user_id"
          │        x-failed-at:          "2026-09-09T14:20:00Z"
          │      Payload: <raw unparsed bytes>
          │
          ├── 3. Produce to Kafka DLQ Topic ("notifications.dlq")
          │
          ├── 4. Increment Prometheus metric:
          │      notification_dlq_messages_total{topic="trades.settled.v1", reason="validation_failed"}
          │
          ▼
Commit Kafka Offset of Poison Message (Allows partition to resume processing)
```

---

### Flow 6: Transactional Outbox Background Relay & Token Verification

```
                       Outbox Worker Loop (Ticker: 100ms)
                                       │
                                       ▼
                       repo.FetchPendingOutbox(ctx, batchSize=50)
                                       │
                 ┌─────────────────────┴─────────────────────┐
                 │       BEGIN PostgreSQL TRANSACTION        │
                 │                                           │
                 │ SELECT id, target_channel, payload        │
                 │ FROM notification_outbox                  │
                 │ WHERE status = 'PENDING'                  │
                 │    OR (status = 'PROCESSING' AND          │
                 │        lease_timeout < NOW())             │
                 │ ORDER BY created_at ASC                   │
                 │ LIMIT 50                                  │
                 │ FOR UPDATE SKIP LOCKED;                   │
                 │                                           │
                 │ Generate ClaimToken = UUID()              │
                 │                                           │
                 │ UPDATE notification_outbox                │
                 │ SET status = 'PROCESSING',                │
                 │     claim_token = $claimToken,            │
                 │     lease_timeout = NOW() + INTERVAL '60s'│
                 │ WHERE id IN (selected_ids);               │
                 │                                           │
                 │ COMMIT                                    │
                 └─────────────────────┬─────────────────────┘
                                       │
                                       ▼
                      For each claimed OutboxEvent:
                                       │
                                       ▼
                   redisClient.Publish(ctx, target_channel, payload)
                                       │
                 ┌─────────────────────┴─────────────────────┐
                 │                                           │
          [Publish Succeeded]                         [Publish Failed]
                 │                                           │
                 ▼                                           ▼
   repo.MarkOutboxPublished(id, token)        repo.IncrementOutboxRetry(id, token, err)
                 │                                           │
                 ├─ UPDATE notification_outbox               ├─ retry_count++
                 │  SET status = 'PROCESSED',                ├─ If retry_count >= 5:
                 │      processed_at = NOW()                 │     status = 'FAILED'
                 │  WHERE id = $id                           │  Else:
                 │    AND claim_token = $token               │     status = 'PENDING',
                 │                                           │     next_retry_at = NOW() + backoff
                 ▼                                           ▼
      RowsAffected == 1?                          RowsAffected == 1?
         ├─ YES: Success                             ├─ YES: Retry Scheduled
         └─ NO:  ErrOutboxClaimLost                  └─ NO:  ErrOutboxClaimLost
                 (Lease expired and stolen;                  (Logged as Warn)
                  logged at Warn)
```

---

### Flow 7: Real-Time WebSocket Delivery Flow (Gateway Hub)

```
Outbox Worker
      │
      ▼
Redis Pub/Sub Channel: "user:notifications:<user_id>"
Payload:
{
  "event_id": "018f-event-uuid",
  "notification_id": "018f-notif-uuid",
  "data": {
    "id": "018f-notif-uuid",
    "user_id": "018f-user-uuid",
    "type": "TRADE_FILL",
    "title": "Trade Executed",
    "message": "Your BUY order of 0.5 BTC on BTC-USDT filled at 96,000 USDT",
    "created_at": "2026-09-09T14:15:00Z"
  }
}
      │
      ▼
Gateway Microservice (Redis Subscriber)
      │
      ├─ Dispatches message to user's active WebSocket connection in Hub
      │
      ▼
User Client (Web Browser / Mobile App)
      │
      ├─ 1. Plays sound / Displays Toast alert
      ├─ 2. Increments unread counter badge (+1)
      └─ 3. Prepends new notification item to in-memory notification list
```

---

### Flow 8: gRPC CreateNotification Idempotency Recovery Flow

```
Caller (e.g. Wallet Service)
      │
      ▼
gRPC Call: CreateNotificationRequest
{
  "user_id": "018f-user-uuid",
  "type": "ACCOUNT",
  "title": "Deposit Confirmed",
  "message": "Your deposit of 5,000 USDT has credited",
  "idempotency_key": "018f-wallet-tx-999"
}
      │
      ▼
handler.CreateNotification()
      │
      ├─ Validates user_id, type enum, title, message, idempotency_key (UUID)
      ▼
service.CreateNotification()
      │
      ▼
repo.CreateWithDedupTx(ctx, notif, outbox, idempotency_key)
      │
      ├─ INSERT INTO processed_events (event_id, user_id, notification_id)
      │  VALUES ('018f-wallet-tx-999', '018f-user-uuid', '018f-notif-uuid')
      │
      ├── Case A: First Attempt (Insert Successful)
      │     ├─ INSERT INTO notifications ...
      │     ├─ INSERT INTO notification_outbox ...
      │     ├─ COMMIT TX
      │     └─ Return new Notification to caller (gRPC OK)
      │
      └── Case B: Network Retry / Duplicate Attempt
            │
            ├─ INSERT INTO processed_events conflicts on (event_id, user_id)
            ├─ ROLLBACK TX
            ├─ Repository returns repository.ErrAlreadyProcessed
            │
            ▼
      service.lookupByIdempotencyKey("018f-user-uuid", "018f-wallet-tx-999")
            │
            ├─ 1. repo.GetNotificationIDByEventID("018f-wallet-tx-999")
            │     └─ Returns original NotificationID: "018f-notif-uuid"
            │
            ├─ 2. repo.GetNotificationByID("018f-user-uuid", "018f-notif-uuid")
            │     └─ Fetches original notification row
            │
            ▼
      Return Original Notification to Caller (Zero duplicates created, gRPC OK)
```

---

### Flow 9: Keyset-Paginated Inbox Query Flow (LIMIT + 1 Slicing)

```
Client Request: GetNotifications(user_id="U1", limit=20, cursor="cursor_token")
      │
      ▼
handler.GetNotifications()
      │
      ├─ Decodes cursor_token -> (cursorCreatedAt, cursorID)
      ├─ Clamps limit (default 20, max 100)
      │
      ▼
repo.GetByUserID(ctx, filter)
      │
      ▼
SQL Query:
SELECT id, user_id, type, title, message, reference_id, reference_type, read_at, created_at
FROM notifications
WHERE user_id = $1
  AND (created_at, id) < ($cursorCreatedAt, $cursorID)
ORDER BY created_at DESC, id DESC
LIMIT 21;  -- (Fetch Limit + 1)
      │
      ▼
Slicing & Response Construction:
      ├─ If rows returned == 21:
      │    - Slices items = rows[0:20]
      │    - HasMore = true
      │    - NextCursor = Encode(rows[19].CreatedAt, rows[19].ID)
      │
      └─ If rows returned <= 20:
           - Items = rows[0:len]
           - HasMore = false
           - NextCursor = ""
      │
      ▼
Return gRPC GetNotificationsResponse
```

---

### Flow 10: Anti-Tenant Enumeration MarkAsRead Flow

```
Client Request: MarkAsRead(user_id="U1", notification_id="N1")
      │
      ▼
handler.MarkAsRead()
      │
      ├─ Validates both U1 and N1 are valid UUIDs
      │
      ▼
repo.MarkAsRead(ctx, userID="U1", notificationID="N1")
      │
      ▼
UPDATE notifications
SET read_at = NOW()
WHERE id = 'N1'
  AND user_id = 'U1'
  AND read_at IS NULL;
      │
      ▼
RowsAffected Check:
      ├─ If RowsAffected == 1:
      │    - Mark successful, return empty OK response
      │
      └─ If RowsAffected == 0:
           - Checks if notification exists for user.
           - If not found or belongs to another user: Returns codes.NotFound ("notification not found")
           - (Attacker probing arbitrary UUIDs receives generic NotFound, preventing ID enumeration)
```

---

## 5. Summary Matrix of System Invariants & Protections

| Invariant / Rule | Enforced Where | Mechanism | Failure Mode Prevented |
|---|---|---|---|
| **Counterparty Privacy** | `service.HandleTradeSettled` | Segregated Buyer/Seller notification builders | De-anonymization of CLOB market participants |
| **Kafka Ingestion Idempotency** | PostgreSQL `processed_events` | `(event_id, user_id)` Unique Constraint + DB Tx | Duplicate notifications on Kafka consumer redelivery |
| **gRPC Call Idempotency** | `service.CreateNotification` + `processed_events` | `lookupByIdempotencyKey` on `ErrAlreadyProcessed` | Duplicate notifications on caller network retries |
| **Write Amplification Prevention** | `service.HandlePortfolioUpdated` | Direct outbox staging, bypassing `notifications` | Database disk exhaustion from high-frequency price ticks |
| **Outbox Lease Stalling Protection** | `publisher.OutboxWorker` | `claim_token` validation on update + 60s lease sweeper | Race conditions and double publishes on lease expiration |
| **Poison Message Deadlock Prevention** | `kafka.Consumer` | Kafka DLQ topic routing + post-DLQ offset commit | Head-of-line blocking on corrupted Kafka payloads |
| **Multi-Tenant Data Isolation** | `repository.Postgres` | Mandatory `WHERE user_id = $1` on all mutations | Cross-account notification snooping or tampering |
| **Offset Paging Degradation** | `repository.GetByUserID` | Keyset `(created_at, id) < ($1, $2)` + `LIMIT + 1` | `O(N)` database table scans on deep page scrolling |
| **Closed Enum Data Integrity** | Database DDL + `service.Service` | PostgreSQL `CHECK` constraints + domain enum validator | Database corruption from invalid strings or typos |
