# `internal/kafka` — Kafka Consumer

**Package:** `kafka`
**Service:** Matching Engine
**Last Updated:** August 2026

---

## 1. What This Package Does

This package is the **ingestion layer** of the Matching Engine. It reads raw
Kafka messages from two topics published by the Order Service, deserialises
them into strongly-typed Go structs, validates every field, and routes them
to the correct `MarketEngine`''s `InputQueue`.

It has exactly one job: get events from Kafka into the right engine''s queue,
correctly and safely. It does not match orders. It does not touch the OrderBook.
It does not publish results. It only routes.

---

## 2. Purpose

The `kafka` package answers one question:

> Which MarketEngine should receive this Kafka message, and is the message valid enough to send?

If yes → write to `engine.InputQueue`.
If no  → log the error and skip. Never crash the consumer for one bad message.

---

## 3. Files In This Package

| File | Purpose |
| :--- | :--- |
| `consumer.go` | `Consumer` struct, `Config`, read loops, handlers, parsers, `TestableConsumer` |
| `consumer_test.go` | 12 unit tests covering all validation and routing scenarios |
| `README.md` | This file |

---

## 4. Topics Consumed

| Topic | Published by | Event type |
| :--- | :--- | :--- |
| `orders.submitted` | Order Service outbox | `OrderCreated` |
| `orders.cancel-requested` | Order Service outbox | `OrderCancelRequested` |

These topic names match exactly what the Order Service publishes via its
transactional outbox pattern. The payload JSON fields are documented and
pinned to the exact lines in Order Service source code.

---

## 5. Structs

### `Consumer`

```go
type Consumer struct {
    createdReader *kafkago.Reader    // reads from orders.submitted
    cancelReader  *kafkago.Reader    // reads from orders.cancel-requested
    manager       *market.MarketManager
}
```

Owns two independent Kafka readers — one per topic. This means:
- The two streams are consumed independently
- A slow cancel-requested consumer never blocks order-submitted consumption
- Each reader runs in its own goroutine via `Start()`

### `Config`

```go
type Config struct {
    Brokers []string   // e.g. ["localhost:9092"]
    GroupID string     // e.g. "matching-engine"
}
```

Both readers share the same Brokers and GroupID. They use different topics.

### `routeFunc`

```go
type routeFunc func(marketID string) chan market.InputEvent
```

An injectable routing function. Used to decouple the package-level handlers
from `MarketManager` so unit tests can inject a mock without starting a real
Kafka connection or a real MarketEngine.

### `TestableConsumer`

```go
type TestableConsumer struct {
    route routeFunc
}
```

A thin wrapper around the package-level handlers. Created only in test code
via `NewTestableConsumer(route)`. Allows unit tests to call `HandleOrderCreated`
and `HandleOrderCancel` directly with synthetic `kafka.Message` objects.

---

## 6. Public Functions

### `NewConsumer(cfg, manager)`

Creates a `Consumer` with two configured `kafka.Reader` instances. Does NOT
start any goroutines — call `Start(ctx)` separately.

**Key reader settings:**
- `CommitInterval: 0` — manual offset commits disabled (see §9)
- `MaxWait: 1s` — reads block at most 1 second before returning, keeping
  the loop responsive to context cancellation
- `MaxBytes: 10MB` — max message size per fetch

### `Start(ctx)`

Launches two goroutines:
```go
go c.consume(ctx, c.createdReader, c.handleOrderCreated)
go c.consume(ctx, c.cancelReader, c.handleOrderCancel)
```

Both goroutines block until `ctx` is cancelled (graceful shutdown).

**MUST be called AFTER all MarketEngines have completed recovery and are
in `ModeLive`.** If called before recovery, live events would enter
InputQueues before the book is rebuilt — producing incorrect matches.

### `Close()`

Closes both readers. Call during graceful shutdown after cancelling the context.

---

## 7. How Each Message Is Processed

### `orders.submitted` → `handleOrderCreated`

