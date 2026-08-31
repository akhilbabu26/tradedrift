# `internal/kafka` — Kafka Producer & Consumer

**Package:** `kafka`  
**Service:** Liquidity Engine  
**Last Updated:** August 2026

---

## 1. What This Package Does

This package has two distinct responsibilities:

1. **Producer** (`producer.go`): Publishes `OrderCreated` and `OrderCancelRequested` command envelopes to the `orders.commands` Kafka topic. Every message it produces must pass the Matching Engine's strict validation rules.

2. **Consumer** (`consumer.go`): Reads `TradeExecuted` events from the `trades.executed` Kafka topic and forwards them to the engine event channel for fast in-memory inventory updates.

---

## 2. Files In This Package

| File | Purpose |
| :--- | :--- |
| `producer.go` | `Producer` struct, `CommandEnvelope`, `PublishCreate`, `PublishCancel` |
| `consumer.go` | `Consumer` struct, `TradeEvent`, `Run` read loop, `parseTradeMessage` |
| `README.md` | This documentation file |

---

## 3. Topics

| Topic | Direction | Purpose |
| :--- | :--- | :--- |
| `orders.commands` | **Write** | Publish MM order create / cancel commands |
| `trades.executed` | **Read** | Consume trade fills to update inventory state |

---

## 4. Producer

### `CommandEnvelope` Schema

Every command published to `orders.commands` is wrapped in a standard envelope. The ME consumer validates every field strictly.

```json
{
  "event_id":      "uuid-v4",
  "event_type":    "OrderCreated" | "OrderCancelRequested",
  "event_version": 1,
  "market_id":     "BTC-USDT",
  "occurred_at":   "2026-08-31T08:00:00Z",
  "payload":       { ... }
}
```

### `OrderCreated` Payload

```json
{
  "order_id":        "uuid-v4",
  "user_id":         "MM-001",
  "side":            "BUY" | "SELL",
  "order_type":      "LIMIT",
  "price":           "96430.12",
  "quantity":        "0.85000",
  "client_order_id": "MM-BTC-USDT-BID-01-G3"
}
```

### `OrderCancelRequested` Payload

```json
{
  "order_id": "uuid-from-ME",
  "user_id":  "MM-001"
}
```

### Critical Invariants (ME Validation Rules)

```
msg.Key == []byte(env.MarketID)   ← ME fails-closed if key mismatches
env.EventVersion == 1              ← ME rejects other versions
env.EventID must be a valid UUID
price must be tick-rounded string
quantity must be lot-rounded string
order_id must be a valid UUID
user_id must be "MM-001"
```

### Per-Market Writers

The Producer creates one `kafka-go.Writer` per market to enforce partition affinity. The ME consumer expects all BTC-USDT orders on partition 0, ETH-USDT on partition 1, etc.

```go
type Producer struct {
    writers map[string]*kafkago.Writer // marketID → partition-pinned Writer
    logger  *zap.Logger
}
```

Writer configuration:
- `RequiredAcks = RequireAll` — waits for all in-sync replicas before ack
- `WriteTimeout = 10s`
- `AllowAutoTopicCreation = false`

---

## 5. Consumer

### `TradeEvent`

The LE's internal representation of a trade fill. Derived fields (`MMSide`, `MMIsMaker`, `MMIsTaker`) are computed during parsing.

```go
type TradeEvent struct {
    TradeID      string
    MarketID     string
    MakerOrderID string
    TakerOrderID string
    BuyerUserID  string
    SellerUserID string
    Price        decimal.Decimal
    Quantity     decimal.Decimal
    ExecutedAt   time.Time

    MMIsMaker bool   // MM-001 was the resting (maker) side
    MMIsTaker bool   // MM-001 was the aggressor (taker) side
    MMSide    string // "BUY" | "SELL" | "" (empty if MM not involved)
}
```

### MM Involvement Detection

```
trades.executed message arrives
    │
    ├── BuyerUserID == "MM-001"  →  MMSide = "BUY"
    │       ├── MakerOrderID == BuyOrderID  →  MMIsMaker = true
    │       └── else                         →  MMIsTaker = true
    │
    ├── SellerUserID == "MM-001" →  MMSide = "SELL"
    │       ├── MakerOrderID == SellOrderID →  MMIsMaker = true
    │       └── else                         →  MMIsTaker = true
    │
    └── Neither →  MMSide = "" (MM not involved — engine ignores this event)
```

### Important: Consumer Is Read-Only

```
Consumer goroutine:
    Fetch message → parse → send to e.events channel

Consumer NEVER:
    - Calls tracker methods
    - Calls inventory methods
    - Mutates any engine state

State mutation only happens inside the single event loop goroutine.
```

### Back-Pressure Handling

If the event channel is full, the consumer does **not** block indefinitely. It selects with a 5-second timeout and drops the event with a warning log. The engine then self-heals via the next `evReconcileTick`.

### Commit Strategy

The consumer uses auto-commit (`CommitInterval: 1s`). The LE is designed to be idempotent on replay — receiving a duplicate trade event will find the order already updated in the tracker and apply a no-op re-computation.

---

## 6. What This Package Does NOT Do

- Does NOT validate Kafka offsets or manage checkpoints (no ME-style checkpoint coordinator)
- Does NOT subscribe to `orders.events` — order state is tracked in-memory via `order.Tracker`
- Does NOT handle ME status events (`ME_LIVE`, `ME_RECOVERING`) — V1 uses pending timeout detection for ME liveness
- Does NOT enforce partition ordering at the consumer level — `trades.executed` is consumed as a simple fan-in
