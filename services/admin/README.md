# TradeDrift Admin Service Architecture & Implementation Guide

The **Admin Service** is the mission-critical **control plane, supervisor, and operational telemetry hub** of the TradeDrift platform. It orchestrates high-privilege administrative actions (emergency market halts, user suspensions, wallet freezes), guarantees strict audit immutability, coordinates distributed sagas, publishes guaranteed events via the Transactional Outbox pattern, and autonomously monitors platform-wide microservice health.

---

## 1. Directory Tree Overview

```
services/admin/
├── README.md                                  # This comprehensive architectural & operational manual
├── Dockerfile                                 # Multi-stage production container build (scratch/alpine base)
├── go.mod / go.sum                            # Dependency definitions (jackc/pgx, segmentio/kafka-go, prometheus)
├── cmd/
│   └── server/
│       └── main.go                            # Service bootstrap, dependency injection & coordinated shutdown
├── migrations/                                # PostgreSQL relational schema and immutability triggers
│   ├── 00001_create_admin_audit_log.sql       # Append-only immutable audit trail with tamper-proof trigger
│   ├── 00002_create_admin_operations.sql      # Idempotent operations ledger with conflict recovery
│   ├── 00003_create_admin_outbox.sql          # Transactional outbox table with distributed worker leasing
│   └── 00004_create_admin_saga_tasks.sql      # Distributed saga queue with exponential backoff & leases
├── internal/
│   ├── client/                                # Resilient downstream gRPC client wrappers
│   │   ├── auth_client.go                     # Auth service client with retryable error classification
│   │   └── wallet_client.go                   # Wallet service client with Ping & Freeze RPCs
│   ├── config/                                # Environment configuration loader
│   │   └── config.go                          # Strongly-typed environment variables with defaults
│   ├── domain/                                # Pure enterprise business domain models & errors
│   │   ├── audit.go                           # Audit entry struct and action types
│   │   ├── errors.go                          # Sentinel domain errors (ErrConflict, ErrNotFound, etc.)
│   │   ├── events.go                          # Event payloads for Outbox Kafka streaming
│   │   ├── operations.go                      # AdminOperation state machine & status enums
│   │   ├── saga.go                            # SagaTask model, lease tokens, and backoff calculator
│   │   └── uuid.go                            # RFC 9562 UUIDv7 generator (time-ordered indexing)
│   ├── handler/                               # HTTP transport layer (Go 1.22+ ServeMux)
│   │   ├── admin_handler.go                   # REST handlers for Halt, Resume, Suspend, Freeze
│   │   ├── dto.go                             # Request and response JSON transfer objects
│   │   ├── health_handler.go                  # /health (liveness), /ready, and /system/health (cached)
│   │   ├── middleware.go                      # JWT auth, role enforcement, panic recovery & Prometheus metrics
│   │   ├── router.go                          # URL routing table with parameterized routes
│   │   └── validation.go                      # Input validators (UUIDs, symbols, reasons)
│   ├── metrics/                               # Centralized Prometheus telemetry registry
│   │   └── metrics.go                         # Gauges, counters, histograms, route normalizer & 1-hot logic
│   ├── repository/                            # Storage interface layer
│   │   ├── interfaces.go                      # Repository abstractions for testing & decoupling
│   │   └── postgres/                          # Production pgx/v5 PostgreSQL implementation
│   │       ├── audit_repo.go                  # Write-only append operations for audit logs
│   │       ├── operations_repo.go             # Operations ledger with idempotency lookup & status transitions
│   │       ├── outbox_repo.go                 # Polling with FOR UPDATE SKIP LOCKED and batching
│   │       ├── saga_repo.go                   # Saga task claiming, retry scheduling, and completion
│   │       └── tx_manager.go                  # Atomic transaction manager wrapper
│   └── service/                               # Business orchestration & background processing
│       ├── admin_service.go                   # Core business logic: Halt, Resume, Suspend, Freeze, Reconcile
│       ├── health_worker.go                   # Autonomous 15s health monitor, cache & 1-hot gauge updater
│       ├── outbox_publisher.go                # Background worker polling outbox & publishing to Kafka with ACKs
│       └── saga_worker.go                     # Background worker driving distributed sagas with backoff
└── test/                                      # Centralized test suite (37 automated tests)
    ├── admin_service_test.go                  # Operation idempotency, conflicts, and lease loss tests
    ├── grpc_test.go                           # Downstream gRPC error classification tests
    ├── health_handler_test.go                 # Liveness, readiness, multi-broker Kafka & cached health tests
    ├── metrics_test.go                        # 1-hot invariants, sub-ms latency, and Prometheus consistency
    ├── middleware_test.go                     # Admin role validation and Idempotency-Key enforcement
    ├── uuid_test.go                           # UUIDv7 monotonic ordering tests
    └── validation_test.go                     # Request input sanitization tests
```

