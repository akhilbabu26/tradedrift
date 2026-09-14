# Business Service & Background Worker Subsystem (`internal/service`)

This document provides a comprehensive architectural and operational manual for the core business orchestration and background worker daemons located in [`services/admin/internal/service/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/).

---

## Table of Contents

1. [Package Overview & Architecture](#1-package-overview--architecture)
2. [What Problems This Package Solves](#2-what-problems-this-package-solves)
3. [File-by-File Deep Dive](#3-file-by-file-deep-dive)
   - [admin_service.go — Struct & Shared Helpers](#admin_servicego--struct--shared-helpers)
   - [user_service.go — User Lifecycle](#user_servicego--user-lifecycle)
   - [wallet_service.go — Wallet Control](#wallet_servicego--wallet-control)
   - [market_service.go — Market Circuit Breaker](#market_servicego--market-circuit-breaker)
   - [health_worker.go — Lifecycle & RunProbe Orchestration](#health_workergo--lifecycle--runprobe-orchestration)
   - [health_probe.go — Transport-Level Probers](#health_probego--transport-level-probers)
   - [incident_transitions.go — Incident FSM](#incident_transitionsgo--incident-fsm)
   - [incident_service.go — Incident Lifecycle API](#incident_servicego--incident-lifecycle-api)
   - [analytics_service.go — Operations Analytics](#analytics_servicego--operations-analytics)
   - [topology.go — Platform Dependency Graph](#topologygo--platform-dependency-graph)
   - [outbox_publisher.go — Guaranteed Kafka Delivery](#outbox_publishergo--guaranteed-kafka-delivery)
   - [saga_worker.go — Distributed Saga Engine](#saga_workergo--distributed-saga-engine)
4. [Critical Coordination & Reliability Patterns](#4-critical-coordination--reliability-patterns)
5. [Architectural & Execution Flows](#5-architectural--execution-flows)

---

## 1. Package Overview & Architecture

The `internal/service` package represents the **Core Business Logic and Background Worker Layer** of the Admin Service under Clean Architecture principles.

All files share `package service`. Methods on `AdminService` are spread across domain-scoped files — Go allows multiple files to define methods on the same struct within a single package. No interfaces change.

```
package service
    │
    ├── AdminService (Control Plane — split across 4 files)
    │   ├── admin_service.go      struct, constructor, shared helpers
    │   ├── user_service.go       SuspendUser, UnsuspendUser
    │   ├── wallet_service.go     FreezeWallet, UnfreezeWallet
    │   └── market_service.go     HaltMarket, ResumeMarket, ReconstructMarketState
    │
    ├── HealthWorker (Health Surveillance — split across 3 files)
    │   ├── health_worker.go      struct, lifecycle, RunProbe orchestration
    │   ├── health_probe.go       individual transport-level probe functions
    │   └── incident_transitions.go  incident FSM (DOWN/DEGRADED/UP handling)
    │
    ├── IncidentService           Incident listing, resolution, forensic correlation
    ├── AnalyticsService          Operations volume & heuristic risk signals
    ├── TopologyEngine            Platform dependency graph + HealthWorker overlay
    ├── OutboxPublisher           Guaranteed Kafka delivery worker
    └── SagaWorker                Distributed side-effect retry engine
```

---

## 2. What Problems This Package Solves

| Problem | Without `internal/service` | How It Solves It |
| :--- | :--- | :--- |
| **Dual-Write Vulnerability** | DB commit succeeds but Kafka crashes before event is sent. Downstream services never receive the mutation. | **Transactional Outbox**: event written to `admin_outbox` in the same ACID transaction. `OutboxPublisher` guarantees at-least-once delivery. |
| **Cascading Downstream Outages** | Suspending a user requires a live Auth service call. If Auth is restarting, the admin request blocks indefinitely. | **Fast-Path + Saga Fallback**: attempts synchronous Auth call; if unavailable, returns HTTP 200 and leaves a `SagaTask` for `SagaWorker` to retry up to 8 hours. |
| **Duplicate Active Incidents** | Each `HealthWorker` probe against a DOWN service would create a new incident, flooding the incident table. | **Transition-Based FSM + Partial Unique Index**: `incident_transitions.go` only creates an incident on a fresh DOWN detection; subsequent probes call `UpdateHeartbeat`. The PostgreSQL `UNIQUE(service_name) WHERE status='OPEN'` index is a database-level backstop. |
| **Concurrent Idempotency Collisions** | Two identical admin requests race to insert the same idempotency key. | Catches PostgreSQL `23505` unique constraint violation and re-fetches the winning operation via `resolveConcurrentConflict`. |
| **Health Probe Latency** | Probing 8 microservices inline in HTTP handlers takes 2–5 seconds. | `HealthWorker` runs autonomously every 15s. `/system/health` reads from `latestHealth` cache in **< 1ms**. |
| **Stale Market State After Restart** | Prometheus market halt gauges are reset to zero when the Admin service restarts. | `ReconstructMarketState()` queries the latest `HALT_MARKET`/`RESUME_MARKET` operations from `admin_operations` on startup and restores all gauges. |
| **Silent MTTD Fabrication** | Setting `DetectedAt = TriggeredAt` on autonomous probe incidents reports MTTD = 0, which is misleading. | `MTTDSeconds` is `*float64` (pointer). For HealthWorker-created incidents, it is left `nil`. Only incidents with a genuine independent failure-start timestamp have a meaningful MTTD. |
| **Thundering Herd on Recovery** | Thousands of queued retry tasks hammer a recovering service simultaneously. | `SagaWorker` and `OutboxPublisher` use exponential backoff with ±10% full randomized jitter across 10 schedule tiers (30s → 8h). |

---

## 3. File-by-File Deep Dive

### `admin_service.go` — Struct & Shared Helpers
- **File**: [`admin_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/admin_service.go)
- **Contents**: `AdminService` struct definition, `NewAdminService` constructor, `resolveConcurrentConflict`, `isUniqueViolation`.
- **Role**: Owns the struct and provides the two helpers that all three operation domains (`user`, `wallet`, `market`) call for concurrent conflict handling.

