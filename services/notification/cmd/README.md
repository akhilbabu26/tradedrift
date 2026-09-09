# Notification Service — Command & Composition Root Guide

This document provides a comprehensive technical guide to the command-line entrypoint and composition root of the **TradeDrift Notification Service** located in `services/notification/cmd/`.

It details the role of `cmd/server/main.go`, the specific operational and systems problems it solves, the 11 sequential bootstrap phases, the health check and observability contracts, and the complete startup and teardown lifecycles.

---

## Architecture Overview & Role of `cmd/`

In Clean Architecture and Domain-Driven Design (DDD), `cmd/` serves as the **Composition Root**. It is the only package that imports all internal and external dependencies, instantiating concrete adapters and injecting them into the domain core.

```
services/notification/cmd/
└── server/
    └── main.go       # Microservice composition root & daemon orchestrator
```

### Core Responsibilities
1. **Deterministic Initialization**: Enforces a strict dependency order (Configuration → Logging → Migrations → Storage Pools → Domain Services → Background Workers → Network Listeners).
2. **Fail-Fast Boundary**: Verifies network reachability and configuration sanity during boot, terminating immediately with non-zero exit codes if required dependencies are missing.
3. **Graceful Draining & Teardown**: Intercepts operating system termination signals (`SIGINT`, `SIGTERM`), draining network RPCs, background outbox batches, and Kafka partitions without dropping messages or corrupting transactions.

---

## Problems Solved by `main.go`

| Problem | Failure Scenario Without `main.go` Coordination | How `main.go` Solves It |
|---|---|---|
| **Startup Race Conditions** | If Kafka consumers or gRPC listeners accept traffic before PostgreSQL schema migrations run, queries fail with missing table/column errors. | Executes `platformpg.RunMigrations` synchronously **before** initializing any background workers or network listeners. |
| **Silent Boot Failures** | If the Kafka Dead-Letter Queue (`KafkaDLQTopic`) is unconfigured, poison messages cannot be routed, permanently blocking consumer partitions. | Validates DLQ settings at startup and invokes `appLogger.Fatal()` immediately if missing. |
| **Zombie Connections / Leaks** | Abruptly terminating containers causes database connection leaks in PostgreSQL and abandoned lock leases in the outbox table. | Uses Go's `context.Context`, `sync.WaitGroup`, and deferred `.Close()` calls to flush connection pools on shutdown. |
| **Hung Process Teardown** | If an RPC hangs or a slow client streams data indefinitely, `grpcServer.GracefulStop()` blocks forever, causing Kubernetes to issue an abrupt `SIGKILL`. | Wraps `GracefulStop()` in a 10-second timeout channel, falling back to forceful `grpcServer.Stop()` if in-flight RPCs exceed the deadline. |
| **False-Positive Traffic Routing** | If traffic is routed to a pod while PostgreSQL or Redis is offline, user requests fail with internal 500 errors. | Separates process liveness (`/healthz`) from dependency readiness (`/ready`), ensuring traffic is only routed when DB and Redis are reachable. |

---

## Step-by-Step Breakdown of the 11 Bootstrap Phases

```
 ┌─────────────────────────────────────────────────────────────┐
 │                    cmd/server/main.go                       │
 └──────────────────────────────┬──────────────────────────────┘
                                │
   [Phase 0] Load Environment & Config
                                │
   [Phase 1] Initialize Structured Logger (Zap)
                                │
   [Phase 2] Setup OS Signal Trap (SIGINT, SIGTERM)
                                │
   [Phase 3] Apply Database Migrations (Goose)
                                │
   [Phase 4] Connect PostgreSQL Connection Pool (pgxpool)
                                │
   [Phase 5] Connect Redis Client (Pub/Sub)
                                │
   [Phase 6] Wire Adapters: Repository → Service → gRPC Handler
                                │
   [Phase 7] Start gRPC Server Daemon (:50059)
                                │
   [Phase 8] Start HTTP Metrics & Health Server (:9092)
                                │
   [Phase 9] Start Transactional Outbox Publisher Loop
                                │
   [Phase 10] Start Kafka Domain Consumer Groups
                                │
   [Phase 11] Await OS Signal & Execute Controlled Teardown
```

---

### Phase 0: Configuration Loading
```go
config.LoadEnv()
cfg, err := notificationconfig.Load()
if err != nil {
    panic("invalid notification configuration: " + err.Error())
}
```
- **Purpose**: Reads `.env` files (for local dev) and parses environment variables into the strongly-typed `Config` struct.
- **Problem Solved**: Replaces unvalidated string lookups with validated types, port formats, and durations. Panics immediately if required variables are missing.

