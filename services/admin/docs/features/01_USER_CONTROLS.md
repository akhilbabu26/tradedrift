# Feature 01: User Controls (Suspension & Unsuspension)

## 1. What This Feature Does
The **User Controls** subsystem provides platform operators with the authority to immediately suspend or restore access for any user account across TradeDrift.
* **Suspend User (`POST /api/v1/admin/users/{user_id}/suspend`)**: Immediately blocks an abusive, compromised, or AML-flagged account from accessing the API Gateway, invalidates their active sessions in the Auth Service, and stops new order submissions.
* **Unsuspend User (`POST /api/v1/admin/users/{user_id}/unsuspend`)**: Restores the user account to `ACTIVE` status in the Auth Service and purges the blacklist key from Redis, allowing normal authentication and trading to resume.

---

## 2. Why We Need It
In a digital asset exchange, compromised accounts, fraudulent deposits, AML/KYC alerts, or security anomalies require **instant cluster-wide neutralization**:
1. **Cryptographic JWT Invalidation Gap**: Access JWTs are stateless and cannot be recalled from client storage once signed. Without an external fast-path check, a suspended user could continue executing trades until token expiry (e.g. 15–60 minutes).
2. **Blast Radius Containment**: An administrative lockout must propagate to edge API Gateways in sub-milliseconds while maintaining eventual consistency across downstream persistence layers.
3. **Audit Compliance**: Financial regulations require an immutable paper trail explaining *who* suspended the user, *when*, and *why* (with required reason strings).

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Types |
| :--- | :--- | :--- |
| **HTTP Transport** | [`services/admin/internal/handler/admin_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/admin_handler.go#L23-L101) | `HandleSuspendUser`, `HandleUnsuspendUser` |
| **Input Validation** | [`services/admin/internal/handler/validation.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/validation.go#L21-L36) | `ValidateUserID` (UUID parser), `ValidateReason` (5-500 chars) |
| **Service Layer** | [`services/admin/internal/service/user_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/user_service.go#L34-L260) | `SuspendUser`, `UnsuspendUser`, Redis write/delete with retries |
| **Auth gRPC Client** | [`services/admin/internal/client/auth_client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/auth_client.go#L44-L90) | `SuspendUser`, `UnsuspendUser` RPC calls |
| **Downstream Auth** | [`services/auth/internal/service/suspension.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/auth/internal/service/suspension.go) | Updates `users.status = 'SUSPENDED'`, increments `token_version` |
| **Gateway Enforcement**| [`services/gateway/internal/middleware/auth.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/middleware/auth.go) | Real-time `EXISTS user:suspended:{id}` Redis guard |

---

## 4. How We Achieve This Feature

1. **Atomic State Commit**:
   Inside an atomic PostgreSQL transaction managed by `TxManager`, Admin persists:
   - `admin_operations` record (`status = 'PROCESSING'`).
   - `admin_audit_log` entry recording the operator's admin ID, remote IP, and reason.
   - `admin_outbox` event (`admin.user-suspended.v1`).
   - `admin_saga_tasks` record for retry resiliency.
2. **Downstream RPC Coordination**:
   Admin calls `AuthClient.SuspendUser(ctx, userID, reason)`. Auth updates `users.status = 'SUSPENDED'` and increments `token_version`.
3. **Hot-Path Redis Blacklisting**:
   Admin synchronously sets `user:suspended:{userID} = "1"` in Redis with 3 exponential backoff retries (50ms, 150ms, 300ms).
4. **Edge Gateway Interception**:
   Every authenticated HTTP request arriving at the API Gateway passes through `AuthMiddleware`, which performs an atomic Redis `EXISTS user:suspended:{userID}`. If found, it immediately aborts the pipeline and returns `HTTP 403 Forbidden`.

---

## 5. Execution Flow

```
                                 OPERATOR / POSTMAN
                                         │
                                         │ 1. POST /api/v1/admin/users/{id}/suspend
                                         │    (Idempotency-Key + Bearer JWT)
                                         ▼
                      ┌──────────────────────────────────────┐
                      │             ADMIN HANDLER            │
                      │  - Validates JWT (Role == "admin")   │
                      │  - Validates Idempotency-Key header  │
                      │  - Validates User UUID & Reason      │
                      └──────────────────┬───────────────────┘
                                         │
                                         │ 2. SuspendUser(ctx, req)
                                         ▼
                      ┌──────────────────────────────────────┐
                      │             USER SERVICE             │
                      │  Check Idempotency in admin_ops      │
                      └──────────────────┬───────────────────┘
                                         │
                       ┌─────────────────┴─────────────────┐
                       │ Cached Operation Exists           │ Fresh Key
                       ▼                                   ▼
         ┌───────────────────────────┐       ┌───────────────────────────────────┐
         │ HTTP 200 OK               │       │ ExecAdminOperationTx (Atomic SQL) │
         │ Return Cached DTO         │       │ ├── admin_operations (PROCESSING) │
         │ (Zero duplicate side-eff) │       │ ├── admin_audit_log (SUSPEND)     │
         └───────────────────────────┘       │ ├── admin_outbox (Outbox Event)   │
                                             │ └── admin_saga_tasks (Retry task) │
                                             └─────────────────┬─────────────────┘
                                                               │
                                                               │ 3. gRPC SuspendUser()
                                                               ▼
                                             ┌───────────────────────────────────┐
                                             │       AUTH SERVICE (:50051)       │
                                             │  - Mark user.status = SUSPENDED   │
                                             │  - Invalidate active sessions     │
                                             │  - Increment token_version        │
                                             └─────────────────┬─────────────────┘
                                                               │
                                              Did Auth gRPC respond successfully?
                                                               │
                                                ┌──────────────┴──────────────┐
                                                ▼                             ▼
                                               YES                            NO / TIMEOUT
                                 ┌─────────────────────────────┐ ┌─────────────────────────────┐
                                 │ CompleteAuthSaga()          │ │ Leave Status = PROCESSING   │
                                 │ - Operation -> COMPLETED    │ │ - Background SagaWorker     │
                                 │ - SET user:suspended:{id}=1 │ │   retries gRPC periodically │
                                 └──────────────┬──────────────┘ └──────────────┬──────────────┘
                                                │                               │
                                                └──────────────┬────────────────┘
                                                               │
                                                               │ 4. Return Operation DTO
                                                               ▼
                                 ┌─────────────────────────────────────────────┐
                                 │          HTTP 200 OK (User Suspended)       │
                                 └─────────────────────────────────────────────┘
                                                               │
                                                               │ Suspended user attempts API call
                                                               ▼
                                 ┌─────────────────────────────────────────────┐
                                 │             API GATEWAY INGRESS             │
                                 │  - Checks Redis: EXISTS user:suspended:{id} │
                                 │  - Reject immediately: HTTP 403 Forbidden   │
                                 └─────────────────────────────────────────────┘
```
