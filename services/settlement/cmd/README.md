# Settlement Service — Server Entrypoint (`cmd/`)

> **Package:** `tradedrift/services/settlement/cmd/server`  
> **Directory:** `services/settlement/cmd/`  
> **Executable Source:** `services/settlement/cmd/server/main.go`  
> **Role:** Bootstrapping, dependency wiring, runtime orchestration, and deterministic graceful shutdown

---

## 1. Purpose

`cmd/` holds the binary entrypoint for the Settlement Service. Following Go's conventional layout, application-specific `main` packages live under `cmd/<binary-name>/` so that a repository can hold multiple binaries (e.g. `cmd/server`, `cmd/migrate`, `cmd/worker`) without conflict.

`main.go` is the **composition root** — the only file that instantiates concrete types and wires them together. All other packages in this service work with interfaces and value types, making them independently testable.

---

## 2. Directory Structure

```
services/settlement/cmd/
├── server/
│   ├── main.go     ← Binary entrypoint — see cmd/server/README.md for full step-by-step
│   └── README.md   ← Detailed function-level documentation for main.go
└── README.md       ← This file
```

For detailed documentation of every startup step in `main.go`, see [`cmd/server/README.md`](./server/README.md).

---

## 3. Bootstrap Lifecycle (overview)

```
🚀 main()
    │
    ├── 0.  config.LoadEnv() + settlementconfig.Load()    ← env vars + validation
    ├── 1.  logger.New(cfg.LogLevel)                      ← structured logger
    ├── 2.  signal.NotifyContext(SIGINT, SIGTERM)          ← shutdown context
    ├── 3.  postgres.RunMigrations(...)                   ← schema up-to-date
    ├── 4.  postgres.NewPool(MaxConns=10)                 ← connection pool
    ├── 5.  settlementpostgres.NewRepository(dbPool)      ← DB access layer
    ├── 6.  client.NewWalletClient(cfg.WalletGRPCAddr)    ← outbound gRPC
    ├── 7.  service.NewService(repo, wallet, log, timeout) ← business logic
    ├── 8.  var wg sync.WaitGroup                         ← shutdown tracker
    ├── 9.  go recoveryLoop(ctx)  [wg.Add(1)]             ← 60s stale PENDING check
    ├── 10. go consumer.Start(ctx) [wg.Add(1)]            ← Kafka event loop
    ├── 11. <-ctx.Done()                                  ← block until SIGTERM
    ├── 12. wg.Wait()                                     ← goroutines finish in-flight work
    └── 13. consumer.Close() → deferred Close()s          ← connection cleanup
```

---

## 4. Key Design Properties

### No gRPC or HTTP Server

Settlement Service exposes **no network port**. It is a pure Kafka consumer. All communication is:

| Direction | Protocol | Endpoint |
|---|---|---|
| **Inbound** | Kafka consumer group | `trades.executed` topic |
| **Outbound** | gRPC client | `Wallet.SettleTrade` |

This makes it horizontally scalable with zero port conflict. Multiple instances share the `settlement-service-group` consumer group — Kafka automatically distributes partitions.

### Deterministic Shutdown

`sync.WaitGroup` ensures both goroutines (consumer + recovery) finish in-flight work before any connection is closed:

```
SIGTERM received
      ↓
ctx cancelled
      ↓
goroutines stop at next ctx.Done() check
      ↓
wg.Wait() unblocks
      ↓
consumer.Close()        ← Kafka reader flushed
walletClient.Close()    ← deferred: gRPC connection released
dbPool.Close()          ← deferred: all DB connections returned
appLogger.Sync()        ← deferred: final log lines flushed
```

Without `wg.Wait()`, closing the DB pool while a goroutine holds a connection mid-transaction would cause a panic.

### Connection Pool Sizing

```go
postgres.NewPool(ctx, cfg.PostgresDSN, postgres.PoolConfig{MaxConns: 10})
```

Settlement holds DB connections only for brief Phase 1 (INSERT) and Phase 3 (UPDATE) transactions. Connections are **never held during gRPC calls**. A pool of 10 is sufficient for current throughput and leaves headroom for concurrent recovery scans.

---

## 5. External Packages

| Package | Why Used |
|---|---|
| `tradedrift/platform/config` | `LoadEnv()` — reads `.env` file into `os.Environ` before any env var is read |
| `tradedrift/platform/logger` | Builds `*zap.Logger` from log level string; unified logging setup across services |
| `tradedrift/platform/postgres` | Shared `RunMigrations` (goose) and `NewPool` (pgxpool) — avoids duplicating infra code in every service |
| `os/signal` | `signal.NotifyContext` — converts OS signals into context cancellation, propagated to all blocking calls |
| `sync` | `sync.WaitGroup` for deterministic multi-goroutine shutdown |
| `syscall` | `SIGINT` / `SIGTERM` constants |
| `go.uber.org/zap` | Structured log fields (brokers, topic, group, timeout) logged at startup |