---

## 2. Purpose and Problem-Solving Details by Folder

### 2.1 `cmd/server/`
> 📖 **Comprehensive Entrypoint Guide**: See [cmd/README.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/README.md) for complete lifecycle breakdowns, fail-fast bootstrapping, and 5-step graceful drainage flows.

- **Purpose**: Application composition root and entrypoint.
- **Files**:
  - [`cmd/server/main.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/server/main.go)
    - *What Problem It Solves*: Microservices often suffer from race conditions on startup (partial initialization) and ungraceful shutdowns (severing in-flight database transactions or dropping HTTP requests).
    - *How It Solves It*:
      1. Performs sequential, fail-fast bootstrapping: Config $\to$ Logger $\to$ DB Connection Pool $\to$ Downstream gRPC Clients $\to$ Repositories $\to$ Core Services $\to$ Background Workers (`SagaWorker`, `OutboxPublisher`, `HealthWorker`) $\to$ HTTP Router.
      2. Coordinates a **5-step graceful drainage** upon receiving `SIGINT`/`SIGTERM`:
         - **Step A**: Immediately transitions `/ready` to HTTP 503 (`isShuttingDown = true`), causing upstream load balancers to cease traffic routing.
         - **Step B**: Drains in-flight HTTP connections with a 10-second timeout (`srv.Shutdown()`).
         - **Step C**: Stops background workers (`HealthWorker.Stop()`, `SagaWorker.Stop()`, `OutboxPublisher.Stop()`) using idempotent `sync.Once` and active probe context cancellation.
         - **Step D**: Gracefully closes downstream gRPC client connections (`authCli`, `walletCli`).
         - **Step E**: Closes the PostgreSQL connection pool (`dbPool.Close()`).
    - *Why We Need It*: Ensures zero dropped requests and zero corrupted transactions during deployments or container restarts.

---

### 2.2 `migrations/`
> 📖 **Comprehensive Migrations Manual**: See [migrations/README.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/README.md) for full schema breakdowns, PL/pgSQL triggers, partial index optimizations, and sequence flows.

- **Purpose**: Defines PostgreSQL relational DDL schemas with strict data integrity guarantees.
- **Files**:
  - [`00001_create_admin_audit_log.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00001_create_admin_audit_log.sql):
    - *Problem*: Compliance regulations in financial systems demand that audit trails can never be altered or purged, even by a database administrator.
    - *How Solved*: Creates `admin_audit_log` with an immutable PostgreSQL trigger function `trg_enforce_audit_immutability()` that executes `BEFORE UPDATE OR DELETE` and raises an uncatchable exception: `RAISE EXCEPTION 'admin_audit_log entries are strictly immutable and append-only' USING ERRCODE = 'P0001'`.
  - [`00002_create_admin_operations.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00002_create_admin_operations.sql):
    - *Problem*: Network timeouts can cause operators to resend mutations (e.g. Halt Market), risking double executions or state inconsistencies.
    - *How Solved*: Creates `admin_operations` with a `UNIQUE(admin_id, idempotency_key)` constraint and state machine columns (`PENDING`, `PROCESSING`, `COMPLETED`, `FAILED`).
  - [`00003_create_admin_outbox.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00003_create_admin_outbox.sql):
    - *Problem*: Dual-write problem: updating a database and publishing to Kafka cannot be done in a single atomic transaction without 2-Phase Commit.
    - *How Solved*: Stores outbound Kafka events in `admin_outbox` within the **same atomic database transaction** as the business operation. Includes worker lease columns (`locked_at`, `locked_by`) and partial indexes (`WHERE published = FALSE`) for ultra-fast polling.
  - [`00004_create_admin_saga_tasks.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00004_create_admin_saga_tasks.sql):
    - *Problem*: Asynchronous multi-step operations (e.g. revoking auth tokens in external services) can fail halfway through.
    - *How Solved*: Creates `admin_saga_tasks` with exponential backoff columns (`attempt_count`, `next_attempt_at`, `max_attempts`, `status`) to power distributed saga execution.
- *Why We Need It*: Enforces foundational ACID guarantees and legal immutability at the database level.

---

### 2.3 `internal/client/`
- **Purpose**: Handles outbound gRPC transport to downstream microservices with smart error handling.
- **Files**:
  - [`internal/client/auth_client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/auth_client.go) & [`internal/client/wallet_client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/wallet_client.go):
    - *Problem*: Downstream gRPC errors vary wildly (transient network drops vs. permanent validation failures). Retrying permanent errors wastes CPU, while failing on transient errors aborts operations unnecessarily.
    - *How Solved*: Implements [`IsRetryableGRPCError(err)`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/auth_client.go#L44):
      - **Retryable**: `codes.Unavailable`, `codes.DeadlineExceeded`, `codes.ResourceExhausted`, `codes.Internal`, network connection resets.
      - **Non-Retryable (Permanent)**: `codes.InvalidArgument`, `codes.NotFound`, `codes.PermissionDenied`, `codes.Unauthenticated`.
      - Provides transport `Ping(ctx)` methods with deadline enforcement for liveness checking.
    - *Why We Need It*: Prevents transient network glitches from permanently failing administrative sagas.

---

### 2.4 `internal/config/`
- **Purpose**: Environment configuration loading and validation.
- **Files**:
  - [`internal/config/config.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/config/config.go):
    - *Problem*: Hardcoded configuration values lead to environment drift and secret leaks.
    - *How Solved*: Reads environment variables (`PORT`, `DATABASE_URL`, `KAFKA_BROKERS`, `JWT_SECRET`, downstream service URLs) with production fallbacks and parsing helpers (e.g. `SplitKafkaBrokers()`).
    - *Why We Need It*: Facilitates 12-factor cloud-native container execution.