---

### `user_service.go` — User Lifecycle
- **File**: [`user_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/user_service.go)

| Method | DB Writes | Downstream | Returns |
| :--- | :--- | :--- | :--- |
| `SuspendUser` | `admin_operations` (PROCESSING), `admin_audit_log`, `admin_outbox` (user-suspended.v1), `admin_saga_tasks` | Fast-path: `authCli.InvalidateUserSessions`. On success → `CompleteAuthSaga` (COMPLETED). On failure → SagaWorker retries. | Operation |
| `UnsuspendUser` | `admin_operations` (COMPLETED), `admin_audit_log`, `admin_outbox` (user-unsuspended.v1) | None | Operation |

---

### `wallet_service.go` — Wallet Control
- **File**: [`wallet_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/wallet_service.go)

Both `FreezeWallet` and `UnfreezeWallet` delegate to the shared `executeWalletMutation` helper. On a PROCESSING reconciliation path (previous attempt timed out), the function re-invokes the idempotent Wallet gRPC RPC using the existing `operation_id` — no duplicate DB row is created.

| Method | DB Writes | Downstream |
| :--- | :--- | :--- |
| `FreezeWallet` | Operation (PROCESSING→COMPLETED), audit, outbox (wallet-frozen.v1) | `walletCli.FreezeWallet(freeze=true)` |
| `UnfreezeWallet` | Operation (PROCESSING→COMPLETED), audit, outbox (wallet-unfrozen.v1) | `walletCli.FreezeWallet(freeze=false)` |

---

