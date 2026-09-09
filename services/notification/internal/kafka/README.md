# Notification Service — Kafka Ingestion & DLQ Layer Guide

This document provides an in-depth technical explanation of the `internal/kafka` package in the **TradeDrift Notification Service** located in `services/notification/internal/kafka/`.

It details the role of the asynchronous event consumer, the distributed systems problems it solves, the at-least-once offset commit guarantee, poison message Dead-Letter Queue (DLQ) routing, a function-by-function breakdown, and complete execution flow diagrams.

---

## Architecture Overview & Role of `internal/kafka`

The `internal/kafka` package serves as the **asynchronous event ingestion boundary** of the Notification Service. It subscribes to core exchange domain topics emitted by the matching engine, order service, and trade settlement pipeline.

```
services/notification/internal/kafka/
├── consumer.go       # Consumer loop, 3-phase processing, DLQ router & topic dispatchers
└── consumer_test.go  # Unit & integration tests using mock readers/writers
```

### Subscribed Topics
| Constant | Topic Name | Source Service | Event Type | Description |
|---|---|---|---|---|
| `TopicTradesSettled` | `trades.settled.v1` | Trade Settlement Service | `TradeSettledEvent` | Fills executed between buyer and seller orders. |
| `TopicOrdersCancelled` | `orders.cancelled.v1` | Order Service | `OrderCancelledEvent` | Cancelled limit/market orders with cancellation reason. |
| `TopicPortfoliosUpdated` | `portfolios.updated.v1` | Portfolio / Ledger Service | `PortfolioUpdatedEvent` | Real-time balance and net-worth portfolio snapshots. |
| `TopicDLQ` | `notifications.dlq` | Notification Service (Writer) | Poison Messages | Dead-Letter Queue for malformed or unprocessable payloads. |

---

## Problems Solved by `consumer.go`

| Problem | Failure Scenario Without `consumer.go` | How `consumer.go` Solves It |
|---|---|---|
| **Data Loss on Crash** | If offsets are committed upon fetching (`AutoCommit = true`) and the container crashes before PostgreSQL commits, the message is permanently lost. | Enforces **Strict At-Least-Once Delivery**: offsets are committed manually **strictly after** the database transaction has successfully committed to disk. |
| **Partition Head-of-Line Blocking** | A poison message (corrupted JSON, missing mandatory `event_id`) that fails validation will retry indefinitely if not acknowledged, permanently halting the partition. | Routes unprocessable messages to `notifications.dlq` with enriched diagnostic headers, commits the offset, and advances to subsequent messages. |
| **Silent Offset Skipping** | If message #10 fails and the consumer continues to process message #11 and commits offset #11, Kafka moves the group offset past #10, causing silent message loss. | Implements an **inner retry loop**: the consumer **never** fetches the next message until the current message has either succeeded or been routed to the DLQ. |
| **Transient Database Outages** | If PostgreSQL connection pool exhausts or experiences a temporary network blip, crashing the pod causes rapid restart flapping. | Uses exponential backoff (1s → 2s → 4s ... capped at 30s) on the same message, pausing partition consumption until database health is restored. |
| **In-Flight Message Dropping on Shutdown** | Killing the process while a message is being processed in Go memory results in uncommitted work or duplicate processing upon restart. | Integrates with `sync.WaitGroup` via `StartWithWaitGroup()`, ensuring in-flight messages finish writing to PostgreSQL and committing offsets before process exit. |
| **PII / Financial Privacy Leakage in Logs** | Logging raw message payloads of failed events exposes user trade details, quantities, and balances to centralized log aggregators. | Logs payload byte length only (`zap.Int("payload_bytes", len(msg.Value))`), omitting raw financial payloads from log files. |

---

## The 3-Phase Consumption Loop Architecture

Each topic runs its own independent `consumeLoop` executing a strict three-phase lifecycle:

```
                  ┌────────────────────────────────────────────────┐
                  │                 consumeLoop()                  │
                  └───────────────────────┬────────────────────────┘
                                          │
    [Phase 1: Fetch]                      ▼
                          reader.FetchMessage(ctx)
                                          │
                                          ▼
    [Phase 2: Process]     ┌──────────────┴──────────────┐
                           │   Inner Retry Loop          │
                           │   (Exponential Backoff:     │
                           │    1s -> 2s -> ... -> 30s)  │
                           └──────────────┬──────────────┘
                                          │
                          handler(ctx, msg) Succeeded
                          (or routed to DLQ cleanly)
                                          │
    [Phase 3: Commit]                     ▼
                       ┌──────────────────┴──────────────────┐
                       │   Commit Offset Loop                │
                       │   reader.CommitMessages(ctx, msg)   │
                       └──────────────────┬──────────────────┘
                                          │
                                          ▼
                        Metrics Inc & Fetch Next Message
```

---

## Function-by-Function Deep Dive

### 1. `NewConsumer` & `NewConsumerWithMocks`

```go
func NewConsumer(cfg ConsumerConfig, svc NotificationService, log *zap.Logger) *Consumer
func NewConsumerWithMocks(tradeReader, cancelReader, portfolioReader MessageReader, dlqWriter DLQWriter, svc NotificationService, log *zap.Logger) *Consumer
```
- **Purpose**: Initializes independent `kafka.Reader` instances for each of the three domain topics and sets up the `dlqWriter`.
- **Tuning**: Configures `MinBytes: 10KB` and `MaxBytes: 10MB` per fetch batch, balancing network throughput with low alert latency.
- **Testability**: `NewConsumerWithMocks` allows injecting mock readers/writers for deterministic unit testing without a live Kafka cluster.

---

### 2. `Start` & `StartWithWaitGroup`

```go
func (c *Consumer) Start(ctx context.Context)
func (c *Consumer) StartWithWaitGroup(ctx context.Context, wg *sync.WaitGroup)
```
- **Purpose**: Spawns one background goroutine per topic (`trades.settled.v1`, `orders.cancelled.v1`, `portfolios.updated.v1`).
- **Graceful Shutdown**: `StartWithWaitGroup` increments the shared `sync.WaitGroup`. When `ctx` is cancelled during shutdown, `consumeLoop` finishes its active message, calls `wg.Done()`, and allows `main.go` to complete teardown without abandoning work.

---

### 3. `consumeLoop` (Core Consumer Engine)

```go
func (c *Consumer) consumeLoop(ctx context.Context, reader MessageReader, topic string, handler messageHandler)
```

#### Detailed Phase Mechanics:
1. **Fetch Phase**:
   - Calls `reader.FetchMessage(ctx)`.
   - Advances the local reader cursor.
   - If a transient network error occurs, sleeps 500ms and retries fetching.
2. **Process Phase (Inner Retry Loop)**:
   - Invokes the topic-specific handler (`processTradeSettled`, `processOrderCancelled`, `processPortfolioUpdated`).
   - If the handler returns an error (e.g. database deadlocks, connection pool timeout):
     - Logs error with topic, partition, and offset.
     - Waits with exponential backoff (`1s → 2s → 4s → 8s → 16s → 30s`).
     - **Does not advance** to the next message. Retries the **same message** until success or shutdown.
3. **Commit Phase**:
   - Calls `reader.CommitMessages(ctx, msg)` strictly after Phase 2 completes.
   - If commit fails (e.g. Kafka coordinator rebalance), retries commit in a 1-second loop until confirmed.
   - Increments Prometheus metric `KafkaEventsConsumedTotal.WithLabelValues(topic, "success")`.

---

### 4. Topic Dispatchers

#### `processTradeSettled(ctx, msg)`
- Unmarshals payload into `service.TradeSettledEvent`.
- Validates required UUID fields (`event_id`, `trade_id`, `buyer_user_id`, `seller_user_id`) via `ev.Validate()`.
- If invalid: routes to DLQ via `handlePoison`.
- If valid: calls `c.svc.HandleTradeSettled(ctx, &ev)`.

#### `processOrderCancelled(ctx, msg)`
- Unmarshals payload into `service.OrderCancelledEvent`.
- Validates `event_id`, `order_id`, and `user_id`.
- Routes malformed messages to DLQ or dispatches to `c.svc.HandleOrderCancelled(ctx, &ev)`.

