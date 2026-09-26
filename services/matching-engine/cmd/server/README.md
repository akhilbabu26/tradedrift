# `cmd/server` — Matching Engine Entry Point & Lifecycle Orchestrator

**Package:** `main`  
**Service:** Matching Engine  
**Binary:** `server`  
**Files Covered:** `main.go`, `config.go`, `http.go`  
**Last Updated:** August 2026  

---

## 1. What This Package Does

This is the **wiring, configuration, and bootstrap layer** of the Matching Engine. It connects all internal packages (`internal/checkpoint`, `internal/kafka`, `internal/market`, `internal/matcher`, `internal/orderbook`, `internal/publisher`, `internal/recovery`, `migration`) into an integrated, runnable service.

The server binary is responsible for:
1. Loading configuration and environment variables via `config.go` (`POSTGRES_DSN`, `KAFKA_BROKERS`, `KAFKA_GROUP_ID`, `REDIS_ADDR`, `HTTP_PORT`).
2. Validating market configurations (`TickSize > 0`, `LotSize > 0`, `SnapshotInterval`, `SnapshotDuration`).
3. Connecting to PostgreSQL connection pool (`pgxpool.Pool`) and Redis (`redis.Client`).
4. Applying database schema migrations automatically (`migration.RunMigrations`).
5. Creating the central `MarketManager` and registering all `MarketEngine` instances.
6. Instantiating the Checkpoint Coordinator (`internal/checkpoint`) to manage contiguous multi-market offset watermarks.
7. Executing the **startup recovery phase** (`recovery.Replayer.ReplayAll`) to restore order books from snapshots and replay Kafka logs up to database checkpoints.
8. Starting the internal **HTTP Server** on `:8082` (`http.go`) serving `/healthz`, `/readyz`, `/status`, and `/markets/{id}/snapshot` (used by the Liquidity Engine reconciler).
9. Spawning per-market **Publisher** goroutines (`publisher.Publisher.Run`) and the background snapshot retention worker (`publisher.StartSnapshotRetentionWorker`).
10. Starting the **Kafka Consumer** on `orders.commands` for live event ingestion.
11. Blocking until `SIGTERM` / `SIGINT` and executing an orderly **4-phase graceful shutdown** with complete queue draining.

---

## 2. Full Startup Lifecycle

```
main()
  │
  ▼
run()
  │
  ├─ loadConfig()                    — read env vars, fail fast if POSTGRES_DSN missing
  ├─ validateMarketConfigs()          — TickSize > 0, LotSize > 0 for every market
  │
  ├─ pgxpool.New()  + Ping()         — connect PostgreSQL
  ├─ migration.RunMigrations()        — apply DDL schema migrations (checkpoints, sequences, snapshots)
  ├─ redis.NewClient() + Ping()      — connect Redis
  │
  ├─ market.NewMarketManager()
  ├─ manager.Add() × 3               — BTC-USDT, ETH-USDT, SOL-USDT (ModeRecovery)
  │
  ├─ checkpoint.NewCoordinator()     — multi-market contiguous watermark coordinator
  │
  ├─ go httpServer.ListenAndServe()  — start HTTP endpoints on :8082 (initially /readyz returns 503)
  │
  ├─ recovery.NewReplayer()
  ├─ replayer.ReplayAll()            ← BLOCKS until all markets are ModeLive
  │     ├─ Restore snapshots <= checkpoint
  │     ├─ Replay Kafka orders.commands from min_offset to checkpoint
  │     ├─ Drain recovery barriers
  │     ├─ Assert sequence consistency
  │     └─ Transition engines to ModeLive
  │
  ├─ publisher.NewPublisher()
  ├─ go pub.Run(opCtx, engine) × 3   — one goroutine per market, tracked by WaitGroup
  ├─ publisher.StartSnapshotRetentionWorker() — purge snapshots older than 7 days
  │
  ├─ kafka.NewConsumer()
  ├─ consumer.Start(opCtx)           — live Kafka command ingestion begins on orders.commands
  ├─ isReady.Store(true)             — flip /readyz to 200 OK
  │
  └─ <-opCtx.Done()                  — block until SIGTERM / SIGINT
        │
        ├─ Phase 1: isReady.Store(false) + httpServer.Shutdown(10s)
        ├─ Phase 2: consumer.Close() — stop Kafka intake
        ├─ Phase 3: manager.CloseInputQueues() + engineWg.Wait() — drain event loops
        ├─ Phase 4: pubWg.Wait() (15s timeout) + teardown Redis & Postgres
        └─ process exits cleanly
```

