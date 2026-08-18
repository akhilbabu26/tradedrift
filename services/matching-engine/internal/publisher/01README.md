# `internal/publisher` — Result Publishing and Checkpointing

**Package:** `publisher`  
**Service:** Matching Engine  
**Last Updated:** August 2026  

---

## 1. What This Package Does

This package is the **egress, projection, and durability coordinator** of the Matching Engine. It receives the `orderbook.MatchResult` outputs produced by each `MarketEngine`'s Event Loop and coordinates downstream writes across three independent infrastructure layers:

1. **Kafka (`trades.executed`)** — Publishes execution events for every matched trade (`Fill`).
2. **Redis (`depth:{market_id}`)** — Pushes real-time Top-N depth snapshots for market data feeds and UI displays.
3. **PostgreSQL (`kafka_checkpoints`)** — Atomically advances the durability checkpoint (`topic`, `partition`, `offset`, `updated_at`).

It operates downstream of the core matching loop, ensuring that execution outputs and durability markers are handled safely and sequentially.

---

## 2. Purpose

The `publisher` package answers three critical operational questions:

| Question | Mechanism |
| :--- | :--- |
| How do other services know a trade happened? | Publishes `trades.executed` to Kafka with `RequiredAcks: RequireAll` |
| How do web clients and trading UI see the latest book depth? | Pushes JSON snapshots to Redis key `depth:{market_id}` |
| How does the engine know where to resume on crash/restart? | UPSERTs the latest Kafka position to Postgres `kafka_checkpoints` |

---

## 3. Files In This Package

| File | Purpose |
| :--- | :--- |
| `publisher.go` | `Publisher` struct, dependency interfaces, JSON models, `Run()` loop, `process()` pipeline, and `writeCheckpoint()` |
| `publisher_test.go` | 11 unit tests covering single/multiple fills, payload verification, Redis writes, checkpoint advancement, failure scenarios, and sequential ordering |
| `README.md` | This file |

---

## 4. Architecture & Goroutine Model

Each `MarketEngine` runs independently. The `Publisher` processes output queues on a **per-market basis**, giving full isolation between markets:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                            Matching Engine                                  │
│                                                                             │
│  BTC-USDT MarketEngine ──► OutputQueue ──► Publisher.Run() (Goroutine 1)    │
│                                                │                            │
│  ETH-USDT MarketEngine ──► OutputQueue ──► Publisher.Run() (Goroutine 2)    │
│                                                │                            │
│  SOL-USDT MarketEngine ──► OutputQueue ──► Publisher.Run() (Goroutine 3)    │
│                                                │                            │
│                                                ▼                            │
│                       ┌─────────────────────────────────┐                   │
│                       │        process(MatchResult)     │                   │
│                       └────────────────┬────────────────┘                   │
│                                        │                                    │
│                 ┌──────────────────────┼──────────────────────┐             │
│                 ▼                      ▼                      ▼             │
│          1. Kafka Writer         2. Redis Writer        3. DB Writer        │
│          (trades.executed)      (depth:BTC-USDT)     (kafka_checkpoints)    │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Key Concurrency Rules:
- **One Goroutine Per Market Engine:** `publisher.Run(ctx, engine)` is spawned once per market. A slow write on BTC-USDT never blocks ETH-USDT or SOL-USDT.
- **Strictly Sequential Per Market:** Within a single market, results are pulled from `OutputQueue` and processed **one at a time**. Concurrent processing within the same market is forbidden to prevent checkpoints from advancing out of order.

---

## 5. Structs & Dependency Interfaces

To ensure high unit testability without running live Kafka/Redis/Postgres instances, `Publisher` depends on small, focused interfaces rather than concrete third-party structs:

```go
type kafkaWriter interface {
    WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
}

type redisWriter interface {
    Set(ctx context.Context, key string, value []byte, expiration time.Duration) error
}

type dbWriter interface {
    Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}
```

### `Publisher` Struct

