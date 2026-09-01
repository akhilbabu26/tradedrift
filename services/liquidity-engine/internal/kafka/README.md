# package `kafka`

## Purpose

Provides the **Kafka producer** (writes order commands to `orders.commands`) and the **Kafka consumer** (reads trade events from `trades.executed`). These are the LE's only two Kafka I/O surfaces.

## Problem It Solves

The LE must send order commands to the Matching Engine and react to trade fills — both asynchronously via Kafka. Two specific problems require careful design:

1. **Command routing correctness**: The ME's command consumer partitions its event loop by `(market_id, partition)`. If the LE sends a BTC command to partition 1 instead of partition 0, the ME will ignore it or misroute it during recovery.
2. **At-least-once trade processing**: If the LE commits a Kafka offset before finishing all state mutations (inventory update, tracker update, targeted reconcile), a crash would cause those mutations to be lost — resulting in incorrect inventory accounting.

## How It Solves It

- **Producer**: Each market has its own `kafka.Writer` pre-configured with the correct partition. Commands are always serialised with `msg.Key = marketID` (required by ME). Uses `RequireAll` acknowledgements to prevent silent write failures.
- **Consumer**: Uses a `TradeEnvelope` with an `Ack chan struct{}`. The consumer **blocks** after sending the envelope to the event loop, waiting for `Ack` to be closed. The engine closes `Ack` only after completing all state mutations. Only then does the consumer commit the Kafka offset.

---

## Flow: OrderCreated Command

```
reconciler.applyCreate()
         │
         ▼
  producer.PublishCreate(ctx, marketID, partition, orderID, clientOrderID, side, price, qty)
         │
         ├── marshal orderCreatedPayload
         │     {order_id, user_id=MM-UUID, side, order_type="LIMIT", price, qty, client_order_id}
         │
         ├── wrap in CommandEnvelope
         │     {event_id=newUUID, event_type="OrderCreated", event_version=1, market_id, ...}
         │
         └── kafka.Writer.WriteMessages()
               msg.Key = []byte(marketID)   INVARIANT: must equal envelope.market_id
               msg.Partition = partition     market-specific from config
               msg.Value = JSON(envelope)
```

---

## Flow: Trade Fill Processing (at-least-once guarantee)

```
ME publishes to trades.executed
         │
         ▼
  consumer.Run() [goroutine]
         │
         ├── FetchMessage() → raw Kafka message
         ├── parseTradeMessage() → TradeEvent
         │     ├── deserialise JSON
         │     ├── parse price/quantity as decimal
         │     └── detect MM involvement:
         │           buyer_user_id == MM-UUID? → MMSide="BUY"
         │           seller_user_id == MM-UUID? → MMSide="SELL"
         │
         ├── send TradeEnvelope{Event, Ack} to e.tradeEvents channel
         │
         ├── BLOCK waiting for Ack
         │         │
         │   engine.handleTrade() closes Ack after all state mutations complete
         │
         └── CommitMessages() ← only after Ack, guarantees at-least-once
```

---

## Files

### [`producer.go`](./producer.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `TopicOrderCommands` | `const` | `"orders.commands"` — ME command ingress topic. |
| `TopicTradesExecuted` | `const` | `"trades.executed"` — ME trade event topic. |
| `CommandEnvelope` | `struct` | Standard wrapper: `event_id`, `event_type`, `event_version=1`, `market_id`, `occurred_at`, `payload`. |
| `Producer` | `struct` | Map of `marketID → kafka.Writer`, one per market, each pre-configured for its partition. |
| `NewProducer(brokers, markets, logger)` | `func` | Creates one `kafka.Writer` per market. `AllowAutoTopicCreation=false` — fails fast if topic missing. |
| `PublishCreate(...)` | `func` | Builds and sends `OrderCreated`. Embeds `account.WalletUUIDStr` as `user_id` — ME parses it as a UUID. |
| `PublishCancel(...)` | `func` | Builds and sends `OrderCancelRequested`. Uses the ME-assigned order UUID (not `client_order_id`). |
| `publish(ctx, marketID, partition, env)` | `func` (internal) | Serialises envelope to JSON, sets `msg.Key = marketID`, sets explicit partition, writes to Kafka. |
| `Close()` | `func` | Flushes and closes all writers. |

---

### [`consumer.go`](./consumer.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `TradeEvent` | `struct` | LE's internal trade representation with the derived `MMSide` field (`"BUY"`, `"SELL"`, or `""`). |
| `TradeEnvelope` | `struct` | Wraps `TradeEvent` with `Ack chan struct{}`. Consumer blocks until engine closes `Ack` before committing. |
| `Consumer` | `struct` | `kafka.Reader` configured for `CommitInterval=0` (strict manual commit). |
| `NewConsumer(brokers, groupID, events, logger)` | `func` | Creates a manual-commit reader for `trades.executed`. |
| `Run(ctx)` | `func` | Main loop: fetch → parse → send envelope → block on Ack → commit. Retries on fetch error with 500ms backoff. |
| `Close()` | `func` | Closes the Kafka reader. |
| `parseTradeMessage(raw)` | `func` (internal) | Deserialises JSON and computes `MMSide` by matching buyer/seller UUID against `account.WalletUUIDStr`. |