#### `processPortfolioUpdated(ctx, msg)`
- Unmarshals payload into `service.PortfolioUpdatedEvent`.
- Validates `event_id` and `user_id`.
- Routes malformed messages to DLQ or dispatches to `c.svc.HandlePortfolioUpdated(ctx, &ev)`.

---

### 5. `handlePoison` (Dead-Letter Queue Router)

```go
func (c *Consumer) handlePoison(ctx context.Context, msg kafka.Message, topic, reason string) error
```

#### Purpose
Safely intercepts permanently unprocessable messages, isolating them from healthy traffic while ensuring operators have full auditability.

#### Diagnostic Header Enrichment
Before writing to `notifications.dlq`, attaches operational metadata headers:
- `original-topic`: Name of the topic where the poison message originated.
- `error-reason`: Detailed validation or unmarshal failure string.
- `dlq-time`: RFC3339 UTC timestamp when the poison event was isolated.

#### Safety Return Contract
- **Returns `nil` on DLQ Success**: The poison message is safely stored in `notifications.dlq`. The caller proceeds to Phase 3 (Offset Commit) and unblocks the partition.
- **Returns `error` on DLQ Failure**: If the DLQ broker is unreachable, returning an error forces the inner retry loop to retry. This guarantees **zero silent message loss**, even for poison messages.

---

### 6. `Close()`

```go
func (c *Consumer) Close() error
```
- Closes all three topic readers (`tradeReader`, `cancelReader`, `portfolioReader`) and the `dlqWriter`.
- Uses Go's `errors.Join` to ensure all resource handles are closed even if an individual handle returns an error.

---

## End-to-End Ingestion Flows

### Flow 1: Happy Path Processing & Post-DB Commit
```
Kafka Topic: trades.settled.v1
       │
       ▼
kafka.Consumer.FetchMessage()
       │
       ▼
processTradeSettled()
       │ 1. json.Unmarshal() ──► Success
       │ 2. ev.Validate()    ──► Success
       ▼
service.HandleTradeSettled()
       │
       ▼
repository.CreateTradeSettledTx() ─── Commits to PostgreSQL
       │
       ▼
Handler returns nil
       │
       ▼
kafka.Consumer.CommitMessages() ─── Commits offset to Kafka Broker
       │
       ▼
Fetch next message
```

---

### Flow 2: Transient Database Outage (Zero Loss Retry)
```
Kafka Message Fetched (Offset: 405)
       │
       ▼
service.HandleTradeSettled()
       │
       ▼
repository.CreateTradeSettledTx() ─── Fails (PostgreSQL pool exhausted)
       │
       ▼
Handler returns error
       │
       ▼
Inner Retry Loop:
       │ Log error: "Transient error processing Kafka event; will retry same message"
       │ Sleep with backoff (1s)
       ▼
Retry #1 on Message 405 ─── Fails ──► Sleep 2s
Retry #2 on Message 405 ─── Fails ──► Sleep 4s
       ...
Retry #N on Message 405 ─── Database recovered!
       │
       ▼
CreateTradeSettledTx() succeeds
       │
       ▼
CommitMessages(Offset: 405)
       │
       ▼
Advance to Offset 406
```

---

### Flow 3: Poison Message Isolation via DLQ
```
Kafka Message Fetched (Malformed JSON / Missing event_id)
       │
       ▼
processOrderCancelled()
       │
       ├── json.Unmarshal() fails  OR
       └── ev.Validate() fails ("missing required field: event_id")
       │
       ▼
handlePoison(topic="orders.cancelled.v1", reason="missing event_id")
       │
       ├── Construct DLQ envelope with original Key & Value
       ├── Attach Headers:
       │      ├── "original-topic": "orders.cancelled.v1"
       │      ├── "error-reason": "missing required field: event_id"
       │      └── "dlq-time": "2026-09-09T14:15:00Z"
       ▼
dlqWriter.WriteMessages(notifications.dlq) ─── Success
       │
       ▼
handlePoison returns nil
       │
       ▼
CommitMessages() (Acknowledges poison message offset)
       │
       ▼
Partition unblocked; advances to next valid message
```
