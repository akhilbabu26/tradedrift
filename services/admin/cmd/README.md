# Admin Service Entrypoint & Composition Root (`cmd/`)

This document provides a comprehensive architectural and operational manual for the `cmd/` package of the **TradeDrift Admin Service**, specifically focusing on [`cmd/server/main.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/server/main.go).

---

## Table of Contents

1. [Overview & Package Purpose](#1-overview--package-purpose)
2. [What Problem This File Solves](#2-what-problem-this-file-solves)
3. [How It Solves the Problems](#3-how-it-solves-the-problems)
4. [Deep Dive: Lifecycle Phases & Responsibilities](#4-deep-dive-lifecycle-phases--responsibilities)
   - [Phase 1: Structured Production Logger (`zap`)](#phase-1-structured-production-logger-zap)
   - [Phase 2: Configuration Ingestion (`config.Load`)](#phase-2-configuration-ingestion-configload)
   - [Phase 3: Root Signal Trapping (`signal.NotifyContext`)](#phase-3-root-signal-trapping-signalnotifycontext)
   - [Phase 4: Fail-Fast Database Migrations (`platformpg.RunMigrations`)](#phase-4-fail-fast-database-migrations-platformpgrunmigrations)
   - [Phase 5: High-Performance Connection Pooling (`pgxpool`)](#phase-5-high-performance-connection-pooling-pgxpool)
   - [Phase 6: Repository Layer Instantiation](#phase-6-repository-layer-instantiation)
   - [Phase 7: Downstream gRPC Clients Initialization](#phase-7-downstream-grpc-clients-initialization)
   - [Phase 8: Domain Service Assembly](#phase-8-domain-service-assembly)
   - [Phase 9: Background Daemon Workers Bootstrap](#phase-9-background-daemon-workers-bootstrap)
   - [Phase 10: HTTP Transport & Middleware Wiring](#phase-10-http-transport--middleware-wiring)
   - [Phase 11: Coordinated Graceful Shutdown](#phase-11-coordinated-graceful-shutdown)
5. [Architecture & Execution Flows](#5-architecture--execution-flows)
   - [Flow 1: Fail-Fast Sequential Startup Sequence](#flow-1-fail-fast-sequential-startup-sequence)
   - [Flow 2: Coordinated 5-Step Graceful Drainage Sequence](#flow-2-coordinated-5-step-graceful-drainage-sequence)
   - [Flow 3: Complete Dependency Injection Wiring Graph](#flow-3-complete-dependency-injection-wiring-graph)
6. [Operational & Verification Guide](#6-operational--verification-guide)

---

## 1. Overview & Package Purpose

In accordance with standard Go project layout conventions, the `cmd/` directory contains the executable entrypoints for the application.

- **Directory**: `services/admin/cmd/server/`
- **Main File**: [`main.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/server/main.go)
- **Role**: The **Composition Root** of the microservice.

### Pure Composition Root Pattern
`cmd/server/main.go` contains **zero business logic**. Instead, it acts as the master coordinator that:
1. Reads environment variables and configures dependencies.
2. Applies necessary relational database migrations.
3. Instantiates data structures, repositories, clients, and services.
4. Spawns background asynchronous processing workers.
5. Boots the HTTP web server with production-grade timeouts.
6. Traps operating system termination signals (`SIGINT`, `SIGTERM`) to coordinate clean drainage and shutdown.

---

## 2. What Problem This File Solves

In distributed financial microservices, naive startup and shutdown mechanisms lead to production incidents. `main.go` specifically prevents the following critical failures:

| Problem | Failure Scenario Without `main.go` Coordination | How `main.go` Solves It |
| :--- | :--- | :--- |
| **Startup Race Conditions (Partial Initialization)** | HTTP server begins accepting incoming traffic while database connection pools or Kafka publishers are still initializing, resulting in immediate 500 errors. | Implements **sequential, fail-fast bootstrapping**. HTTP listener is started only after all dependencies, clients, and workers are healthy. |
| **Schema Drift & DDL Desync** | Service starts against an unmigrated database, failing at runtime when queries target missing columns or triggers. | Runs database migrations automatically at boot before initializing connection pools or HTTP handlers. |
| **Dropped In-Flight Requests** | Process terminates immediately upon receiving a deployment `SIGTERM`, severing active client TCP connections and dropping transactions. | Marks `/ready` as HTTP 503 first, then gives in-flight HTTP requests a dedicated **10-second graceful drainage window** via `srv.Shutdown()`. |
| **In-Flight Saga & Outbox Corruption** | Killing workers abruptly while publishing Kafka events or executing Saga compensations can result in duplicate events or leaked worker leases. | Gracefully calls `.Stop()` on background workers (`HealthWorker`, `SagaWorker`, `OutboxPublisher`), allowing active batches to commit cleanly. |
| **Resource & Connection Leaks** | Abandoned gRPC connections and unclosed PostgreSQL pool sockets cause database connection exhaustion on the server. | Sequentially closes gRPC client channels (`authCli`, `walletCli`) and closes `dbPool.Close()` as the final exit step. |

---

## 3. How It Solves the Problems

`main.go` solves these problems by splitting the service lifecycle into two deterministic, unidirectional state machines:

```
[BOOTSTRAP / STARTUP]
Logger -> Config -> Signals -> Migrations -> DB Pool -> Repositories -> gRPC Clients -> Domain Services -> Background Workers -> HTTP Server

                                     │
                             (Process Active)
                                     │
                             [SIGINT / SIGTERM]
                                     ↓

[COORDINATED DRAINAGE / SHUTDOWN]
Step A: Cut Traffic (Readiness -> 503)
Step B: Drain HTTP Requests (srv.Shutdown)
Step C: Stop Workers (HealthWorker -> SagaWorker -> OutboxPublisher)
Step D: Close Downstream Clients (Auth gRPC -> Wallet gRPC)
Step E: Close DB Pool (pgxpool.Close)
```

---

## 4. Deep Dive: Lifecycle Phases & Responsibilities

### Phase 1: Structured Production Logger (`zap`)
```go
log, err := zap.NewProduction()
if err != nil {
    fmt.Printf("Failed to initialize logger: %v\n", err)
    os.Exit(1)
}
defer log.Sync()
```
- **Purpose**: Instantiates high-throughput, structured JSON logging.
- **Problem Solved**: Unstructured string logging cannot be easily queried in centralized log aggregators (e.g. Datadog, ELK). Buffered log statements can be lost on sudden crashes.
- **Why We Need It**: `defer log.Sync()` ensures all log buffers are flushed to standard output before the process exits.

---

### Phase 2: Configuration Ingestion (`config.Load`)
```go
cfg := config.Load()
```
- **Purpose**: Reads, validates, and sets fallback defaults for all environment variables (`ADMIN_PORT`, `POSTGRES_DSN`, `KAFKA_BROKERS`, `JWT_SECRET`, etc.).
- **Problem Solved**: Eliminates scattered `os.Getenv()` calls throughout the codebase.
- **Why We Need It**: Provides strongly-typed configuration guarantees across all downstream components.

---

### Phase 3: Root Signal Trapping (`signal.NotifyContext`)
```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()
```
- **Purpose**: Creates a root context that automatically cancels when the operating system sends a termination signal (`Ctrl+C` or Docker/Kubernetes `SIGTERM`).
- **Problem Solved**: Default Go behavior on `SIGINT`/`SIGTERM` is immediate termination, which forcefully severs active TCP connections.
- **Why We Need It**: Provides the master event trigger that activates the coordinated graceful shutdown pipeline.

---

