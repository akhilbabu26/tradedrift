# Business Service & Background Worker Subsystem (`internal/service`)

This document provides a comprehensive architectural and operational manual for the core business orchestration and background worker daemons located in [`services/admin/internal/service/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/).

---

## Table of Contents

1. [Package Overview & Architecture](#1-package-overview--architecture)
2. [What Problems This Package Solves](#2-what-problems-this-package-solves)
3. [File-by-File Deep Dive](#3-file-by-file-deep-dive)
   - [admin_service.go (Core Business Orchestrator)](#admin_servicego-core-business-orchestrator)
   - [health_worker.go (Autonomous Platform Health Monitor)](#health_workergo-autonomous-platform-health-monitor)
   - [outbox_publisher.go (Transactional Outbox Publisher)](#outbox_publishergo-transactional-outbox-publisher)
   - [saga_worker.go (Distributed Saga Engine)](#saga_workergo-distributed-saga-engine)
4. [Critical Coordination & Reliability Patterns](#4-critical-coordination--reliability-patterns)
   - [Synchronous Fast-Path with Asynchronous Saga Fallback](#synchronous-fast-path-with-asynchronous-saga-fallback)
   - [PostgreSQL Error 23505 Conflict Recovery](#postgresql-error-23505-conflict-recovery)
   - [Worker Lifecycle Contract (Idempotent Start & Stop)](#worker-lifecycle-contract-idempotent-start--stop)
   - [Kafka RequireAll Synchronous Broker Acknowledgment](#kafka-requireall-synchronous-broker-acknowledgment)
   - [Individual Broker Timeout Budgeting](#individual-broker-timeout-budgeting)
5. [Architectural & Execution Flows](#5-architectural--execution-flows)
   - [Flow 1: User Suspension with Asynchronous Saga Handoff](#flow-1-user-suspension-with-asynchronous-saga-handoff)
   - [Flow 2: Outbox Worker Polling & Kafka ACK Delivery](#flow-2-outbox-worker-polling--kafka-ack-delivery)
   - [Flow 3: Distributed Saga Processing & Exponential Backoff](#flow-3-distributed-saga-processing--exponential-backoff)
   - [Flow 4: Autonomous Platform Health Sweep & Diagnostic Caching](#flow-4-autonomous-platform-health-sweep--diagnostic-caching)

---

## 1. Package Overview & Architecture

The `internal/service` package represents the **Core Business Logic and Background Worker Layer** of the Admin Service under Clean Architecture principles.

It contains:
1. **`AdminService`**: The primary synchronous command orchestrator that executes user suspensions, wallet freezing, and market circuit breakers.
2. **`HealthWorker`**: An autonomous daemon running every 15 seconds that concurrently probes 8 downstream dependencies, collects queue backlog statistics, updates Prometheus 1-hot boolean gauges, and caches a diagnostic snapshot for sub-millisecond API delivery.
3. **`OutboxPublisher`**: A background worker running every 1 second that polls `admin_outbox` with non-blocking leases, publishes events to Apache Kafka with synchronous `RequireAll` ACKs, and updates delivery states.
4. **`SagaWorker`**: A resilient background worker running every 5 seconds that polls `admin_saga_tasks`, retries downstream session invalidations with exponential backoff and jitter, and atomically completes operations.

---

## 2. What Problems This Package Solves

| Problem | Failure Scenario Without Service Layer | How `internal/service` Solves It |
| :--- | :--- | :--- |
| **The Dual-Write Vulnerability** | An admin suspends a user, but Kafka crashes before receiving the event. Upstream trading engines continue allowing the user to place orders. | Employs the **Transactional Outbox Pattern**: `AdminService` writes the event to `admin_outbox` in the same ACID transaction, and `OutboxPublisher` guarantees at-least-once delivery to Kafka. |
| **Cascading Downstream Outages** | Suspending a user requires calling the Auth Service. If the Auth Service is restarting, an inline synchronous call hangs the admin request and exhausts HTTP connections. | **Fast-Path with Saga Fallback**: Attempts synchronous Auth invalidation; if Auth is temporarily unavailable, `AdminService` returns HTTP 200 while leaving the task for `SagaWorker` to retry asynchronously. |
| **Race Conditions on Duplicate Requests** | Multiple requests with the same idempotency key arrive simultaneously. Both check the database, see no existing row, and both attempt to execute the action. | Catches PostgreSQL unique constraint error **`23505`** (`unique_violation`), re-reads the active record, and safely returns the existing operation without duplicate execution. |
| **Health Probing Request Timeouts** | Probing 8 microservices synchronously inside an HTTP handler takes 2 to 5 seconds, causing client timeouts. | `HealthWorker` probes autonomously in the background every 15s. HTTP requests to `/system/health` read from an in-memory cache in **< 1ms**. |
| **Thundering Herd on Recovery** | A downstream service comes back online after an outage. Thousands of queued retry tasks hammer the service at once, crashing it again. | `SagaWorker` and `OutboxPublisher` calculate **exponential backoff with full randomized jitter** ($\pm 10\%$) across 10 distinct schedule tiers. |

---

## 3. File-by-File Deep Dive

### `admin_service.go` (Core Business Orchestrator)
- **File**: [`admin_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/admin_service.go)
- **Primary Type**: `type AdminService struct`