---

### 2.5 `internal/domain/`
- **Purpose**: Core business domain logic, data structures, and errors completely decoupled from frameworks or transport layers.
- **Files**:
  - [`operations.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/operations.go): Defines operation types (`halt_market`, `resume_market`, `suspend_user`, `unsuspend_user`, `freeze_wallet`, `unfreeze_wallet`) and transition states (`PENDING`, `COMPLETED`, `FAILED`).
  - [`audit.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/audit.go): Defines `AuditLogEntry` capturing `AdminID`, `Action`, `TargetResource`, `Reason`, `ClientIP`, and `UserAgent`.
  - [`events.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/events.go): Event payload definitions published to Kafka topics (`admin.market.halted`, `admin.user.suspended`, etc.).
  - [`saga.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/saga.go): Defines `SagaTask` and calculates exponential backoff with full jitter to avoid the "thundering herd" problem on downstream services.
  - [`uuid.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/uuid.go): Implements RFC 9562 **UUIDv7** generation (combining millisecond timestamp and cryptographic entropy). This ensures sequential database B-Tree index inserts, avoiding the massive random I/O fragmentation caused by standard UUIDv4.
  - [`errors.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/errors.go): Standardized domain errors (`ErrConflict`, `ErrNotFound`, `ErrUnauthorized`, `ErrValidation`).

---

### 2.6 `internal/repository/` & `internal/repository/postgres/`
- **Purpose**: Data access layer managing persistence, row locking, and transactions.
- **Files**:
  - [`interfaces.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/interfaces.go): Repository contracts enabling pure unit testing with mocks.
  - [`tx_manager.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/tx_manager.go): Implements `RunInTx(ctx, fn)` with automatic `COMMIT` on success and `ROLLBACK` on error or panic.
  - [`operations_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/operations_repo.go): Manages operation records with `ON CONFLICT (idempotency_key) DO NOTHING`.
  - [`outbox_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/outbox_repo.go): Implements non-blocking distributed worker leasing using `SELECT ... FOR UPDATE SKIP LOCKED`. Multiple admin replicas can poll outbox events without locking each other or processing duplicates.
  - [`saga_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/saga_repo.go): Implements `FetchDue` using `FOR UPDATE SKIP LOCKED` to lease due saga tasks, and tracks transition states (`PENDING`, `RETRYING`, `COMPLETED`, `EXHAUSTED`).
  - [`audit_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/audit_repo.go): Append-only audit logger.

---