### `market_service.go` — Market Circuit Breaker
- **File**: [`market_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/market_service.go)

| Method | DB Writes | Prometheus Side-Effect |
| :--- | :--- | :--- |
| `HaltMarket` | Operation (COMPLETED), audit, outbox (market-halted.v1) | `RecordMarketHalted(marketID, now)` — sets halt gauge + start timestamp |
| `ResumeMarket` | Operation (COMPLETED), audit, outbox (market-resumed.v1) | `RecordMarketResumed(marketID)` — zeros halt gauge |
| `ReconstructMarketState` | Read-only | Restores all market halt gauges from `admin_operations` history on startup |

`ReconstructMarketState` is called during `main.go` bootstrap **before** the HTTP server starts accepting traffic, ensuring Prometheus always reflects the correct market state.

---

### `health_worker.go` — Lifecycle & RunProbe Orchestration
- **File**: [`health_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/health_worker.go)
- **Primary Type**: `type HealthWorker struct`

**Lifecycle contract**: `Start` is idempotent via `sync.Once`. `Stop` is idempotent via `sync.Once` and cancels the worker context, immediately aborting in-flight network probes. `wg.Wait()` guarantees no goroutine leaks.

**`RunProbe`** orchestrates the probe fan-out (delegating to `health_probe.go`), collects backlog stats, computes platform status, updates Prometheus, caches the result under `mu`, then calls `processIncidentTransitions` (implemented in `incident_transitions.go`).

**Probe serialization**: `probeMu sync.Mutex` ensures consecutive probe cycles never overlap, preventing double-counting in metrics.

---

### `health_probe.go` — Transport-Level Probers
- **File**: [`health_probe.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/health_probe.go)

| Function | Protocol | Timeout Behaviour |
| :--- | :--- | :--- |
| `probeHTTPService` | HTTP GET | `ctx.Err() != nil` → `TIMEOUT`; non-200 → `DOWN`/`DEGRADED` |
| `probeAuthGRPC` | gRPC `Ping` | `codes.DeadlineExceeded` → `TIMEOUT`; other error → `DOWN` |
| `probeWalletGRPC` | gRPC `Ping` | Same as Auth |
| `probePostgres` | `dbPool.Ping` | `context.DeadlineExceeded` → `TIMEOUT` |
| `probeKafka` (standalone func) | TCP dial per broker | 1500ms per broker; stops at first reachable broker |
| `classifyFailureReason` | — | Maps `ServiceStatusReport` to a Prometheus label string |

`probeKafka` is a standalone (non-method) function because it does not need access to `HealthWorker` state — only the broker list and context.

---

### `incident_transitions.go` — Incident FSM
- **File**: [`incident_transitions.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/incident_transitions.go)

Called once per `RunProbe` cycle for every service in the merged `services + adminComponents` map.

```
currStatus
    │
    ├─ DOWN / TIMEOUT  ──────────────► handleDownTransition()
    │                                        │
    │                                 GetActiveByService()
    │                                 ├── Exists → UpdateHeartbeat(count+1)
    │                                 └── None   → Create Incident (severity by tier)
    │
    ├─ DEGRADED (≥3 consecutive) ───► handleSustainedDegradation()
    │                                        │
    │                                 GetActiveByService()
    │                                 ├── Exists → UpdateHeartbeat
    │                                 └── None   → Create P2 Incident
    │
    └─ UP ──────────────────────────► handleRecovery()
                                             │
                                      GetActiveByService()
                                      ├── Exists → Resolve (MTTR = now - triggered_at)
                                      └── None   → No-op
```

**Severity mapping** (`outageIncidentSeverity`):

| Services | Severity |
| :--- | :--- |
| `postgres`, `auth`, `wallet`, `trade` | P1 Critical |
| `kafka`, `liquidity_engine` | P2 High |
| `portfolio`, `notification` | P3 Moderate |

**Deduplication**: `ErrActiveIncidentExists` (from the PostgreSQL partial unique index) is silently swallowed and logged at DEBUG — it means a concurrent probe cycle already created the incident.

