# `cmd/server` — Liquidity Engine Entry Point & Lifecycle Orchestrator

**Package:** `main`  
**Service:** Liquidity Engine  
**Binary:** `liquidity-engine`  
**Last Updated:** August 2026

---

## 1. What This Package Does

This is the **service entry point and dependency wiring layer** for the Liquidity Engine (LE). It initializes logging, loads and validates configuration, establishes gRPC/Kafka connections, instantiates all internal components (`internal/order`, `internal/inventory`, `internal/reconciler`, `internal/kafka`, `internal/metrics`, `internal/engine`), sets up the health and metrics HTTP servers, and starts the core single-goroutine engine event loop.

`cmd/server/main.go` is responsible for:
1. **Config & Logging Setup**: Initializing structured Uber Zap logger, loading environment configurations, and validating Kafka partition mappings across markets.
2. **Order Service gRPC Client**: Connecting to the Order Service read-only gRPC endpoint for startup discovery and order state tracking.
3. **In-Memory Tracking & Inventory**: Instantiating the in-memory order `Tracker` and the `inventory.Manager` for balance calculations.
4. **Kafka Subsystems**: Instantiating partition-pinned Kafka `Producer` writers (publishing to `orders.commands`) and the Kafka `Consumer` (reading from `trades.executed`).
5. **Reconciler & Engine Wiring**: Initializing `reconciler.Reconciler` and `engine.Engine` with shared Prometheus metrics.
6. **Dual HTTP Servers**:
   - **Health Server (`:8080`)**: Exposing `/healthz` (liveness) and `/readyz` (readiness).
   - **Metrics Server (`:9090`)**: Exposing `/metrics` for Prometheus scraping.
7. **Signal Handling & Graceful Shutdown**: Trapping `SIGINT` / `SIGTERM` to stop the engine and cleanly shutdown HTTP servers within a 10s deadline.

---

## 2. Singleton Constraint

> [!IMPORTANT]
> **SINGLETON REQUIREMENT:** The Liquidity Engine MUST run with exactly `1` replica in Kubernetes (`replicas: 1`).
> 
> Because market making requires strict inventory control, level slot generation consistency, and deterministic quoting, horizontal scaling is intentionally not supported. Running multiple LE instances would result in race conditions, duplicate quote placements, and over-committing wallet balances.

---

## 3. Full Startup Lifecycle

```
main()
  │
  ├─ zap.NewProduction()            — initialize structured JSON logger
  ├─ config.Load()                  — read environment variables into Config struct
  ├─ cfg.ValidatePartitions()       — assert unique partitions per market
  │
  ├─ metrics.New()                  — register all Prometheus gauges, histograms, counters
  │
  ├─ orderservice.NewClient()       — connect gRPC client to Order Service (read-only)
  │
  ├─ order.NewTracker()             — instantiate in-memory MM order state cache
  ├─ inventory.NewManager()         — instantiate balance and effective available calculator
  │
  ├─ kafka.NewProducer()            — initialize partition-assigned Kafka writers (orders.commands)
  ├─ kafka.NewConsumer()            — initialize Kafka reader on trades.executed
  │
  ├─ reconciler.NewReconciler()     — wire tracker, producer, orderSvc, config, metrics
  ├─ engine.NewEngine()             — assemble orchestrator with all subsystems
  │
  ├─ signal.NotifyContext()         — trap SIGINT, SIGTERM for graceful exit
  │
  ├─ Start Health Server (:8080)    — /healthz, /readyz endpoints (background goroutine)
  ├─ Start Metrics Server (:9090)   — /metrics Prometheus endpoint (background goroutine)
  │
  ├─ eng.Run(ctx)                   ← BLOCKS in main event loop until context cancelled
  │     ├─ Set state: STARTING
  │     ├─ Spawn kafka.Consumer.Run()
  │     ├─ Set state: SYNCING
  │     ├─ syncAllMarkets() from Order Service
  │     ├─ Start tickers (reconcile, wallet, pending, cancelling, resync)
  │     ├─ Set state: RUNNING
  │     └─ Process events on loopEvent channel
  │
  └─ Graceful Shutdown
        ├─ eng.Run returns on ctx.Done()
        ├─ healthServer.Shutdown(10s timeout)
        ├─ metricsServer.Shutdown(10s timeout)
        ├─ orderSvc.Close()
        ├─ producer.Close()
        └─ zap.Logger.Sync()
```

---

## 4. Subsystem Dependency Wiring