### 2.7 `internal/service/`
- **Purpose**: Core business orchestration and asynchronous worker routines.
- **Files**:
  - [`admin_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/admin_service.go):
    - *Problem*: Orchestrating multi-table atomic operations (e.g. record operation + write immutable audit + insert outbox event + insert saga task) while handling concurrent requests with the same idempotency key.
    - *How Solved*: Implements atomic transactions via `TxManager`. If a concurrent collision occurs, reads the existing operation and safely returns it or reports progress.
  - [`health_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/health_worker.go):
    - *Problem*: Evaluating downstream microservice availability synchronously in incoming HTTP requests causes high latency and cascade failures if a dependency hangs.
    - *How Solved*: An autonomous background worker running on a 15s cadence:
      1. Concurrently probes downstream HTTP services, gRPC endpoints, PostgreSQL, and Kafka with per-broker/service timeouts.
      2. Concurrently collects Outbox and Saga backlog statistics.
      3. Classifies timeouts as `TIMEOUT` and unconfigured endpoints as `UNKNOWN`.
      4. Calculates overall platform status (`HEALTHY`, `DEGRADED`, `UNHEALTHY`) and Admin status.
      5. Updates 1-hot boolean Prometheus gauges and sub-millisecond latencies.
      6. Caches the result in memory (`sync.RWMutex`). Subsequent `/system/health` requests are served in **< 1 millisecond**.
      7. Features idempotent `Start()` and `Stop()`, plus instant probe cancellation on shutdown.
  - [`outbox_publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/outbox_publisher.go):
    - *Problem*: Ensuring events are published to Kafka without message loss or double publishing.
    - *How Solved*: Polls leased outbox events, writes to Kafka with `RequiredAcks: RequireAll` (synchronous broker ACK), and marks records `published_at = NOW()` only upon receiving the broker ACK. If Kafka is down, backs off and increments retry metrics.
  - [`saga_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/saga_worker.go):
    - *Problem*: Asynchronous side effects (like invalidating sessions in the Auth microservice) can fail due to network blips.
    - *How Solved*: Claims tasks with leases, executes the action, and on transient error calculates exponential backoff with jitter (`next_attempt_at`). If `attempt_count >= max_attempts`, marks `EXHAUSTED` and fires Prometheus alert.

---

### 2.8 `internal/handler/`
- **Purpose**: HTTP routing, serialization, and middleware enforcement.
- **Files**:
  - [`router.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/router.go): Sets up standard Go 1.22 `http.ServeMux` with parameterized pattern matching (`POST /api/v1/admin/markets/{market_id}/halt`).
  - [`admin_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/admin_handler.go): Parses requests, validates `Idempotency-Key` headers, invokes `AdminService`, and returns structured JSON responses.
  - [`health_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/health_handler.go):
    - `/health`: Instant process liveness check (200 OK).
    - `/ready`: Readiness check probing DB, Auth, Wallet, and all configured Kafka brokers. Returns 503 during graceful drainage.
    - `/api/v1/admin/system/health`: Serves the cached platform diagnostic from `HealthWorker` in sub-millisecond time with zero on-demand probing.
  - [`middleware.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/middleware.go):
    - Enforces JWT cryptographic signature and verifies `role == "admin"`.
    - Enforces mandatory `Idempotency-Key` header on mutations.
    - Protects against panics using defer/recover (returns HTTP 500 cleanly).
    - Measures request duration and normalizes route labels for Prometheus.
  - [`validation.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/validation.go) & [`dto.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/dto.go): Request parameter validation (reason length, UUID syntax, asset symbols).

---