---

## 3. Configuration & Environment Variables

| Variable | Required | Default | Description |
| :--- | :--- | :--- | :--- |
| `POSTGRES_DSN` | ✅ Yes | — | PostgreSQL connection string |
| `KAFKA_BROKERS` | No | `localhost:9092` | Comma-separated list of Kafka brokers |
| `KAFKA_GROUP_ID` | No | `matching-engine-group` | Kafka consumer group ID |
| `REDIS_ADDR` | No | `localhost:6379` | Redis host:port |
| `HTTP_PORT` | No | `8082` | HTTP health, status, and snapshot server port |
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

## 5. HTTP Endpoints & Observability (`http.go`)

The matching engine exposes an internal HTTP server (default `:8082`) for orchestration, Kubernetes probes, and inter-service reconciliation:

| Endpoint | Method | Response | Purpose |
| :--- | :---: | :--- | :--- |
| `/healthz` | `GET` | `{"status": "alive"}` (200) | Liveness probe indicating server process is running. |
| `/readyz` | `GET` | `{"status": "ready"}` (200) or `{"status": "recovering"}` (503) | Readiness probe indicating whether startup recovery is finished and live ingestion is active. |
| `/status` | `GET` | `{"ready": true, "markets": [...], "market_details": {"BTC-USDT": {"state": "LIVE", "sequence": 141, "order_count": 22}}}` | Comprehensive engine health status exposing state, monotonic sequence, and resting MM order count per market. |
| `/markets/{id}/snapshot` | `GET` | `{"market_id": "BTC-USDT", "state": "LIVE", "sequence": 141, "snapshot_at": "...", "orders": [...]}` | Authoritative point-in-time snapshot of resting MM orders used by the **Liquidity Engine Reconciler** for exact order identity matching and generation tracking. |

---

## 6. 4-Phase Graceful Shutdown

```
SIGTERM / SIGINT received
       │
[Phase 1: Mark Not Ready & Stop Ingress Probing]
       ├─ isReady.Store(false)  — immediately signal 503 to /readyz
       └─ httpServer.Shutdown(10s timeout)
       │
[Phase 2: Stop Ingestion Pipeline]
       └─ consumer.Close()      — disconnect Kafka reader; no new commands enter
       │
[Phase 3: Drain Engine Event Loops]
       ├─ manager.CloseInputQueues() — close InputQueues for all market engines
       └─ engineWg.Wait()            — each event loop drains remaining inputs, triggers final snapshot, and closes OutputQueue
       │
[Phase 4: Drain Publishers & Teardown Infrastructure]
       ├─ pubWg.Wait() (15s timeout) — publishers persist all final match results to Kafka, Redis, and Postgres
       ├─ checkpoint.Close()         — close checkpoint coordinator
       ├─ rdb.Close()                — disconnect Redis client
       └─ db.Close()                 — close PostgreSQL pool
       │
Process exits with code 0
```

---

## 7. File Breakdown

- **`main.go`**: Core application entry point, lifecycle runner, database/Kafka/Redis connection wiring, recovery orchestration, background publisher startup, and 4-phase graceful shutdown coordination.
- **`config.go`**: Environment variable loading (`Config` struct) and market configuration validation rules (`validateMarketConfigs`).
- **`http.go`**: HTTP server factory and routing handlers for `/healthz`, `/readyz`, `/status`, and `/markets/{id}/snapshot`.
