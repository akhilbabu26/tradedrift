# Admin Service Internal Architecture & Subsystems Manual

This document details the **internal package structure, design patterns, dependency layers, and data flows** within `services/admin/internal/`.

---

## 1. Internal Package Hierarchy & Layered Architecture

The Admin Service follows **Clean / Hexagonal Architecture** principles, enforcing strict one-way dependency boundaries:

```
┌──────────────────────────────────────────────────────────────────────────────────┐
│                            TRANSPORT LAYER (handler)                             │
│  HTTP Router (Go 1.22+ ServeMux) • Middlewares (JWT, Idempotency, Metrics, Panic)│
│  REST Handlers (admin_handler.go, health_handler.go) • DTOs & Validators         │
└────────────────────────────────────────┬─────────────────────────────────────────┘
                                         │ Calls
                                         ▼
┌──────────────────────────────────────────────────────────────────────────────────┐
│                             SERVICE LAYER (service)                              │
│  AdminService (Business Orchestration & Atomic Unit of Work)                     │
│  HealthWorker (15s Autonomous Probing, Caching, 1-Hot Metrics)                   │
│  OutboxPublisher (Guaranteed Kafka Delivery with Synchronous ACKs)               │
│  SagaWorker (Distributed Side-Effect Execution with Exponential Backoff)         │
└────────────┬───────────────────────────┬───────────────────────────┬─────────────┘
             │ Calls                     │ Emits Telemetry           │ Invokes RPCs
             ▼                           ▼                           ▼
┌──────────────────────────┐┌──────────────────────────┐┌──────────────────────────┐
│ REPOSITORY (repository)  ││    METRICS (metrics)     ││     CLIENT (client)      │
│ Interfaces & Postgres    ││ Prometheus Registry,     ││ Downstream gRPC Clients  │
│ TxManager (Atomic Tx)    ││ Bounded Cardinality,     ││ (Auth & Wallet with      │
│ SKIP LOCKED Leases       ││ 1-Hot Boolean Invariants ││ Smart Error Classifier)  │
└────────────┬─────────────┘└──────────────────────────┘└──────────────────────────┘
             │ Persists Entities
             ▼
┌──────────────────────────────────────────────────────────────────────────────────┐
│                             DOMAIN LAYER (domain)                                │
│  Entities (AdminOperation, AuditLogEntry, SagaTask, OutboxEvent)                 │
│  Value Objects (UUIDv7, Reason, Symbols) • Domain Errors (ErrConflict, etc.)     │
└──────────────────────────────────────────────────────────────────────────────────┘
```

### Dependency Rules:
1. **`domain`** is completely pure. It imports zero internal packages and has no external framework dependencies.
2. **`repository`** defines interfaces for persistence; **`repository/postgres`** implements them using `jackc/pgx/v5`.
3. **`service`** coordinates business logic via repository interfaces and downstream clients. It knows nothing about HTTP.
4. **`handler`** adapts incoming HTTP JSON requests to domain commands, delegating work to `service`.
5. **`metrics`** is cross-cutting, offering thread-safe metric updates consumed by middleware and background workers.
6. **`client`** encapsulates downstream gRPC transport connections and smart retry classifications.
7. **`config`** provides strongly typed, immutable runtime configurations.

---

## 2. Package-by-Package Deep Dive

### 2.1 `internal/domain/`
> 📖 **Comprehensive Domain Guide**: See [internal/domain/README.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/README.md) for complete state machine specifications, progressive backoff schedules, and UUIDv7 indexing rationale.

