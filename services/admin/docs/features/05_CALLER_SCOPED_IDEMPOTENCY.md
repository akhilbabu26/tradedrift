# Feature 05: Caller-Scoped Idempotency Engine

## 1. What This Feature Does
The **Caller-Scoped Idempotency Engine** guarantees that duplicate HTTP requests submitting the same administrative operation never trigger duplicate side effects:
* **Header Requirement**: Every mutating request (`/halt`, `/resume`, `/suspend`, `/unsuspend`, `/freeze`, `/unfreeze`) requires an `Idempotency-Key` HTTP header.
* **Cached Execution Short-Circuit**: If an admin re-submits a request with an existing key, the service immediately returns the cached database record without touching PostgreSQL transactions, Redis keys, or downstream gRPC services.
* **Tenant/Admin Isolation**: Idempotency keys are scoped strictly to the specific administrator who submitted them (`UNIQUE(admin_id, idempotency_key)`).

---

## 2. Why We Need It
In a high-stakes financial control plane, non-idempotent endpoints cause catastrophic side effects:
1. **Network Retry Duplication**: If an operator clicks "Halt Market" and the response packet drops due to transient packet loss, the client or browser will automatically retry. Without idempotency, this would generate multiple duplicate audit records and conflicting saga tasks.
2. **Double-Spend & Race Hazards**: In wallet and user management, duplicate execution can race against in-flight transactions, creating inconsistent state transitions.
3. **Deterministic State Recovery**: If an operation is interrupted during network timeout, retrying with the same key allows safe in-flight reconciliation rather than failing with duplicate key errors.

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **Ingress Middleware** | [`services/admin/internal/handler/middleware.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/middleware.go#L85-L105) | `RequireIdempotencyKey` asserts presence and length (1-128 chars) |
| **Database Constraint**| [`services/admin/migrations/00002_create_admin_operations.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00002_create_admin_operations.sql#L27) | `CONSTRAINT uq_admin_operation_idempotency UNIQUE (admin_id, idempotency_key)` |
| **Repository Lookup** | [`services/admin/internal/repository/postgres/operations_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/operations_repo.go#L60-L85) | `GetByAdminIdempotencyKey` fetches cached record and response payload |
| **Market Service** | [`services/admin/internal/service/market_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/market_service.go#L42-L58) | Short-circuits if operation exists |
| **User Service** | [`services/admin/internal/service/user_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/user_service.go#L37-L53) | Short-circuits if operation exists |
| **Wallet Service** | [`services/admin/internal/service/wallet_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/wallet_service.go#L52-L68) | Short-circuits if operation exists |

---

## 4. How We Achieve This Feature

1. **Header Validation**:
   The `RequireIdempotencyKey` middleware checks:
   - Header exists and is not empty.
   - String length is between 1 and 128 characters.
   - Injects the key into `req.Context()`.
2. **Lookup Prior to Mutation**:
   The domain service calls `repo.GetByAdminIdempotencyKey(ctx, adminID, idempotencyKey)`.
3. **Execution Branching**:
   - **Scenario A: Record Exists & `status == 'COMPLETED'`**:
     Deserializes stored `response_payload` JSON directly and returns `HTTP 200 OK`. Zero database writes, zero Redis calls, zero gRPC calls.
   - **Scenario B: Record Exists & `status == 'PROCESSING'`**:
     Re-evaluates in-flight reconciliation to complete downstream RPC without re-creating outbox events.
   - **Scenario C: Record Not Found**:
     Proceeds to execute `ExecAdminOperationTx()`, inserting the row. If a concurrent duplicate slips through, PostgreSQL's `UNIQUE` constraint raises a conflict error, preventing race conditions.

---

## 5. Execution Flow

```
                                 INCOMING MUTATING REQUEST
                                             │
                                             ▼
                             ┌───────────────────────────────┐
                             │ RequireIdempotencyKey Header? │
                             └───────────────┬───────────────┘
                                             │
                              ┌──────────────┴──────────────┐
                              │ Missing / Invalid Length    │ Valid (1-128 chars)
                              ▼                             ▼
                ┌───────────────────────────┐ ┌───────────────────────────┐
                │ HTTP 400 Bad Request      │ │ Injected into req.Context │
                │ "Idempotency-Key required"│ └─────────────┬─────────────┘
                └───────────────────────────┘               │
                                                            ▼
                                              ┌───────────────────────────┐
                                              │ Check (admin_id, key)     │
                                              │ in admin_operations       │
                                              └─────────────┬─────────────┘
                                                            │
                                             ┌──────────────┴──────────────┐
                                             ▼                             ▼
                                        Record Found?                 New Record
                                             │                             │
                              ┌──────────────┴──────────────┐              ▼
                              │                             │ ┌─────────────────────────┐
                              ▼                             ▼ │ Begin DB Transaction    │
                     status == COMPLETED          status == PROCESSING│ INSERT INTO admin_ops   │
                              │                             │ │ (UNIQUE constraint)     │
                              ▼                             ▼ └────────────┬────────────┘
                ┌───────────────────────────┐ ┌─────────────────────────┐  │
                │ Return Cached ResponseDTO │ │ In-Flight Reconciler:   │  │
                │ (<1ms latency)            │ │ Re-invoke gRPC or await │  │
                │ Zero downstream side-effs │ │ Saga background worker  │  │
                └───────────────────────────┘ └─────────────────────────┘  ▼
                                                              ┌─────────────────────────┐
                                                              │ Perform Downstream Ops  │
                                                              │ Commit COMPLETED & DTO  │
                                                              └─────────────────────────┘
```
