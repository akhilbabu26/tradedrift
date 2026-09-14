# TradeDrift Admin Service — Architecture & Implementation Guide

The **Admin Service** is the mission-critical **control plane, supervisor, and operational telemetry hub** of the TradeDrift platform. It orchestrates high-privilege administrative actions (emergency market halts, user suspensions, wallet freezes), guarantees strict audit immutability, coordinates distributed sagas, publishes guaranteed events via the Transactional Outbox pattern, autonomously monitors platform-wide microservice health, and provides a complete incident lifecycle and analytics engine.

---

## 1. Directory Tree Overview

```
services/admin/
├── README.md                                       # This comprehensive architectural & operational manual
├── Dockerfile                                      # Multi-stage production container build (scratch/alpine base)
├── go.mod / go.sum                                 # Dependency definitions (jackc/pgx, segmentio/kafka-go, prometheus)
├── cmd/
│   └── server/
│       └── main.go                                 # Service bootstrap, dependency injection & coordinated shutdown
├── migrations/                                     # PostgreSQL relational schema and immutability triggers
│   ├── 00001_create_admin_audit_log.sql            # Append-only immutable audit trail with tamper-proof trigger
│   ├── 00002_create_admin_operations.sql           # Idempotent operations ledger with conflict recovery
│   ├── 00003_create_admin_outbox.sql               # Transactional outbox table with distributed worker leasing
│   ├── 00004_create_admin_saga_tasks.sql           # Distributed saga queue with exponential backoff & leases
│   └── 00005_create_admin_incidents.sql            # Incident tracking with partial unique index & MTTD/MTTR
├── internal/
│   ├── client/                                     # Resilient downstream gRPC client wrappers
│   │   ├── auth_client.go                          # Auth service client with retryable error classification
│   │   └── wallet_client.go                        # Wallet service client with Ping & Freeze RPCs
│   ├── config/                                     # Environment configuration loader
│   │   └── config.go                               # Strongly-typed environment variables with defaults
│   ├── domain/                                     # Pure enterprise business domain models & errors
│   │   ├── audit.go                                # Audit entry struct and action types
│   │   ├── errors.go                               # Sentinel domain errors (ErrConflict, ErrNotFound, etc.)
│   │   ├── events.go                               # Event payloads for Outbox Kafka streaming
│   │   ├── incidents.go                            # Incident model, severity enum, MTTD/MTTR semantics
│   │   ├── operations.go                           # AdminOperation state machine & status enums
│   │   ├── saga.go                                 # SagaTask model, lease tokens, and backoff calculator
│   │   └── uuid.go                                 # RFC 9562 UUIDv7 generator (time-ordered indexing)
│   ├── handler/                                    # HTTP transport layer (Go 1.22+ ServeMux)
│   │   ├── admin_handler.go                        # REST handlers for Halt, Resume, Suspend, Freeze
│   │   ├── analytics_handler.go                    # GET /analytics/operations & /analytics/risk
│   │   ├── dto.go                                  # Request and response JSON transfer objects
│   │   ├── health_handler.go                       # /health (liveness), /ready, and /system/health (cached)
│   │   ├── incident_handler.go                     # CRUD + resolution + forensic correlation endpoints
│   │   ├── middleware.go                           # JWT auth, role enforcement, panic recovery & Prometheus metrics
│   │   ├── router.go                               # URL routing table with parameterized routes
│   │   ├── topology_handler.go                     # GET /topology endpoint
│   │   └── validation.go                          # Input validators (UUIDs, symbols, reasons)
│   ├── metrics/                                    # Centralized Prometheus telemetry registry
│   │   └── metrics.go                              # Gauges, counters, histograms, route normalizer & 1-hot logic
│   ├── repository/                                 # Storage interface layer
│   │   ├── interfaces.go                           # Repository abstractions for testing & decoupling
│   │   └── postgres/                              # Production pgx/v5 PostgreSQL implementation
│   │       ├── audit_repo.go                       # Write-only append operations for audit logs
│   │       ├── incident_repo.go                    # Incident CRUD, heartbeat updates, pagination & stats
│   │       ├── operations_repo.go                  # Operations ledger with idempotency lookup & status transitions
│   │       ├── outbox_repo.go                      # Polling with FOR UPDATE SKIP LOCKED and batching
│   │       ├── saga_repo.go                        # Saga task claiming, retry scheduling, and completion
│   │       └── tx_manager.go                       # Atomic transaction manager wrapper
│   └── service/                                    # Business orchestration & background processing
│       ├── admin_service.go                        # AdminService struct, constructor, and shared helpers
│       ├── analytics_service.go                    # Operations volume analytics & heuristic risk signals
│       ├── health_probe.go                         # Transport-level probe functions (HTTP, gRPC, Kafka, Postgres)
│       ├── health_worker.go                        # Autonomous 15s health monitor lifecycle & RunProbe orchestration
│       ├── incident_service.go                     # Incident listing, pagination, forensic correlation & resolution
│       ├── incident_transitions.go                 # Transition-based incident FSM (DOWN/DEGRADED/UP handling)
│       ├── market_service.go                       # HaltMarket, ResumeMarket, ReconstructMarketState
│       ├── outbox_publisher.go                     # Background worker polling outbox & publishing to Kafka with ACKs
│       ├── saga_worker.go                          # Background worker driving distributed sagas with backoff
│       ├── topology.go                             # Platform dependency graph with HealthWorker overlay
│       ├── user_service.go                         # SuspendUser, UnsuspendUser
│       └── wallet_service.go                       # FreezeWallet, UnfreezeWallet, executeWalletMutation
└── test/                                           # Centralized test suite (31+ automated tests)
    ├── admin_service_test.go                       # Operation idempotency, conflicts, and lease loss tests
    ├── grpc_test.go                                # Downstream gRPC error classification tests
    ├── health_handler_test.go                      # Liveness, readiness, multi-broker Kafka & cached health tests
    ├── metrics_test.go                             # 1-hot invariants, sub-ms latency, and Prometheus consistency
    ├── middleware_test.go                          # Admin role validation and Idempotency-Key enforcement
    ├── phase3_intelligence_test.go                 # Incident FSM, analytics, topology, and market reconstruction
    ├── uuid_test.go                                # UUIDv7 monotonic ordering tests
    └── validation_test.go                         # Request input sanitization tests
```