- **Purpose**: Houses pure business models, state machines, and invariants.
- **Files**:
  - [`operations.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/operations.go):
    - *Problem*: Administrative mutations need a deterministic lifecycle and typed representation.
    - *How Solved*: Defines `AdminOperation` and canonical constants:
      - Types: `OperationHaltMarket`, `OperationResumeMarket`, `OperationSuspendUser`, `OperationUnsuspendUser`, `OperationFreezeWallet`, `OperationUnfreezeWallet`.
      - States: `OperationPending` $\to$ `OperationCompleted` or `OperationFailed`.
  - [`audit.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/audit.go):
    - *Problem*: Compliance regulations mandate recording who performed an action, against what target, and why.
    - *How Solved*: Defines `AuditLogEntry` capturing `AdminID`, `Action`, `TargetResource`, `Reason`, `ClientIP`, `UserAgent`, and timestamps.
  - [`events.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/events.go):
    - *Problem*: Event payloads published to Kafka must follow a strict, versioned JSON schema.
    - *How Solved*: Defines schemas for `MarketHaltedEvent`, `MarketResumedEvent`, `UserSuspendedEvent`, `UserUnsuspendedEvent`, `WalletFrozenEvent`, and `OutboxEvent`.
  - [`saga.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/saga.go):
    - *Problem*: Distributed asynchronous actions (e.g. revoking Auth sessions) need retry scheduling with randomized backoff.
    - *How Solved*: Defines `SagaTask` (`PENDING`, `RETRYING`, `COMPLETED`, `EXHAUSTED`) and implements exponential backoff with full jitter:
      $$\text{Backoff} = \min(\text{MaxBackoff}, \text{InitialBackoff} \times 2^{\text{attempt}}) \pm \text{RandomJitter}$$
      This eliminates thundering herds when a downstream dependency recovers.
  - [`uuid.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/uuid.go):
    - *Problem*: Standard random UUIDv4 causes severe B-tree index fragmentation in PostgreSQL on high-throughput inserts.
    - *How Solved*: Implements RFC 9562 **UUIDv7** generation: embeds a 48-bit UNIX millisecond timestamp at the beginning of the 128-bit identifier, ensuring strictly monotonic index clustering and optimal database write performance.
  - [`errors.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/errors.go):
    - *Problem*: Transport layers shouldn't inspect raw database error strings.
    - *How Solved*: Exports typed sentinel domain errors: `ErrConflict`, `ErrNotFound`, `ErrValidation`, `ErrUnauthorized`, `ErrInternal`.

---

### 2.2 `internal/repository/` & `internal/repository/postgres/`
> 📖 **Comprehensive Repository Guide**: See [internal/repository/README.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/README.md) for full interface specifications, multi-table atomic transactions, SKIP LOCKED worker leases, and fencing token protection.

- **Purpose**: Data access layer managing database transactions, locks, and worker leases.
- **Files**:
  - [`interfaces.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/interfaces.go): Declares `TxManager`, `OperationsRepository`, `OutboxRepository`, `SagaRepository`, and `AuditRepository` interfaces.
  - [`tx_manager.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/tx_manager.go):
    - *Problem*: Ensuring multi-table atomic operations commit together or roll back on error.
    - *How Solved*: Implements `RunInTx(ctx, fn)`. If `fn` returns an error or panics, it automatically issues `ROLLBACK` and restores connection pool state; otherwise, it executes `COMMIT`.
  - [`operations_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/operations_repo.go):
    - *Problem*: Concurrent requests with identical idempotency keys must not create duplicate records.
    - *How Solved*: Uses `INSERT ... ON CONFLICT (idempotency_key) DO NOTHING` and provides `GetByIdempotencyKey`.
  - [`outbox_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/outbox_repo.go):
    - *Problem*: In a horizontally scaled deployment, multiple admin worker replicas must poll unpublished events without processing the same record or deadlocking each other.
    - *How Solved*: Uses PostgreSQL's non-blocking row-level lock:
      ```sql
      SELECT id, event_type, topic, payload FROM admin_outbox
      WHERE published_at IS NULL AND (leased_until IS NULL OR leased_until < NOW())
      ORDER BY created_at ASC LIMIT $1
      FOR UPDATE SKIP LOCKED;
      ```
      The leased rows are stamped with a unique `worker_token` and `leased_until = NOW() + LeaseDuration`.
  - [`saga_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/saga_repo.go):
    - *Problem*: Saga tasks must be leased and retried safely across distributed worker instances.
    - *How Solved*: Implements `FetchDue` with `FOR UPDATE SKIP LOCKED` where `status IN ('PENDING', 'RETRYING') AND next_attempt_at <= NOW()`. Provides `MarkCompleted`, `UpdateRetry`, and `MarkExhausted`.
  - [`audit_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/audit_repo.go):
    - *Problem*: Audit log writes must never be blocked by operational tables.
    - *How Solved*: Appends records directly into the trigger-protected `admin_audit_log` table.