### Phase 4: Fail-Fast Database Migrations (`platformpg.RunMigrations`)
```go
migrationDir := "migrations"
if _, err := os.Stat(migrationDir); os.IsNotExist(err) {
    migrationDir = "services/admin/migrations"
}
if err := platformpg.RunMigrations(cfg.PostgresDSN, migrationDir); err != nil {
    log.Fatal("Could not apply database migrations; halting startup", zap.Error(err))
}
```
- **Purpose**: Executes pending Goose SQL migrations (`00001` through `00004`) before running any application queries.
- **Problem Solved**: Guarantees tables, constraints, triggers, and partial indexes exist before the service starts serving traffic.
- **Why We Need It**: If migrations fail (e.g. invalid credentials or locked tables), `log.Fatal` immediately halts startup, preventing the deployment of broken pods.

---

### Phase 5: High-Performance Connection Pooling (`pgxpool`)
```go
poolCfg, err := pgxpool.ParseConfig(cfg.PostgresDSN)
poolCfg.MaxConns = 25
poolCfg.MinConns = 5

dbPool, err := pgxpool.NewWithConfig(ctx, poolCfg)
```
- **Purpose**: Establishes a tuned PostgreSQL connection pool using `jackc/pgx/v5`.
- **Problem Solved**: Opening a new PostgreSQL connection per request incurs high latency and TCP overhead. Unbounded connections can exhaust database memory.
- **Why We Need It**: Maintains warm idle connections (`MinConns = 5`) for sub-millisecond query execution while bounding peak concurrency (`MaxConns = 25`).

---

### Phase 6: Repository Layer Instantiation
```go
txMgr := postgresRepo.NewTxManager(dbPool)
opsRepo := postgresRepo.NewOperationsRepo(dbPool)
outboxRepo := postgresRepo.NewOutboxRepo(dbPool)
sagaRepo := postgresRepo.NewSagaRepo(dbPool)
```
- **Purpose**: Constructs database access repositories using the connection pool.
- **Problem Solved**: Decouples raw SQL queries and transaction management from business domain logic.
- **Why We Need It**: Allows mocking in unit tests and cleanly encapsulates database access patterns.

---

### Phase 7: Downstream gRPC Clients Initialization
```go
authCli, err := client.NewAuthClient(cfg.AuthGRPCAddr)
walletCli, err := client.NewWalletClient(cfg.WalletGRPCAddr)
```
- **Purpose**: Connects to the Auth Service and Wallet Service via gRPC.
- **Problem Solved**: Prevents connection stalls by validating gRPC transport initialization during boot.
- **Why We Need It**: Prepares clients used by `AdminService` and `SagaWorker` for session revocations and wallet status queries.

---

### Phase 8: Domain Service Assembly
```go
adminSvc := service.NewAdminService(txMgr, opsRepo, authCli, walletCli, log)
```
- **Purpose**: Instantiates the core domain orchestration service.
- **Problem Solved**: Glues transactional repositories, gRPC clients, and idempotency checks into unified business commands (`HaltMarket`, `SuspendUser`, etc.).

---

### Phase 9: Background Daemon Workers Bootstrap
```go
outboxPub := service.NewOutboxPublisher(outboxRepo, cfg.KafkaBrokers, log, cfg.OutboxInterval)
outboxPub.Start(ctx)

sagaWorker := service.NewSagaWorker(txMgr, sagaRepo, opsRepo, authCli, log, cfg.SagaInterval)
sagaWorker.Start(ctx)

healthWorker := service.NewHealthWorker(dbPool, authCli, walletCli, outboxRepo, sagaRepo, healthWorkerCfg, log, cfg.HealthInterval)
healthWorker.Start(ctx)
```
- **Purpose**: Spawns 3 background workers running on their own goroutine loops:
  1. `OutboxPublisher`: Polls `admin_outbox` and publishes events to Apache Kafka.
  2. `SagaWorker`: Polls `admin_saga_tasks` and retries downstream session revocations.
  3. `HealthWorker`: Continuously probes microservices, updating Prometheus gauges and caching `/system/health`.
- **Problem Solved**: Keeps heavy I/O, retries, and network probing completely off the synchronous HTTP request path.

