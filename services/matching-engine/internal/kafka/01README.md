# `internal/kafka` — Kafka Consumer & Command Ingestion

**Package:** `kafka`  
**Service:** Matching Engine  
**Last Updated:** August 2026  

---

## 1. What This Package Does

This package is the **live ingestion, validation, and command routing layer** of the Matching Engine. It reads raw Kafka messages from the unified `orders.commands` topic published by the Order Service transactional outbox, validates every envelope and payload field, and routes them to the correct `MarketEngine`'s `InputQueue`.

It has exactly one job: get command events from Kafka into the right engine's queue safely, strictly, and with guaranteed partition-to-market ordering.

---

## 2. Purpose & Core Invariants

The `kafka` package enforces four fundamental operational invariants:

1. **Market Affinity & Single Partition Ordering**:
   Orders for the same market must arrive on the same partition and in strict sequence. The consumer enforces that `msg.Key == envelope.MarketID`. Any key mismatch is rejected.
2. **Startup Offset Realignment (Issue #1)**:
   Before live intake begins, `seekToPostgresCheckpoints()` dynamically discovers all topic partitions and positions the consumer group on the broker to the authoritative checkpoint from PostgreSQL (`kafka_checkpoints`). Live intake resumes seamlessly from `checkpoint + 1`.
3. **In-Flight Offset Registration**:
   Informs the Checkpoint Coordinator (`internal/checkpoint`) of newly received message offsets (`offsetTracker.Track(pos)`) *before* dispatching to the market worker goroutine.
4. **Fail-Closed Malformed Command Protection (Issue #10)**:
   If a corrupted, malformed, or malicious message arrives during live consumption (bad JSON, invalid UUID, unsupported schema version, or unparseable decimal), rather than silently dropping it or entering a CPU spin loop, the consumer logs a `FATAL` error and triggers graceful fail-closed shutdown via `cancelCtx()`.

---

## 3. Files In This Package

| File | Purpose |
| :--- | :--- |
| `consumer.go` | `Consumer` struct, `Config`, `CommandEnvelope`, read loops, handlers, parsers, and `TestableConsumer` |
| `consumer_test.go` | Unit tests covering order creation, cancellation, partition key validation, fail-closed shutdowns, and checkpoint seeks |
| `01README.md` | Primary package documentation |
| `02READEME.md` | Comprehensive deep-dive reference |

---

## 4. Topics Consumed

| Topic | Published by | Payload Event Types |
| :--- | :--- | :--- |
| `orders.commands` | Order Service Transactional Outbox | `OrderCreated`, `OrderCancelRequested` |

All messages arrive inside a standardized polymorphic `CommandEnvelope`:
```json
{
  "event_id": "019163f5-93b6-710b-b187-2c93b6710bb1",
  "event_type": "OrderCreated",
  "event_version": 1,
  "market_id": "BTC-USDT",
  "occurred_at": "2026-08-25T10:00:00.123456789Z",
  "payload": { ... }
}
```

---

## 5. Structs & Interfaces

### `Consumer`
```go
type Consumer struct {
    commandReader          *kafkago.Reader
    manager                *market.MarketManager
    tracker                offsetTracker
    cancelCtx              context.CancelFunc
    brokers                []string
    groupID                string
    db                     dbQueryer
    discoverPartitionsFunc func(topic string) ([]int, error)
    commitMessagesFunc     func(ctx context.Context, brokers []string, topic string, groupID string, partition int, offset int64) error
}
```

### `offsetTracker`
```go
type offsetTracker interface {
    Track(pos orderbook.KafkaPosition)
    MarkDone(ctx context.Context, pos orderbook.KafkaPosition) error
}
```

---

## 6. How Messages Are Processed (`HandleOrderCommand`)

```
Kafka Message (`orders.commands`)
         │
         ▼
Unmarshal `CommandEnvelope`
         │
         ├── 1. Partition Key Check: `msg.Key == env.MarketID` (must match)
         ├── 2. Event ID Validation: `uuid.Parse(env.EventID)`
         ├── 3. Schema Version Check: `env.EventVersion == 1`
         ├── 4. Resolve Route: `route(env.MarketID)` (must exist)
         │
         ▼
Switch on `env.EventType`:
  - "OrderCreated" ────────► Parse OrderID, UserID, Side (BUY/SELL), OrderType (LIMIT/MARKET), Price, Quantity
                             queue <- market.InputEvent{Type: EventOrderCreated, ...}
  - "OrderCancelRequested" ─► Parse OrderID, UserID
                             queue <- market.InputEvent{Type: EventOrderCancel, ...}
  - Default ────────────────► Error: unknown event_type
```

---

## 7. What This Package Does NOT Do

- Does NOT execute matches or modify the `OrderBook` — handled by `../market/` and `../matcher/`
- Does NOT publish trade fills to Kafka — handled by `../publisher/`
- Does NOT commit contiguous checkpoints to PostgreSQL — handled by `../checkpoint/`
- Does NOT project depth to Redis — handled by `../projection/`
- Does NOT validate user balances or account funding — handled by Order & Wallet Services
- Does NOT replay historical logs for startup recovery — handled by `../recovery/`
