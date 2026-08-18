# `cmd/server` — Matching Engine Entry Point

**Package:** `main`  
**Service:** Matching Engine  
**Binary:** `server`  
**Last Updated:** August 2026  

---

## 1. What This Package Does

This is the **wiring layer** of the Matching Engine. It connects all internal packages into a working, runnable service. No matching logic lives here — all business logic stays in `internal/`.

`cmd/server/main.go` is responsible for:

1. Loading configuration from environment variables
2. Connecting to PostgreSQL and Redis
3. Creating and registering all `MarketEngine` instances
4. Running the **recovery phase** to rebuild order books from Kafka history
5. Starting **Publisher** goroutines (one per market)
6. Starting the **Kafka Consumer** for live event ingestion
7. Blocking until `SIGTERM` or `SIGINT`
8. Executing a **graceful shutdown** with a 15-second drain timeout

---

## 2. Files In This Package

| File | Purpose |
| :--- | :--- |
| `main.go` | Entry point, config, market definitions, `run()` lifecycle |
| `README.md` | This file |

---

## 3. Full Startup Lifecycle

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
  ├─ recovery.NewReplayer()
  ├─ replayer.ReplayAll()           ← BLOCKS until all markets are ModeLive
  │     ├─ BTC: checkpoint → HWM → replay → drain → Redis depth → SetLive()
  │     ├─ ETH: checkpoint → HWM → replay → drain → Redis depth → SetLive()
  │     └─ SOL: checkpoint → HWM → replay → drain → Redis depth → SetLive()
  │
  ├─ publisher.NewPublisher()
  ├─ go pub.Run(opCtx, engine) × 3  — one goroutine per market, tracked by WaitGroup
  │
  ├─ kafka.NewConsumer()
  ├─ consumer.Start(opCtx)          — live Kafka ingestion begins
  │
  └─ <-opCtx.Done()                 — block until SIGTERM / SIGINT
        │
        ├─ consumer.Close()         — stop Kafka readers
        ├─ wg.Wait() / 15s timeout  — drain in-flight publisher work
        └─ defer db/rdb.Close()     — tear down infrastructure last
```

---

## 4. Configuration

All configuration is loaded from environment variables. The binary fails fast at startup if required variables are missing.

| Variable | Required | Default | Description |
| :--- | :--- | :--- | :--- |
| `POSTGRES_DSN` | ✅ Yes | — | Full Postgres connection string |
| `KAFKA_BROKERS` | No | `localhost:9092` | Comma-separated list of Kafka brokers |
| `KAFKA_GROUP_ID` | No | `matching-engine` | Kafka consumer group ID |
| `REDIS_ADDR` | No | `localhost:6379` | Redis host:port |

Example `.env`:

```env
POSTGRES_DSN=postgres://tradedrift:secret@localhost:5432/tradedrift_matching
KAFKA_BROKERS=kafka1:9092,kafka2:9092
KAFKA_GROUP_ID=matching-engine-prod
REDIS_ADDR=redis-prod:6379
```

---

## 5. Market Configuration (V1 Hardcoded)

Markets and their trading rules are currently hardcoded in `marketConfigs()`:

```go
{MarketID: "BTC-USDT", TickSize: 0.01,   LotSize: 0.00001}
{MarketID: "ETH-USDT", TickSize: 0.01,   LotSize: 0.0001 }
{MarketID: "SOL-USDT", TickSize: 0.001,  LotSize: 0.01   }
```

| Field | Meaning | Effect |
| :--- | :--- | :--- |
| `TickSize` | Minimum price increment | Orders with prices not aligned to TickSize are rejected |
| `LotSize` | Minimum quantity increment | Orders with quantities not aligned to LotSize are rejected |

**V2 migration path:** Replace `marketConfigs()` with a call to the Market Service API at startup. The API returns the same `MarketConfig` structure — no other changes required.

---

## 6. Startup Validation

Before connecting to any infrastructure, `validateMarketConfigs()` checks:

- `TickSize > 0` — prevents all orders from bypassing price validation
- `LotSize > 0` — prevents all orders from bypassing quantity validation

A zero TickSize or LotSize silently disables the corresponding check in `event_loop.go`'s `validTickAndLot()`. This would allow any price or quantity through the matcher — a misconfiguration that would be hard to detect in production.

---

## 7. Goroutine Ownership

After startup, the process runs the following goroutines:

| Goroutine | Starts From | Exits When |
| :--- | :--- | :--- |
| `engine.Run(opCtx)` × 3 | `recovery.ReplayAll()` (persists into live) | `opCtx` cancelled |
| `pub.Run(opCtx, engine)` × 3 | `run()` after recovery | `opCtx` cancelled |
| `consumer` created reader × 2 | `consumer.Start(opCtx)` | `opCtx` cancelled → `consumer.Close()` |

Total: **8 goroutines** in steady state (3 engines + 3 publishers + 2 consumer readers).

---

## 8. Shutdown Sequence

Shutdown is triggered by `SIGTERM` or `SIGINT`. The process uses the **two-context pattern**:

```
SIGTERM / SIGINT
       │
opCtx cancelled         — live goroutines see ctx.Done(), stop accepting new work
       │
consumer.Close()        — Kafka readers closed; no new events enter InputQueues
       │
wg.Wait() with timeout  — publisher goroutines drain their current result
       │
time.After(15s)         — hard deadline if drain stalls
       │