---

## 2. Purpose and Problem-Solving Details by Folder

### 2.1 `cmd/server/`
> 📖 See [cmd/README.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/README.md) for complete lifecycle breakdowns.

- **Purpose**: Application composition root and entrypoint.
- [`cmd/server/main.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/server/main.go):
  - *Problem*: Microservices suffer from partial initialization and ungraceful shutdowns that corrupt in-flight database transactions.
  - *How Solved*: Sequential, fail-fast bootstrapping: Config → Logger → DB → gRPC Clients → Repositories → Services → Background Workers → HTTP Router. Includes **`ReconstructMarketState()`** on startup — queries `admin_operations` to restore Prometheus market halt gauges that would otherwise be lost across restarts.
  - 5-step graceful drainage on `SIGINT`/`SIGTERM`: (A) `/ready` → 503, (B) HTTP drain, (C) Worker stop, (D) gRPC close, (E) DB pool close.

---

### 2.2 `migrations/`
> 📖 See [migrations/README.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/README.md) for full schema breakdowns.

- [`00001_create_admin_audit_log.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00001_create_admin_audit_log.sql): Immutable audit trail via `BEFORE UPDATE OR DELETE` trigger (`RAISE EXCEPTION … ERRCODE = 'P0001'`).
- [`00002_create_admin_operations.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00002_create_admin_operations.sql): Idempotency via `UNIQUE(admin_id, idempotency_key)`.
- [`00003_create_admin_outbox.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00003_create_admin_outbox.sql): Transactional Outbox with worker lease columns and `WHERE published = FALSE` partial indexes.
- [`00004_create_admin_saga_tasks.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00004_create_admin_saga_tasks.sql): Distributed saga queue with exponential backoff columns.
- [`00005_create_admin_incidents.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00005_create_admin_incidents.sql):
  - *Problem*: Multiple `HealthWorker` probe cycles hitting a DOWN service could create duplicate open incidents.
  - *How Solved*: Enforces a **partial unique index**: `UNIQUE(service_name) WHERE status = 'OPEN'`. Only one active incident per service can exist at the database level. `mttd_seconds` is explicitly `NULL` for autonomous HealthWorker incidents (no independent failure-start timestamp is known).

---

### 2.3 `internal/client/`
- [`auth_client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/auth_client.go) & [`wallet_client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/wallet_client.go):
  - **Retryable**: `Unavailable`, `DeadlineExceeded`, `ResourceExhausted`, `Internal`, connection resets.
  - **Non-Retryable**: `InvalidArgument`, `NotFound`, `PermissionDenied`, `Unauthenticated`.

---