```go
type Publisher struct {
    writer kafkaWriter
    redis  redisWriter
    db     dbWriter
}
```

- `NewPublisher(brokers, rdb, db)` binds production clients (`kafkago.Writer`, `redisClientAdapter`, `pgxpool.Pool`).
- `NewTestable(w, r, db)` allows unit tests to inject fakes.

---

## 6. The 3-Step Processing Pipeline

Every `orderbook.MatchResult` is processed via `Publisher.process()` in a strict, non-reorderable sequence:

```
MatchResult
    │
    ▼
[Step 1: Publish Fills to Kafka]
    ├── Iterate result.Fills
    ├── Marshal tradeExecutedMessage (including fill.MarketID)
    ├── Write to "trades.executed" with partition key = BuyOrderID
    └── Error? ──► Return immediately. (Checkpoint NOT written)
    │
    ▼
[Step 2: Push Depth to Redis]
    ├── Marshal result.DepthSnapshot
    ├── SET "depth:{market_id}" (TTL = 0)
    └── Error? ──► Return immediately. (Checkpoint NOT written)
    │
    ▼
[Step 3: Advance Postgres Checkpoint]
    ├── UPSERT kafka_checkpoints (topic, partition, offset, updated_at = NOW())
    └── Error? ──► Return error.
```

---

## 7. Downstream Contracts & Payload Schemas

### 1. Kafka: `trades.executed` Topic

Published when `len(result.Fills) > 0`. Each trade fill produces one Kafka event:

```json
{
  "trade_id": "019163f5-93b6-710b-b187-2c93b6710bb1",
  "market_id": "BTC-USDT",
  "maker_order_id": "019163f5-93b6-710b-b187-2c93b6710bb2",
  "taker_order_id": "019163f5-93b6-710b-b187-2c93b6710bb3",
  "buy_order_id": "019163f5-93b6-710b-b187-2c93b6710bb2",
  "sell_order_id": "019163f5-93b6-710b-b187-2c93b6710bb3",
  "buyer_user_id": "019163f5-93b6-710b-b187-2c93b6710bb4",
  "seller_user_id": "019163f5-93b6-710b-b187-2c93b6710bb5",
  "price": "50000.00",
  "quantity": "0.5000",
  "executed_at": "2026-08-18T10:30:00.123456789Z"
}
```

- **Partition Key:** `BuyOrderID` — ensures all fills related to a given buy order land on the same Kafka partition and remain ordered.
- **Consumers:**
  - **Order Service:** Updates order remaining quantity and status (`PARTIALLY_FILLED` / `FILLED`).
  - **Wallet / Settlement Service:** Unlocks reserved balances, credits buyer, credits seller, and deducts trading fees.

### 2. Redis: `depth:{market_id}` Key

Updated on **every single event** (trades, cancels, restings):

```json
{
  "market_id": "BTC-USDT",
  "bids": [
    { "price": "49990.00", "quantity": "1.2500" },
    { "price": "49980.00", "quantity": "3.5000" }
  ],
  "asks": [
    { "price": "50010.00", "quantity": "0.8000" },
    { "price": "50020.00", "quantity": "2.1000" }
  ],
  "snapshot_at": "2026-08-18T10:30:00.123456789Z"
}
```

- **TTL:** None (`0`). The latest snapshot continually overwrites the previous snapshot.

### 3. PostgreSQL: `kafka_checkpoints` Table

Stores the highest continuously committed offset per Kafka `(topic, partition)` pair:

```sql
CREATE TABLE IF NOT EXISTS kafka_checkpoints (
    topic      VARCHAR(255) NOT NULL,
    partition  INTEGER      NOT NULL,
    offset     BIGINT       NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (topic, partition)
);
```

```sql
INSERT INTO kafka_checkpoints (topic, partition, offset, updated_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (topic, partition)
DO UPDATE SET
    offset     = EXCLUDED.offset,
    updated_at = NOW();
```

---

## 8. Failure Modes & Durability Guarantees

