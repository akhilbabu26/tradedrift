# Repository & Persistence Subsystem (`internal/repository`)

This document provides a comprehensive architectural and operational manual for the persistence and data access layer located in [`services/admin/internal/repository/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/).

---

## Table of Contents

1. [Package Overview & Architecture](#1-package-overview--architecture)
2. [What Problems This Package Solves](#2-what-problems-this-package-solves)
3. [File-by-File Deep Dive](#3-file-by-file-deep-dive)
   - [interfaces.go (Abstract Data Access Contracts)](#interfacesgo-abstract-data-access-contracts)
   - [postgres/tx_manager.go (Atomic Transaction Coordinator)](#postgrestx_managergo-atomic-transaction-coordinator)
   - [postgres/operations_repo.go (Operations State & Idempotency)](#postgresoperations_repogo-operations-state--idempotency)
   - [postgres/audit_repo.go (Append-Only Audit Log)](#postgresaudit_repogo-append-only-audit-log)
   - [postgres/outbox_repo.go (Transactional Outbox & Worker Leases)](#postgresoutbox_repogo-transactional-outbox--worker-leases)
   - [postgres/saga_repo.go (Distributed Saga Queue & Retries)](#postgressaga_repogo-distributed-saga-queue--retries)
4. [Key Concurrency & Reliability Patterns](#4-key-concurrency--reliability-patterns)
   - [Non-Blocking Worker Polling with SKIP LOCKED](#non-blocking-worker-polling-with-skip-locked)
   - [Worker Lease Fencing Tokens (`locked_by`)](#worker-lease-fencing-tokens-locked_by)
   - [Atomic Unit of Work (Four-Way Coordinated Inserts)](#atomic-unit-of-work-four-way-coordinated-inserts)
   - [Two-Phase Atomic Saga Resolution](#two-phase-atomic-saga-resolution)
5. [Architectural & Execution Flows](#5-architectural--execution-flows)
   - [Flow 1: Atomic Unit of Work Mutation Flow](#flow-1-atomic-unit-of-work-mutation-flow)
   - [Flow 2: Non-Blocking Outbox Worker Polling & Publishing](#flow-2-non-blocking-outbox-worker-polling--publishing)
   - [Flow 3: Worker Lease Expiration & Fencing Rejection Flow](#flow-3-worker-lease-expiration--fencing-rejection-flow)
   - [Flow 4: Coordinated Two-Phase Saga Completion Flow](#flow-4-coordinated-two-phase-saga-completion-flow)
6. [SQL Performance & Indexing Highlights](#6-sql-performance--indexing-highlights)

---

## 1. Package Overview & Architecture

The `internal/repository` package implements the **Data Access Layer** for the TradeDrift Admin Service according to Clean/Hexagonal Architecture principles.

It is split into two distinct tiers:
1. **Root Interface Tier ([`interfaces.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/interfaces.go))**: Defines pure Go interfaces (`TxManager`, `OperationsRepository`, `OutboxRepository`, `SagaRepository`, `AuditRepository`) and operational telemetry DTOs. Business services depend exclusively on these interfaces, completely decoupling domain logic from database drivers.
2. **PostgreSQL Adapter Tier ([`postgres/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/))**: Provides the production PostgreSQL implementation utilizing high-performance connection pooling (`jackc/pgx/v5/pgxpool`).

```
┌────────────────────────────────────────────────────────────────────────┐
│                        DOMAIN & SERVICE LAYER                          │
│               AdminService • OutboxPublisher • SagaWorker              │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ Invokes
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                    REPOSITORY CONTRACTS (interfaces.go)                │
│   TxManager • OperationsRepository • OutboxRepository • SagaRepository │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ Implemented By
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                   POSTGRESQL ADAPTER (postgres/)                       │
│    tx_manager.go • operations_repo.go • outbox_repo.go • saga_repo.go  │
│    Uses jackc/pgx/v5, ReadCommitted Transactions, SKIP LOCKED Leases   │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. What Problems This Package Solves

| Problem | Failure Scenario Without This Subsystem | How `internal/repository` Solves It |
| :--- | :--- | :--- |
| **Split-Brain Mutations (The Dual-Write Bug)** | Admin operation record is saved, but system crashes before writing outbox events or audit logs, leaving Kafka unaware and audit trails missing. | `TxManager.ExecAdminOperationTx` executes all 4 inserts (`admin_audit_log`, `admin_operations`, `admin_outbox`, `admin_saga_tasks`) in **one atomic ACID transaction**. |
| **Worker Contention & Deadlocks** | Multiple horizontally scaled worker instances poll the database for pending events, locking the entire table or causing serialization deadlocks. | Implements **`SELECT ... FOR UPDATE SKIP LOCKED`**, allowing worker pods to claim distinct batches simultaneously with zero blocking. |
| **Stale Worker Race Conditions (Zombies)** | A slow worker pauses due to GC or network delay, loses its lease to another pod, and later wakes up and marks the event as completed, overwriting the new worker's work. | Implements **Optimistic Lease Fencing (`locked_by = workerToken`)**: queries assert lease ownership and return `ErrWorkerLeaseLost` if the lease expired. |
| **Saga Re-Execution Race Conditions** | A Saga task finishes via gRPC, but before the database marks it complete, the background worker claims the task again and issues a duplicate session revocation. | `TxManager.CompleteAuthSaga` atomically updates both the `admin_operation` and `admin_saga_task` to `COMPLETED` in a single transaction. |
| **Tightly Coupled SQL Code** | Domain services write raw SQL queries directly, making it impossible to mock the database during unit testing. | Service layers depend solely on mockable Go interfaces declared in `interfaces.go`. |

---

## 3. File-by-File Deep Dive

### `interfaces.go` (Abstract Data Access Contracts)
- **File**: [`interfaces.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/interfaces.go)

#### Key Interfaces & Signatures

```go
type TxManager interface {
    ExecAdminOperationTx(ctx context.Context, req AdminOperationTxRequest) (*AdminOperationTxResult, error)
    CompleteAuthSaga(ctx context.Context, opID string, sagaID string, responseBody []byte) error
}

type OperationsRepository interface {
    GetByIdempotencyKey(ctx context.Context, adminID, key string) (*domain.AdminOperation, error)
    GetByID(ctx context.Context, id string) (*domain.AdminOperation, error)
    Insert(ctx context.Context, op *domain.AdminOperation) error
    UpdateStatus(ctx context.Context, id string, status domain.OperationStatus, responseBody []byte) error
}

type OutboxRepository interface {
    Insert(ctx context.Context, event *domain.OutboxEvent) error
    FetchDue(ctx context.Context, workerToken string, limit int) ([]*domain.OutboxEvent, error)
    MarkPublished(ctx context.Context, id string, workerToken string) error
    UpdateRetry(ctx context.Context, id string, workerToken string, nextAttemptAt time.Time, attemptCount int, lastError string) error
    GetBacklogStats(ctx context.Context) (*OutboxBacklogStats, error)
}

type SagaRepository interface {
    Insert(ctx context.Context, task *domain.SagaTask) error
    FetchDue(ctx context.Context, workerToken string, limit int) ([]*domain.SagaTask, error)
    UpdateRetry(ctx context.Context, id string, workerToken string, nextAttemptAt time.Time, attemptCount int, lastError string) error
    MarkCompleted(ctx context.Context, id string, workerToken string) error
    MarkExhausted(ctx context.Context, id string, workerToken string, lastError string) error
    GetQueueStats(ctx context.Context) (*SagaQueueStats, error)
}
```

---

### `postgres/tx_manager.go` (Atomic Transaction Coordinator)
- **File**: [`tx_manager.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/tx_manager.go)
- **Primary Type**: `type txManager struct`

#### 1. Purpose
Coordinates multi-table atomic mutations inside a single PostgreSQL transaction with `ReadCommitted` isolation level.

#### 2. Key Methods
- **`ExecAdminOperationTx(ctx, req)`**:
  - Begins transaction: `tx, err := t.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})`.
  - Step 1: `insertAuditLogTx` (strictly append-only).
  - Step 2: `insertOperationTx` (enforces unique idempotency constraint).
  - Step 3: `insertOutboxEventTx` (with default leasing fields).
  - Step 4: `insertSagaTaskTx` (conditional if `req.SagaTask != nil`).
  - Commits transaction: `tx.Commit(ctx)`. Rolls back on any error.
- **`CompleteAuthSaga(ctx, opID, sagaID, responseBody)`**:
  - Atomically marks `admin_operations.status = 'COMPLETED'` and `admin_saga_tasks.status = 'COMPLETED'` in a single transaction, clearing locks and setting `completed_at = NOW()`.

---

### `postgres/operations_repo.go` (Operations State & Idempotency)
- **File**: [`operations_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/operations_repo.go)
- **Primary Type**: `type operationsRepo struct`

#### 1. Purpose
Manages reads, writes, and status transitions for `admin_operations`.

#### 2. Key Methods
- **`GetByIdempotencyKey(ctx, adminID, key)`**: Looks up existing operations for a specific caller. Returns `nil, nil` if `pgx.ErrNoRows` is encountered.
- **`GetByID(ctx, id)`**: Retrieves operation by UUIDv7.
- **`Insert(ctx, op)`**: Standalone insert outside of an atomic transaction (used in unit tests and recovery procedures).
- **`UpdateStatus(ctx, id, status, responseBody)`**: Transitions status (`PROCESSING`, `COMPLETED`, `FAILED`) and updates cached JSON response payload.

---

### `postgres/audit_repo.go` (Append-Only Audit Log)
- **File**: [`audit_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/audit_repo.go)
- **Primary Type**: `type auditRepo struct`

#### 1. Purpose
Provides standalone write capabilities to `admin_audit_log`.

#### 2. Key Methods
- **`Insert(ctx, log)`**: Serializes `log.Metadata` to JSONB and executes `INSERT INTO admin_audit_log`. Any attempt to update or delete these rows later is blocked by database triggers with SQL state `P0001`.

---

### `postgres/outbox_repo.go` (Transactional Outbox & Worker Leases)
- **File**: [`outbox_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/outbox_repo.go)
- **Primary Type**: `type outboxRepo struct`

#### 1. Purpose
Powers the transactional outbox publisher with worker lease locking and queue telemetry.

#### 2. Key Methods
- **`FetchDue(ctx, workerToken, limit)`**:
  - Atomically claims pending rows whose retry time has passed (`next_attempt_at <= NOW()`) and whose lease is unheld or expired (`locked_at IS NULL OR locked_at < NOW() - INTERVAL '2 minutes'`).
  - Uses `FOR UPDATE SKIP LOCKED` inside a subquery `UPDATE`.
  - Sets `locked_by = workerToken`, `status = 'PROCESSING'`, and `locked_at = NOW()`.
- **`MarkPublished(ctx, id, workerToken)`**:
  - Sets `published = TRUE`, `status = 'PUBLISHED'`, and clears locks `WHERE id = $1 AND locked_by = $2`.
  - If `RowsAffected == 0`, returns `domain.ErrWorkerLeaseLost`.
- **`UpdateRetry(ctx, id, workerToken, nextAttemptAt, attemptCount, lastError)`**:
  - Advances attempt count and calculates next attempt timestamp. Transitions to `'FAILED'` if max attempts are reached. Verifies `locked_by = workerToken`.
- **`GetBacklogStats(ctx)`**:
  - Executes `SELECT COUNT(*), COALESCE(EXTRACT(EPOCH FROM (NOW() - MIN(created_at))), 0) WHERE published = FALSE`.
  - Returns current pending count and age of the oldest unpublished message for Prometheus metrics.

---

### `postgres/saga_repo.go` (Distributed Saga Queue & Retries)
- **File**: [`saga_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/saga_repo.go)
- **Primary Type**: `type sagaRepo struct`

#### 1. Purpose
Powers the asynchronous saga worker driving multi-step distributed operations (e.g. Auth session revocations).

#### 2. Key Methods
- **`FetchDue(ctx, workerToken, limit)`**:
  - Claims tasks with `status IN ('PENDING', 'RETRYING')` where `next_attempt_at <= NOW()` and lease expired (`locked_at < NOW() - INTERVAL '5 minutes'`).
  - Uses `FOR UPDATE SKIP LOCKED` to prevent concurrent worker pod collisions.
- **`UpdateRetry(ctx, id, workerToken, nextAttemptAt, attemptCount, lastError)`**:
  - Sets `status = 'RETRYING'` and records `last_error` while clearing worker locks.
- **`MarkCompleted(ctx, id, workerToken)`**:
  - Transitions task to terminal `COMPLETED` state.
- **`MarkExhausted(ctx, id, workerToken, lastError)`**:
  - Transitions task to terminal `EXHAUSTED` state after all 10 attempts fail.
- **`GetQueueStats(ctx)`**:
  - Aggregates task counts partitioned by status (`PENDING`, `RETRYING`, `EXHAUSTED`) in a single query.

---

## 4. Key Concurrency & Reliability Patterns

### Non-Blocking Worker Polling with SKIP LOCKED
```sql
UPDATE admin_outbox
SET locked_at = NOW(), locked_by = $1, status = 'PROCESSING'
WHERE id IN (
    SELECT id FROM admin_outbox
    WHERE published = FALSE
      AND next_attempt_at <= NOW()
      AND (locked_at IS NULL OR locked_at < NOW() - INTERVAL '2 minutes')
    ORDER BY next_attempt_at ASC
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
```
- **Why?** Multiple worker pods can execute this query concurrently. `SKIP LOCKED` skips rows currently held by other workers rather than waiting in a queue.
- **Result**: Zero row lock contention, linear horizontal scaling, and zero deadlocks.

---

### Worker Lease Fencing Tokens (`locked_by`)
Every batch poll assigns a unique `workerToken` (UUIDv4/UUIDv7) to claimed rows. When updating completion or retry status:
```sql
UPDATE admin_outbox
SET published = TRUE, ...
WHERE id = $1 AND locked_by = $2
```
If a worker takes longer than 2 minutes, another worker re-claims the row with a new token. When the original worker finally finishes:
1. `RowsAffected` evaluates to `0`.
2. The repository returns `domain.ErrWorkerLeaseLost`.
3. The original worker safely discards its state without overwriting the new worker's claim.

---

### Atomic Unit of Work (Four-Way Coordinated Inserts)
```
BEGIN TRANSACTION;
  1. INSERT INTO admin_audit_log (...)
  2. INSERT INTO admin_operations (...)
  3. INSERT INTO admin_outbox (...)
  4. INSERT INTO admin_saga_tasks (...)  [optional]
COMMIT;
```
If any insert violates a constraint (e.g. duplicate idempotency key) or fails due to network disconnect, the entire transaction is rolled back. No orphaned outbox events or untracked audit logs can ever be created.

---

## 5. Architectural & Execution Flows

### Flow 1: Atomic Unit of Work Mutation Flow

```
                         AdminService
                              │
                              ▼
                 TxManager.ExecAdminOperationTx()
                              │
                      BEGIN TRANSACTION
                              │
         ┌────────────────────┼────────────────────┐
         ▼                    ▼                    ▼
INSERT admin_audit_log  INSERT admin_ops   INSERT admin_outbox
 (Trigger Prevents        (Enforces Unique     (published=FALSE)
  Tampering/Edits)         Idempotency)            │
         │                    │                    ▼
         │                    │            [Optional Saga Task]
         │                    │           INSERT admin_saga_tasks
         │                    │                    │
         └────────────────────┼────────────────────┘
                              │
                              ▼
                      COMMIT TRANSACTION
                              │
                (All 4 Entities Persisted or
                 Rolled Back Automatically)
```

---

### Flow 2: Non-Blocking Outbox Worker Polling & Publishing

```
                      OutboxPublisher
                      (1s Polling Loop)
                              │
                              ▼
              FetchDue(workerToken, limit=50)
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
                     Publish to Kafka
                              │
               ┌──────────────┴──────────────┐
       (Broker ACK)                  (Network Error)
               ▼                             ▼
   MarkPublished(workerToken)     UpdateRetry(workerToken)
  published=TRUE, locked_by=NULL  attempt+1, next_attempt=delay
```

---

### Flow 3: Worker Lease Expiration & Fencing Rejection Flow

```
    Worker 1 (Pod 1)                              Worker 2 (Pod 2)
           │                                             │
   Claims Outbox Row                                     │
 (locked_by="token-1")                                   │
           │                                             │
   [Worker 1 Hangs / GC]                                 │
           │                                             │
   [Lease Expires after 2m]                              │
           │                                      Claims Stale Row
           │                                    (locked_by="token-2")
           │                                             │
           │                                      Publishes to Kafka
           │                                    (locked_by=NULL, pub=TRUE)
           │                                             │
   Worker 1 Wakes Up!                                    │
   UPDATE admin_outbox                                   │
   WHERE locked_by="token-1"                             │
           │                                             │
     RowsAffected == 0                                   │
           │                                             │
           ▼                                             │
  ErrWorkerLeaseLost                                     ▼
 (Fenced Off Safely!)                           Normal Completion
```

---

### Flow 4: Coordinated Two-Phase Saga Completion Flow

```
                         SagaWorker
                              │
                              ▼
             AuthClient.InvalidateUserSessions()
                              │
                     (Auth RPC Succeeded)
                              │
                              ▼
                 TxManager.CompleteAuthSaga()
                              │
                      BEGIN TRANSACTION
                              │
        ┌─────────────────────┴─────────────────────┐
        ▼                                           ▼
 UPDATE admin_operations                    UPDATE admin_saga_tasks
  status='COMPLETED'                         status='COMPLETED'
  response_body=jsonBytes                    completed_at=NOW()
  updated_at=NOW()                           locked_by=NULL
        │                                           │
        └─────────────────────┬─────────────────────┘
                              │
                              ▼
                      COMMIT TRANSACTION
           (Both Saga and Operation Completed Atomically)
```

---

## 6. SQL Performance & Indexing Highlights

1. **Partial Index for Outbox Polling**:
   ```sql
   CREATE INDEX idx_admin_outbox_due ON admin_outbox(next_attempt_at ASC)
   WHERE published = FALSE;
   ```
   *Indexes only unhandled messages. Even with 50,000,000 historical rows, the index size remains tiny and queries complete in < 1ms.*

2. **Partial Index for Saga Task Polling**:
   ```sql
   CREATE INDEX idx_saga_tasks_pending ON admin_saga_tasks(next_attempt_at ASC)
   WHERE status IN ('PENDING', 'RETRYING');
   ```
   *Ensures workers scan only active, due tasks, completely ignoring millions of historical completed records.*

3. **Composite Idempotency Constraint**:
   ```sql
   CONSTRAINT uq_admin_operation_idempotency UNIQUE (admin_id, idempotency_key);
   ```
   *Guarantees zero duplicate operations per caller directly at the database engine level.*