#### Operations Breakdown

| Method | Target Entity | Transactional Actions | Downstream Side-Effects |
| :--- | :--- | :--- | :--- |
| `SuspendUser` | User Account | Inserts `admin_operations` (`PROCESSING`), `admin_audit_log`, `admin_outbox` (`admin.user-suspended.v1`), and `admin_saga_tasks`. | Fast-path calls `authCli.InvalidateUserSessions`. If successful, calls `CompleteAuthSaga`. If Auth fails, leaves task for `SagaWorker`. |
| `UnsuspendUser` | User Account | Inserts `admin_operations` (`COMPLETED`), `admin_audit_log`, and `admin_outbox` (`admin.user-unsuspended.v1`). | None. Restores user state across trading engines via Kafka outbox event. |
| `FreezeWallet` | User Asset | Inserts `admin_operations` (`COMPLETED`), `admin_audit_log`, and `admin_outbox` (`admin.wallet-frozen.v1`). | Synchronously calls `walletCli.FreezeWallet(freeze=true)` before committing the transaction. |
| `UnfreezeWallet` | User Asset | Inserts `admin_operations` (`COMPLETED`), `admin_audit_log`, and `admin_outbox` (`admin.wallet-unfrozen.v1`). | Synchronously calls `walletCli.FreezeWallet(freeze=false)` before committing the transaction. |
| `HaltMarket` | Market Order Book | Inserts `admin_operations` (`COMPLETED`), `admin_audit_log`, and `admin_outbox` (`admin.market-halted.v1`). | Emits Kafka event to halt matching engine order execution. |
| `ResumeMarket` | Market Order Book | Inserts `admin_operations` (`COMPLETED`), `admin_audit_log`, and `admin_outbox` (`admin.market-resumed.v1`). | Emits Kafka event to resume matching engine order execution. |

---

### `health_worker.go` (Autonomous Platform Health Monitor)
- **File**: [`health_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/health_worker.go)
- **Primary Type**: `type HealthWorker struct`

#### 1. Purpose
Performs continuous, non-blocking platform health surveillance and acts as the **single source of truth** for all platform health telemetry.

#### 2. Key Mechanisms
- **Concurrent 8-Way Sweeps**: Probes PostgreSQL (`dbPool.Ping`), Kafka brokers, Auth gRPC, Wallet gRPC, and 4 HTTP microservices (`trade`, `portfolio`, `liquidity_engine`, `notification`).
- **Individual Broker Timeout (1500ms)**: When probing Kafka brokers, applies a 1500ms deadline per broker, preventing an offline broker from exhausting the overall 5-second probe context.
- **Queue Backlog Telemetry**: Queries `outboxRepo.GetBacklogStats` and `sagaRepo.GetQueueStats` with a dedicated 1-second timeout.
- **Strict Overall Status Logic**:
  - `Postgres != UP` $\to$ **`UNHEALTHY`**
  - `Kafka != UP` $\to$ **`DEGRADED`**
  - Any core dependency `DOWN` $\to$ **`DEGRADED`** or **`UNHEALTHY`**
- **1-Hot Boolean Gauge Updates**: Updates `tradedrift_admin_system_health_status{service, status}` so exactly one status is `1.0` and all other 4 are `0.0`.
- **Sub-Millisecond Snapshot Cache**: Stores `SystemHealthResponse` under `sync.RWMutex`. Calls to `GetLatestHealth()` return a defensive copy in **< 1ms**.
- **Instant Shutdown**: Provides a dedicated `workerCancel()` context that cancels active in-flight network probes immediately when `Stop()` is invoked.

---

### `outbox_publisher.go` (Transactional Outbox Publisher)
- **File**: [`outbox_publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/outbox_publisher.go)
- **Primary Type**: `type OutboxPublisher struct`