---

### 2.3 `internal/service/`
> 📖 **Comprehensive Service & Workers Guide**: See [internal/service/README.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/README.md) for full orchestration flows, fast-path/saga fallbacks, autonomous health loops, and outbox publisher mechanics.

- **Purpose**: Houses business logic and continuous background processing engines.
- **Files**:
  - [`admin_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/admin_service.go):
    - *Problem*: Executing administrative actions requires an atomic unit of work across operations, audit logs, outbox events, and distributed saga tasks.
    - *How Solved*:
      1. Wraps operations in `TxManager.RunInTx`.
      2. Handles concurrent idempotency key collisions: if an insertion conflict occurs, it loads the existing operation, verifies parameters, and returns the existing result safely.
      3. For market halts/resumes: enqueues an outbox event for Kafka broadcast to Trading and Matching Engines.
      4. For user suspensions: enqueues an outbox event AND queues a Saga task to asynchronously revoke active JWT sessions in the Auth microservice.
      5. For wallet freezing: enqueues an outbox event AND invokes downstream Wallet gRPC to lock balances.
  - [`health_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/health_worker.go):
    - *Problem*: Real-time health probing of multiple services inside HTTP request handlers causes cascading latency spikes and timeouts if a downstream dependency hangs.
    - *How Solved*:
      1. Runs an autonomous 15-second background loop.
      2. Concurrently probes HTTP services (`trade`, `portfolio`, `liquidity_engine`, `notification`), gRPC transports (`auth`, `wallet`), database pool (`postgres`), and Kafka brokers.
      3. Probes Kafka with individual broker timeouts (1500ms), ensuring a dead broker does not consume the overall probe budget.
      4. Concurrently collects Outbox and Saga backlog statistics within a dedicated 1-second timeout.
      5. Enforces explicit status semantics:
         - `Postgres` $\ne$ `UP` $\to$ Overall status **`UNHEALTHY`**
         - `Kafka` $\ne$ `UP` $\to$ Overall status **`DEGRADED`**
      6. Enforces the strict 1-hot boolean invariant across all 5 states: `UP`, `DOWN`, `DEGRADED`, `TIMEOUT`, `UNKNOWN`.
      7. Caches the diagnostic report in memory (`sync.RWMutex`). HTTP requests to `/system/health` read the defensive copy in **< 1ms**.
      8. Features idempotent `Start()` and `Stop()`, with instant probe context cancellation on shutdown.
  - [`outbox_publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/outbox_publisher.go):
    - *Problem*: Dual-write vulnerability: database updates succeed, but Kafka message is lost due to crash before publishing.
    - *How Solved*: Polls leased outbox records and writes to Kafka using `kafka-go.Writer` with `RequiredAcks: RequireAll` (synchronous replica acknowledgment). Only marks `published_at = NOW()` after Kafka confirms the write. If Kafka fails, backs off and increments `tradedrift_admin_outbox_retries_total`.
  - [`saga_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/saga_worker.go):
    - *Problem*: Downstream side-effects (e.g. invalidating tokens in Auth service) can fail due to transient network drops.
    - *How Solved*: Polls due saga tasks, executes the action, and checks `IsRetryableGRPCError(err)`. On retryable failure, calculates exponential backoff with jitter and reschedules. If max attempts are reached, marks `EXHAUSTED` and triggers Prometheus critical alerts.

---

### 2.4 `internal/client/`
> 📖 **Comprehensive Client Guide**: See [internal/client/README.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/README.md) for complete details on RPC error classification, distributed metadata injection, and health probing.

