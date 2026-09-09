# TradeDrift Notification Service — Complete End-to-End System Flows

This document provides an exhaustive, end-to-end architectural and operational mapping of **every flow** executed by the **Notification Service** (`services/notification`).

It serves as the definitive reference for understanding how events enter the system, how data is persisted and protected, how outbox events are published, and how user inbox operations interact with other microservices.

---

## Table of Contents

1. [Global System Architecture & Topology](#1-global-system-architecture--topology)
2. [Flow 1: Service Lifecycle, Bootstrap & Graceful Teardown](#flow-1-service-lifecycle-bootstrap--graceful-teardown)
3. [Flow 2: Kafka Asynchronous Ingestion & Event Processing](#flow-2-kafka-asynchronous-ingestion--event-processing)
   - [2.1 Trade Settled Flow (`trades.settled.v1`) — Counterparty Privacy Dual Fan-Out](#21-trade-settled-flow-tradessettledv1--counterparty-privacy-dual-fan-out)
   - [2.2 Order Cancelled Flow (`orders.cancelled.v1`) — Inbox Persistence](#22-order-cancelled-flow-orderscancelledv1--inbox-persistence)
   - [2.3 Portfolio Updated Flow (`portfolios.updated.v1`) — Ephemeral Streaming](#23-portfolio-updated-flow-portfoliosupdatedv1--ephemeral-streaming)
   - [2.4 Kafka Poison Message & Dead-Letter Queue (DLQ) Flow](#24-kafka-poison-message--dead-letter-queue-dlq-flow)
4. [Flow 3: Transactional Outbox Background Relayer](#flow-3-transactional-outbox-background-relayer)
   - [3.1 Batch Claim with Distributed Lease & Token](#31-batch-claim-with-distributed-lease--token)
   - [3.2 Redis Pub/Sub Broadcast & Token Verification](#32-redis-pubsub-broadcast--token-verification)
   - [3.3 Retry Backoff & Exhaustion](#33-retry-backoff--exhaustion)
   - [3.4 Orphan Lease Recovery Flow](#34-orphan-lease-recovery-flow)
5. [Flow 4: Real-Time WebSocket Delivery Flow (Notification to Gateway)](#flow-4-real-time-websocket-delivery-flow-notification-to-gateway)
6. [Flow 5: Synchronous gRPC Inbox Operations](#flow-5-synchronous-grpc-inbox-operations)
   - [5.1 `CreateNotification` Flow with Idempotency Recovery](#51-createnotification-flow-with-idempotency-recovery)
   - [5.2 `GetNotifications` Flow with Keyset Pagination](#52-getnotifications-flow-with-keyset-pagination)
   - [5.3 `MarkAsRead` Flow with Anti-Tenant Enumeration Protection](#53-markasread-flow-with-anti-tenant-enumeration-protection)
   - [5.4 `MarkAllAsRead` Flow](#54-markallasread-flow)
   - [5.5 `GetUnreadCount` Flow](#55-getunreadcount-flow)
7. [Flow 6: Observability, Metrics & Health Probes](#flow-6-observability-metrics--health-probes)
8. [Summary Matrix of System Invariants](#8-summary-matrix-of-system-invariants)

---

## 1. Global System Architecture & Topology

The Notification Service acts as the central communication hub of the TradeDrift exchange. It bridges asynchronous domain events from Apache Kafka and synchronous gRPC alerts from internal microservices (Wallet, Auth, Matching Engine) into persistent user inboxes and low-latency Redis Pub/Sub channels for real-time WebSocket delivery.

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

## Flow 1: Service Lifecycle, Bootstrap & Graceful Teardown

```
[OS Boot / Container Start]
      │
      ▼
1. Load Environment Configuration (config.Load)
      │  ├─ Validates Database URL, Port, Redis, Kafka Brokers, Topics, DLQs
      │  └─ Rejects invalid/missing values immediately (Fail-Fast)
      ▼
2. Initialize Prometheus Telemetry (metrics.New)
      │  └─ Registers CounterVecs & Histograms with global promauto
      ▼
3. Connect PostgreSQL Connection Pool (sql.Open "pgx")
      │  ├─ Configures MaxOpenConns, MaxIdleConns, ConnMaxLifetime
      │  └─ Executes db.PingContext(ctx)
      ▼
4. Run/Verify Database Schema Migrations (Goose)
      │  └─ Enforces sequential migrations (00001, 00002, 00003)
      ▼
5. Connect Redis Client (go-redis)
      │  └─ Executes redisClient.Ping(ctx)
      ▼
6. Initialize Kafka Consumer Group & DLQ Producer
      │  ├─ Creates kafka.Reader for configured topics
      │  └─ Creates kafka.Writer for dead-letter-queue fallback
      ▼
7. Wire Ports & Adapters (Dependency Injection)
      │  ├─ Repo: postgres.New(db)
      │  ├─ Service: service.New(repo, logger)
      │  ├─ Publisher: publisher.New(repo, redisClient, cfg, logger)
      │  ├─ Handler: handler.New(service, logger)
      │  └─ Consumer: kafka.NewConsumer(service, reader, dlqWriter, logger)
      ▼
8. Launch Background Goroutines
      │  ├─ go publisher.Start(ctx)          (Transactional Outbox Relayer)
      │  ├─ go consumer.Start(ctx)            (Kafka Consumer Event Loop)
      │  ├─ go startGRPCServer(listener)      (gRPC API on :50051)
      │  └─ go startHTTPServer(router)        (Health & Metrics on :8081)
      ▼
[SERVICE OPERATIONAL - READY TO SERVE TRAFFIC]
      │
[OS Signal: SIGINT / SIGTERM Received]
      │
      ▼
9. Graceful Teardown Phase (10-second Context Timeout)
      │  ├─ 1. HTTP Server Shutdown (Stop healthz/ready traffic)
      │  ├─ 2. gRPC Server GracefulStop() (Finish in-flight RPCs)
      │  ├─ 3. Stop Kafka Consumer (Cancel ctx, finish current batch, close reader)
      │  ├─ 4. Stop Outbox Publisher (Release active claim tokens, close worker)
      │  ├─ 5. Close Kafka DLQ Producer
      │  ├─ 6. Close Redis Client
      │  └─ 7. Close PostgreSQL Connection Pool
      ▼
[Process Exits Cleanly with Code 0]
```

---

## Flow 2: Kafka Asynchronous Ingestion & Event Processing

### 2.1 Trade Settled Flow (`trades.settled.v1`) — Counterparty Privacy Dual Fan-Out

In a Central Limit Order Book (CLOB), revealing counterparty identities or order IDs enables malicious market participants to track competitors. The Notification Service enforces strict **counterparty privacy isolation**.

```
                         Kafka Topic: "trades.settled.v1"
                                       │
                                       ▼
                             kafka.Consumer.Start()
                                       │
                                       ▼
                       FetchMessage(ctx) -> Parse JSON
                                       │
                                       ▼
                     Validate TradeSettledEvent Schema:
                     - event_id, trade_id, market_id: UUID
                     - buyer_user_id, seller_user_id: UUID
                     - buy_order_id, sell_order_id: UUID
                     - price, quantity > 0
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
                 │    VALUES ($event_id, $buyer_id, $notif1) │
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

### 2.2 Order Cancelled Flow (`orders.cancelled.v1`) — Inbox Persistence

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

### 2.3 Portfolio Updated Flow (`portfolios.updated.v1`) — Ephemeral Streaming

Balance updates occur continuously (after every price tick, execution fill, deposit, or withdrawal). Writing every portfolio snapshot to `notifications` would generate millions of dead rows daily.

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

### 2.4 Kafka Poison Message & Dead-Letter Queue (DLQ) Flow

A corrupted message (malformed JSON, broken types) must never permanently stall consumer group partition consumption.

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

## Flow 3: Transactional Outbox Background Relayer

The Outbox Worker runs continuously in the background, reliably publishing staged outbox events to Redis Pub/Sub using distributed claims and optimistic concurrency.

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

### 3.4 Orphan Lease Recovery Flow

```
Periodic Sweeper Ticker (Every 15 Seconds)
                 │
                 ▼
repo.ReleaseOutboxClaims(ctx, maxAge=60s)
                 │
                 ▼
UPDATE notification_outbox
SET status = 'PENDING',
    claim_token = NULL,
    lease_timeout = NULL
WHERE status = 'PROCESSING'
  AND lease_timeout < NOW();
                 │
                 ▼
Stalled events (e.g. from crashed pods) immediately return to PENDING pool
```

---

## Flow 4: Real-Time WebSocket Delivery Flow (Notification to Gateway)

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

## Flow 5: Synchronous gRPC Inbox Operations

### 5.1 `CreateNotification` Flow with Idempotency Recovery

Internal services (such as the Wallet Service depositing funds) call `CreateNotification` via gRPC. Network timeouts between caller and Notification Service are safely handled without creating duplicates.

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

### 5.2 `GetNotifications` Flow with Keyset Pagination

Avoids slow `OFFSET` table scans by using `(created_at, id)` composite cursor pairs and a `LIMIT + 1` sentinel row.

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

### 5.3 `MarkAsRead` Flow with Anti-Tenant Enumeration Protection

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

### 5.4 `MarkAllAsRead` Flow

```
Client Request: MarkAllAsRead(user_id="U1")
      │
      ▼
UPDATE notifications
SET read_at = NOW()
WHERE user_id = $1
  AND read_at IS NULL;
      │
      ▼
Returns total count of notifications marked as read.
Client resets local unread counter badge to 0.
```

---

### 5.5 `GetUnreadCount` Flow

```
Client Request: GetUnreadCount(user_id="U1")
      │
      ▼
SELECT COUNT(*)
FROM notifications
WHERE user_id = $1
  AND read_at IS NULL;
      │
      ▼
Returns integer count (e.g. 4) for navbar icon badge rendering.
```

---

## Flow 6: Observability, Metrics & Health Probes

```
                         HTTP / Prometheus Port (:8081)
                                       │
        ┌──────────────────────────────┼──────────────────────────────┐
        ▼                              ▼                              ▼
GET /healthz                   GET /ready                     GET /metrics
(Liveness Probe)               (Readiness Probe)              (Prometheus Scrape)
        │                              │                              │
Check process status           Ping PostgreSQL & Redis        Scrape PromAuto Counters:
- Return 200 OK                - db.PingContext()             - notification_events_total
                               - redis.Ping()                 - notification_db_latency_seconds
                               - Return 200 OK or 503         - notification_outbox_latency
                                                              - notification_dlq_messages_total
```

---

## 8. Summary Matrix of System Invariants

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