### 2.4 `internal/domain/`
- [`operations.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/operations.go): Operation types and status states (`PROCESSING`, `COMPLETED`, `FAILED`).
- [`incidents.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/incidents.go): `Incident` struct with `MTTDSeconds *float64` (pointer — intentionally `nil` for probe-created incidents) and `MTTRSeconds float64`. Severity enum: `P1Critical`, `P2High`, `P3Moderate`.
- [`audit.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/audit.go): Immutable audit entry.
- [`events.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/events.go): Versioned Kafka event envelope schemas.
- [`saga.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/saga.go): Exponential backoff with ±10% randomized jitter.
- [`uuid.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/uuid.go): RFC 9562 UUIDv7 — monotonic, sequential database inserts.
- [`errors.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/errors.go): Typed sentinel errors including `ErrActiveIncidentExists` for deduplication.

---

### 2.5 `internal/repository/` & `internal/repository/postgres/`
- [`interfaces.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/interfaces.go): Declares `TxManager`, `OperationsRepository`, `OutboxRepository`, `SagaRepository`, `AuditRepository`, and **`IncidentRepository`**.
- [`incident_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/incident_repo.go):
  - `Create` — inserts with partial unique index guard; translates `23505` to `ErrActiveIncidentExists`.
  - `GetActiveByService` — fetches the single open incident per service.
  - `UpdateHeartbeat` — advances `last_seen_at` and `probe_failure_count`.
  - `Resolve` — sets `resolved_at`, `mttr_seconds`, and flips status to `RESOLVED`.
  - `List` with stable pagination (`triggered_at DESC, id DESC`).
  - `GetStats` — total, open, resolved counts and `avg_mttd_seconds` / `avg_mttr_seconds`.
- [`operations_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/operations_repo.go): Includes `GetLatestMarketStates` for startup market halt reconstruction.
- [`tx_manager.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/tx_manager.go): Atomic `COMMIT`/`ROLLBACK` manager for multi-table operations.
- All polling repos use `FOR UPDATE SKIP LOCKED` for non-blocking distributed worker leasing.

---

### 2.6 `internal/service/`
> 📖 See [internal/service/01README.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/01README.md) for full orchestration flows.

The service package is split into focused files within the same Go package (`package service`):

| File | Responsibility |
|------|---------------|
| [`admin_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/admin_service.go) | `AdminService` struct, `NewAdminService`, `resolveConcurrentConflict`, `isUniqueViolation` |
| [`user_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/user_service.go) | `SuspendUser`, `UnsuspendUser` — user lifecycle with Auth saga |
| [`wallet_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/wallet_service.go) | `FreezeWallet`, `UnfreezeWallet`, `executeWalletMutation` |
| [`market_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/market_service.go) | `HaltMarket`, `ResumeMarket`, `ReconstructMarketState` |
| [`health_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/health_worker.go) | `HealthWorker` struct + lifecycle (`Start`/`Stop`/`GetLatestHealth`) + `RunProbe` orchestration |
| [`health_probe.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/health_probe.go) | `probeHTTPService`, `probeAuthGRPC`, `probeWalletGRPC`, `probePostgres`, `probeKafka`, `classifyFailureReason` |
| [`incident_transitions.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/incident_transitions.go) | `processIncidentTransitions` FSM — `handleDownTransition`, `handleSustainedDegradation`, `handleRecovery`, `outageIncidentSeverity` |
| [`incident_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/incident_service.go) | `IncidentService` — list with pagination, manual resolution, forensic correlation |
| [`analytics_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/analytics_service.go) | `AnalyticsService` — operations volume analytics & heuristic risk signal evaluation |
| [`topology.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/topology.go) | `TopologyEngine` — platform dependency graph with HealthWorker real-time overlay |
| [`outbox_publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/outbox_publisher.go) | `OutboxPublisher` — guaranteed Kafka delivery with `RequireAll` ACKs |
| [`saga_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/saga_worker.go) | `SagaWorker` — distributed saga retries with exponential backoff |

---

### 2.7 `internal/handler/`
- [`router.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/router.go): Go 1.22+ `http.ServeMux` with parameterized patterns.
- [`admin_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/admin_handler.go): `HandleSuspendUser`, `HandleUnsuspendUser`, `HandleFreezeWallet`, `HandleUnfreezeWallet`, `HandleHaltMarket`, `HandleResumeMarket`.
- [`incident_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/incident_handler.go): `GET /incidents`, `GET /incidents/{id}`, `POST /incidents/{id}/resolve`, `GET /incidents/{id}/correlated`. Uses strict `json.NewDecoder` with `DisallowUnknownFields` for resolution payloads.
- [`analytics_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/analytics_handler.go): `GET /analytics/operations?window=24h` and `GET /analytics/risk`.
- [`topology_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/topology_handler.go): `GET /topology` — live platform graph with node status and edge health.
- [`health_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/health_handler.go): `/health` (instant liveness), `/ready` (DB+gRPC+Kafka probe), `/api/v1/admin/system/health` (cached sub-ms diagnostic).
- [`middleware.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/middleware.go): JWT HMAC validation + `role=admin` enforcement, mandatory `Idempotency-Key` on mutations, panic recovery, Prometheus route normalization.