#### 1. Purpose
Polls `admin_outbox` table and guarantees at-least-once streaming delivery to Apache Kafka.

#### 2. Key Mechanisms
- **Non-Blocking Batch Claiming**: Calls `outboxRepo.FetchDue(ctx, workerToken, 50)` using `FOR UPDATE SKIP LOCKED`.
- **Synchronous Broker ACKs**: Configured with `RequiredAcks: kafka.RequireAll` and `Async: false`. Only marks the database row published after Kafka confirms write to all in-sync replicas.
- **Lease Ownership Protection**: Calls `MarkPublished` and `UpdateRetry` verifying `locked_by = workerToken`. Returns `domain.ErrWorkerLeaseLost` if the lease expired during a slow write, safely discarding stale local state.
- **Progressive Backoff with Jitter**: When Kafka errors occur, calculates next attempt time using `domain.NextDelay(attempt)` with $\pm 10\%$ randomized jitter.

---

### `saga_worker.go` (Distributed Saga Engine)
- **File**: [`saga_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/saga_worker.go)
- **Primary Type**: `type SagaWorker struct`

#### 1. Purpose
Drives distributed asynchronous sagas (specifically Auth session invalidation) to completion.

#### 2. Key Mechanisms
- **Non-Blocking Task Claiming**: Calls `sagaRepo.FetchDue(ctx, workerToken, 10)` with a 5-minute lease window.
- **Smart gRPC Error Classification**: Evaluates `client.IsRetryableGRPCError(err)`:
  - *Non-Retryable Errors* (`InvalidArgument`, `NotFound`): Immediately terminates the task, marks `EXHAUSTED`, updates the operation to `FAILED`, and logs a critical alert.
  - *Retryable Errors* (`Unavailable`, `DeadlineExceeded`): Calculates progressive backoff (up to 8 hours) with jitter and schedules retry.
- **Two-Phase Atomic Completion**: On success, calls `txMgr.CompleteAuthSaga` to mark both `admin_operations` and `admin_saga_tasks` as `COMPLETED` in a single PostgreSQL transaction.

---

## 4. Critical Coordination & Reliability Patterns

### Synchronous Fast-Path with Asynchronous Saga Fallback
```
Admin Client
    │
    ▼
AdminService.SuspendUser()
    │
    ├─► 1. Commit DB Tx: [Operation=PROCESSING, Audit, Outbox, SagaTask=PENDING]
    │
    └─► 2. Fast-Path: Call authCli.InvalidateUserSessions()
             │
             ├─► SUCCESS: txMgr.CompleteAuthSaga() ──► Operation marked COMPLETED immediately!
             │
             └─► FAILURE: (Network drop / Auth restarting)
                     │
                     ▼
                 Return HTTP 200 with Status="PROCESSING"
                 SagaWorker picks up SagaTask in background and retries up to 8 hours!
```

---

### PostgreSQL Error 23505 Conflict Recovery
When two identical requests race simultaneously:
1. Request A inserts into `admin_operations` and commits.
2. Request B attempts insert and fails with PostgreSQL error `23505` (`unique_violation` on `admin_id, idempotency_key`).
3. `AdminService` intercepts error code `23505`:
   ```go
   var pgErr *pgconn.PgError
   if errors.As(err, &pgErr) && pgErr.Code == "23505" {
       existing, err := s.opsRepo.GetByIdempotencyKey(ctx, req.AdminID, req.IdempotencyKey)
       // Validates parameters match, then returns existing operation!
   }
   ```
4. Request B receives the existing operation without error.

---

### Worker Lifecycle Contract (Idempotent Start & Stop)
All workers implement strict thread-safe lifecycle control:
- **`Start(ctx)`**: Uses `startOnce.Do()` to launch exactly one background goroutine loop. Repeated calls are no-ops.
- **`Stop()`**: Uses `stopOnce.Do()` to close the `done` channel and wait for in-flight tasks via `wg.Wait()`.
- **Termination Guarantee**: Probes and tasks never leak goroutines upon process exit.

---

### Kafka RequireAll Synchronous Broker Acknowledgment
```go
writer := &kafka.Writer{
    Addr:         kafka.TCP(brokers...),
    RequiredAcks: kafka.RequireAll,
    Async:        false,
}
```
Ensures that outbox events are only marked published when **every in-sync replica** in the Kafka cluster has written the message to disk.

---

### Individual Broker Timeout Budgeting
```go
for _, broker := range h.cfg.KafkaBrokers {
    brokerCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
    conn, err := kafka.DialContext(brokerCtx, "tcp", broker)
    cancel()
    ...
}
```
If Broker 1 is dead and takes 1500ms to time out, the loop moves on to Broker 2 with remaining context budget, ensuring a single offline broker cannot cause the entire probe to fail.

---

## 5. Architectural & Execution Flows

### Flow 1: User Suspension with Asynchronous Saga Handoff

```
                      Admin Client
                           │
                           ▼
               AdminService.SuspendUser()
                           │
                           ▼
          PostgreSQL Atomic Transaction Commit
   (Operation=PROCESSING, AuditLog, Outbox, SagaTask)
                           │
                           ▼
              Fast-Path: Call Auth gRPC
                           │
        ┌──────────────────┴──────────────────┐
 (Auth Online)                         (Auth Down / Timeout)
        ▼                                     ▼
 CompleteAuthSaga(COMPLETED)          HTTP 200 OK (Status: PROCESSING)
 HTTP 200 OK (Status: COMPLETED)      (Caller Released without Blocking)
                                              │
                                              ▼
                                         SagaWorker
                                      (Background Loop)
                                              │
                                      Poll Due Saga Tasks
                                      (FOR UPDATE SKIP LOCKED)
                                              │
                                      Retry Auth Invalidation
                                              │
                                      CompleteAuthSaga(COMPLETED)
```

---

### Flow 2: Outbox Worker Polling & Kafka ACK Delivery

```
                     OutboxPublisher
                    (Every 1s Ticker)
                            │
                            ▼
          SELECT id FROM admin_outbox WHERE published=FALSE
                 FOR UPDATE SKIP LOCKED
           UPDATE locked_by=workerToken, locked_at=NOW()
                            │
                            ▼
                   Claimed Event Batch
                            │
                            ▼
               Publish to Kafka Broker
               (segmentio/kafka-go Writer)
                            │
             ┌──────────────┴──────────────┐
     (Broker ACK)                  (Broker Unreachable)
             ▼                             ▼
 MarkPublished(workerToken)     UpdateRetry(workerToken)
published=TRUE, locked_by=NULL  next_attempt=now+30s+jitter
```

---

### Flow 3: Distributed Saga Processing & Exponential Backoff

```
                        SagaWorker
                     (Every 5s Ticker)
                            │
                            ▼
            FetchDue(workerToken, limit=10)
                 FOR UPDATE SKIP LOCKED
                            │
                            ▼
           Execute Task: InvalidateUserSessions
                            │
         ┌──────────────────┴──────────────────┐
 (gRPC Unavailable / Timeout)            (InvalidArgument - 400)
         ▼                                     ▼
 IsRetryableGRPCError() == TRUE        IsRetryableGRPCError() == FALSE
         ▼                                     ▼
 UpdateRetry(workerToken)              MarkExhausted(workerToken)
 attempt_count++, delay=Progressive    Operation marked FAILED
 Next Attempt Scheduled (30s to 8h)    Dead-Letter Alert Triggered
```

---

### Flow 4: Autonomous Platform Health Sweep & Diagnostic Caching

```
                       HealthWorker
                     (Every 15s Ticker)
                            │
                            ▼
        Concurrent Health Probes (With 1.5s - 5s Timeouts)
     [DB, Kafka, Auth, Wallet, Trade, Port, Liq, Notif]
                            │
                            ▼
       Update Prometheus Registry & 1-Hot Gauges
     SetSystemOverallStatus (2=HEALTHY, 1=DEGRADED, 0=DOWN)
                            │
                            ▼
           Update in-memory latestHealth Cache
                    (Protected by RWMutex)
                            │
            ┌───────────────┴───────────────┐
            ▼                               ▼
   Prometheus Scraping           GET /api/v1/admin/system/health
     (Scrapes /metrics)            (Returns JSON Cache in < 1ms)
```
