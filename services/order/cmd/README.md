# Order Service — Executable Commands Package (`cmd`)

> **Package:** `main`  
> **Directory:** `services/order/cmd/`  
> **Role:** Entrypoint executable binary initialization & microservice wiring

---

## 1. Purpose & Architectural Role

The `cmd` package provides the main entrypoint executable for the Order Service (`cmd/server/main.go`). It coordinates process startup, environment configuration, database migrations, connection pool allocation, gRPC server binding, background Outbox worker execution, and graceful shutdown signal traps.

Key responsibilities:
1. **Startup Initialization**: Configures a 30-second startup context deadline (`startupCtx`) for running database migrations and connecting to PostgreSQL.
2. **Database Migrations**: Runs Goose SQL migrations (`postgres.RunMigrations`) on the `tradedrift_order` database.
3. **Connection Pooling**: Allocates a thread-safe `pgxpool.Pool` connection pool.
4. **Inter-Service Client**: Initializes the gRPC client transport (`wallet.NewClient`) connecting to the Wallet Service.
5. **Worker Lifecycle Management**: Spawns the background `OutboxPublisher` worker in a dedicated goroutine monitored by a `sync.WaitGroup`.
6. **Graceful Shutdown**: Traps `SIGINT` / `SIGTERM` signals to execute a clean 2-step shutdown:
   - **Step A**: Stop receiving new RPCs and drain active requests (`grpcServer.GracefulStop()`).
   - **Step B**: Signal the Outbox worker to stop (`cancelOutbox()`) and wait for goroutine completion (`wg.Wait()`).

---

## 2. Directory Structure

```
services/order/cmd/
├── README.md                            <-- This documentation file
└── server/
    └── main.go                          <-- Main executable file
```

---

## 3. Packages & Dependencies Used

| Package | Purpose & Rationale |
| :--- | :--- |
| `context` | Manages startup timeouts and background worker cancellation contexts. |
| `net` | TCP listener binding (`net.Listen("tcp", ":50053")`). |
| `os` & `os/signal` | Intercepts operating system OS signals (`SIGINT`, `SIGTERM`). |
| `sync` | `sync.WaitGroup` synchronization ensuring background goroutines exit before process termination. |
| `syscall` | System call signal constants. |
| `time` | Timestamps, durations, and shutdown timeout deadlines. |
| `go.uber.org/zap` | High-performance structured application logging. |
| `google.golang.org/grpc` | gRPC server instantiation, service registration, and graceful stop. |
| `tradedrift/platform/api/gen/order/v1` | Generated Protobuf gRPC server interface (`orderv1.RegisterOrderServiceServer`). |
| `tradedrift/platform/config` | Global environment loader (`platformconfig.LoadEnv`). |
| `tradedrift/platform/logger` | Shared Zap logger constructor (`logger.New`). |
| `tradedrift/platform/postgres` | Shared Goose migration runner and `pgxpool` constructor. |
| `tradedrift/services/order/internal/config` | Local Order Service configuration parser. |
| `tradedrift/services/order/internal/handler` | gRPC endpoint server handler (`handler.NewGRPCHandler`). |
| `tradedrift/services/order/internal/kafka/publisher` | Outbox background worker & producer (`publisher.NewOutboxPublisher`). |
| `tradedrift/services/order/internal/repository/postgres` | PostgreSQL repository pool implementation (`repoPostgres.NewOrderRepository`). |
| `tradedrift/services/order/internal/service` | Core business logic service layer (`service.NewService`). |
| `tradedrift/services/order/internal/wallet` | Infrastructure gRPC client adapter for Wallet Service (`wallet.NewClient`). |

---

## 4. Main Function Execution Flow (`main.go`)

```
   1. platformconfig.LoadEnv() & orderconfig.Load()
                          │
                          ▼
   2. Run Goose Migrations (001_create_orders.sql)
                          │
                          ▼
   3. Initialize Postgres Pool (pgxpool.Pool)
                          │
                          ▼
   4. Initialize Wallet gRPC Client
                          │
                          ▼
   5. Wire Repository -> Service -> Handler -> Kafka Producer
                          │
                          ▼
   6. Start Outbox Worker Goroutine (sync.WaitGroup)
                          │
                          ▼
   7. Start gRPC Server Listener (:50053)
                          │
                          ▼
   8. SIGINT / SIGTERM Trap -> Graceful Shutdown Sequence
```