---

### 2.8 `internal/metrics/`
- [`metrics.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/metrics/metrics.go):
  - `NormalizeRoute` — strips dynamic path segments to static templates, preventing cardinality explosion.
  - **1-hot boolean invariant**: for each service × status combination, exactly one gauge = `1.0`; all other 4 states = `0.0`.
  - Market halt gauges: `tradedrift_admin_market_halted{market_id}` (binary gauge) and `tradedrift_admin_market_halt_start_timestamp_seconds{market_id}` (Unix epoch float for alerting on duration).
  - Incident worker error counter: `tradedrift_admin_incident_worker_errors_total{operation}`.

---

### 2.9 `test/`
- `admin_service_test.go`: Idempotency, concurrent collision, wallet reconciliation.
- `health_handler_test.go`: Liveness, 503 drainage, multi-broker Kafka, cached diagnostic.
- `metrics_test.go`: 1-hot invariants, sub-ms precision, worker lifecycle idempotency.
- `middleware_test.go`: JWT role enforcement, missing idempotency key.
- `grpc_test.go`: Retryable vs non-retryable gRPC error classification.
- `phase3_intelligence_test.go`: Incident FSM (DOWN→create, DEGRADED×3→create, UP→resolve, DEGRADED→DOWN heartbeat continuity), analytics (burst detection, window validation), topology (UNKNOWN edges for missing health), market halt reconstruction.
- `uuid_test.go` / `validation_test.go`: UUIDv7 monotonicity and input sanitization.

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
       ┌────────────────┼────────────────┐
       ▼                ▼                ▼
INSERT admin_ops  INSERT audit_log  INSERT admin_outbox
(COMPLETED)       (tamper-proof)   (admin.market.halted)
       │                │                │
       └────────────────┼────────────────┘
                        │
                        ▼
             tx.Commit() -> HTTP 200 OK
                        │
                        ▼
    metrics.RecordMarketHalted(marketID, now)
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

### Flow 2: Autonomous Incident Lifecycle (Service DOWN)
```
                   HealthWorker
                  (Every 15s probe)
                        │
              Service status = DOWN
                        │
                        ▼
         processIncidentTransitions()
          → handleDownTransition()
                        │
              GetActiveByService()
              ┌──────────┴──────────┐
          Exists                 None
              │                    │
              ▼                    ▼
    UpdateHeartbeat()      Create Incident
    (last_seen, count++)   (severity=P1/P2/P3)
                           (partial UQ guard)
                                   │
              Service recovers → UP
                                   │
                                   ▼
                         handleRecovery()
                                   │
                                   ▼
                     incidentRepo.Resolve()
                   (MTTR = now - triggered_at)
```

### Flow 3: Market Halt State Reconstruction on Startup
```
                     main.go startup
                          │
                          ▼
         adminSvc.ReconstructMarketState(ctx)
                          │
                          ▼
         opsRepo.GetLatestMarketStates()
         (Latest HALT_MARKET / RESUME_MARKET
          per market_id from admin_operations)
                          │
               ┌──────────┴──────────┐
           IsHalted                Resumed
               │                    │
               ▼                    ▼
  metrics.RecordMarketHalted()  metrics.RecordMarketResumed()
  (Restores halt gauge + start   (Zeros halt gauge)
   timestamp in Prometheus)
```

### Flow 4: Autonomous Health Aggregation & Sub-Millisecond Diagnosis
```
                     HealthWorker
                   (Every 15s Loop)
                          │
       ┌──────────────────┴───────────────────┐
       ▼                                      ▼
Concurrent Probes (5 goroutines)     Concurrent Backlog Stats
 probeHTTPService × 4                 outboxRepo.GetBacklogStats
 probeAuthGRPC                        sagaRepo.GetQueueStats
 probeWalletGRPC
 probePostgres
 probeKafka
       │                                      │
       └──────────────────┬───────────────────┘
                          │
                          ▼
            Compute Platform Health Score
         Postgres≠UP → UNHEALTHY
         Kafka≠UP    → DEGRADED
                          │
                          ▼
         1-Hot Gauge Updates (Prometheus)
         Store latestHealth (RWMutex)
                          │
                          ▼
    processIncidentTransitions(services, adminComponents)
```

---

## 4. Verification & Testing Strategy

```powershell
# Run full automated test suite
go test -v ./test/...

# Verify static analysis
go vet ./...

# Verify complete package compilation
go build ./...
```

All architectural patterns — from **idempotency** and **immutable audit logging** to **zero-drift Prometheus telemetry**, **non-blocking worker leasing**, and **transition-based incident tracking** — are enforced to ensure maximum reliability and operational excellence.
