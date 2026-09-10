# Order Service — Kafka Integration Package (`internal/kafka`)

> **Package:** `tradedrift/services/order/internal/kafka`  
> **Directory:** `services/order/internal/kafka/`  
> **Role:** Outbox-backed Kafka Event Publisher & Asynchronous Trade Fill Consumer

---

## 1. Purpose & Architectural Role

The `kafka` package contains both the **Transactional Outbox Publisher worker** and the **Trade Fill Consumer worker** for the Order Service:
1. **Outbox Publisher (`publisher/`)**: Polls pending outbox events committed to the `tradedrift_order` database, routes them to target Kafka topics (`orders.submitted`, `orders.cancel-requested`), and guarantees **at-least-once delivery** to downstream components.
2. **Trade Fill Consumer (`consumer/`)**: Subscribes to execution settlement events (`trades.settled`) from Kafka, records execution progress, and updates order states (`OPEN` $\to$ `PARTIALLY_FILLED` $\to$ `FILLED`) idempotently using the `processed_trades` table.

---

## 2. Directory Structure

```
services/order/internal/kafka/
├── README.md                            <-- This documentation file
├── consumer/
│   └── consumer.go                      <-- Consumes trades.settled, invokes ApplyTradeFill
└── publisher/
    ├── outbox_publisher.go             <-- Background polling loop & topic routing logic
    ├── producer.go                     <-- Producer interface & LogProducer (dev stub only)
    ├── kafka_producer.go               <-- Real KafkaProducer (segmentio/kafka-go) — Option B Explicit Partitioning
    └── kafka_producer_test.go          <-- Unit tests for explicit partition mapping
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

### 4.3 Struct `KafkaProducer` (`kafka_producer.go`) — Production (Option B Explicit Partitioning)

```go
type KafkaProducer struct {
    writer             *kafkago.Writer
    logger             *zap.Logger
    partitionOverrides map[string]int
}
```
* **Purpose**: Real Kafka producer using `segmentio/kafka-go` Writer with **Option B (Explicit Partition Assignment)**. Wired in `cmd/server/main.go` via:
  ```go
  kafkaProducer, err := publisher.NewKafkaProducer(cfg.KafkaBrokers, appLogger)
  ```
* **Explicit Market-to-Partition Routing**:
  - `ResolvePartition(marketID)` maps trading pairs directly to dedicated Kafka partitions:
    - `BTC-USDT` $\to$ **Partition 0** (`BTC_PARTITION`, default `0`)
    - `ETH-USDT` $\to$ **Partition 1** (`ETH_PARTITION`, default `1`)
    - `SOL-USDT` $\to$ **Partition 2** (`SOL_PARTITION`, default `2`)
  - Explicitly sets `msg.Partition = partition` and `msg.Key = []byte(market_id)` to guarantee 100% deterministic agreement with the Matching Engine.
* **Key Config**:
  - `AllowAutoTopicCreation: true` — topics created automatically on first publish.
  - `RequiredAcks: RequireOne` — waits for leader broker ACK before returning.

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

---

### 4.7 Struct `Consumer` (`consumer/consumer.go`)

```go
type Consumer struct {
    reader *kafkago.Reader
    repo   repository.OrderRepository
    logger *zap.Logger
}
```

* **Purpose**: Subscribes to the Kafka topic configured by `TOPIC_TRADES_SETTLED` (`trades.settled`) with consumer group `order-service-settlement-group`.
* **Wire Event Model (`TradeSettledEvent`)**:
  ```go
  type TradeSettledEvent struct {
      TradeID     string `json:"trade_id"`
      BuyOrderID  string `json:"buy_order_id"`
      SellOrderID string `json:"sell_order_id"`
      Quantity    string `json:"quantity"`
  }
  ```
* **Execution Loop (`Start(ctx)`)**:
  1. Fetches message from Kafka using `c.reader.FetchMessage(ctx)`.
  2. Deserializes `TradeSettledEvent` JSON payload. Malformed payloads are committed to prevent infinite poison loops.
  3. Invokes `c.repo.ApplyTradeFill(ctx, ev.TradeID, ev.BuyOrderID, ev.SellOrderID, ev.Quantity)` inside PostgreSQL to update order filled/remaining quantities and transition status (`PARTIALLY_FILLED` or `FILLED`).
  4. Manually commits Kafka message offset via `c.reader.CommitMessages(ctx, msg)` only after successful database transaction commit.

