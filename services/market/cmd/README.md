# Market Service — Server Binary & Entrypoint (`cmd/server`)

> **Package:** `tradedrift/services/market/cmd/server`  
> **Directory:** `services/market/cmd/`  
> **Executable Source:** `services/market/cmd/server/main.go`  
> **Network Bind:** gRPC TCP `:50054`  
> **Message Bus:** Apache Kafka (Consumer on `trade.executed.v1`)  
> **Role:** Bootstrapping, database migration execution, connection pooling, dual-engine runtime orchestration (gRPC Server + Kafka Event Consumer), and zero-data-loss graceful shutdown.

---

## 1. Purpose & Responsibilities

The `cmd/server/main.go` file is the central runtime orchestrator for the **Market Service**. Unlike single-protocol services, the Market Service operates as a **Dual-Engine Microservice**:

1. **Synchronous Query Server (gRPC on `:50054`):** Responds in sub-milliseconds to API Gateway queries for market pair specifications (`BTC-USDT`), 24h rolling tickers, and historical OHLC candlestick chart data.
2. **Asynchronous Event Worker (Kafka Consumer):** Continuously listens on the Kafka topic `trade.executed.v1` in the background, ingests trade matches from the Matching Engine, and updates candlestick aggregations atomically in PostgreSQL.

---

## 2. The 9-Stage Bootstrapping & Runtime Lifecycle

```
                                  🚀 main() Entrypoint
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 0. Load Configuration (.env + Environment)    │
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 1. Initialize Uber Zap Structured Logger      │
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 2. Run Database Migrations (DDL Schemas)      │
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 3. Initialize PostgreSQL Connection Pool      │
                    │    (MaxConns: 20 for High-Throughput Reads)   │
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 4. Instantiate Repositories & Domain Service  │
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 5. Start Kafka Consumer (Background Worker)   │
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 6. Wire gRPC Server & Register Handler        │
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 7. Launch gRPC TCP Listener (:50054)          │
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 8. Block on OS Signals (SIGINT/SIGTERM) or    │
                    │    Unexpected gRPC Server Failure Channel     │
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 9. Graceful Shutdown & Drain Execution        │
                    │    • grpcServer.GracefulStop()                │
                    │    • consumer.Close()                         │
                    │    • dbPool.Close()                           │
                    └───────────────────────────────────────────────┘
```

---

## 3. Deep-Dive: Key Subsystems & Design Choices

### 1. Dual-Engine Concurrency Pattern
The Market Service runs two long-running workloads concurrently inside a single binary:
* **Workload A (Kafka Consumer):** Launched via `go consumer.Start(ctx)`. It runs an event loop consuming from Kafka until the shared `ctx` is cancelled.
* **Workload B (gRPC Server):** Launched via `go grpcServer.Serve(lis)` inside a separate goroutine with an error notification channel (`serverErrCh`).

```go
// Starts asynchronous event ingestion
go consumer.Start(ctx)

// Starts synchronous RPC server
serverErrCh := make(chan error, 1)
go func() {
    if err := grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
        serverErrCh <- err
    }
}()
```

---

### 2. Auto-Migration on Startup
Before accepting any RPC traffic or Kafka messages, `main.go` applies pending SQL migrations:
```go
postgres.RunMigrations(cfg.PostgresDSN, cfg.MigrationsDir)
```
* **Why:** Guarantees that tables (`markets`, `market_trades`, `ohlc_candles`) and indexes (`idx_ohlc_market_res_time`) always exist before the application queries or inserts data, preventing runtime schema mismatches.

---

### 3. Sized PostgreSQL Connection Pool (`MaxConns: 20`)
```go
dbPool, err := postgres.NewPool(poolCtx, cfg.PostgresDSN, postgres.PoolConfig{
    MaxConns: 20, // Tuned for high-frequency ticker & candle queries
})
```
* **Why:** High-frequency TradingView chart queries from multiple users and rapid trade ingestion from Kafka require concurrent database connections without causing connection starvation or PostgreSQL connection limits.

---

### 4. Zero-Data-Loss Graceful Shutdown & Error Trapping
```go
select {
case <-ctx.Done():
    appLogger.Info("Shutdown signal received, gracefully stopping...")
case err := <-serverErrCh:
    appLogger.Error("gRPC server error, triggering shutdown...", zap.Error(err))
    stop() // Cancel context to immediately stop Kafka worker
}

// 1. Stop accepting new gRPC calls and finish in-flight RPCs
grpcServer.GracefulStop()

// 2. Stop Kafka consumer, finish current message commit, and close reader
if err := consumer.Close(); err != nil {
    appLogger.Error("Failed to close Kafka consumer cleanly", zap.Error(err))
}

// 3. Close database connection pool (via defer dbPool.Close())
```
* **Benefit:** If Docker stops the container or Kubernetes reschedules the pod, active in-flight Kafka offsets are committed cleanly, preventing duplicate trade reprocessing upon restart.

---

## 4. Tools & Packages Used

| Tool / Package | Role in `main.go` | Why It Was Chosen |
| :--- | :--- | :--- |
| **`go.uber.org/zap`** | Structured Logger | High-speed, zero-allocation logging with structured key-value context. |
| **`google.golang.org/grpc`** | RPC Server Framework | Low-latency binary protocol communication over HTTP/2. |
| **`github.com/segmentio/kafka-go`** | Event Streaming | Pure Go consumer implementation with manual offset commit control. |
| **`tradedrift/platform/postgres`** | DB Pool & Migrations | Wraps `pgxpool` with health checks, timeout management, and migration execution. |
| **`os/signal` + `syscall`** | OS Signal Trapping | Catches `SIGINT` (Ctrl+C) and `SIGTERM` (Docker/K8s stop) for clean shutdown. |

---

## 5. Configuration Environment Variables

| Variable | Default Value | Description |
| :--- | :--- | :--- |
| `MARKET_GRPC_PORT` | `:50054` | TCP port for gRPC listener |
| `MARKET_DB_URL` | `postgres://.../tradedrift_market` | PostgreSQL connection DSN |
| `KAFKA_BROKERS` | `localhost:9092` | Comma-separated list of Kafka broker endpoints |
| `KAFKA_GROUP_ID` | `market-service-group` | Consumer group identifier |
| `KAFKA_TOPIC` | `trade.executed.v1` | Kafka topic name for trade execution events |
| `LOG_LEVEL` | `info` | Verbosity level (`debug`, `info`, `warn`, `error`) |

---

## 6. How to Run Locally

```powershell
cd services/market
go run cmd/server/main.go
```