**DEGRADED→DOWN continuity**: When a service transitions from DEGRADED to DOWN, `GetActiveByService` finds the existing DEGRADED incident and calls `UpdateHeartbeat` rather than creating a duplicate.

---

### `incident_service.go` — Incident Lifecycle API
- **File**: [`incident_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/incident_service.go)

| Method | Description |
| :--- | :--- |
| `ListIncidents` | Paginated listing (`triggered_at DESC, id DESC` — stable ordering even with equal timestamps) |
| `GetIncident` | Fetch single incident by ID |
| `ResolveIncident` | Manual resolution with root-cause note; guards against already-resolved incidents |
| `GetCorrelatedIncidents` | Forensic window query — other incidents whose `triggered_at` falls within ±`correlationWindow` of the target incident |
| `GetIncidentStats` | Counts (total/open/resolved) and `avg_mttd_seconds` / `avg_mttr_seconds`; MTTD is `null` when no incidents have a genuine failure-start timestamp |

Correlation window is validated: must be positive and capped at 24 hours.

---

### `analytics_service.go` — Operations Analytics
- **File**: [`analytics_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/analytics_service.go)

| Method | Description |
| :--- | :--- |
| `GetOperationAnalytics` | Operations volume, success rate, and oldest PROCESSING age by window (validated: positive, capped at 24h) |
| `GetRiskSignals` | Heuristic risk signal evaluator: burst velocity (1-minute buckets), mass suspension detection, IP diversity |

Risk signals are sorted deterministically by signal name before being returned — ensuring consistent API output.

---

### `topology.go` — Platform Dependency Graph
- **File**: [`topology.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/topology.go)

Builds a static 9-node directed dependency graph (admin → auth, wallet, trade, portfolio, liquidity_engine, notification, postgres, kafka) and overlays real-time status from `HealthWorker.GetLatestHealth()`.

- **Node status**: Mapped from `HealthWorker` probe results. Services with no probe data → `UNKNOWN`.
- **Edge health**: An edge is `DOWN` if the target node is `DOWN` or `TIMEOUT`; `DEGRADED` if target is `DEGRADED`; `UNKNOWN` if the target has no probe data.
- **Summary counters**: Tracks `healthy`, `degraded`, `down`, `timeout`, `unknown` node counts for dashboard-level status.

---

### `outbox_publisher.go` — Guaranteed Kafka Delivery
- **File**: [`outbox_publisher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/outbox_publisher.go)

- **Non-blocking batch claiming**: `outboxRepo.FetchDue(ctx, workerToken, 50)` using `FOR UPDATE SKIP LOCKED`.
- **Synchronous ACK**: `RequiredAcks: kafka.RequireAll`, `Async: false`. Marks `published_at = NOW()` only after all in-sync replicas acknowledge.
- **Lease fencing**: `MarkPublished` verifies `locked_by = workerToken` — returns `ErrWorkerLeaseLost` if the lease expired during a slow write.
- **Progressive backoff with jitter**: `domain.NextDelay(attempt)` with ±10% randomization.

---