---

### Phase 1: Structured Logging
```go
appLogger := logger.New(cfg.LogLevel)
defer appLogger.Sync()
```
- **Purpose**: Instantiates high-throughput Uber Zap structured logger.
- **Problem Solved**: Guarantees JSON log outputs with timestamps, severity levels, and stack traces. `defer appLogger.Sync()` flushes in-memory log buffers to stdout before process exit.

---

### Phase 2: OS Signal Handling
```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()
```
- **Purpose**: Creates a root context that is automatically cancelled when Kubernetes, Docker, or the OS sends `SIGINT` (Ctrl+C) or `SIGTERM` (container stop).
- **Problem Solved**: Provides a clean cancellation signal propagated to all child workers, background loops, and network handlers.

---

### Phase 3: Database Migrations
```go
if err := platformpg.RunMigrations(cfg.PostgresDSN, cfg.MigrationsDir); err != nil {
    appLogger.Fatal("Failed to apply notification migrations", zap.Error(err))
}
```
- **Purpose**: Runs pending Goose SQL migrations (`00001`, `00002`, `00003`) against the target database before accepting traffic.
- **Problem Solved**: Eliminates schema version mismatches. Acquires PostgreSQL advisory locks to guarantee that multiple horizontal pod replicas do not execute migrations concurrently.

---

### Phase 4: PostgreSQL Connection Pool
```go
poolCtx, cancelPool := context.WithTimeout(ctx, 10*time.Second)
defer cancelPool()
dbPool, err := platformpg.NewPool(poolCtx, cfg.PostgresDSN, platformpg.PoolConfig{
    MaxConns: 15,
})
```
- **Purpose**: Establishes a production-tuned `pgxpool.Pool` with health checks.
- **Problem Solved**: Connection exhaustion. Restricts maximum connections to 15 per replica, uses prepared statements, and verifies database ping with a 10-second timeout.

---

### Phase 5: Redis Client Initialization
```go
redisClient, err := platformredis.NewClient(poolCtx, platformredis.Config{
    Addr:           cfg.RedisAddr,
    SentinelMaster: cfg.RedisSentinelMaster,
    Password:       cfg.RedisPassword,
    DB:             cfg.RedisDB,
})
```
- **Purpose**: Connects to the Redis cluster or standalone instance for real-time WebSocket Pub/Sub broadcasting.
- **Problem Solved**: Verifies broker reachability on boot and configures support for Redis Sentinel high-availability if enabled.

---

### Phase 6: Dependency Injection & Adapter Wiring
```go
repo := postgresrepo.NewRepository(dbPool)
svc := service.NewService(repo, appLogger)
grpcHandler := handler.NewNotificationHandler(svc, appLogger)
```
- **Purpose**: Pure Inversion of Control (IoC). Constructs the repository adapter, injects it into the core domain service, and wraps the service with the transport gRPC handler.
- **Problem Solved**: Enforces unidirectional dependency boundaries. High-level business rules never depend on low-level database drivers.

---

### Phase 7: gRPC Server Daemon (:50059)
```go
grpcServer := grpc.NewServer()
notificationv1.RegisterNotificationServiceServer(grpcServer, grpcHandler)

wg.Add(1)
go func() {
    defer wg.Done()
    lis, err := net.Listen("tcp", cfg.GRPCPort)
    ...
    if err := grpcServer.Serve(lis); err != nil { ... }
}()
```
- **Purpose**: Binds TCP socket `:50059` and exposes the compiled Protobuf API for internal microservices (Wallet, Auth, Admin, Gateway).
- **Problem Solved**: Runs as an asynchronous background goroutine tracked by `sync.WaitGroup`, ensuring non-blocking execution while remaining under graceful shutdown control.

---

### Phase 8: HTTP Metrics & Health Probes (:9092)
```go
metricsMux := http.NewServeMux()
metricsMux.Handle("/metrics", promhttp.Handler())
metricsMux.HandleFunc("/healthz", ...)
metricsMux.HandleFunc("/ready", ...)
```
- **Endpoints Provided**:
  - `/metrics`: Prometheus exposition format for scrapers.
  - `/healthz`: Liveness probe. Returns `200 OK` as long as the process is alive.
  - `/ready`: Readiness probe. Executes live `dbPool.Ping()` and `redisClient.Ping()`. Returns `503 Service Unavailable` if infrastructure is down, preventing Kubernetes from directing user traffic to a disconnected pod.