### 2.9 `internal/metrics/`
- **Purpose**: Centralized Prometheus instrumentation and metric registration.
- **Files**:
  - [`metrics.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/metrics/metrics.go):
    - *Problem*: High-cardinality labels (UUIDs, user IDs) crash Prometheus TSDB. Inconsistent metric naming leads to broken alerts and broken Grafana panels.
    - *How Solved*:
      1. Normalizes all routes to static template strings (`/api/v1/admin/users/{user_id}/suspend`), stripping dynamic IDs.
      2. Implements a strict **1-hot boolean invariant** for component health:
         $$\text{UP} + \text{DEGRADED} + \text{DOWN} + \text{TIMEOUT} + \text{UNKNOWN} = 1.0$$
         Exactly one gauge is set to `1.0`; all other 4 states are set to `0.0`.
      3. Preserves nanosecond/sub-millisecond latency precision in histograms and gauges (`latency.Seconds()`).
      4. Centralizes all metric definitions under the `tradedrift_admin_*` namespace.

---

### 2.10 `test/`
- **Purpose**: Comprehensive unit, regression, and integration testing suite.
- **Files**:
  - `admin_service_test.go`: Tests idempotent execution, concurrent conflict handling, and lease safety.
  - `health_handler_test.go`: Tests liveness, 503 readiness drainage, multi-broker Kafka fallback, and cached system health responses.
  - `metrics_test.go`: Tests 1-hot boolean invariants, sub-millisecond latency preservation, worker start/stop idempotency, probe overlap serialization, Kafka/Postgres degradation logic, and Prometheus metric registry consistency.
  - `middleware_test.go`: Tests admin role enforcement, invalid JWTs, and missing idempotency keys.
  - `grpc_test.go`: Tests retryable vs non-retryable gRPC error categorization.
  - `uuid_test.go` & `validation_test.go`: Tests UUIDv7 monotonicity and input sanitization.

---

## 3. End-to-End Operational Flows

### Flow 1: Administrative Mutation (Market Halt) with Transactional Outbox
```
                      Admin Operator
                            │
                            ▼
              POST /api/v1/admin/markets/BTC-USDT/halt
      (RequireAdmin JWT + RequireIdempotencyKey Middleware)
                            │
                            ▼
                AdminService.HaltMarket()
                            │
                            ▼
           PostgreSQL ACID Transaction (pgxpool)
                            │
         ┌──────────────────┼──────────────────┐
         ▼                  ▼                  ▼
INSERT admin_ops   INSERT admin_audit   INSERT admin_outbox
(status='PENDING') (tamper-proof log)   (admin.market.halted)
         │                  │                  │
         └──────────────────┼──────────────────┘
                            │
                            ▼
             UPDATE admin_ops (status='COMPLETED')
                            │
                            ▼
                 tx.Commit() -> HTTP 200 OK
                            │
                            ▼
                 OutboxPublisher (Every 1s)
              SELECT FOR UPDATE SKIP LOCKED
                            │
                            ▼
                 Produce to Apache Kafka
                  (Acks: RequireAll)
                            │
                            ▼
                Broker ACK -> Published=TRUE
```

---

### Flow 2: Distributed Saga Coordination (User Suspension)
```
                      Admin Operator
                            │
                            ▼
            POST /api/v1/admin/users/usr_123/suspend
                            │
                            ▼
               AdminService.SuspendUser()
                            │
                            ▼
           PostgreSQL ACID Transaction (pgxpool)
  (Insert Operation, AuditLog, Outbox, and SagaTask)
                            │
                            ▼
        HTTP 200 OK (Account Suspended Locally)
                            │
                            ▼
                 SagaWorker (Every 1s)
         SELECT FOR UPDATE SKIP LOCKED
                            │
                            ▼
            Call Auth gRPC: RevokeUserSessions
                            │
        ┌───────────────────┴───────────────────┐
 (RPC Success)                           (RPC Error)
        ▼                                       ▼
  MarkCompleted(taskID)                  IsRetryableGRPCError?
        │                                 /                 \
        ▼                           (Yes)/                   \(No / Max Attr)
  Saga COMPLETED                        ▼                     ▼
                               UpdateRetry(taskID)      MarkExhausted(taskID)
                               Exponential Backoff      Prometheus Alert Fires
                               (30s to 8h)
```

---

### Flow 3: Autonomous Health Aggregation & Sub-Millisecond Diagnosis
```
                       HealthWorker
                     (Every 15s Loop)
                            │
        ┌───────────────────┴───────────────────┐
        ▼                                       ▼
 Concurrent Downstream Probes            Concurrent Backlog Stats
  [Trade, Portfolio, Liq, Notif]          Query Outbox backlog depth
  [Auth & Wallet gRPC, Postgres]          Query Saga queue counts
  [Kafka brokers: 1.5s timeout]                 │
        │                                       │
        └───────────────────┬───────────────────┘
                            │
                            ▼
               Compute Platform Health Score
            (HEALTHY = 2, DEGRADED = 1, DOWN = 0)
                            │
                            ▼
               RecordHealthProbe (Prometheus)
              Update 1-Hot Boolean Status Gauges
                            │
                            ▼
              Store in latestHealth Cache
                 (Protected by RWMutex)
                            │
            ┌───────────────┴───────────────┐
            ▼                               ▼
     Prometheus Scraping        GET /api/v1/admin/system/health
       (Scrapes /metrics)          (Returns In-Memory JSON < 1ms)
```

---

## 4. Verification & Testing Strategy

The Admin Service maintains a 100% passing test suite across all subsystems:

```powershell
# Run full automated test suite (37 tests)
go test -v ./services/admin/test/...

# Verify static analysis & formatting
go vet ./services/admin/...

# Verify complete workspace package compilation
go build ./services/admin/...
```

All architectural patterns—from **idempotency** and **immutable audit logging** to **zero-drift Prometheus telemetry** and **non-blocking worker leasing**—are rigorously enforced to ensure maximum reliability and operational excellence.