---

### Phase 10: HTTP Transport & Middleware Wiring
```go
healthHdr := handler.NewHealthHandler(...)
healthHdr.SetHealthWorker(healthWorker)
adminHdr := handler.NewAdminHandler(adminSvc, log)
jwtValidator := platformjwt.NewHMACValidator([]byte(cfg.JWTSecret))

router := handler.NewRouter(adminHdr, healthHdr, jwtValidator, log)

srv := &http.Server{
    Addr:         fmt.Sprintf(":%s", cfg.Port),
    Handler:      router,
    ReadTimeout:  10 * time.Second,
    WriteTimeout: 10 * time.Second,
    IdleTimeout:  60 * time.Second,
}

go func() {
    if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
        log.Fatal("HTTP server failed", zap.Error(err))
    }
}()
```
- **Purpose**: Wires the HTTP multiplexer with middleware (`MetricsMiddleware`, `PanicRecoveryMiddleware`, `RequireAdmin`, `RequireIdempotencyKey`) and starts listening on a background goroutine.
- **Problem Solved**: Protects against Slowloris attacks and socket leaks by enforcing strict `ReadTimeout`, `WriteTimeout`, and `IdleTimeout`.

---

### Phase 11: Coordinated Graceful Shutdown
```go
<-ctx.Done() // Blocks until SIGINT or SIGTERM is intercepted
```
Once unblocked, `main.go` coordinates a 5-step teardown:

1. **Step A: Cut Ingress Traffic (`healthHdr.SetShuttingDown()`)**
   - Marks `/ready` as HTTP 503 (`StatusShuttingDown`).
   - Load balancers and Kubernetes ingress immediately drop this instance from active endpoints, routing new requests elsewhere.
2. **Step B: Drain In-Flight HTTP Requests (`srv.Shutdown(shutdownCtx)`)**
   - Allows existing HTTP requests up to 10 seconds to finish execution and return responses.
3. **Step C: Stop Background Workers (`Stop()`)**
   - Calls `.Stop()` on `HealthWorker`, `SagaWorker`, and `OutboxPublisher`.
   - Ensures active Kafka publishing batches and saga transactions finish and release their database leases before worker goroutines exit.
4. **Step D: Close Downstream gRPC Clients (`Close()`)**
   - Closes client connections to Auth and Wallet services.
5. **Step E: Close Database Connection Pool (`dbPool.Close()`)**
   - Terminates all PostgreSQL sockets cleanly without abruptly severing connections.

---

## 5. Architecture & Execution Flows

### Flow 1: Fail-Fast Sequential Startup Sequence

```
                       Operating System
                              │
                              ▼
                     cmd/server/main.go
                              │
                    ┌─────────▼─────────┐
                    │ 1. Logger (zap)   │
                    └─────────┬─────────┘
                              ▼
                    ┌───────────────────┐
                    │ 2. config.Load()  │
                    └─────────┬─────────┘
                              ▼
                    ┌───────────────────┐
                    │ 3. Signal Traps   │ (SIGINT, SIGTERM)
                    └─────────┬─────────┘
                              ▼
                    ┌───────────────────┐
                    │ 4. DB Migrations  │──(Failure)──► log.Fatal (Exit 1)
                    └─────────┬─────────┘
                          (Success)
                              ▼
                    ┌───────────────────┐
                    │ 5. pgxpool.New()  │──(Failure)──► log.Fatal (Exit 1)
                    └─────────┬─────────┘
                          (Success)
                              ▼
                    ┌───────────────────┐
                    │ 6. gRPC Clients   │──(Failure)──► log.Fatal (Exit 1)
                    └─────────┬─────────┘
                          (Success)
                              ▼
                    ┌───────────────────┐
                    │ 7. Repos & Svc    │
                    └─────────┬─────────┘
                              ▼
                    ┌───────────────────┐
                    │ 8. Workers.Start  │ (Outbox, Saga, Health)
                    └─────────┬─────────┘
                              ▼
                    ┌───────────────────┐
                    │ 9. srv.Listen()   │──► Ready on :8085
                    └───────────────────┘
```

