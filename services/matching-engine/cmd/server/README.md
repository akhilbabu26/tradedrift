# `cmd/server` — Matching Engine Entry Point & Lifecycle Orchestrator

**Package:** `main`  
**Service:** Matching Engine  
**Binary:** `server`  
**Last Updated:** August 2026  

---

## 1. What This Package Does

This is the **wiring, configuration, and bootstrap layer** of the Matching Engine. It connects all internal packages (`internal/checkpoint`, `internal/kafka`, `internal/market`, `internal/matcher`, `internal/orderbook`, `internal/publisher`, `internal/recovery`) into an integrated, runnable service.

`cmd/server/main.go` is responsible for:
1. Loading configuration from environment variables (`POSTGRES_DSN`, `KAFKA_BROKERS`, `KAFKA_GROUP_ID`, `REDIS_ADDR`).
2. Validating market configurations (`TickSize > 0`, `LotSize > 0`, `SnapshotInterval`, `SnapshotDuration`).
3. Connecting to PostgreSQL connection pool (`pgxpool.Pool`) and Redis (`redis.Client`).
4. Creating the central `MarketManager` and registering all `MarketEngine` instances.
5. Instantiating the Checkpoint Coordinator (`internal/checkpoint`) to manage contiguous multi-market offset watermarks.
6. Executing the **startup recovery phase** (`recovery.Replayer.ReplayAll`) to restore order books from snapshots and replay Kafka logs up to database checkpoints.
7. Spawning per-market **Publisher** goroutines (`publisher.Publisher.Run`).
8. Starting the **Kafka Consumer** on `orders.commands` for live event ingestion.
9. Blocking until `SIGTERM` / `SIGINT` and executing a **graceful shutdown** with queue draining.

---

## 2. Full Startup Lifecycle

```
main()
  │
  ▼
run()
  │
  ├─ loadConfig()                   — read env vars, fail fast if POSTGRES_DSN missing
  ├─ validateMarketConfigs()         — TickSize > 0, LotSize > 0 for every market
  │
  ├─ pgxpool.New()  + Ping()        — connect PostgreSQL
  ├─ redis.NewClient() + Ping()     — connect Redis
  │
  ├─ market.NewMarketManager()
  ├─ manager.Add() × 3              — BTC-USDT, ETH-USDT, SOL-USDT (ModeRecovery)
  │
  ├─ checkpoint.NewCoordinator()    — multi-market contiguous watermark coordinator
  │
  ├─ recovery.NewReplayer()
  ├─ replayer.ReplayAll()           ← BLOCKS until all markets are ModeLive
  │     ├─ Restore snapshots <= checkpoint
  │     ├─ Replay Kafka orders.commands from min_offset to checkpoint
  │     ├─ Drain recovery barriers
  │     ├─ Assert sequence consistency
  │     └─ Transition engines to ModeLive
  │
  ├─ publisher.NewPublisher()
  ├─ go pub.Run(opCtx, engine) × 3  — one goroutine per market, tracked by WaitGroup
  │
  ├─ kafka.NewConsumer()
  ├─ consumer.Start(opCtx)          — live Kafka command ingestion begins on orders.commands
  │
  └─ <-opCtx.Done()                 — block until SIGTERM / SIGINT
        │
        ├─ consumer.Close()         — stop Kafka command reader
        ├─ wg.Wait() / 15s timeout  — drain in-flight publisher results
        └─ defer db/rdb.Close()     — tear down infrastructure connections
```

---

## 3. Configuration & Environment Variables

| Variable | Required | Default | Description |
| :--- | :--- | :--- | :--- |
| `POSTGRES_DSN` | ✅ Yes | — | PostgreSQL connection string |
| `KAFKA_BROKERS` | No | `localhost:9092` | Comma-separated list of Kafka brokers |
| `KAFKA_GROUP_ID` | No | `matching-engine-group` | Kafka consumer group ID |
| `REDIS_ADDR` | No | `localhost:6379` | Redis host:port |
| `BTC_PARTITION` | No | `0` | Kafka partition assigned to BTC-USDT |
| `ETH_PARTITION` | No | `1` | Kafka partition assigned to ETH-USDT |
| `SOL_PARTITION` | No | `2` | Kafka partition assigned to SOL-USDT |

---

## 4. Market Configurations (Dedicated Per-Market Partitions)

To eliminate Head-of-Line ingestion blocking and enable horizontal multi-node scaling, each market is mapped to its own dedicated Kafka partition:

| Market | Default Partition | Tick Size | Lot Size | Snapshot Triggers |
| :--- | :---: | :--- | :--- | :--- |
| **`BTC-USDT`** | `0` | `0.01` | `0.00001` | Every 10,000 orders / 60 seconds |
| **`ETH-USDT`** | `1` | `0.01` | `0.0001` | Every 10,000 orders / 60 seconds |
| **`SOL-USDT`** | `2` | `0.001` | `0.01` | Every 10,000 orders / 60 seconds |

```go
{
    MarketID:         "BTC-USDT",
    TickSize:         decimal.RequireFromString("0.01"),
    LotSize:          decimal.RequireFromString("0.00001"),
    Partition:        getEnvInt("BTC_PARTITION", 0),
    SnapshotInterval: 10000,
    SnapshotDuration: 60 * time.Second,
}
```

---

## 5. Graceful Shutdown

```
SIGTERM / SIGINT
       │
opCtx cancelled         — live goroutines see ctx.Done(), stop accepting new work
       │
consumer.Close()        — Kafka reader closed; no new events enter InputQueues
       │
wg.Wait() with timeout  — publisher goroutines drain pending OutputQueue results
       │
time.After(15s)         — safety deadline
       │
deferred rdb.Close()    — Redis disconnected
deferred db.Close()     — PostgreSQL pool closed
       │
process exits cleanly
```
