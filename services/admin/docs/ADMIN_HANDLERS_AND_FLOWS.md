# Admin Service: Control Plane Handlers & Operational Flows

This document details the internal architecture, request lifecycles, and technical execution flows for how the **TradeDrift Admin Service** handles **Users**, **Markets**, and **Wallets**.

---

## Architecture Overview

The Admin Service serves as the authoritative orchestrator for all platform administrative operations. It enforces an architectural boundary: **Admin never directly touches or mutates downstream database tables**. Instead, it coordinates changes across **PostgreSQL (System of Record)**, **Redis (Hot Enforcement Cache)**, **Kafka (Event Broadcast)**, and downstream microservices (**Auth & Wallet via gRPC**).

```
                      HTTP REST Ingress (:8085)
                                │
               ┌────────────────┴────────────────┐
               ▼                                 ▼
      RequireAdmin Middleware        RequireIdempotencyKey
      (JWT Role Assertion)           (UUID / Caller Deduplication)
               │                                 │
               └────────────────┬────────────────┘
                                │
                                ▼
                       AdminHandler Router
            ┌───────────────────┼───────────────────┐
            ▼                   ▼                   ▼
       User Flows          Market Flows        Wallet Flows
     (Suspend/Unsuspend)  (Halt/Resume)       (Freeze/Unfreeze)
            │                   │                   │
            └───────────────────┼───────────────────┘
                                │
                                ▼
                   PostgreSQL Atomic Transaction
             ├── 1. admin_operations (State Machine)
             ├── 2. admin_audit_log (Immutable Audit)
             ├── 3. admin_outbox (Reliable Kafka Events)
             └── 4. admin_saga_tasks (Resilient Retries)
                                │
        ┌───────────────────────┼───────────────────────┐
        ▼                       ▼                       ▼
 Downstream gRPC        Synchronous Redis        Async Workers
 (Auth & Wallet)       (Circuit Breakers)     (Outbox & Saga & Recon)
```

---

## Table of Contents