### `saga_worker.go` — Distributed Saga Engine
- **File**: [`saga_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/saga_worker.go)

- **Task claiming**: `sagaRepo.FetchDue(ctx, workerToken, 10)` with `FOR UPDATE SKIP LOCKED` and `status IN ('PENDING','RETRYING') AND next_attempt_at <= NOW()`.
- **gRPC error classification**: `client.IsRetryableGRPCError(err)` — non-retryable errors (`InvalidArgument`, `NotFound`) → immediate `MarkExhausted` + Prometheus alert. Retryable → `UpdateRetry` with progressive backoff (30s → 8h).
- **Two-phase atomic completion**: `txMgr.CompleteAuthSaga` marks both `admin_operations` and `admin_saga_tasks` as `COMPLETED` in a single transaction.

---

## 4. Critical Coordination & Reliability Patterns

### PostgreSQL Error 23505 Conflict Recovery
```go
var pgErr *pgconn.PgError
if errors.As(err, &pgErr) && pgErr.Code == "23505" {
    return s.resolveConcurrentConflict(ctx, adminID, idempotencyKey)
}
```
Both the primary insert path and the concurrent conflict recovery path exist in `admin_service.go`, shared by all three operation domains.

---

### Partial Unique Index for Incident Deduplication
```sql
-- 00005_create_admin_incidents.sql
CREATE UNIQUE INDEX idx_admin_incidents_active_service
    ON admin_incidents(service_name)
    WHERE status = 'OPEN';
```
The index allows multiple resolved incidents for the same service (historical record), but enforces at most one open incident per service at the database level.

---

### Worker Lifecycle Contract (Idempotent Start & Stop)
All workers (`HealthWorker`, `OutboxPublisher`, `SagaWorker`) implement:
- `Start(ctx)` → `startOnce.Do(…)` — exactly one background goroutine.
- `Stop()` → `stopOnce.Do(…)` + `wg.Wait()` — no goroutine leaks after stop.

---

### Individual Broker Timeout Budgeting
```go
// health_probe.go – probeKafka
for _, broker := range brokers {
    brokerCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
    conn, err := kafka.DialContext(brokerCtx, "tcp", broker)
    cancel()
    if err == nil { /* UP */ break }
}
```
A single offline broker times out in 1500ms and the loop moves to the next broker within the remaining parent context budget.

---

## 5. Architectural & Execution Flows

### Flow 1: User Suspension with Asynchronous Saga Handoff
```
               Admin Client
                    │
                    ▼
       AdminService.SuspendUser()    [user_service.go]
                    │
                    ▼
      PostgreSQL Atomic Transaction
 (Operation=PROCESSING, Audit, Outbox, SagaTask)
                    │
                    ▼
          Fast-Path: Call Auth gRPC
                    │
      ┌─────────────┴─────────────┐
  (Auth Online)             (Auth Down / Timeout)
      ▼                           ▼
CompleteAuthSaga()          HTTP 200 OK (PROCESSING)
HTTP 200 OK (COMPLETED)           │
                                  ▼
                             SagaWorker
                          (Background Loop)
                                  │
                          Retry Auth Invalidation
                          (up to 10 attempts / 8h)
                                  │
                          CompleteAuthSaga()
```

### Flow 2: Outbox Worker Polling & Kafka ACK Delivery
```
             OutboxPublisher
            (Every 1s Ticker)
                    │
                    ▼
      SELECT id FROM admin_outbox
       WHERE published=FALSE
         FOR UPDATE SKIP LOCKED
       UPDATE locked_by=workerToken
                    │
                    ▼
          Kafka Write (RequireAll)
                    │
        ┌───────────┴───────────┐
    (Broker ACK)          (Broker Error)
        ▼                      ▼
MarkPublished()         UpdateRetry()
published=TRUE          next=now+30s+jitter
```

### Flow 3: Incident FSM Probe Cycle
```
         HealthWorker.RunProbe()
                    │
                    ▼
   processIncidentTransitions()   [incident_transitions.go]
                    │
       ┌────────────┴────────────┐
   service DOWN             service UP
       │                        │
   handleDownTransition()    handleRecovery()
       │                        │
 GetActiveByService()       GetActiveByService()
 ├─ Exists →                ├─ Exists → Resolve(MTTR)
 │   UpdateHeartbeat()      └─ None   → No-op
 └─ None →
     Create Incident
     (partial UQ guard)
```

### Flow 4: Market Halt Reconstruction on Restart
```
             main.go bootstrap
                    │
                    ▼
  ReconstructMarketState(ctx)   [market_service.go]
                    │
                    ▼
  opsRepo.GetLatestMarketStates()
  (Latest op per market_id)
                    │
        ┌───────────┴───────────┐
     IsHalted               Resumed
        │                       │
        ▼                       ▼
RecordMarketHalted()     RecordMarketResumed()
(Gauge=1, timestamp)     (Gauge=0)
```