deferred rdb.Close()    — Redis disconnected
deferred db.Close()     — Postgres pool closed
       │
process exits
```

### Why two contexts?

After `opCtx` is cancelled, using it for teardown would result in all infrastructure operations (Redis, Postgres) returning `context.Canceled` immediately. The WaitGroup + `time.After` drain uses implicit fresh context in each goroutine's exit path.

### Why checkpoint is safe across restarts

If a Publisher goroutine exits mid-processing (e.g. with a pending `MatchResult` in `OutputQueue`), the Postgres checkpoint is not advanced. On restart, `recovery.ReplayAll()` replays from the last written checkpoint — the unfinished event is re-processed. This gives **at-least-once delivery** of `trades.executed` events to Kafka.

> Downstream services (Trade Service, Order Service) must be **idempotent on `TradeID`** to handle duplicate delivery correctly.

---

## 9. Architecture Invariants Enforced Here

### Recovery-before-Consumer invariant

```go
replayer.ReplayAll(opCtx)   // ← blocks until ALL markets are ModeLive
// ...
consumer.Start(opCtx)       // ← only called after all engines are live
```

If this order were reversed, live Kafka events could be routed into an engine still replaying historical data, producing a corrupted order book.

### Publisher-after-Recovery invariant

```go
replayer.ReplayAll(opCtx)         // ← drains OutputQueue internally during recovery
// ...
for _, engine := range manager.All() {
    go pub.Run(opCtx, engine)      // ← only started after recovery drains its results
}
```

If publishers started during recovery, they would consume `MatchResults` from the recovery drain and write spurious checkpoints and Kafka events.

---

## 10. Building and Running

**Build the binary:**

```powershell
go build -o server.exe ./cmd/server/...
```

**Run locally (requires Kafka, Redis, Postgres running):**

```powershell
$env:POSTGRES_DSN = "postgres://tradedrift:secret@localhost:5432/tradedrift_matching"
go run ./cmd/server/...
```

**Run all tests (excluding cmd — no tests here by design):**

```powershell
go test ./internal/...
```

**Expected startup log (clean start, no history):**

```
[server] starting TradeDrift Matching Engine
[server] postgres connected
[server] redis connected
[server] registered market: BTC-USDT
[server] registered market: ETH-USDT
[server] registered market: SOL-USDT
[server] starting recovery phase...
[recovery] topic=orders.submitted partition=0 — at HWM (checkpoint=-1 hwm=0), nothing to replay
[recovery] topic=orders.cancel-requested partition=0 — at HWM (checkpoint=-1 hwm=0), nothing to replay
[recovery] market=BTC-USDT recovered 0 events — now LIVE
... (ETH, SOL same)
[server] recovery complete — 3 markets in LIVE mode
[server] publisher started for market: BTC-USDT
[server] publisher started for market: ETH-USDT
[server] publisher started for market: SOL-USDT
[server] kafka consumer started
[server] ✓ all systems live — matching engine ready
```

---

## 11. What This Package Does NOT Do

- Does NOT contain any matching logic — that is `../internal/matcher/`
- Does NOT manage order books — that is `../internal/orderbook/`
- Does NOT have unit tests — integration tests belong in a dedicated `test/integration/` package
- Does NOT fetch market configs from Market Service — hardcoded for V1
- Does NOT implement health check endpoints — add an HTTP server in V2

---

## 12. V2 Upgrade Path

| V1 (current) | V2 upgrade |
| :--- | :--- |
| Markets hardcoded in `marketConfigs()` | Fetch from Market Service REST API at startup |
| No health check endpoint | Add `GET /health` returning `{status: "live", markets: [...]}` |
| 15-second drain timeout hardcoded | Make configurable via `SHUTDOWN_TIMEOUT_SECONDS` env var |
| Sequential market recovery | Parallel recovery with `errgroup` when startup time matters |
| No metrics | Expose Prometheus metrics (queue depth, match latency, checkpoint lag) |


# Shutdown sequence now:

SIGTERM
   │
opCtx cancelled   ← goroutines stop accepting NEW work
   │
consumer.Close()  ← Kafka readers closed, no new events enter InputQueues
   │
wg.Wait()         ← publishers drain their current OutputQueue item
   │              ← 15s timeout safety net
   │
defer rdb.Close() ← Redis torn down
defer db.Close()  ← Postgres torn down last

# Lifecycle in main.go

main()
  │
  ├─ Connect Postgres + Redis (ping both)
  │
  ├─ NewMarketManager() + Register 3 engines (ModeRecovery)
  │
  ├─ replayer.ReplayAll(ctx)          ← BLOCKS until all markets are live
  │     ├─ BTC: loadCheckpoint → HWM → replay → drain → Redis → SetLive
  │     ├─ ETH: loadCheckpoint → HWM → replay → drain → Redis → SetLive
  │     └─ SOL: loadCheckpoint → HWM → replay → drain → Redis → SetLive
  │
  ├─ go pub.Run(ctx, btcEngine)       ← one goroutine per market
  ├─ go pub.Run(ctx, ethEngine)
  ├─ go pub.Run(ctx, solEngine)
  │
  ├─ consumer.Start(ctx)              ← live Kafka ingestion begins
  │     ├─ goroutine: orders.submitted reader
  │     └─ goroutine: orders.cancel-requested reader
  │
  └─ <-ctx.Done()                     ← blocks until SIGTERM/SIGINT
        └─ graceful drain (15s max)
