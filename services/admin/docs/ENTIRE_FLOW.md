# TradeDrift Admin Service: Complete End-to-End Operational Flows

This document is the master architectural reference detailing **every execution flow** inside the **TradeDrift Admin Control Plane**, spanning startup bootstrapping, ingress middleware, operational mutations, background asynchronous workers, intelligence APIs, and coordinated graceful drainage.

---

## Master Architecture Map

```
                                  KUBERNETES / OPERATOR / TRAFFIC
                                                │
       ┌────────────────────────────────────────┼────────────────────────────────────────┐
       ▼                                        ▼                                        ▼
Public Health Probes                   Mutating Operations                    Intelligence & Queries
(/health, /ready, /metrics)            (Users, Markets, Wallets)              (Topology, Analytics, Incidents)
       │                                        │                                        │
       │                               ┌────────┴────────┐                               │
       │                               ▼                 ▼                               │
       │                      RequireAdmin       RequireIdempotency                      │
       │                      (JWT Auth)         (Deduplication)                         │
       │                               │                 │                               │
       │                               └────────┬────────┘                               │
       │                                        │                                        │
       │                                        ▼                                        │
       │                                Admin Mux Router ◄───────────────────────────────┘
       │                                        │
       │                     ┌──────────────────┴──────────────────┐
       │                     ▼                                     ▼
       │         PostgreSQL System of Record             Redis Enforcement Cache
       │         ├── admin_operations (State)            ├── market:halted:{id}
       │         ├── admin_audit_log (Audit)             ├── market:enforcement:ready
       │         ├── admin_outbox (Kafka Events)         └── user:suspended:{id}
       │         ├── admin_saga_tasks (Retries)
       │         └── admin_incidents (Outages)
       │
       └────────────────────────────────────────┬────────────────────────────────────────┐
                                                │
                     ┌──────────────────────────┴──────────────────────────┐
                     ▼                                                     ▼
        Synchronous Downstream gRPC                            Asynchronous Background Engines
        ├── Auth Service (:50051)                              ├── OutboxPublisher (Kafka Writer)
        └── Wallet Service (:50052)                            ├── SagaWorker (Multi-Step Retries)
                                                               ├── StateReconciler (15s Self-Healer)
                                                               └── HealthWorker (15s Cluster Prober)
```

---

## Table of Contents

