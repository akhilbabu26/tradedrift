# Settlement Service — Entrypoint (`cmd/server`)

> **Package:** `main`  
> **File:** `main.go`  
> **Binary:** `settlement-service`  
> **Role:** Bootstraps every dependency, wires them together, runs the service, and handles graceful shutdown

---

## 1. Purpose

`main.go` is the **composition root** — the only place in the settlement service where concrete implementations are instantiated and injected into each other. Every other package depends only on interfaces or simple value types.

The startup sequence is strictly ordered: environment → logger → migrations → DB pool → repository → gRPC client → service → recovery goroutine → Kafka consumer.

---

## 2. Files

```
services/settlement/cmd/server/
├── main.go   ← Composition root, bootstrap, graceful shutdown
└── README.md ← This file
```

---

## 3. `main()` — Step by Step

### Step 0: Load Environment

```go
config.LoadEnv()
cfg, err := settlementconfig.Load()
```

**Why `config.LoadEnv()` first?**  
`LoadEnv` reads `.env` from disk into `os.Environ`. `Load()` calls `os.Getenv` internally — so the env file must be loaded before `Load()` runs, or `.env` values are silently ignored.  
**Why `panic` on `Load` error, not `log.Fatal`?**  
The structured logger (`appLogger`) is built from `cfg.LogLevel`, which requires a valid `Config`. A config failure cannot be logged with the structured logger — `panic` produces a visible stack trace with the error message.

---

### Step 1: Logger

```go
appLogger := logger.New(cfg.LogLevel)
defer appLogger.Sync()
```

**Why `defer appLogger.Sync()`?**  
`zap` buffers log output for performance. `Sync` flushes the buffer — without it, the final log lines before shutdown may be lost if the process exits before the buffer is flushed.

---