```
Kafka message received
        │
        ▼
json.Unmarshal(msg.Value, &orderCreatedMessage)
        │
        ├─ fail → return error (logged, offset not advanced)
        ▼
uuid.Parse(order_id)
        │
        ├─ fail → return error
        ▼
uuid.Parse(user_id)
        │
        ├─ fail → return error
        ▼
decimal.NewFromString(price)
        │
        ├─ fail → return error
        ▼
decimal.NewFromString(quantity)
        │
        ├─ fail → return error
        ▼
parseSide(side)     "BUY" | "SELL"
        │
        ├─ fail → return error
        ▼
parseOrderType(order_type)   "LIMIT" | "MARKET"
        │
        ├─ fail → return error
        ▼
route(market_id) → InputQueue channel
        │
        ├─ nil (unknown market) → log + return nil (skip)
        ▼
InputQueue <- market.InputEvent{
    Type:      EventOrderCreated,
    OrderCreated: &OrderCreatedPayload{...},
    Topic:     msg.Topic,        ← part of KafkaPosition checkpoint
    Partition: msg.Partition,    ← part of KafkaPosition checkpoint
    Offset:    msg.Offset,       ← part of KafkaPosition checkpoint
}
```

### `orders.cancel-requested` → `handleOrderCancel`

Same flow but simpler — only `order_id`, `user_id`, `market_id` to validate.
No decimal parsing, no side/type parsing.

---

## 8. KafkaPosition — Why Topic + Partition + Offset

The `InputEvent` carries three fields for the Kafka message position:

```go
type InputEvent struct {
    ...
    Topic     string
    Partition int
    Offset    int64
}
```

**Why all three?** Kafka offsets are local to a partition within a topic:

```
orders.submitted      partition 0  offset 125  ← different event
orders.cancel-requested  partition 0  offset 125  ← different event
```

`offset 125` alone is ambiguous. Only `topic + partition + offset` uniquely
identifies one Kafka message globally.

These three fields flow through `InputEvent` → `MatchResult.SourcePosition`
→ Publisher checkpoint. The Publisher writes exactly one row to Postgres per
`MatchResult`:

```sql
INSERT INTO kafka_checkpoints (topic, partition, offset)
VALUES ($1, $2, $3)
ON CONFLICT (topic, partition) DO UPDATE SET offset = $3
```

On restart, recovery reads this row to know exactly where to resume replay.

---

## 9. Why We Do NOT Commit Kafka Offsets

```go
CommitInterval: 0  // manual — checkpoint is handled by Publisher via Postgres
```

The Matching Engine uses its own Postgres checkpoint table as the source of
truth for recovery, NOT Kafka consumer group offsets.

**Why?**

If Kafka offsets were committed, there would be two sources of truth:
- Kafka consumer group: "I consumed up to offset X"
- Postgres checkpoint: "I fully processed and published up to offset X"

These could drift. For example:
- Kafka offset committed after consuming but before publishing the TradeExecuted
- Process crashes between those two steps
- On restart: Kafka says "start from X+1", but TradeExecuted for X was never published

The Postgres checkpoint is only written after the Publisher confirms the
TradeExecuted event was delivered to Kafka AND the depth snapshot was
written to Redis. This is the only correct atomicity boundary.

---

## 10. Error Handling Strategy

| Error scenario | What happens |
| :--- | :--- |
| Malformed JSON | Return error — log, skip message, offset NOT advanced |
| Invalid UUID | Return error — log, skip message, offset NOT advanced |
| Invalid decimal | Return error — log, skip message, offset NOT advanced |
| Unknown side/order_type | Return error — log, skip message, offset NOT advanced |
| Unknown market_id | Return nil — log and skip SILENTLY (not an error) |
| FetchMessage network error | Log and retry — loop continues |
| Context cancelled | Return immediately — clean shutdown |

**Why unknown market returns nil instead of error:**
An unknown market_id is not a corrupt message — it could be a valid message
for a market that this ME instance does not manage (future multi-node routing).
Returning an error would cause the loop to retry indefinitely without ever
advancing. Skipping silently is the correct behaviour.

**Why errors do NOT crash the consumer:**
One bad message must never stop ALL markets. Crashing the consumer would
halt BTC-USDT, ETH-USDT, and SOL-USDT because of one malformed order from
one user. The error is logged with full context (topic, partition, offset)
so it can be investigated without impacting the live system.

---

## 11. Backpressure

The send to `engine.InputQueue` is a synchronous channel write:

```go
queue <- market.InputEvent{...}
```