1. [Service Bootstrapping & Startup Flow](#1-service-bootstrapping--startup-flow)
2. [HTTP Ingress & Middleware Pipeline](#2-http-ingress--middleware-pipeline)
3. [Market Control Flows (Halt & Resume)](#3-market-control-flows)
4. [User Control Flows (Suspend & Unsuspend)](#4-user-control-flows)
5. [Wallet Control Flows (Freeze & Unfreeze)](#5-wallet-control-flows)
6. [Asynchronous Background Worker Flows](#6-asynchronous-background-worker-flows)
   - [6.1 Transactional Outbox Publisher Flow](#61-transactional-outbox-publisher-flow)
   - [6.2 Distributed Saga Worker Flow](#62-distributed-saga-worker-flow)
   - [6.3 Anti-Drift State Reconciler Flow](#63-anti-drift-state-reconciler-flow)
   - [6.4 Autonomous Health Prober Flow](#64-autonomous-health-prober-flow)
7. [Observability & Operational Intelligence Flows](#7-observability--operational-intelligence-flows)
   - [7.1 Liveness & Readiness Probing Flow](#71-liveness--readiness-probing-flow)
   - [7.2 Service Dependency Topology Flow](#72-service-dependency-topology-flow)
   - [7.3 Operational Analytics & Risk Signals Flow](#73-operational-analytics--risk-signals-flow)
   - [7.4 Incident Lifecycle & Correlation Flow](#74-incident-lifecycle--correlation-flow)
8. [Graceful Shutdown & Drainage Flow](#8-graceful-shutdown--drainage-flow)
9. [Complete Codebase File Directory Reference](#9-complete-codebase-file-directory-reference)

---

## 1. Service Bootstrapping & Startup Flow

The Admin Service implements a **fail-fast, ordered initialization lifecycle** in [`cmd/server/main.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/server/main.go):

```
                        ┌────────────────────────────────────────┐
                        │            CONTAINER START             │
                        └───────────────────┬────────────────────┘
                                            │
                                            ▼
                        ┌────────────────────────────────────────┐
                        │ 1. platformconfig.LoadEnv              │
                        │    Process Env > admin/.env > .env     │
                        └───────────────────┬────────────────────┘
                                            │
                                            ▼
                        ┌────────────────────────────────────────┐
                        │ 2. config.Load                         │
                        │    Fail-fast validation of Secrets,    │
                        │    DSNs, and worker intervals          │
                        └───────────────────┬────────────────────┘
                                            │
                                            ▼
                        ┌────────────────────────────────────────┐
                        │ 3. platformlogger.New                  │
                        │    Structured JSON Zap Logger          │
                        └───────────────────┬────────────────────┘
                                            │
                                            ▼
                        ┌────────────────────────────────────────┐
                        │ 4. signal.NotifyContext                │
                        │    Captures SIGINT & SIGTERM for root  │
                        └───────────────────┬────────────────────┘
                                            │
                                            ▼
                        ┌────────────────────────────────────────┐
                        │ 5. platformpg.RunMigrations            │
                        │    Executes Goose SQL DDL migrations   │
                        └───────────────────┬────────────────────┘
                                            │
                                            ▼
                        ┌────────────────────────────────────────┐
                        │ 6. platformpg.NewPool                  │
                        │    Connect & Ping PostgreSQL           │
                        └───────────────────┬────────────────────┘
                                            │
                                            ▼
                        ┌────────────────────────────────────────┐
                        │ 7. Instantiate Repositories            │
                        │    TxMgr, Ops, Outbox, Saga,           │
                        │    Incident, and Audit Repos           │
                        └───────────────────┬────────────────────┘
                                            │
                                            ▼
                        ┌────────────────────────────────────────┐
                        │ 8. Connect Downstream gRPC Clients     │
                        │    AuthClient (:50051)                 │
                        │    WalletClient (:50052)               │
                        └───────────────────┬────────────────────┘
                                            │
                                            ▼
                        ┌────────────────────────────────────────┐
                        │ 9. Redis Client Connect & PING         │
                        │    Verifies connection to Redis :6379  │
                        └───────────────────┬────────────────────┘
                                            │
                                            ▼
                        ┌────────────────────────────────────────┐
                        │ 10. ReconstructMarketState             │
                        │     Query Postgres for past operations │
                        │     and repopulate halt Prometheus     │
                        └───────────────────┬────────────────────┘
                                            │
                                            ▼
                        ┌────────────────────────────────────────┐
                        │ 11. ReconcileOnce                      │
                        │     Cold-start sync DB -> Redis & set  │
                        │     market:enforcement:ready = "1"     │
                        └───────────────────┬────────────────────┘
                                            │
                                            ▼
                        ┌────────────────────────────────────────┐
                        │ 12. Start Background Workers           │
                        │     OutboxPublisher, SagaWorker,       │
                        │     StateReconciler, HealthWorker      │
                        └───────────────────┬────────────────────┘
                                            │
                                            ▼
                        ┌────────────────────────────────────────┐
                        │ 13. Wire HTTP Router & Middleware      │
                        │     AdminHandler, HealthHandler,       │
                        │     Topology, Incident, Analytics      │
                        └───────────────────┬────────────────────┘
                                            │
                                            ▼
                        ┌────────────────────────────────────────┐
                        │ 14. Start HTTP Server (:8085)          │
                        │     Listening for incoming requests    │
                        └────────────────────────────────────────┘
```

### Key Invariants During Startup:
1. **Migrations must succeed:** If PostgreSQL migrations fail, startup halts immediately (`log.Fatal`).
2. **Postgres & Redis reachability:** If PostgreSQL or Redis cannot be pinged, the process exits.
3. **Cold-Start Anti-Drift Sync:** Before accepting orders or HTTP traffic, `ReconcileOnce` syncs PostgreSQL market halt status into Redis and sets the `market:enforcement:ready = "1"` sentinel.

---

## 2. HTTP Ingress & Middleware Pipeline

Every HTTP request traverses a standardized middleware onion before reaching the domain handlers:

```
                            INCOMING HTTP REQUEST
                                      │
                                      ▼
             ┌─────────────────────────────────────────────────┐
             │ 1. StructuredLoggingMiddleware                  │
             │    - Captures start time & wraps ResponseWriter │
             │    - Extracts admin_id, request_id, remote_ip   │
             │    - Logs structured JSON on completion         │
             └────────────────────────┬────────────────────────┘
                                      │
                                      ▼
             ┌─────────────────────────────────────────────────┐
             │ 2. MetricsMiddleware                            │
             │    - Normalizes URL route pattern               │
             │    - Records Prometheus request count & latency │
             └────────────────────────┬────────────────────────┘
                                      │
                                      ▼
                           Is endpoint protected?
                                ├── NO ──► Public Handler (/health, /ready, /metrics)
                                │
                               └── YES
                                      │
                                      ▼
             ┌─────────────────────────────────────────────────┐
             │ 3. RequireAdmin Middleware                      │
             │    - Validates Bearer JWT signature via HMAC    │
             │    - Asserts claims.Role == "admin"             │
             │    - Injects admin_id and request_id to context │
             └────────────────────────┬────────────────────────┘
                                      │
                                      ▼
                           Is mutation POST request?
                                ├── NO ──► Read-only Handlers (/topology, /analytics, /incidents)
                                │
                               └── YES
                                      │
                                      ▼
             ┌─────────────────────────────────────────────────┐
             │ 4. RequireIdempotencyKey Middleware             │
             │    - Validates presence of Idempotency-Key      │
             │    - Asserts length (1 to 128 characters)       │
             │    - Injects key into request context           │
             └────────────────────────┬────────────────────────┘
                                      │
                                      ▼
                       DOMAIN HANDLERS (:8085)
                   (/halt, /resume, /suspend, /freeze)
```

---

## 3. Market Control Flows

### 3.1 Halt Market (`POST /api/v1/admin/markets/{market_id}/halt`)
Halting stops all new order ingress for a market in sub-milliseconds:

```
OPERATOR (Postman)         ADMIN HANDLER               ADMIN SERVICE           POSTGRESQL               REDIS CACHE           ORDER SERVICE
     │                           │                           │                      │                        │                      │
     │── 1. POST /halt ─────────►│                           │                      │                        │                      │
     │   (Idempotency-Key)       │                           │                      │                        │                      │
     │                           │── 2. HaltMarket() ───────►│                      │                        │                      │
     │                           │                           │── 3. Check Key ─────►│                        │                      │
     │                           │                           │◄─ Key Not Found ─────│                        │                      │
     │                           │                           │                      │                        │                      │
     │                           │                           │── 4. Begin Tx ──────►│                        │                      │
     │                           │                           │   - admin_operations │                        │                      │
     │                           │                           │   - admin_audit_log  │                        │                      │
     │                           │                           │   - admin_outbox     │                        │                      │
     │                           │                           │◄─ Tx Committed ──────│                        │                      │
     │                           │                           │                      │                        │                      │
     │                           │                           │── 5. SET market:halted:BTC-USDT = "1" ───────►│                      │
     │                           │                           │◄─ OK (3 retries on failure) ──────────────────│                      │
     │                           │                           │                                               │                      │
     │                           │                           │── 6. Update Prometheus Metrics                │                      │
     │                           │◄─ 7. Return Operation ────│                                               │                      │
     │◄─ 8. HTTP 200 (COMPLETED) │                           │                                               │                      │
     │                           │                           │                                               │                      │
     │                                                                                                       │                      │
     │   [USER PLACES ORDER AFTER HALT]                                                                      │                      │
     │   POST /api/v1/orders ────────────────────────────────────────────────────────────────────────────────┼─────────────────────►│
     │                                                                                                       │                      │
     │                                                                                                       │◄── 9. Check Sentinel │
     │                                                                                                       │    GET market:ready  │
     │                                                                                                       │─── "1" (Ready) ─────►│
     │                                                                                                       │                      │
     │                                                                                                       │◄── 10. Check Halt ───│
     │                                                                                                       │    GET halted:BTC    │
     │                                                                                                       │─── "1" (Halted!) ───►│
     │                                                                                                       │                      │
     │◄── 11. HTTP 409 Conflict: "market is currently halted" ───────────────────────────────────────────────┼──────────────────────│
```

### 3.2 Resume Market (`POST /api/v1/admin/markets/{market_id}/resume`)
```
OPERATOR (Postman)         ADMIN HANDLER               ADMIN SERVICE           POSTGRESQL               REDIS CACHE           ORDER SERVICE
     │                           │                           │                      │                        │                      │
     │── 1. POST /resume ───────►│                           │                      │                        │                      │
     │   (Idempotency-Key)       │                           │                      │                        │                      │
     │                           │── 2. ResumeMarket() ─────►│                      │                        │                      │
     │                           │                           │── 3. Commit Tx ─────►│                        │                      │
     │                           │                           │   - RESUME_MARKET    │                        │                      │
     │                           │                           │   - admin_outbox     │                        │                      │
     │                           │                           │                      │                        │                      │
     │                           │                           │── 4. DEL market:halted:BTC-USDT ─────────────►│                      │
     │                           │                           │◄─ Key Deleted ────────────────────────────────│                      │
     │                           │                           │                                               │                      │
     │                           │                           │── 5. Reset Market Halt Gauge                  │                      │
     │◄─ 6. HTTP 200 (Resumed) ──│◄──────────────────────────│                                               │                      │
     │                                                                                                       │                      │
     │   [USER RETRIES ORDER]                                                                                │                      │
     │   POST /api/v1/orders ────────────────────────────────────────────────────────────────────────────────┼─────────────────────►│
     │                                                                                                       │                      │
     │                                                                                                       │◄── 7. Check Halt ────│
     │                                                                                                       │─── nil (Not halted) ─►│
     │                                                                                                       │                      │
     │◄── 8. HTTP 200 OK: Order Accepted & Status OPEN ──────────────────────────────────────────────────────┼──────────────────────│
```

---

## 4. User Control Flows

### 4.1 Suspend User (`POST /api/v1/admin/users/{user_id}/suspend`)
Coordinates user lockout across Auth, Gateway, and Redis:

```
OPERATOR (Postman)         ADMIN SERVICE               POSTGRESQL              AUTH SERVICE (gRPC)      REDIS CACHE            API GATEWAY
     │                           │                          │                           │                    │                      │
     │── 1. POST /suspend ──────►│                          │                           │                    │                      │
     │                           │── 2. Begin Tx ──────────►│                           │                    │                      │
     │                           │   - admin_operations     │                           │                    │                      │
     │                           │   - admin_audit_log      │                           │                    │                      │
     │                           │   - admin_outbox         │                           │                    │                      │
     │                           │   - admin_saga_tasks     │                           │                    │                      │
     │                           │◄─ Tx Committed ──────────│                           │                    │                      │
     │                           │                                                      │                    │                      │
     │                           │── 3. gRPC SuspendUser(userID) ──────────────────────►│                    │                      │
     │                           │                                                      │                    │                      │
     │                           │   [AUTH COMMITS STATUS]                              │                    │                      │
     │                           │   users.status = 'SUSPENDED'                         │                    │                      │
     │                           │   users.token_version++                              │                    │                      │
     │                           │                                                      │                    │                      │
     │                           │◄── 4. gRPC Response (Success) ───────────────────────│                    │                      │
     │                           │                                                      │                    │                      │
     │                           │── 5. CompleteAuthSaga() ─►│                          │                    │                      │
     │                           │   (Operation -> COMPLETED)│                          │                    │                      │
     │                           │                                                      │                    │                      │
     │                           │── 6. SET user:suspended:{id} = "1" ──────────────────────────────────────►│                      │
     │                           │◄── OK ────────────────────────────────────────────────────────────────────│                      │
     │                           │                                                                           │                      │
     │◄─ 7. HTTP 200 (Suspended)─│                                                                           │                      │
     │                                                                                                       │                      │
     │   [SUSPENDED USER SENDS REQUEST WITH VALID JWT]                                                       │                      │
     │   GET /api/v1/wallet/balances ────────────────────────────────────────────────────────────────────────┼─────────────────────►│
     │                                                                                                       │                      │
     │                                                                                                       │◄── 8. Check Blacklist│
     │                                                                                                       │─── Key exists ("1") ─►│
     │                                                                                                       │                      │
     │◄── 9. HTTP 403 Forbidden: "user account is suspended" ────────────────────────────────────────────────┼──────────────────────│
```

### 4.2 Unsuspend User (`POST /api/v1/admin/users/{user_id}/unsuspend`)
1. Commits `UNSUSPEND_USER` operation and audit log.
2. Calls Auth gRPC `UnsuspendUser`, restoring user status in the Auth DB to `ACTIVE`.
3. Synchronously executes `DEL user:suspended:{id}` in Redis.
4. User can immediately authenticate and place orders again.

---

## 5. Wallet Control Flows

### 5.1 Freeze Wallet (`POST /api/v1/admin/users/{user_id}/wallets/{asset}/freeze`)
Applies granular, per-asset locking without affecting other currencies:

```
OPERATOR (Postman)         ADMIN SERVICE               ADMIN POSTGRESQL        WALLET SERVICE (gRPC)    WALLET POSTGRESQL       ORDER SERVICE
     │                           │                            │                          │                      │                     │
     │── 1. POST /wallets/BTC ──►│                            │                          │                      │                     │
     │      /freeze              │                            │                          │                      │                     │
     │                           │── 2. Begin Tx ────────────►│                          │                      │                     │
     │                           │   - admin_operations       │                          │                      │                     │
     │                           │   - admin_audit_log        │                          │                      │                     │
     │                           │   - admin_outbox           │                          │                      │                     │
     │                           │◄─ Tx Committed ────────────│                          │                      │                     │
     │                           │                                                       │                      │                     │
     │                           │── 3. gRPC FreezeWallet(userID, asset="BTC", true) ───►│                      │                     │
     │                           │                                                       │                      │                     │
     │                           │                                                       │── 4. SQL UPDATE ────►│                     │
     │                           │                                                       │   is_frozen = true   │                     │
     │                           │                                                       │   frozen_at = NOW()  │                     │
     │                           │                                                       │◄─ 1 row updated ─────│                     │
     │                           │                                                       │                      │                     │
     │                           │◄── 5. gRPC Response { is_frozen: true } ──────────────│                      │                     │
     │                           │                                                                              │                     │
     │                           │── 6. UpdateOperationStatus(COMPLETED) ──►│                                   │                     │
     │◄─ 7. HTTP 200 (Frozen) ───│                                          │                                   │                     │
     │                                                                                                          │                     │
     │   [USER ATTEMPTS TO SELL BTC ON ORDER SERVICE]                                                           │                     │
     │   POST /api/v1/orders (Sell 0.001 BTC) ──────────────────────────────────────────────────────────────────┼────────────────────►│
     │                                                                                                          │                     │
     │                                                                                           [CALLS WALLET] │                     │
     │                                                                                           ReserveFunds() ┼────────────────────►│
     │                                                                                                          │                     │
     │                                                                                                          │◄── 8. Check Freeze  │
     │                                                                                                          │─── is_frozen = true │
     │                                                                                                          │                     │
     │                                                                                           Wallet returns │                     │
     │                                                                                           FAILED_PRECOND ◄─────────────────────│
     │                                                                                                          │                     │
     │◄── 9. HTTP 400/409: "insufficient funds / wallet is frozen" ─────────────────────────────────────────────┼─────────────────────│
```

### 5.2 Enforcement Rules on Frozen Wallets
The user's funds **stay in their account** (`available_balance` is not deducted), but the Wallet Service rejects outbound commitments:
- **`ReserveFunds` (Placing orders):** Blocked with `ErrWalletFrozen`.
- **`DepositFunds` (Incoming top-ups):** Blocked with `ErrWalletFrozen`.
- **Withdrawals / Debits:** Blocked because funds cannot be reserved.
- **`ReleaseFunds` (Order cancellation):** **Allowed** so reserved funds return safely to available balance.
- **`SettleTrade` (Clearing):** **Allowed** to maintain double-entry balance invariants.

---

## 6. Asynchronous Background Worker Flows

### 6.1 Transactional Outbox Publisher Flow
* **File:** [`services/admin/internal/service/outbox_publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/outbox_publisher.go)
* **Schedule:** 1-second interval (`OUTBOX_INTERVAL`)

```
                 TICK EVENT (Every 1 Second)
                             │
                             ▼
 ┌──────────────────────────────────────────────────────────┐
 │ 1. Poll Unpublished Events from PostgreSQL               │
 │    SELECT * FROM admin_outbox WHERE published_at IS NULL │
 │    ORDER BY created_at LIMIT 100 FOR UPDATE SKIP LOCKED  │
 └───────────────────────────┬──────────────────────────────┘
                             │
                  Are there any events?
                             ├── NO ──► Sleep until next tick
                             │
                            └── YES
                             │
                             ▼
 ┌──────────────────────────────────────────────────────────┐
 │ 2. Group Events by Topic & Dispatch Parallel Goroutines  │
 │    - admin.market-halted.v1                              │
 │    - admin.market-resumed.v1                             │
 │    - admin.wallet-frozen.v1                              │
 │    - admin.user-suspended.v1                             │
 └───────────────────────────┬──────────────────────────────┘
                             │
                             ▼
 ┌──────────────────────────────────────────────────────────┐
 │ 3. Publish to Apache Kafka via kafka.Writer              │
 │    - Balance: Hash partitioning                          │
 │    - RequiredAcks: RequireAll                            │
 └───────────────────────────┬──────────────────────────────┘
                             │
                    Did Kafka Acknowledge?
                             ├── NO ──► Log error, back off with jitter, retry next tick
                             │
                            └── YES
                             │
                             ▼
 ┌──────────────────────────────────────────────────────────┐
 │ 4. Mark Outbox Events Published in PostgreSQL            │
 │    UPDATE admin_outbox SET published_at = NOW()          │
 └──────────────────────────────────────────────────────────┘
```

### 6.2 Distributed Saga Worker Flow
* **File:** [`services/admin/internal/service/saga_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/saga_worker.go)
* **Schedule:** 5-second interval (`SAGA_INTERVAL`)

```
                 TICK EVENT (Every 5 Seconds)
                             │
                             ▼
 ┌──────────────────────────────────────────────────────────┐
 │ 1. Query Pending Sagas                                   │
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
│    - Write response body  │ │    - if > 10 attempts:    │
└───────────────────────────┘ │      Mark EXHAUSTED &     │
                              │      Trigger SRE alert    │
                              └───────────────────────────┘
```

### 6.3 Anti-Drift State Reconciler Flow
* **File:** [`services/admin/internal/service/reconciler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/reconciler.go)
* **Schedule:** 15-second interval (`RECONCILIATION_INTERVAL`)

```
                 TICK EVENT (Every 15 Seconds)
                             │
                             ▼
 ┌──────────────────────────────────────────────────────────┐
 │ 1. Fetch Authoritative Snapshot from PostgreSQL          │
 │    GetLatestMarketStates() queries admin_operations      │
 └───────────────────────────┬──────────────────────────────┘
                             │
                             ▼
 ┌──────────────────────────────────────────────────────────┐
 │ 2. Scan Current Keys in Redis Cache                      │
 │    Non-blocking SCAN cursor: 0, MATCH market:halted:*    │
 └───────────────────────────┬──────────────────────────────┘
                             │
                             ▼
 ┌──────────────────────────────────────────────────────────┐
 │ 3. Bi-directional Convergence Engine                     │
 │    ├── Missing in Redis?  ──► SET market:halted:ID = "1" │
 │    └── Stale in Redis?    ──► DEL stale key from Redis   │
 └───────────────────────────┬──────────────────────────────┘
                             │
                             ▼
 ┌──────────────────────────────────────────────────────────┐
 │ 4. Assert Market Enforcement Sentinel                    │
 │    SET market:enforcement:ready = "1" (Guarantees orders │
 │    cannot pass until Redis state is confirmed clean)     │
 └──────────────────────────────────────────────────────────┘
```

### 6.4 Autonomous Health Prober Flow
* **File:** [`services/admin/internal/service/health_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/health_worker.go)
* **Schedule:** 15-second interval (`HEALTH_INTERVAL`)

```
                 TICK EVENT (Every 15 Seconds)
                             │
                             ▼
 ┌──────────────────────────────────────────────────────────┐
 │ 1. Acquire probeMu (Strict Serialization)                │
 └───────────────────────────┬──────────────────────────────┘
                             │
                             ▼
 ┌──────────────────────────────────────────────────────────┐
 │ 2. Launch Concurrent Probers in Parallel Goroutines      │
 │    ├── HTTP GET /ready (Trade, Port, Liq, Notif) [1.5s]  │
 │    ├── gRPC Transport Pings (Auth, Wallet)               │
 │    ├── PostgreSQL Connection Pool Ping                   │
 │    ├── Kafka TCP Dial across configured brokers          │
 │    └── Backlog Stats (Outbox depth, Saga retry counts)   │
 └───────────────────────────┬──────────────────────────────┘
                             │
                             ▼
 ┌──────────────────────────────────────────────────────────┐
 │ 3. Evaluate Status Rules (HEALTHY / DEGRADED / UNHEALTHY)│
 └───────────────────────────┬──────────────────────────────┘
                             │
                             ▼
 ┌──────────────────────────────────────────────────────────┐
 │ 4. Update Prometheus Telemetry                           │
 │    - Sets 1-hot gauges (admin_dependency_health_state)   │
 │    - Records latency histograms & failure counters       │
 └───────────────────────────┬──────────────────────────────┘
                             │
                             ▼
 ┌──────────────────────────────────────────────────────────┐
 │ 5. Autonomous Incident Lifecycle Transitions             │
 │    - If DOWN / TIMEOUT ──► Open P1 incident (or heartbeat│
 │    - If DEGRADED >= 3  ──► Open P2 degradation incident  │
 │    - If UP             ──► Auto-resolve & calculate MTTR │
 └───────────────────────────┬──────────────────────────────┘
                             │
                             ▼
 ┌──────────────────────────────────────────────────────────┐
 │ 6. Save Thread-Safe In-Memory Snapshot                   │
 │    Writes defensive copy to latestHealth (under RWMutex) │
 └──────────────────────────────────────────────────────────┘
```

---

## 7. Observability & Operational Intelligence Flows

### 7.1 Liveness & Readiness Probing Flow
* **Liveness (`GET /health`):** Directly returns `{"status": "ok"}` with `HTTP 200`. Zero network I/O.
* **Readiness (`GET /ready`):** 
  - If `isShuttingDown.Load() == true`, returns `HTTP 503 Service Unavailable`.
  - Runs parallel 2-second pings to PostgreSQL, Kafka, Auth, and Wallet. Returns `HTTP 200` if all pass, `HTTP 503` if any fail.

### 7.2 Service Dependency Topology Flow (`GET /api/v1/admin/topology`)
* Handler: [`topology_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/topology_handler.go)
* Calls `topologyEng.GetTopology()`.
* Combines static platform architecture definitions with live status from `HealthWorker.GetLatestHealth()`.
* Returns 9 graph nodes (with latencies, tiers, circuit breaker status), 18 directional communication edges (with protocol types), and an operational summary.

### 7.3 Operational Analytics & Risk Signals Flow
* **Analytics Overview (`GET /api/v1/admin/analytics/overview`):** Queries PostgreSQL for a 24-hour aggregation of operations by type, status, success rate percentage, and top operating admin IDs.
* **Risk Signals (`GET /api/v1/admin/analytics/risk-signals`):** Evaluates operational anomalies over rolling 1-hour windows:
  - `RAPID_USER_SUSPENSIONS`: Spike in user lockouts.
  - `FREQUENT_MARKET_HALTS`: Repeated halting of trading pairs.
  - `ADMIN_IP_DIVERSITY`: Administrative actions originating from multiple distinct IP addresses.
  - `EXTENDED_OUTAGE`: Infrastructure dependencies remaining degraded for >30 minutes.

### 7.4 Incident Lifecycle & Correlation Flow
* **List Incidents (`GET /api/v1/admin/incidents`):** Lists outages recorded in `admin_incidents`.
* **Incident Statistics (`GET /api/v1/admin/incidents/stats`):** Computes total outages, open vs resolved counts, and Mean Time to Resolution (MTTR in seconds).
* **Blast Radius Correlation (`GET /api/v1/admin/incidents/{id}/correlated`):**
  - Fetches the incident by ID.
  - Queries `admin_audit_log` within a **+/- 15-minute window** around `triggered_at`.
  - Correlates whether any administrative action (e.g. market halt, configuration change) preceded or accompanied the outage.
* **Manual Resolution (`POST /api/v1/admin/incidents/{id}/resolve`):** Allows an operator to manually resolve an incident with a root cause explanation (returns `HTTP 409` if already resolved).

---

## 8. Graceful Shutdown & Drainage Flow

When Kubernetes sends `SIGTERM` or the process is stopped:

```
                  OPERATING SYSTEM / KUBERNETES SENDS SIGTERM
                                      │
                                      ▼
             ┌─────────────────────────────────────────────────┐
             │ Step A: Set Readiness to 503                    │
             │         healthHdr.SetShuttingDown()             │
             │         isShuttingDown.Store(true)              │
             │         Next /ready returns 503 immediately!    │
             │         Load Balancers stop sending new traffic │
             └────────────────────────┬────────────────────────┘
                                      │
                                      ▼
             ┌─────────────────────────────────────────────────┐
             │ Step B: Drain In-Flight HTTP Requests           │
             │         srv.Shutdown(ctx) with 10s budget       │
             │         Existing requests complete cleanly      │
             └────────────────────────┬────────────────────────┘
                                      │
                                      ▼
             ┌─────────────────────────────────────────────────┐
             │ Step C: Terminate Background Workers            │
             │         ├── outboxPub.Stop()                    │
             │         ├── sagaWorker.Stop()                   │
             │         ├── reconciler.Stop()                   │
             │         └── healthWorker.Stop()                 │
             │         Awaits active batches to finish         │
             └────────────────────────┬────────────────────────┘
                                      │
                                      ▼
             ┌─────────────────────────────────────────────────┐
             │ Step D: Close Database Pools                    │
             │         dbPool.Close()                          │
             │         Closes all idle & active connections    │
             └────────────────────────┬────────────────────────┘
                                      │
                                      ▼
                          PROCESS TERMINATES CLEANLY
                                (Exit Code 0)
```

---

## 9. Complete Codebase File Directory Reference

| Layer | File Path | Primary Responsibilities |
| :--- | :--- | :--- |
| **Entrypoint** | [`cmd/server/main.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/server/main.go) | Service bootstrap, dependency injection, startup reconciliation, graceful drainage. |
| **Configuration** | [`internal/config/config.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/config/config.go) | Environment precedence loader and fail-fast validation. |
| **Domain Definitions** | [`internal/domain/events.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/events.go) | Canonical Kafka topic names, event envelopes, audit actions. |
| **Domain Operations** | [`internal/domain/operations.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/operations.go) | Operation types, statuses, saga types, and domain error definitions. |
| **HTTP Router** | [`internal/handler/router.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/router.go) | URL muxing, route normalization, metrics wrapping. |
| **Middleware** | [`internal/handler/middleware.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/middleware.go) | JWT verification, idempotency checking, structured request logging. |
| **Admin Handlers** | [`internal/handler/admin_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/admin_handler.go) | User, market, and wallet mutation endpoint handlers. |
| **Health Handlers** | [`internal/handler/health_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/health_handler.go) | `/health`, `/ready`, and `/api/v1/admin/system/health` handlers. |
| **Topology Handler** | [`internal/handler/topology_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/topology_handler.go) | Interactive cluster architecture graph endpoint. |
| **Incident Handlers** | [`internal/handler/incident_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/incident_handler.go) | Outage tracking, stats, correlation, and resolution handlers. |
| **Analytics Handlers** | [`internal/handler/analytics_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/analytics_handler.go) | Operations overview and anomaly risk signal handlers. |
| **Core Service** | [`internal/service/admin_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/admin_service.go) | Admin service struct, Redis retry helpers, conflict resolvers. |
| **User Service** | [`internal/service/user_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/user_service.go) | Suspend and unsuspend user execution logic. |
| **Market Service** | [`internal/service/market_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/market_service.go) | Halt and resume market execution, gauge updates. |
| **Wallet Service** | [`internal/service/wallet_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/wallet_service.go) | Freeze and unfreeze wallet execution, in-flight reconciliation. |
| **Outbox Publisher** | [`internal/service/outbox_publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/outbox_publisher.go) | Transactional outbox polling and Apache Kafka publisher. |
| **Saga Worker** | [`internal/service/saga_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/saga_worker.go) | Asynchronous distributed retry worker. |
| **State Reconciler** | [`internal/service/reconciler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/reconciler.go) | 15s bi-directional PostgreSQL ↔ Redis anti-drift synchronizer. |
| **Health Worker** | [`internal/service/health_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/health_worker.go) | Autonomous concurrent prober of all 9 cluster microservices. |
| **Incident Transitions** | [`internal/service/incident_transitions.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/incident_transitions.go) | Automated outage detection, heartbeating, and recovery MTTR calculation. |
| **Topology Engine** | [`internal/service/topology_engine.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/topology_engine.go) | Graph generator across 4 tiers and live circuit breaker states. |
| **Incident Service** | [`internal/service/incident_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/incident_service.go) | Incident querying and +/- 15-minute audit log blast radius correlation. |
| **Analytics Service** | [`internal/service/analytics_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/analytics_service.go) | 24h operational aggregations and anomaly risk detection. |
| **Auth Client** | [`internal/client/auth_client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/auth_client.go) | Auth microservice gRPC client. |
| **Wallet Client** | [`internal/client/wallet_client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/wallet_client.go) | Wallet microservice gRPC client. |
| **Tx Manager** | [`internal/repository/postgres/tx_manager.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/tx_manager.go) | Atomic multi-table database transaction coordinator. |
| **Operations Repo** | [`internal/repository/postgres/operations_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/operations_repo.go) | Idempotency lookup and authoritative market snapshots. |
| **Audit Repo** | [`internal/repository/postgres/audit_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/audit_repo.go) | Immutable audit log persistence and time-window queries. |
| **Outbox Repo** | [`internal/repository/postgres/outbox_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/outbox_repo.go) | Outbox polling with `SKIP LOCKED` and publish confirmations. |
| **Saga Repo** | [`internal/repository/postgres/saga_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/saga_repo.go) | Distributed saga task queue persistence and retry state. |
| **Incident Repo** | [`internal/repository/postgres/incident_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/incident_repo.go) | Incident creation, heartbeating, resolution, and MTTR queries. |
| **Metrics** | [`internal/metrics/metrics.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/metrics/metrics.go) | Prometheus 1-hot gauges, latency histograms, and failure counters. |
| **E2E Test Suite** | [`test/e2e/test_control_plane.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/test/e2e/test_control_plane.go) | End-to-end black-box integration test harness. |