### Step 2: Graceful Shutdown Context

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()
```

**Why `signal.NotifyContext`?**  
This creates a context that is automatically cancelled when the OS sends `SIGINT` (Ctrl+C) or `SIGTERM` (Docker stop, Kubernetes pod eviction). Every long-running operation (`FetchMessage`, `SettleTrade`, `FindStalePending`) accepts this `ctx` — they all unblock immediately on shutdown.  
**Why `defer stop()`?**  
`stop()` releases the OS signal channel. Without it, the OS signal delivery goroutine is leaked.

---

### Step 3: Database Migrations

```go
postgres.RunMigrations(cfg.PostgresDSN, cfg.MigrationsDir)
```

**Why at startup?**  
Running migrations on startup ensures the DB schema matches the code version automatically — no manual migration step is required on deploy. If a migration fails (e.g. schema already at a newer version), the service exits with a clear error before accepting any traffic.  
**Tool used:** `github.com/pressly/goose/v3` via the `platform/postgres` package.

---

### Step 4: PostgreSQL Connection Pool

```go
poolCtx, cancelPool := context.WithTimeout(ctx, 10*time.Second)
defer cancelPool()
dbPool, err := postgres.NewPool(poolCtx, cfg.PostgresDSN, postgres.PoolConfig{MaxConns: 10})
defer dbPool.Close()
```

**Why `MaxConns: 10`?**  
Settlement uses short-lived single-query transactions (Phase 1 = one INSERT, Phase 3 = one UPDATE). At any moment, the consumer holds at most 1 connection and the recovery goroutine holds at most 1. A pool of 10 is sufficient for the current architecture and leaves headroom for the `FindByTradeID` idempotency check.  
**Why `10*time.Second` timeout for pool creation?**  
Prevents startup from hanging indefinitely if PostgreSQL is not yet ready. Docker Compose health checks ensure Postgres is up before the service starts, but this timeout is a safety net.

---

### Step 5: Repository

```go
repo := settlementpostgres.NewRepository(dbPool)
```

Creates the concrete PostgreSQL repository and stores it as the `repository.Repository` interface. The service layer below never imports the `postgres` sub-package.

---

### Step 6: Wallet gRPC Client

```go
walletClient, err := settlementclient.NewWalletClient(cfg.WalletGRPCAddr)
defer walletClient.Close()
```

**Why `defer Close()`?**  
Ensures the gRPC connection is cleanly closed on any exit path — including `appLogger.Fatal` panics. Without this, the OS TCP socket and the gRPC keepalive goroutine would be leaked.

---

### Step 7: Settlement Service

```go
svc := service.NewService(repo, walletClient, appLogger, cfg.WalletGRPCTimeout)
```

Injects all dependencies into the service. From this point, `svc` is the only reference passed to the consumer and recovery goroutine — neither has direct access to `repo` or `walletClient`.

---

### Step 8: WaitGroup

```go
var wg sync.WaitGroup
```

**Purpose:** Ensures deterministic shutdown. `main()` calls `wg.Wait()` after the context is cancelled, blocking until both the consumer goroutine and the recovery goroutine have fully exited before closing Kafka, Wallet, and PostgreSQL connections.  
**Why needed?** Without `wg.Wait()`, `main()` might call `consumer.Close()` and `dbPool.Close()` while a goroutine is mid-way through a `SettleTrade` gRPC call or a Phase 3 `UPDATE`. That would cause a panic or data corruption.

---

### Step 9: Recovery Goroutine

```go
wg.Add(1)
go func() {
    defer wg.Done()
    // tick every 60s, call svc.RecoverStalePending(ctx)
}()
```

**Why 60 seconds?** Balance between recovery latency and database load. A stuck trade older than 60 seconds represents a crash between Phase 2 and Phase 3 — the Wallet has already moved funds but the ledger still says PENDING.

---

### Step 10: Kafka Consumer

```go
wg.Add(1)
go func() {
    defer wg.Done()
    consumer.Start(ctx)
}()
```

**Why `consumer.Start` in a goroutine?**  
`Start` blocks until `ctx` is cancelled. Running it in a goroutine lets `main()` proceed to the `<-ctx.Done()` wait. When the signal arrives, `ctx` is cancelled → `Start` returns → `wg.Done()` is called → `wg.Wait()` unblocks.

---

### Step 11–12: Shutdown

```go
<-ctx.Done()
wg.Wait()
consumer.Close()
// deferred: walletClient.Close(), dbPool.Close(), appLogger.Sync()
```

**Shutdown order:**
1. `ctx` cancelled → goroutines stop fetching/ticking
2. `wg.Wait()` → all in-flight operations complete
3. `consumer.Close()` → Kafka reader flushed and connection closed
4. `walletClient.Close()` (deferred) → gRPC TCP connection closed
5. `dbPool.Close()` (deferred) → all DB connections returned and closed
6. `appLogger.Sync()` (deferred) → final log lines flushed

---

## 4. External Packages

| Package | Why Used |
|---|---|
| `tradedrift/platform/config` | `LoadEnv()` — reads `.env` file into environment |
| `tradedrift/platform/logger` | Builds `*zap.Logger` from log level string |
| `tradedrift/platform/postgres` | `RunMigrations` + `NewPool` — shared infrastructure layer |
| `tradedrift/services/settlement/internal/config` | Typed config for this service |
| `tradedrift/services/settlement/internal/client` | Wallet gRPC client |
| `tradedrift/services/settlement/internal/kafka` | Kafka consumer |
| `tradedrift/services/settlement/internal/repository/postgres` | PostgreSQL repository implementation |
| `tradedrift/services/settlement/internal/service` | 3-phase settlement business logic |
| `os/signal` | `signal.NotifyContext` — converts OS signals into context cancellation |
| `sync` | `sync.WaitGroup` for deterministic goroutine shutdown |
| `syscall` | `syscall.SIGINT`, `syscall.SIGTERM` signal constants |
| `go.uber.org/zap` | Structured log fields in startup/shutdown messages |