- **Purpose**: Manages outbound gRPC communication to Auth and Wallet services.
- **Files**:
  - [`auth_client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/auth_client.go):
    - Connects to Auth service gRPC.
    - Provides `Ping(ctx)` for liveness probes via TCP socket and connection state.
    - Provides `InvalidateUserSessions(ctx, userID, reason, requestID, operationID)` for saga task execution with metadata injection.
    - Implements `IsRetryableGRPCError(err)` to categorize transient network errors (`Unavailable`, `DeadlineExceeded`, `ResourceExhausted`) vs permanent validation errors (`InvalidArgument`, `NotFound`).
  - [`wallet_client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/wallet_client.go):
    - Connects to Wallet service gRPC.
    - Provides `Ping(ctx)` via native application `Health()` RPC and `FreezeWallet(ctx, userID, asset, reason, requestID, operationID, freeze)`.

---

### 2.5 `internal/handler/`
> 📖 **Comprehensive Transport & Handlers Guide**: See [internal/handler/README.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/README.md) for full endpoint specifications, route normalization mechanisms, and middleware pipelines.

- **Purpose**: HTTP transport layer, input validation, and routing.
- **Files**:
  - [`router.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/router.go): Configures Go 1.22+ `http.ServeMux` with static and parameterized routes (`/api/v1/admin/markets/{market_id}/halt`).
  - [`admin_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/admin_handler.go): Handles HTTP endpoints for market and user operations, mapping domain results to JSON responses.
  - [`health_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/health_handler.go):
    - `/health`: Liveness probe (returns 200 OK if process is running).
    - `/ready`: Readiness probe checking PostgreSQL, Auth, Wallet, and all configured Kafka brokers (with fallback). Returns 503 during graceful drainage.
    - `/api/v1/admin/system/health`: Serves the cached platform diagnostic report directly from `HealthWorker` with zero on-demand probing.
  - [`middleware.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/middleware.go):
    - `RequireAdmin`: Validates JWT HMAC signature and enforces `role == "admin"`.
    - `RequireIdempotencyKey`: Enforces mandatory `Idempotency-Key` header on all mutation endpoints.
    - `Recoverer`: Recovers from unhandled panics, logs stack traces, and returns HTTP 500 cleanly.
    - `MetricsMiddleware`: Measures duration and normalizes route labels for Prometheus.
  - [`validation.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/validation.go) & [`dto.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/dto.go): Validates market IDs (e.g. `BTC-USDT`), user IDs, assets, and minimum reason lengths.

---

### 2.6 `internal/metrics/`
> 📖 **Comprehensive Metrics Guide**: See [internal/metrics/README.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/metrics/README.md) for the full metric catalog, 1-hot boolean gauge invariants, and PromQL alerting queries.

- **Purpose**: Centralized Prometheus instrumentation engine.
- **Files**:
  - [`metrics.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/metrics/metrics.go):
    - *What Problem It Solves*:
      1. **Cardinality Explosion**: Dynamic URL parameters (like `/users/usr_123e4567-e89b-12d3-a456-426614174000/suspend`) create millions of unique metric series, crashing Prometheus.
      2. **Metric Inconsistencies**: Ad-hoc metric naming breaks alert rules and Grafana dashboards.
      3. **Boolean Gauge Ambiguity**: Storing a single gauge that flips values can leave stale states in Prometheus.
    - *How Solved*:
      1. `NormalizeRoute(r)` converts dynamic URL paths to static templates (`/api/v1/admin/users/{user_id}/suspend`).
      2. Enforces a **1-hot boolean gauge** for `tradedrift_admin_system_health_status{service, status}`: when a status updates, the target state is set to `1.0` and all other 4 states are set to `0.0`.
      3. Records exact probe latencies using `time.Duration.Seconds()`, preventing sub-millisecond truncation.
      4. Tracks in-flight operations with paired `RecordOperationStart` / `RecordOperationComplete`.

---

### 2.7 `internal/config/`
> 📖 **Comprehensive Config Guide**: See [internal/config/README.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/config/README.md) for environment variables tables, 12-Factor precedence rules, and Kafka parsing sanitization.

- **Purpose**: Environment configuration loading.
- **Files**:
  - [`config.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/config/config.go):
    - Loads environment variables (`PORT`, `POSTGRES_DSN`, `KAFKA_BROKERS`, `JWT_SECRET`, downstream service URLs) with sensible fallback defaults.
    - Provides parsing utilities like `SplitKafkaBrokers()` to sanitize and extract multiple brokers.