---

### Flow 2: Coordinated 5-Step Graceful Drainage Sequence

```
                       Operating System
                              │
                      (SIGINT / SIGTERM)
                              ▼
                     cmd/server/main.go
                              │
                    ┌─────────▼─────────┐
                    │      Step A       │
                    │ SetShuttingDown() │──► /ready returns 503 (Traffic Stopped)
                    └─────────┬─────────┘
                              ▼
                    ┌───────────────────┐
                    │      Step B       │
                    │   srv.Shutdown()  │──► 10s Window (Drain In-Flight HTTP)
                    └─────────┬─────────┘
                              ▼
                    ┌───────────────────┐
                    │      Step C       │
                    │   Workers.Stop()  │──► HealthWorker, SagaWorker, OutboxPublisher
                    └─────────┬─────────┘
                              ▼
                    ┌───────────────────┐
                    │      Step D       │
                    │   Clients.Close() │──► Auth gRPC, Wallet gRPC
                    └─────────┬─────────┘
                              ▼
                    ┌───────────────────┐
                    │      Step E       │
                    │   dbPool.Close()  │──► PostgreSQL Pool Sockets Drained
                    └─────────┬─────────┘
                              ▼
                        Process Exit (0)
```

---

### Flow 3: Complete Dependency Injection Wiring Graph

```
                           Environment Variables
                                     │
                                     ▼
                              internal/config
                                     │
         ┌───────────────────┬───────┴───────────┬────────────────────┐
         ▼                   ▼                   ▼                    ▼
     PostgreSQL            Kafka             Auth gRPC           Wallet gRPC
   (pgxpool.Pool)        (Brokers)           (Address)            (Address)
         │                   │                   │                    │
 ┌───────┴───────┐           │           ┌───────┴────────┐   ┌───────┴────────┐
 ▼               ▼           │           ▼                ▼   ▼                ▼
TxManager  Repositories      │       AuthClient      WalletClient          │
 │               │           │           │                │                │
 └───────┬───────┘           │           └───────┬────────┘                │
         ▼                   │                   ▼                         │
   AdminService ◄────────────┼───────────────────┴─────────────────────────┘
         │                   │
 ┌───────┴───────┐           │
 ▼               ▼           │
AdminHandler   HealthWorker ◄┘
 │               │
 └───────┬───────┘
         ▼
    HTTP Router (ServeMux + Middleware)
         │
         ▼
     http.Server (Port :8085)
```

---

## 6. Operational & Verification Guide

### Starting the Service
To compile and run the Admin Service entrypoint:
```bash
go run services/admin/cmd/server/main.go
```

### Verifying Normal Bootstrap
When booted successfully, the log stream displays:
```json
{"level":"info","msg":"Starting TradeDrift Admin Service..."}
{"level":"info","msg":"Running admin database migrations...","dir":"services/admin/migrations"}
{"level":"info","msg":"Admin Service listening for requests","addr":":8080"}
```

### Verifying Graceful Shutdown (Testing `SIGTERM`)
Send `SIGINT` (`Ctrl+C` in terminal) or run `kill -TERM <PID>`:
```json
{"level":"info","msg":"Shutdown signal received: initiating graceful drainage..."}
{"level":"info","msg":"Readiness probe set to 503 (shutting down)"}
{"level":"info","msg":"HTTP server drained"}
{"level":"info","msg":"Stopping background workers..."}
{"level":"info","msg":"Health worker stopped cleanly"}
{"level":"info","msg":"Saga worker stopped cleanly"}
{"level":"info","msg":"Outbox publisher stopped and flushed"}
{"level":"info","msg":"Admin Service terminated cleanly"}
```
Notice that every step completes in order before the process terminates with exit code 0.