---

### Phase 9: Transactional Outbox Publisher
```go
outboxPub := publisher.NewPublisher(repo, redisClient, appLogger, publisher.DefaultConfig())
wg.Add(1)
go func() {
    defer wg.Done()
    if err := outboxPub.Start(ctx); err != nil && err != context.Canceled { ... }
}()
```
- **Purpose**: Starts the autonomous outbox publisher loop draining `notification_outbox` and publishing to Redis Pub/Sub channels.
- **Problem Solved**: Completely separates domain persistence from external message broadcast, surviving Redis outages without dropping notifications.

---

### Phase 10: Kafka Domain Consumers
```go
if cfg.KafkaDLQTopic == "" {
    appLogger.Fatal("KafkaDLQTopic is not configured; refusing to start Kafka consumers")
}

kafkaConsumer := notificationkafka.NewConsumer(
    notificationkafka.ConsumerConfig{
        Brokers:  cfg.KafkaBrokers,
        GroupID:  cfg.KafkaGroupID,
        DLQTopic: cfg.KafkaDLQTopic,
    },
    svc,
    appLogger,
)
kafkaConsumer.StartWithWaitGroup(ctx, &wg)
```
- **Purpose**: Subscribes to `trades.settled.v1`, `orders.cancelled.v1`, and `portfolios.updated.v1`.
- **Problem Solved**: Enforces fail-fast validation on DLQ topics and coordinates partition consumption loops within the main `sync.WaitGroup` for deterministic draining.

---

### Phase 11: Controlled Graceful Teardown
```go
<-ctx.Done()
appLogger.Info("Shutdown signal received; draining notification service resources...")

// 1. gRPC Graceful Stop with 10s Deadline
grpcStopped := make(chan struct{})
go func() {
    grpcServer.GracefulStop()
    close(grpcStopped)
}()
select {
case <-grpcStopped:
    appLogger.Info("Notification gRPC server stopped gracefully")
case <-time.After(10 * time.Second):
    appLogger.Warn("gRPC graceful stop timed out after 10s; forcing stop")
    grpcServer.Stop()
}

// 2. Metrics HTTP Server Draining
metricsCtx, cancelMetrics := context.WithTimeout(context.Background(), 3*time.Second)
defer cancelMetrics()
_ = metricsServer.Shutdown(metricsCtx)

// 3. Kafka Consumer Partition Closing
_ = kafkaConsumer.Close()

// 4. Wait for Outbox Worker and Goroutines to complete
wg.Wait()
```

#### Teardown Sequence & Invariants:
1. **Stop Ingress First**: Halts gRPC listeners and stops accepting new incoming RPCs while allowing in-flight requests to complete.
2. **10-Second Deadline Guard**: If an in-flight RPC is stuck or a client connection hangs, `grpcServer.Stop()` is invoked forcefully after 10 seconds to guarantee termination.
3. **Drain Kafka Partitions**: Closes Kafka consumer handles, flushing pending offset commits to the broker.
4. **Drain Outbox Publisher**: `outboxPub.Start` listens to `ctx.Done()`, completes its active batch, and releases claimed outbox rows before returning.
5. **Close Storage Pools**: `defer dbPool.Close()` and `defer redisClient.Close()` execute last, ensuring no active background query encounters a closed connection pool.

---

## End-to-End Teardown Flow Diagram

```
OS sends SIGTERM / SIGINT
       │
       ▼
root ctx.Done() triggers
       │
       ├────────────────────────────────────────────────────────┐
       ▼                                                        ▼
gRPC Server GracefulStop()                       HTTP Metrics Server Shutdown(3s)
       │                                                        │
       ├── Completed < 10s ──► Normal Close                     ▼
       │                                                HTTP Listener Closed
       └── Exceeded 10s   ──► Forceful Stop()
       │
       ▼
gRPC Sockets Closed
       │
       ▼
kafkaConsumer.Close()
       │
       ▼
Commit In-Flight Kafka Offsets
       │
       ▼
outboxPub finishes active batch & exits loop
       │
       ▼
wg.Wait() unblocks
       │
       ▼
defer redisClient.Close()
       │
       ▼
defer dbPool.Close()
       │
       ▼
defer appLogger.Sync()
       │
       ▼
Process Exit (Code 0)
```