---

## 3. Core Internal Execution Flows

### Flow 1: High-Privilege Administrative Mutation
```
                      Administrator Request
               (POST /api/v1/admin/markets/BTC-USDT/halt)
                                 │
                                 ▼
                     HTTP Router & Middleware
      (StructuredLogging -> Metrics -> RequireAdmin -> Idempotency)
                                 │
                                 ▼
                    AdminHandler.HandleHaltMarket()
                    (ValidateMarketID, ValidateReason)
                                 │
                                 ▼
                    AdminService.HaltMarket()
                                 │
                                 ▼
                     TxManager.RunInTx() (ACID)
                                 │
         ┌───────────────────────┼───────────────────────┐
         ▼                       ▼                       ▼
INSERT admin_operations   INSERT admin_audit_log   INSERT admin_outbox
 (idempotency_key, PEND)    (forensic immutability) (admin.market.halted)
         │                       │                       │
         └───────────────────────┼───────────────────────┘
                                 │
                                 ▼
                    UPDATE admin_operations (COMPLETED)
                                 │
                                 ▼
                         tx.Commit()
                                 │
                                 ▼
                    HTTP 200 OK (OperationDTO)
```

---

### Flow 2: Asynchronous Outbox & Saga Processing
```
        Outbox Loop (Every 1s)                 Saga Loop (Every 1s)
                  │                                      │
                  ▼                                      ▼
         OutboxPublisher.FetchDue()              SagaWorker.FetchDue()
        (FOR UPDATE SKIP LOCKED)                (FOR UPDATE SKIP LOCKED)
                  │                                      │
                  ▼                                      ▼
         Produce Kafka Message                  Call Auth gRPC Revoke
        (Acks: RequireAll)                               │
                  │                       ┌──────────────┴──────────────┐
                  ▼                (RPC Succeeded)             (Unavailable / Timeout)
         Broker ACK Confirmed             ▼                             ▼
                  │              MarkCompleted(taskID)       UpdateRetry(taskID)
                  ▼                       │                  attempt++, next_delay
         MarkPublished(eventID)           ▼                             │
                  │              metrics.RecordSagaComplete             ▼
                  ▼                                          Exponential Backoff
         metrics.RecordOutboxPublish                         (30s to 8h)
```

---

### Flow 3: Continuous Health Aggregation & Sub-Millisecond Diagnostics
```
                       HealthWorker
                     (Every 15s Loop)
                            │
        ┌───────────────────┴───────────────────┐
        ▼                                       ▼
 Concurrent Downstream Probes            Concurrent Backlog Stats
  [Trade, Portfolio, Liq, Notif]          Outbox backlog depth & oldest age
  [Auth & Wallet gRPC, Postgres]          Saga queue counts by status
  [Kafka brokers: 1.5s timeout]                 │
        │                                       │
        └───────────────────┬───────────────────┘
                            │
                            ▼
               RecordHealthProbe (Prometheus)
              SetSystemOverallStatus (2, 1, 0)
                            │
                            ▼
              Store in latestHealth Cache
                 (Protected by RWMutex)
                            │
            ┌───────────────┴───────────────┐
            ▼                               ▼
     Prometheus Scraping        GET /api/v1/admin/system/health
       (Scrapes /metrics)          (Serves Cache in < 1ms)
```

---

## 4. Verification & Testing Strategy

All internal packages are verified via the centralized test suite in `services/admin/test/`:
```powershell
# Run all automated tests
go test -v ./services/admin/test/...

# Verify package compilation & static analysis
go vet ./services/admin/...
go build ./services/admin/...
```
The architecture strictly guarantees **zero data races**, **bounded memory and label cardinality**, **ACID transactional atomicity**, and **high-availability operational telemetry**.
