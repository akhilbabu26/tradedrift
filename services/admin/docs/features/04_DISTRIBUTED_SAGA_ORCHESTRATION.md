# Feature 04: Distributed Saga Orchestration

## 1. What This Feature Does
The **Distributed Saga Orchestration** engine guarantees eventual consistency for multi-service administrative mutations without requiring fragile distributed two-phase commits (2PC):
* **Atomic Saga Staging**: When an administrative mutation begins, a durable `admin_saga_tasks` record is committed in the same database transaction as the operation and audit log.
* **Autonomous SagaWorker**: If a downstream gRPC call (to Auth or Wallet) fails or times out, the operation remains in `PROCESSING`. A background worker continuously polls pending tasks, re-invokes downstream RPCs with exponential backoff, and marks the operation `COMPLETED` once confirmed.

---

## 2. Why We Need It
In a distributed microservice architecture, synchronous network requests across service boundaries can fail unexpectedly:
1. **The Partial Failure Dilemma**: If Admin commits an account suspension but the network times out while calling Auth, returning an error to the operator leaves the platform in an inconsistent state (Admin has an audit record, but Auth still permits logins).
2. **Crash Resilience**: Background in-memory retry queues are lost if the Admin container restarts. Sagas must be persisted to disk in PostgreSQL so retries survive process restarts.
3. **Preventing Cascading Overload**: Fixed retry loops can trigger thundering herds on recovering services. The Saga engine enforces exponential backoff with jitter and caps max retries.

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **Worker Engine** | [`services/admin/internal/service/saga_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/saga_worker.go#L35-L160) | Periodic ticker (5s), `ProcessPendingSagas`, task execution |
| **Task Repository** | [`services/admin/internal/repository/postgres/saga_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/saga_repo.go) | `GetPendingSagaTasks`, `UpdateSagaTaskStatus`, `RecordSagaFailure` |
| **Atomic Transaction**| [`services/admin/internal/repository/postgres/tx_manager.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/tx_manager.go) | Enqueues saga task inside initial operation commit |
| **Database Schema** | [`services/admin/migrations/00004_create_admin_saga_tasks.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00004_create_admin_saga_tasks.sql) | DDL defining task types, attempt counts, and `next_attempt_at` |
| **Metrics** | [`services/admin/internal/metrics/metrics.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/metrics/metrics.go#L117-L125) | `RecordSagaQueueDepth`, `RecordSagaAttempt` |

---

## 4. How We Achieve This Feature

1. **Step 1: Atomic Enqueue**:
   During `ExecAdminOperationTx`, the saga task is written with:
   - `status = 'PENDING'`
   - `attempt_count = 0`
   - `next_attempt_at = NOW()`
   - `payload_json = { "user_id": "...", "reason": "..." }`
2. **Step 2: Polling Loop (`SAGA_INTERVAL = 5s`)**:
   `SagaWorker` queries:
   ```sql
   SELECT id, operation_id, task_type, payload, attempt_count 
   FROM admin_saga_tasks
   WHERE status = 'PENDING' AND next_attempt_at <= NOW()
   ORDER BY next_attempt_at ASC
   LIMIT 50;
   ```
3. **Step 3: Execution & Result Evaluation**:
   - For `SagaTaskAuthInvalidateSessions`, calls `authClient.SuspendUser(ctx, userID, reason)`.
   - If successful:
     - Marks saga task as `COMPLETED`.
     - Updates `admin_operations` status from `PROCESSING` to `COMPLETED`.
     - Writes `user:suspended:{id} = "1"` to Redis.
   - If failed:
     - `attempt_count++`
     - Backoff calculated: `next_attempt_at = NOW() + (2^attempt_count) seconds`.
     - If `attempt_count > 10`: Marks status as `EXHAUSTED` and triggers SRE alert.

---

## 5. Execution Flow

```
                 TICK EVENT (Every 5 Seconds)
                             │
                             ▼
  ┌──────────────────────────────────────────────────────────┐
  │ 1. Query Pending Sagas from PostgreSQL                   │
  │    SELECT * FROM admin_saga_tasks                        │
  │    WHERE status = 'PENDING' AND next_attempt_at <= NOW() │
  └───────────────────────────┬──────────────────────────────┘
                              │
                   Any tasks to retry?
                              ├── NO ──► Sleep until next tick
                              │
                             └── YES
                              │
                              ▼
  ┌──────────────────────────────────────────────────────────┐
  │ 2. Inspect Task Payload & Re-Invoke Downstream RPC       │
  │    - SagaTaskAuthInvalidateSessions -> authCli.Suspend() │
  │    - SagaTaskWalletFreeze          -> walletCli.Freeze() │
  └───────────────────────────┬──────────────────────────────┘
                              │
                  Did Downstream Call Succeed?
                              │
               ┌──────────────┴──────────────┐
               ▼                             ▼
              YES                            NO
 ┌───────────────────────────┐ ┌───────────────────────────┐
 │ 3. Atomic Completion      │ │ 4. Exponential Backoff    │
 │    - Mark saga COMPLETED  │ │    - attempt_count++      │
 │    - Mark op COMPLETED    │ │    - backoff: 2^attempt s │
 │    - Update Redis state   │ │    - if > 10 attempts:    │
 └───────────────────────────┘ │      Mark EXHAUSTED &     │
                               │      Trigger SRE alert    │
                               └───────────────────────────┘
```
