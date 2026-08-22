# Order Service — Kafka Publisher Package (`internal/kafka`)

> **Package:** `tradedrift/services/order/internal/kafka/publisher`  
> **Directory:** `services/order/internal/kafka/`  
> **Role:** Outbox-backed Kafka Event Publisher & Event Broker Adapter

---

## 1. Purpose & Architectural Role

The `kafka` package contains the **Transactional Outbox Publisher worker** for the Order Service. It is responsible for polling pending outbox events committed to the `tradedrift_order` PostgreSQL database, routing them to target Kafka topics, and guaranteeing **at-least-once delivery** to downstream consumers (such as the Matching Engine).

Key responsibilities:
1. **Producer Abstraction**: Defines a clean `Producer` interface (`Publish`, `Close`). Two implementations exist:
   - `LogProducer` — dev-only stub, prints to stdout, no Kafka needed.
   - `KafkaProducer` — real production implementation using `github.com/segmentio/kafka-go`. Wired in `main.go` via `KAFKA_BROKERS` env var.
2. **Topic Routing**: Resolves domain event types (`OrderCreated`, `OrderCancelRequested`) to target Kafka topics (`orders.submitted`, `orders.cancel-requested`). Rejects unknown event types to prevent misdirected events.
3. **Delivery ACK Verification**: Marks an outbox event as `PUBLISHED` **only after** receiving successful delivery confirmation from the Kafka broker.
4. **Linear Retry Backoff**: If publishing fails, calls `RecordOutboxPublishError` to store the error message and apply progressive linear backoff (`1s, 2s, 3s... capped at 60s max`) to prevent retry spam.
5. **Clean Goroutine Exit**: Responds to `ctx.Done()` signals during graceful shutdown.

---

## 2. Directory Structure

```
services/order/internal/kafka/
├── README.md                            <-- This documentation file
└── publisher/
    ├── outbox_publisher.go             <-- Background polling loop & topic routing logic
    ├── producer.go                     <-- Producer interface & LogProducer (dev stub only)
    └── kafka_producer.go               <-- Real KafkaProducer (segmentio/kafka-go) — used in production & Docker
```

---

## 3. Packages & Dependencies Used

| Package | Purpose & Rationale |
| :--- | :--- |
| `context` | Manages worker background loop lifecycle and cancellation signals. |
| `fmt` | Formats topic resolution errors for unknown event types. |
| `time` | Configures polling intervals (`time.Ticker` at 200ms) and retry backoff. |
| `go.uber.org/zap` | Structured logging for event delivery ACKs and failure errors. |
| `tradedrift/services/order/internal/repository` | Imports `OutboxRepository` interface and `OutboxEvent` entity model. |

---

## 4. Components & Method Breakdown

### 4.1 Interface `Producer` (`producer.go`)

```go
type Producer interface {
    Publish(ctx context.Context, topic, partitionKey string, payload []byte) error
    Close() error
}
```
* **`Publish`**: Delivers event byte payloads to Kafka with a specified topic and partition key (`market_id`).
* **`Close`**: Flushes pending messages and closes the broker connection.

---

### 4.2 Struct `LogProducer` (`producer.go`) — Dev Stub Only

```go
type LogProducer struct {
    logger *zap.Logger
}
```
* **Purpose**: Local development implementation of `Producer`. Logs event publications to stdout without needing a live Kafka broker. **NOT used in Docker/production.**

---

### 4.3 Struct `KafkaProducer` (`kafka_producer.go`) — Production

```go
type KafkaProducer struct {
    writer *kafkago.Writer
    logger *zap.Logger
}
```
* **Purpose**: Real Kafka producer using `segmentio/kafka-go` Writer. Wired in `cmd/server/main.go` via:
  ```go
  kafkaProducer := publisher.NewKafkaProducer(cfg.KafkaBrokers, appLogger)
  ```
* **Key Config**:
  - `AllowAutoTopicCreation: true` — topics created automatically on first publish.
  - `RequiredAcks: RequireOne` — waits for leader broker ACK before returning.
  - `Balancer: Hash{}` — routes same `market_id` to same partition (ordering guarantee).

---

### 4.3 Struct `OutboxPublisher` (`outbox_publisher.go`)

```go
type OutboxPublisher struct {
    repo     repository.OutboxRepository
    producer Producer
    logger   *zap.Logger
    interval time.Duration
    topicMap map[string]string
}
```

#### Topic Catalog:
```go
topicMap = map[string]string{
    "OrderCreated":         "orders.submitted",
    "OrderCancelRequested": "orders.cancel-requested",
}
```

---

### 4.4 Method `Start(ctx)`
* **Signature:** `func (p *OutboxPublisher) Start(ctx context.Context)`
* **Behavior**: Runs a `for { select }` loop driven by a 200ms `time.Ticker`. Stops cleanly when `ctx.Done()` receives a cancellation signal during shutdown.

---

### 4.5 Method `processPendingEvents(ctx)`
* **Signature:** `func (p *OutboxPublisher) processPendingEvents(ctx context.Context)`
* **Step-by-Step Execution**:
  1. Calls `p.repo.GetUnpublishedOutboxEvents(ctx, 50)` (executes atomic `UPDATE ... RETURNING` claim query).
  2. For each claimed event:
     - Calls `resolveTopic(event.EventType)`. If unknown, logs error and records failure via `RecordOutboxPublishError`.
     - Calls `p.producer.Publish(ctx, topic, event.PartitionKey, event.Payload)`.
     - If publish fails $\rightarrow$ calls `RecordOutboxPublishError` (applies linear backoff) and uses `continue` to proceed with remaining batch items.
     - If publish succeeds $\rightarrow$ calls `p.repo.MarkOutboxEventAsPublished(ctx, event.ID)` (sets `published_at = NOW()`, `processing_at = NULL`, `last_error = NULL`).

---

### 4.6 Method `resolveTopic(eventType)`
* **Signature:** `func (p *OutboxPublisher) resolveTopic(eventType string) (string, error)`
* **Error Prevention**: Explicitly checks `p.topicMap[eventType]`. Returns error `unknown outbox event type: <type>` if an event is unmapped, preventing silent publish failures.