If the engine''s InputQueue (capacity 1000) is full, this line blocks.
The consequence is intentional:

```
Engine slow or paused
        ↓
InputQueue fills to 1000
        ↓
Consumer goroutine blocks on send
        ↓
FetchMessage not called
        ↓
Kafka messages remain uncommitted on the broker
        ↓
Consumer lag grows (visible in monitoring)
        ↓
Engine catches up → queue drains → consumer unblocks
```

This provides natural backpressure all the way to the Kafka broker. Events
are never dropped — they stay safely on the Kafka partition until consumed.

Do NOT introduce a goroutine-per-message to avoid the blocking send. That
would allow unlimited parallelism into the Event Loop, destroying the
single-goroutine ownership model and requiring locks on the OrderBook.

---

## 12. Test Coverage

12 unit tests in `consumer_test.go`. All tests use `TestableConsumer` with
injected `routeFunc` — no real Kafka connection required.

| Test | Scenario |
| :--- | :--- |
| `TestHandleOrderCreated_Valid` | Full happy path — all fields correct, event routed, position propagated |
| `TestHandleOrderCreated_MalformedJSON` | Returns error |
| `TestHandleOrderCreated_InvalidOrderID` | Returns error for bad UUID |
| `TestHandleOrderCreated_InvalidUserID` | Returns error for bad UUID |
| `TestHandleOrderCreated_InvalidDecimalPrice` | Returns error |
| `TestHandleOrderCreated_InvalidDecimalQuantity` | Returns error |
| `TestHandleOrderCreated_InvalidSide` | Returns error for unknown side string |
| `TestHandleOrderCreated_InvalidOrderType` | Returns error for unknown order type |
| `TestHandleOrderCreated_UnknownMarket_Skipped` | Returns nil, does not error |
| `TestHandleOrderCancel_Valid` | Full happy path — topic, partition, offset all verified |
| `TestHandleOrderCancel_MalformedJSON` | Returns error |
| `TestHandleOrderCancel_UnknownMarket_Skipped` | Returns nil, does not error |

All 12 tests pass.

---

## 13. V1 Limitations and V2 Upgrade Path

### Current V1

| Aspect | V1 Behaviour |
| :--- | :--- |
| Goroutines per topic | 1 — single goroutine routes all partitions |
| Partition assignment | Kafka consumer group auto-assigns partitions |
| Dead letter queue | Not implemented — bad messages are logged and skipped |
| Message schema versioning | Not implemented — single JSON struct per topic |
| Multi-node routing | Not implemented — single ME node handles all markets |

### Future V2 Upgrades

**1. One goroutine per partition**

V1: one goroutine reads all partitions of `orders.submitted`.
If BTC-USDT''s InputQueue fills, ALL partitions stall.

V2: assign partitions explicitly and run one goroutine per partition.
A backed-up BTC-USDT partition no longer stalls ETH-USDT.

**2. Dead letter topic**

Currently bad messages (malformed JSON, invalid UUID) are logged and skipped.
If the Order Service has a bug and sends thousands of bad messages, the logs
fill up silently.

V2: publish bad messages to a `matching-engine.dlq` topic so they can be
inspected, replayed, or alerted on without manual log scraping.

**3. Schema versioning**

The current `orderCreatedMessage` struct is coupled to the exact JSON shape
published by Order Service. If Order Service adds or renames a field, the
consumer silently ignores it.

V2: add a `schema_version` field to every Kafka message and use version-aware
deserialisation. Allows the consumer to handle multiple payload versions during
a rolling deploy.

**4. Multi-node routing**

V1 assumes one ME instance handles all markets. If `market_id` is unknown,
the message is skipped.

V2: multiple ME instances each own a subset of markets. The consumer needs
to know which markets this instance owns, and re-route unknown markets to
the correct ME instance (or to a shared routing topic).

---

## 14. What This Package Does NOT Do

- Does NOT touch the OrderBook — that is `../market/` and `../matcher/`
- Does NOT publish events to Kafka — that is `../publisher/`
- Does NOT write checkpoints to Postgres — that is `../recovery/`
- Does NOT write to Redis — that is `../projection/`
- Does NOT validate fund availability — that is Order Service
- Does NOT replay historical events for recovery — recovery uses a separate reader