| Failure Point | What Happens | On Engine Restart |
| :--- | :--- | :--- |
| **Kafka publish fails** | Error returned; Redis push & Checkpoint are skipped | Replays from previous checkpoint offset in `ModeRecovery`. Re-attempts match. |
| **Redis push fails** | Error returned; Checkpoint is skipped | Replays from previous checkpoint offset. Order book rebuilt. |
| **Postgres checkpoint fails** | Error returned; offset is not saved | Replays from previous checkpoint offset. |
| **No fills generated (e.g. Cancel)** | Kafka publish is skipped; Redis & Checkpoint proceed normally | Checkpoint advances smoothly. |

### Why Duplicate Trades Are Prevented:
During recovery replay, the `MarketEngine` operates in `ModeRecovery`. In `ModeRecovery`, `matcher.Match()` runs the full matching algorithm to reconstruct the in-memory order book state, but **suppresses output (returns `nil` fills)**. Therefore, previously executed trades are never republished to Kafka twice.

---

## 9. Unit Test Coverage

The package has 11 automated unit tests in `publisher_test.go`:

| Test Name | Verification Goal |
| :--- | :--- |
| `TestProcess_OneFill_OneKafkaMessage` | Single trade fill produces exactly 1 Kafka message |
| `TestProcess_MultipleFills_MultipleKafkaMessages` | Multi-level sweeps produce N Kafka messages |
| `TestProcess_MarketID_IncludedInPayload` | Verifies `market_id` is correctly included in JSON payload |
| `TestProcess_BuyOrderID_UsedAsPartitionKey` | Verifies partition key matches `BuyOrderID` |
| `TestProcess_DepthSnapshot_WrittenToRedis` | Verifies Top-N depth snapshot is serialized and written to Redis |
| `TestProcess_CheckpointWritten_AfterSuccess` | Verifies Postgres checkpoint UPSERT on complete success |
| `TestProcess_KafkaFailure_CheckpointNotWritten` | Kafka failure stops pipeline before checkpoint write |
| `TestProcess_RedisFailure_CheckpointNotWritten` | Redis failure stops pipeline before checkpoint write |
| `TestProcess_CheckpointFailure_ReturnsError` | DB error returns cleanly to caller |
| `TestProcess_NoFills_SkipsKafka_StillWritesDepthAndCheckpoint` | Cancel / resting order skips Kafka but writes Redis & checkpoint |
| `TestProcess_Sequential_CheckpointAlwaysAdvances` | Verifies monotonic checkpoint advancement across 5 consecutive events |

---

## 10. V1 Limitations and V2 Upgrade Path

### Current V1 Implementation:
- **Synchronous Kafka Writes:** Writes with `RequiredAcks: RequireAll` synchronously for absolute safety.
- **Single Redis Key:** Uses standard `SET depth:{market_id}` string key.
- **Single DB Connection Pool:** Uses pgx pool for checkpoint UPSERTs.

### Future V2 Upgrades:
1. **Batch Checkpointing:**
   - Instead of UPSERTing to Postgres on every single `MatchResult`, buffer checkpoints and flush every $N$ events or $T$ milliseconds (e.g. every 100ms or 50 events) to reduce DB I/O under extreme load.
2. **Redis Streams / PubSub for Depth Diff:**
   - In addition to overwriting full snapshots, publish depth diff deltas to a Redis Stream / PubSub channel for low-bandwidth WebSocket client consumption.
3. **Partition-Specific Checkpoints Table Partitioning:**
   - Partition the `kafka_checkpoints` table across distributed databases if scaling to hundreds of markets across separate matching clusters.

---

## 11. What This Package Does NOT Do

- Does NOT run order matching or modify the `OrderBook` — handled by `../matcher/`
- Does NOT ingest or parse input messages from Kafka — handled by `../kafka/`
- Does NOT replay historical Kafka events on engine startup — handled by `../recovery/`
- Does NOT own goroutines or market routing logic — handled by `../market/`
- Does NOT settle account balances or calculate fees — handled by Wallet Service