```
                         ┌────────────────────────────────────┐
                         │         cmd/server/main.go         │
                         └─────────────────┬──────────────────┘
                                           │
          ┌────────────────────────────────┼────────────────────────────────┐
          ▼                                ▼                                ▼
  ┌──────────────┐                 ┌──────────────┐                 ┌──────────────┐
  │   metrics    │                 │   orderSvc   │                 │   order.     │
  │  (Prometheus)│                 │ (Order gRPC) │                 │   Tracker    │
  └───────┬──────┘                 └───────┬──────┘                 └───────┬──────┘
          │                                │                                │
          │                                │              ┌─────────────────┴────────────────┐
          │                                │              ▼                                  ▼
          │                                │      ┌──────────────┐                   ┌──────────────┐
          │                                │      │  inventory.  │                   │  reconciler. │
          │                                │      │   Manager    │                   │  Reconciler  │
          │                                │      └───────┬──────┘                   └───────┬──────┘
          │                                │              │                                  │
          │                                └──────────────┼──────────────────────────────────┤
          │                                               │                                  │
          ▼                                               ▼                                  ▼
┌────────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                           engine.Engine                                            │
│   (Orchestrates Event Loop, State Machine, Skew Calculation, Reconcile Ticks, and Kafka Streams)   │
└─────────────────────────────────┬──────────────────────────────────┬───────────────────────────────┘
                                  │                                  │
                                  ▼                                  ▼
                           ┌──────────────┐                   ┌──────────────┐
                           │    kafka.    │                   │    kafka.    │
                           │   Producer   │                   │   Consumer   │
                           │(orders.cmd)  │                   │(trades.exec) │
                           └──────────────┘                   └──────────────┘
```

---

## 5. Dual HTTP Endpoints

The LE binary runs two distinct HTTP listeners:

### Health Server (Default `:8080`)
- **`GET /healthz`**: Liveness probe. Returns `HTTP 200 ("ok")` as long as engine state is not `STOPPED`. Returns `HTTP 503 ("stopped")` otherwise.
- **`GET /readyz`**: Readiness probe. Returns `HTTP 200 ("ready")` if state is active (`RUNNING` / `DEGRADED`) and resting order requirements are satisfied (`eng.IsReady() == true`). Returns `HTTP 503` if `PAUSED`, `SYNCING`, `STARTING`, or resting order depth is below minimum.

### Metrics Server (Default `:9090`)
- **`GET /metrics`**: Prometheus metrics exposition endpoint via `promhttp.Handler()`. Exposes internal state, active level gauges, reconcile duration histograms, and command counters.

---

## 6. Configuration & Environment Variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `KAFKA_BROKERS` | `localhost:9092` | Comma-separated Kafka broker addresses |
| `KAFKA_GROUP_ID` | `liquidity-engine-group` | Kafka consumer group for `trades.executed` |
| `WALLET_GRPC_ADDR` | `localhost:50052` | Address of Wallet Service gRPC server |
| `ORDER_GRPC_ADDR` | `localhost:50053` | Address of Order Service gRPC server |
| `BTC_PARTITION` | `0` | Dedicated Kafka partition for BTC-USDT |
| `ETH_PARTITION` | `1` | Dedicated Kafka partition for ETH-USDT |
| `SOL_PARTITION` | `2` | Dedicated Kafka partition for SOL-USDT |
| `BTC_REFERENCE_PRICE` | `96450.00` | Reference price for BTC-USDT quoting ladder |
| `ETH_REFERENCE_PRICE` | `2780.50` | Reference price for ETH-USDT quoting ladder |
| `SOL_REFERENCE_PRICE` | `188.20` | Reference price for SOL-USDT quoting ladder |
| `RECONCILE_INTERVAL` | `30s` | Frequency of full state reconciliation loops |
| `PENDING_TIMEOUT` | `10s` | Timeout before querying OS for unconfirmed `PENDING` orders |
| `CANCELLING_TIMEOUT` | `30s` | Timeout before retrying unconfirmed `CANCELLING` orders |
| `CANCEL_RETRY_LIMIT` | `3` | Max cancel retries before transitioning order to `STALE` |
| `ME_LIVENESS_THRESHOLD` | `3` | Consecutive pending timeouts triggering engine `PAUSED` state |
| `HEALTH_PORT` | `8080` | Port for `/healthz` and `/readyz` |
| `METRICS_PORT` | `9090` | Port for `/metrics` Prometheus scrape endpoint |

---

## 7. Graceful Shutdown Flow

```
SIGINT / SIGTERM received
       │
ctx cancelled by signal.NotifyContext
       │
eng.Run(ctx) unblocks and shuts down event loop
       │
Kafka consumer stops reading from trades.executed
       │
HTTP servers begin shutdown with 10s timeout:
       ├─ healthServer.Shutdown(shutdownCtx)
       └─ metricsServer.Shutdown(shutdownCtx)
       │
Deferred resource cleanup:
       ├─ orderSvc.Close()      — close gRPC connection
       ├─ producer.Close()      — flush and close all partition Kafka writers
       └─ logger.Sync()         — flush log buffer
       │
Process exits cleanly (code 0)
```