1. [User Handling Flow (Suspend & Unsuspend)](#1-user-handling-flow)
2. [Market Handling Flow (Halt & Resume)](#2-market-handling-flow)
3. [Wallet Handling Flow (Freeze & Unfreeze)](#3-wallet-handling-flow)
4. [Cross-Cutting Architectural Mechanisms](#4-cross-cutting-architectural-mechanisms)
5. [Codebase Implementation Reference Table](#5-codebase-implementation-reference-table)

---

## 1. User Handling Flow

### 1.1 Overview
User management allows platform operators to immediately revoke an abusive, compromised, or AML-flagged account across the entire distributed cluster. 

* **Endpoints:**
  - `POST /api/v1/admin/users/{user_id}/suspend`
  - `POST /api/v1/admin/users/{user_id}/unsuspend`
* **Target:** User account identified by UUID.
* **Impact:** Immediate API Gateway request rejection (`HTTP 403 Forbidden`) and session revocation, even if the user possesses an unexpired cryptographic JWT.

### 1.2 Step-by-Step Execution Lifecycle (Suspend User)

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
                      │  Idempotency Check in DB             │
                      └──────────────────┬───────────────────┘
                                         │
                       ┌─────────────────┴─────────────────┐
                       │ Cached Operation Exists           │ Fresh Key
                       ▼                                   ▼
         ┌───────────────────────────┐       ┌───────────────────────────────────┐
         │ HTTP 200 OK               │       │ ExecAdminOperationTx (Atomic SQL) │
         │ Return Cached DTO         │       │ ├── admin_operations (PROCESSING) │
         │ (Zero duplicate mutation) │       │ ├── admin_audit_log (SUSPEND)     │
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

### 1.3 Unsuspend User Execution Lifecycle
When `POST /api/v1/admin/users/{user_id}/unsuspend` is executed:
1. Validates operator role and idempotency key.
2. Commits atomic operation, audit log, outbox event (`admin.user-unsuspended.v1`), and unsuspend saga task.
3. Calls Auth gRPC `UnsuspendUser`, restoring user status in the Auth database to `ACTIVE`.
4. Executes synchronous Redis command: `DEL user:suspended:{id}` (with 3 retries).
5. Marks the operation `COMPLETED`. The user can now authenticate and access the API Gateway normally.

### 1.4 Where It Is Implemented in Code
| Component | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **HTTP Transport** | [`services/admin/internal/handler/admin_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/admin_handler.go#L23-L101) | `HandleSuspendUser`, `HandleUnsuspendUser` |
| **Input Validation** | [`services/admin/internal/handler/validation.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/validation.go#L21-L36) | `ValidateUserID` (UUID parser), `ValidateReason` (5-500 chars) |
| **Service Layer** | [`services/admin/internal/service/user_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/user_service.go#L34-L260) | `SuspendUser`, `UnsuspendUser`, Redis write/delete with retries |
| **Auth Client** | [`services/admin/internal/client/auth_client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/auth_client.go#L44-L90) | `SuspendUser`, `UnsuspendUser` gRPC client invocation |
| **Saga Worker** | [`services/admin/internal/service/saga_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/saga_worker.go#L80-L160) | Retries incomplete suspension sagas on downstream timeouts |
| **Gateway Guard** | [`services/gateway/internal/middleware/auth.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/middleware/auth.go) | Real-time `user:suspended:{id}` check in Redis |

---

## 2. Market Handling Flow

### 2.1 Overview
Market controls give operators an emergency **circuit breaker** to halt trading on a specific pair (e.g., `BTC-USDT`) during extreme volatility, bad oracle data, or suspected exploit attempts.

* **Endpoints:**
  - `POST /api/v1/admin/markets/{market_id}/halt`
  - `POST /api/v1/admin/markets/{market_id}/resume`
* **Target:** Canonical market pair (e.g. `BTC-USDT`).
* **Impact:** Immediate sub-millisecond rejection of new orders by the Order Service (`HTTP 409 Conflict`), while allowing existing in-flight cancellations.

### 2.2 Step-by-Step Execution Lifecycle (Halt Market)

```
                                 OPERATOR / POSTMAN
                                         │
                                         │ 1. POST /api/v1/admin/markets/{id}/halt
                                         │    (Idempotency-Key + Bearer JWT)
                                         ▼
                      ┌──────────────────────────────────────┐
                      │             ADMIN HANDLER            │
                      │  - Validates JWT (Role == "admin")   │
                      │  - Validates Idempotency-Key header  │
                      │  - Validates Market Format BASE-QUOTE│
                      └──────────────────┬───────────────────┘
                                         │
                                         │ 2. HaltMarket(ctx, req)
                                         ▼
                      ┌──────────────────────────────────────┐
                      │            MARKET SERVICE            │
                      │  Idempotency Check in DB             │
                      └──────────────────┬───────────────────┘
                                         │
                       ┌─────────────────┴─────────────────┐
                       │ Cached Operation Exists           │ Fresh Key
                       ▼                                   ▼
         ┌───────────────────────────┐       ┌───────────────────────────────────┐
         │ HTTP 200 OK               │       │ ExecAdminOperationTx (Atomic SQL) │
         │ Return Cached DTO         │       │ ├── admin_operations (COMPLETED)  │
         │ (Zero duplicate mutation) │       │ ├── admin_audit_log (HALT_MARKET) │
         └───────────────────────────┘       │ └── admin_outbox (Outbox Event)   │
                                             └─────────────────┬─────────────────┘
                                                               │
                                                               │ 3. Synchronous Redis Circuit Breaker
                                                               ▼
                                             ┌───────────────────────────────────┐
                                             │            REDIS CACHE            │
                                             │  SET market:halted:{id} = "1"     │
                                             │  (3 retries: 50ms, 150ms, 300ms)  │
                                             └─────────────────┬─────────────────┘
                                                               │
                                                               │ 4. Telemetry Update
                                                               ▼
                                             ┌───────────────────────────────────┐
                                             │ metrics.RecordMarketHalted(id)    │
                                             │ - Set market halt gauge = 1       │
                                             └─────────────────┬─────────────────┘
                                                               │
                                                               │ 5. Return Operation DTO
                                                               ▼
                                             ┌───────────────────────────────────┐
                                             │         HTTP 200 OK (Halted)      │
                                             └───────────────────────────────────┘
                                                               │
                                                               │ User attempts order placement:
                                                               │ POST /api/v1/orders
                                                               ▼
                                             ┌───────────────────────────────────┐
                                             │        ORDER SERVICE INGRESS      │
                                             │  1. Check market:enforcement:ready│
                                             │     -> "1" (Enforcement active)   │
                                             │  2. Check market:halted:{id}      │
                                             │     -> "1" (Market halted!)       │
                                             │  3. Reject immediately:           │
                                             │     HTTP 409 Conflict             │
                                             └───────────────────────────────────┘
```

### 2.3 Resume Market Execution Lifecycle
When `POST /api/v1/admin/markets/{market_id}/resume` is executed:
1. Validates market ID format (regex `^[A-Z0-9]{2,10}-[A-Z0-9]{2,10}$`) and unique idempotency key.
2. Commits operation (`RESUME_MARKET`), audit log, and outbox event (`admin.market-resumed.v1`).
3. Executes synchronous Redis key eviction: `DEL market:halted:{marketID}` with retries.
4. Resets Prometheus market halt gauges (`RecordMarketResumed`).
5. Subsequent calls to Order Service find no halt key in Redis, allowing trading to resume seamlessly.

### 2.4 Self-Healing & Startup Anti-Drift Architecture
To guarantee that Redis never drifts from PostgreSQL:
1. **Startup Reconstruction:** When Admin Service starts, `ReconstructMarketState` queries PostgreSQL for all historical operations and initializes halt gauges.
2. **Sentinel Protection:** The `StateReconciler` verifies all active halts in Redis. Only after full verification does it set `market:enforcement:ready = "1"`. If Redis restarts, the sentinel disappears, causing the Order Service to **fail closed** until Admin re-synchronizes Redis.
3. **Periodic Reconciler:** A 15-second background loop runs non-blocking `SCAN market:halted:*`, comparing Redis against PostgreSQL snapshots.

### 2.5 Where It Is Implemented in Code
| Component | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **HTTP Transport** | [`services/admin/internal/handler/admin_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/admin_handler.go#L201-L273) | `HandleHaltMarket`, `HandleResumeMarket` |
| **Market Validation** | [`services/admin/internal/handler/validation.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/validation.go#L47-L54) | `ValidateMarketID` (Strict `BASE-QUOTE` regex) |
| **Service Logic** | [`services/admin/internal/service/market_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/market_service.go#L41-L280) | `HaltMarket`, `ResumeMarket`, `ReconstructMarketState` |
| **State Reconciler** | [`services/admin/internal/service/reconciler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/reconciler.go#L104-L171) | `reconcileMarkets`, sets `market:enforcement:ready` |
| **Order Guard** | [`services/order/internal/service/guard.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/guard.go#L26-L58) | `RedisMarketGuard.IsHalted` (Checks readiness sentinel + halt key) |
| **Order Ingress** | [`services/order/internal/service/service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/service.go#L94-L113) | Checks `marketGuard.IsHalted` before balance reservation |

---

## 3. Wallet Handling Flow

### 3.1 Overview
Wallet freezing provides fine-grained, asset-specific risk mitigation. Unlike User Suspension (which blocks the entire user), Wallet Freezing locks a **single currency** for a user (e.g., freezing only `BTC` while leaving `USDT` and `INR` active).

* **Endpoints:**
  - `POST /api/v1/admin/users/{user_id}/wallets/{asset}/freeze`
  - `POST /api/v1/admin/users/{user_id}/wallets/{asset}/unfreeze`
* **Target:** User UUID + Asset Code (e.g. `BTC`, `ETH`, `USDT`).
* **Impact:** Locks balance mutations for that currency. User cannot withdraw, deposit, or reserve funds for orders involving that asset.

### 3.2 Step-by-Step Execution Lifecycle (Freeze Wallet)

```
                                 OPERATOR / POSTMAN
                                         │
                                         │ 1. POST /api/v1/admin/users/{id}/wallets/{asset}/freeze
                                         │    (Idempotency-Key + Bearer JWT)
                                         ▼
                      ┌──────────────────────────────────────┐
                      │             ADMIN HANDLER            │
                      │  - Validates User UUID               │
                      │  - Validates Asset Code (e.g. "BTC") │
                      │  - Validates Reason & Idempotency Key│
                      └──────────────────┬───────────────────┘
                                         │
                                         │ 2. FreezeWallet(ctx, req)
                                         ▼
                      ┌──────────────────────────────────────┐
                      │            WALLET SERVICE            │
                      │  Idempotency Check in DB             │
                      └──────────────────┬───────────────────┘
                                         │
                       ┌─────────────────┴─────────────────┐
                       │ Cached Operation Exists           │ Fresh Key
                       ▼                                   ▼
         ┌───────────────────────────┐       ┌───────────────────────────────────┐
         │ HTTP 200 OK               │       │ ExecAdminOperationTx (Atomic SQL) │
         │ Return Cached DTO         │       │ ├── admin_operations (PROCESSING) │
         │ (Zero duplicate mutation) │       │ ├── admin_audit_log (FREEZE)      │
         └───────────────────────────┘       │ └── admin_outbox (Outbox Event)   │
                                             └─────────────────┬─────────────────┘
                                                               │
                                                               │ 3. gRPC FreezeWallet()
                                                               ▼
                                             ┌───────────────────────────────────┐
                                             │    WALLET MICROSERVICE (:50052)   │
                                             │  - UPDATE wallets                 │
                                             │    SET is_frozen = true,          │
                                             │        frozen_at = NOW(),         │
                                             │        freeze_reason = ...        │
                                             │    WHERE user_id AND asset        │
                                             └─────────────────┬─────────────────┘
                                                               │
                                              Did Wallet gRPC respond successfully?
                                                               │
                                                ┌──────────────┴──────────────┐
                                                ▼                             ▼
                                               YES                            NO / TIMEOUT
                                 ┌─────────────────────────────┐ ┌─────────────────────────────┐
                                 │ UpdateOperationStatus()     │ │ Leave Status = PROCESSING   │
                                 │ - Operation -> COMPLETED    │ │ - Client retry reconciles   │
                                 │ - Return response struct    │ │   in-flight operation       │
                                 └──────────────┬──────────────┘ └──────────────┬──────────────┘
                                                │                               │
                                                └──────────────┬────────────────┘
                                                               │
                                                               │ 4. Return Operation DTO
                                                               ▼
                                 ┌─────────────────────────────────────────────┐
                                 │         HTTP 200 OK (Wallet Frozen)         │
                                 └─────────────────────────────────────────────┘
                                                               │
                                                               │ User submits trade/withdraw:
                                                               ▼
                                 ┌─────────────────────────────────────────────┐
                                 │          WALLET ENFORCEMENT ENGINE          │
                                 │  - ReserveFunds(BTC):                       │
                                 │    Checks wallet.IsFrozen == true           │
                                 │    -> Rejects: codes.FailedPrecondition     │
                                 │    ("wallet is frozen")                     │
                                 │  - SettleTrade / ReleaseFunds:              │
                                 │    Allowed to complete for ledger safety    │
                                 └─────────────────────────────────────────────┘
```

### 3.3 Enforcement Semantics: What is Blocked vs. Allowed
When a wallet is frozen, the user's funds **stay in `available_balance`** (they are not deducted or moved away), but the Wallet Service enforces the following rules:

| Operation | Frozen Wallet Status | Technical Enforcement Point |
| :--- | :---: | :--- |
| **New Order (Sell BTC)** | ❌ **BLOCKED** | `ReserveFunds` Step 8 checks `wallet.IsFrozen` &rarr; returns `ErrWalletFrozen`. |
| **New Order (Buy with USDT)** | ❌ **BLOCKED** | If USDT wallet is frozen, `ReserveFunds` rejects quote asset reservation. |
| **Withdrawal / Transfer** | ❌ **BLOCKED** | Debits require reservation &rarr; rejected immediately. |
| **Deposit / Top-up** | ❌ **BLOCKED** | `DepositFunds` checks `wallet.IsFrozen` &rarr; returns `ErrWalletFrozen`. |
| **Order Cancellation** | ✅ **ALLOWED** | `ReleaseFunds` does **not** check freeze state, allowing locked funds to safely return to available balance. |
| **Trade Settlement** | ✅ **ALLOWED** | `SettleTrade` clearing allows crossed trades to settle without corrupting the double-entry ledger. |
| **Other Asset Wallets** | ✅ **ALLOWED** | If `BTC` is frozen, user's `USDT`, `ETH`, and `INR` wallets operate normally. |

### 3.4 In-Flight Reconciliation on Network Timeout
If the Admin Service commits the operation as `PROCESSING` and the gRPC call times out, a client retry with the same idempotency key executes **in-flight reconciliation**:
1. It detects the operation is already in `PROCESSING`.
2. It re-invokes the idempotent Wallet gRPC call using the existing `operation_id`.
3. If Wallet confirms the freeze, Admin marks the operation `COMPLETED` and returns success.

### 3.5 Where It Is Implemented in Code
| Component | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **HTTP Transport** | [`services/admin/internal/handler/admin_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/admin_handler.go#L103-L199) | `HandleFreezeWallet`, `HandleUnfreezeWallet` |
| **Asset Validation** | [`services/admin/internal/handler/validation.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/validation.go#L38-L45) | `ValidateAsset` (`^[A-Z0-9]{2,10}$`) |
| **Service Logic** | [`services/admin/internal/service/wallet_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/wallet_service.go#L50-L230) | `FreezeWallet`, `UnfreezeWallet`, in-flight reconciliation |
| **Wallet Client** | [`services/admin/internal/client/wallet_client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/wallet_client.go#L44-L64) | `FreezeWallet` gRPC transport call |
| **Wallet Service Logic**| [`services/wallet/internal/service/freeze.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/freeze.go#L12-L44) | Updates `wallets` table in Wallet DB |
| **Reservation Guard** | [`services/wallet/internal/service/reserve_funds.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/reserve_funds.go#L93-L96) | Checks `wallet.IsFrozen` and returns `ErrWalletFrozen` |
| **Deposit Guard** | [`services/wallet/internal/service/deposit_funds.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/deposit_funds.go#L86-L88) | Rejects deposits on frozen wallets |

---

## 4. Cross-Cutting Architectural Mechanisms

### 4.1 Strict Caller-Scoped Idempotency Engine
To protect against network replays and duplicate submissions:
- The database enforces uniqueness: `UNIQUE (admin_id, idempotency_key)`.
- If an admin submits an identical idempotency key, the server **short-circuits** and returns the cached execution result from the database without executing duplicate side effects.
- **Rule:** Every *new* administrative intent requires a fresh, distinct idempotency key (e.g. `{{$guid}}`).

### 4.2 Transactional Outbox Pattern
To prevent dual-write inconsistencies between PostgreSQL and Kafka:
1. State changes and outbox events are committed in the **same database transaction**.
2. An independent background worker ([`outbox_publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/outbox_publisher.go)) polls the `admin_outbox` table and publishes events to Apache Kafka with `RequiredAcks: kafka.RequireAll`.
3. Events are only marked as `published_at = NOW()` after Kafka acknowledges delivery.
4. Broadcast topics:
   - `admin.market-halted.v1` & `admin.market-resumed.v1`
   - `admin.wallet-frozen.v1` & `admin.wallet-unfrozen.v1`
   - `admin.user-suspended.v1` & `admin.user-unsuspended.v1`

### 4.3 Immutable Audit Logging
Every mutation automatically creates a record in `admin_audit_log`:
- **Actor:** `admin_id` (extracted from cryptographically validated JWT).
- **Network Tracing:** Client IP address and `User-Agent`.
- **Correlation:** `request_id` (propagated from `X-Request-ID` header or generated as UUIDv7).
- **Context:** Action type, target ID, reason, and structured metadata.

---

## 5. Codebase Implementation Reference Table

| Functional Area | Source File | Core Responsibility |
| :--- | :--- | :--- |
| **Main Entrypoint** | [`cmd/server/main.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/server/main.go) | Bootstrap, dependency injection, graceful shutdown, startup reconciliation. |
| **Router** | [`internal/handler/router.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/router.go) | URL muxing, route normalization, metrics middleware. |
| **Middleware** | [`internal/handler/middleware.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/middleware.go) | JWT admin role verification, idempotency header validation, structured JSON logging. |
| **Admin Handlers** | [`internal/handler/admin_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/admin_handler.go) | HTTP request decoding, DTO conversion, error code mapping. |
| **User Service** | [`internal/service/user_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/user_service.go) | User suspension orchestration, Auth gRPC client, Redis token blacklist. |
| **Market Service** | [`internal/service/market_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/market_service.go) | Halt/Resume execution, Redis circuit breaker write/del, gauge metrics. |
| **Wallet Service** | [`internal/service/wallet_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/wallet_service.go) | Per-asset wallet freeze orchestration, Wallet gRPC client, in-flight reconciliation. |
| **State Reconciler** | [`internal/service/reconciler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/reconciler.go) | 15-second background sync between PostgreSQL and Redis; sets readiness sentinel. |
| **Saga Worker** | [`internal/service/saga_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/saga_worker.go) | Background retry processor for multi-step distributed operations. |
| **Outbox Publisher** | [`internal/service/outbox_publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/outbox_publisher.go) | Reliable Kafka publisher using transactional outbox polling. |
| **Operations Repo** | [`internal/repository/postgres/operations_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/operations_repo.go) | Idempotent PostgreSQL persistence and authoritative state queries. |
| **Transaction Manager** | [`internal/repository/postgres/tx_manager.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/tx_manager.go) | Atomic multi-table commits (operations, audit logs, outbox events, saga tasks). |
| **End-to-End Suite** | [`test/e2e/test_control_plane.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/test/e2e/test_control_plane.go) | Full black-box verification of market halt, user suspension, and sentinel eviction. |
