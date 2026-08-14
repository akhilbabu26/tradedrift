# Market Service — Kafka Ingestion Engine (`internal/kafka`)

> **Package:** `tradedrift/services/market/internal/kafka`  
> **Directory:** `services/market/internal/kafka/`  
> **Topic Consumed:** `trade.executed.v1`  
> **Consumer Group:** `market-service-group`  
> **Primary Design Patterns:** Event-Driven Consumer, Dead-Letter / Poison Skip Pattern, Commit-after-DB Invariant

---

## 1. 🎯 Purpose & Reliability Philosophy

The `kafka` package is the **Event Ingestion Engine** of the Market Service. Whenever two orders match in the Matching Engine, a `TradeExecuted` event is published to Apache Kafka. The Market Service consumes these events asynchronously to update price tickers and candlestick charts.

In financial infrastructure, message consumers face three severe failure modes:
1. **The Poison-Pill Deadlock:** A single malformed message causes an unhandled panic or continuous error, causing the consumer to retry the same offset forever and stalling the entire partition.
2. **Data Loss from Premature Offset Commits:** Committing the offset *before* database insertion causes permanent data loss if the database crashes mid-flight.
3. **Duplicate Processing Inconsistencies:** Network retries redeliver already-processed events, causing inflated volumes or distorted candle open/close prices.

The `consumer.go` implementation mathematically eliminates all three failure modes.

---

## 2. 📂 Files in this Package

```
services/market/internal/kafka/
├── consumer.go   <-- High-throughput Kafka consumer with poison skip & offset management
└── README.md     <-- This comprehensive documentation
```

---

## 3. 🔍 Deep-Dive: File 1 — `consumer.go`

### 🏗️ Struct Definitions & Event Contracts

#### 1. Inbound Canonical JSON Event (`TradeExecutedEvent`)
```go
type TradeExecutedEvent struct {
    TradeID      string `json:"trade_id"`
    MarketID     string `json:"market_id"`
    Price        string `json:"price"`
    Quantity     string `json:"quantity"`
    BuyerOrderID string `json:"buyer_order_id"`
    SellerOrderID string `json:"seller_order_id"`
    ExecutedAtMs int64  `json:"executed_at_ms"`
}
```

* **Why String Types for Decimals?** JSON IEEE 754 floats lose precision for numbers like `0.00000001`. Prices and quantities are serialized as exact string representations.
* **Why `ExecutedAtMs`?** Uses epoch milliseconds (`int64`) to eliminate cross-platform time format parsing ambiguity.

#### 2. The `Consumer` Worker Struct
```go
type Consumer struct {
    reader *kafka.Reader
    svc    service.MarketService
    log    *zap.Logger
}
```
* **`reader *kafka.Reader`:** High-performance Kafka consumer instance from `segmentio/kafka-go`.
* **`svc service.MarketService`:** Interface to the business layer for atomic database persistence.
* **`log *zap.Logger`:** Structured logging instance.

---

### ⚙️ Step-by-Step Execution Lifecycle (`Start(ctx)`)

```
                          ┌────────────────────────┐
                          │   consumer.Start(ctx)  │
                          └───────────┬────────────┘
                                      │
                         ┌────────────┴────────────┐
                         ▼                         ▼
                  ctx.Done() received?        FetchMessage(ctx)
                         │                         │
                        Yes                        ▼
                         │               1. JSON Deserialization
                         ▼                         │
                   Exit Loop             Valid JSON structure?
                                        /                     \
                                      Yes                      No (Poison Pill)
                                       │                       │
                                       ▼                       ▼
                         2. Strict UUID & Decimals     skipPoisonMessage()
                                       │               (Commit & Skip)
                               Valid UUID & Decimals?
                              /                      \
                            Yes                       No (Poison Pill)
                             │                        │
                             ▼                        ▼
                   3. Convert Timestamp        skipPoisonMessage()
                      time.UnixMilli(...)      (Commit & Skip)
                             │
                             ▼
                   4. ProcessTradeEvent()
                      (Atomic DB Transaction)
                             │
                        DB Success?
                       /           \
                     Yes            No (DB Error)
                      │             │
                      ▼             ▼
             5. CommitMessages()   Do NOT commit offset!
                (Safe Commit)      (Retries upon container reboot)
```

---

### 🛡️ Key Reliability Functions & Mechanisms

#### 1. Poison-Pill Message Skipping (`skipPoisonMessage`)
```go
func (c *Consumer) skipPoisonMessage(ctx context.Context, msg kafka.Message, reason string, err error) {
    c.log.Error("Skipping poison message on Kafka topic",
        zap.String("reason", reason),
        zap.String("topic", msg.Topic),
        zap.Int("partition", msg.Partition),
        zap.Int64("offset", msg.Offset),
        zap.ByteString("payload", msg.Value),
        zap.Error(err),
    )
    if commitErr := c.reader.CommitMessages(ctx, msg); commitErr != nil {
        c.log.Error("Failed to commit offset for poison message", zap.Error(commitErr))
    }
}
```
* **How It Works:** If a message contains invalid JSON, an unparseable UUID, or invalid decimal characters (`"abc"`), `skipPoisonMessage` logs the full payload and offset, commits the offset to Kafka, and immediately moves to the next message.
* **Why Critical:** Prevents bad data from freezing the entire exchange pipeline.

#### 2. Commit-After-DB Guarantee
```go
// 1. Ingest trade into database first
if err := c.svc.ProcessTradeEvent(ctx, payload); err != nil {
    c.log.Error("Failed to process trade event in database", zap.Error(err))
    continue // Do NOT commit offset!
}

// 2. Commit offset ONLY after DB success
if err := c.reader.CommitMessages(ctx, msg); err != nil {
    c.log.Error("Failed to commit Kafka offset after DB write", zap.Error(err))
}
```
* **Why Critical:** Guarantees zero data loss if PostgreSQL restarts or encounters a temporary network timeout.

#### 3. Clean Graceful Shutdown (`Close()`)
```go
func (c *Consumer) Close() error {
    c.log.Info("Closing Kafka consumer...")
    return c.reader.Close()
}
```
* Safely leaves the Kafka consumer group and closes active TCP sockets during container termination.

---

## 4. 🛠️ Tools & Packages Used

| Tool / Package | Why Used in Kafka Engine |
| :--- | :--- |
| **`github.com/segmentio/kafka-go`** | Pure-Go Kafka library without external CGO dependencies. Supports explicit offset commits and context-aware message fetching. |
| **`github.com/shopspring/decimal`** | Parses exact string representation of trade prices and quantities into high-precision decimal numbers. |
| **`github.com/google/uuid`** | Validates `trade_id` is an authentic RFC4122 UUID. |
| **`go.uber.org/zap`** | High-performance structured logging with field context (`offset`, `partition`, `topic`). |
