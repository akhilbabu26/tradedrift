# Admin Service: Health Architecture, Handlers & Probing Flows

This document details the complete design, runtime workflows, and code implementation of the **Health and Diagnostic Subsystem** within the **TradeDrift Admin Control Plane**.

---

## Architectural Overview

In a distributed financial platform, health monitoring must be far more sophisticated than a basic HTTP `200 OK` ping. The Admin Service implements a **decoupled, multi-tier health monitoring architecture** that continuously monitors all 9 TradeDrift microservices, internal queues, and infrastructure components without creating dependency amplification loops.

```
                   Kubernetes / Load Balancer / Operator
                                     │
           ┌─────────────────────────┼─────────────────────────┐
           ▼                         ▼                         ▼
   GET /health (Liveness)     GET /ready (Readiness)    GET /system/health
   Process alive?             Ready for traffic?        Deep Diagnostic State
   (200 OK)                   (Local SQL/Kafka/gRPC)    (Full Cluster Map)
                                     │                         │
                                     │                         ▼
                                     │               Cached In-Memory Snapshot
                                     │               (Sub-millisecond read lock)
                                     │                         ▲
                                     │                         │ Writes snapshot
                                     │                         │ (every 15s)
                                     │               ┌─────────────────────────┐
                                     │               │   Autonomous Background  │
                                     │               │      HealthWorker       │
                                     │               └────────────┬────────────┘
                                     │                            │
                     ┌───────────────┴──────────────┐             │ Probes dependencies
                     ▼                              ▼             ▼ concurrently
              Graceful Drainage             Direct Probes   ┌───────────────┐
              isShuttingDown = true         - PostgreSQL    │ HTTP Targets  │ Trade, Portfolio, Liq, Notif
              (Returns 503 immediately)     - Kafka Cluster │ gRPC Targets  │ Auth, Wallet
                                            - Auth gRPC     │ Infrastructure│ Postgres, Kafka
                                            - Wallet gRPC   │ Queues        │ Outbox Depth, Saga Retries
                                                            └───────┬───────┘
                                                                    │ State transitions
                                                                    ▼
                                                            Autonomous Incident
                                                            Lifecycle (MTTR & DB)
```

---

## Table of Contents

1. [The Three Health Tiers Explained](#1-the-three-health-tiers-explained)
2. [Liveness Probe (`GET /health`)](#2-liveness-probe-get-health)
3. [Readiness Probe (`GET /ready`) & Graceful Drainage](#3-readiness-probe-get-ready--graceful-drainage)
4. [System Diagnostic Health (`GET /api/v1/admin/system/health`)](#4-system-diagnostic-health-get-apiv1adminsystemhealth)
5. [Autonomous HealthWorker Engine](#5-autonomous-healthworker-engine)
6. [Prometheus Telemetry & 1-Hot Health Gauges](#6-prometheus-telemetry--1-hot-health-gauges)
7. [Automated Incident Lifecycle & Recovery Detection](#7-automated-incident-lifecycle--recovery-detection)
8. [Service Dependency Topology Engine (`GET /api/v1/admin/topology`)](#8-service-dependency-topology-engine)
9. [Codebase Implementation Reference Table](#9-codebase-implementation-reference-table)

---

## 1. The Three Health Tiers Explained

| Tier | Endpoint | Auth | Target Consumer | Purpose |
| :--- | :--- | :---: | :--- | :--- |
| **Tier 1: Liveness** | `GET /health` | None | Kubernetes / Docker | Verifies the Go process is running and the HTTP mux is responsive. |
| **Tier 2: Readiness** | `GET /ready` | None | Ingress Load Balancers | Verifies Admin has connectivity to its own direct dependencies (PostgreSQL, Kafka, Auth, Wallet). |
| **Tier 3: Diagnostic Health** | `GET /api/v1/admin/system/health` | Bearer JWT (`admin`) | SRE / Ops Dashboard | Deep diagnostic report across all 9 platform microservices, queues, latencies, and circuit breakers. |

---

## 2. Liveness Probe (`GET /health`)

### 2.1 Purpose & Execution
The liveness probe guarantees that the operating system process has not deadlocked. It intentionally executes zero I/O or network requests to prevent temporary network blips from triggering container restarts.

```
                      ┌──────────────────────────────────────┐
                      │         KUBERNETES / DOCKER          │
                      └──────────────────┬───────────────────┘
                                         │
                                         │ 1. GET /health (Liveness Ping)
                                         ▼
                      ┌──────────────────────────────────────┐
                      │            HEALTH HANDLER            │
                      │  - Evaluates process & mux status    │
                      │  - Zero network I/O (deadlock check) │
                      └──────────────────┬───────────────────┘
                                         │
                                         │ 2. HTTP 200 OK {"status":"ok"}
                                         ▼
                      ┌──────────────────────────────────────┐
                      │          POD MARKED HEALTHY          │
                      └──────────────────────────────────────┘
```

* **HTTP Method:** `GET`
* **Route:** `/health`
* **Handler:** `HealthHandler.HandleLiveness`
* **Status Code:** `200 OK`

### 2.2 Where It Is Implemented in Code
* Handler: [`services/admin/internal/handler/health_handler.go:73-76`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/health_handler.go#L73-L76)
* Router Registration: [`services/admin/internal/handler/router.go:60`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/router.go#L60)

---

## 3. Readiness Probe (`GET /ready`) & Graceful Drainage

### 3.1 Purpose & Execution
The readiness probe informs Kubernetes and upstream load balancers whether this specific Admin instance is capable of accepting incoming operational requests.

If any required local dependency fails, or if the service is initiating a graceful shutdown, it returns `HTTP 503 Service Unavailable`, prompting the load balancer to route traffic away before the container terminates.

```
                                INGRESS LOAD BALANCER
                                          │
                                          │ 1. GET /ready
                                          ▼
                         ┌─────────────────────────────────┐
                         │         HEALTH HANDLER          │
                         │ Check isShuttingDown.Load()?    │
                         └────────────────┬────────────────┘
                                          │
                        ┌─────────────────┴─────────────────┐
                        │ YES (Shutting down)               │ NO (Active)
                        ▼                                   ▼
          ┌───────────────────────────┐       ┌───────────────────────────────────┐
          │ HTTP 503 Service Unavail  │       │ Parallel Direct Dependency Checks │
          │ {"status":"not_ready"}    │       │ (2-Second Timeout Budget)         │
          │ (Graceful Drainage Mode)  │       └─────────────────┬─────────────────┘
          └───────────────────────────┘                         │
                                  ┌─────────────────────────────┼─────────────────────────────┐
                                  ▼                             ▼                             ▼
                    ┌───────────────────────────┐ ┌───────────────────────────┐ ┌───────────────────────────┐
                    │      POSTGRESQL POOL      │ │       KAFKA CLUSTER       │ │    AUTH & WALLET gRPC     │
                    │      dbPool.Ping(ctx)     │ │    kafka.DialContext()    │ │     Active gRPC Pings     │
                    └─────────────┬─────────────┘ └─────────────┬─────────────┘ └─────────────┬─────────────┘
                                  │                             │                             │
                                  └─────────────────────────────┼─────────────────────────────┘
                                                                │
                                                Did all 4 direct checks succeed?
                                                                │
                                                 ┌──────────────┴──────────────┐
                                                 ▼                             ▼
                                                YES                            NO
                                 ┌───────────────────────────────┐ ┌───────────────────────────────┐
                                 │ HTTP 200 OK {"status":"ready"}│ │ HTTP 503 Service Unavailable  │
                                 │ Checks: db, kafka, auth, wall │ │ Checks show failed dependency │
                                 └───────────────────────────────┘ └───────────────────────────────┘
```

### 3.2 Coordinated Graceful Shutdown Drainage Sequence
When the container receives `SIGTERM` or `SIGINT`:
1. **Step A (Immediate Unreadiness):** In [`cmd/server/main.go:188`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/server/main.go#L188), it immediately calls `healthHdr.SetShuttingDown()`. The internal atomic boolean `isShuttingDown.Store(true)` causes `/ready` to instantly return `HTTP 503`.
2. **Step B (HTTP Drainage):** The HTTP server drains existing in-flight connections with a 10-second timeout.
3. **Step C (Worker Termination):** Background workers (`HealthWorker`, `OutboxPublisher`, `SagaWorker`, `StateReconciler`) complete active batches and terminate cleanly.

### 3.3 Where It Is Implemented in Code
* Readiness Handler: [`services/admin/internal/handler/health_handler.go:80-160`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/health_handler.go#L80-L160)
* Shutdown Step A: [`services/admin/cmd/server/main.go:187-190`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/server/main.go#L187-L190)

---

## 4. System Diagnostic Health (`GET /api/v1/admin/system/health`)

### 4.1 Purpose
Operators and admin dashboards require complete visibility into the health and latencies of all platform microservices, database pools, and messaging queues.

### 4.2 Non-Blocking Architecture
To prevent dashboard traffic from turning into a distributed denial-of-service attack on internal services:
- The HTTP handler **never executes network probes directly**.
- It reads an in-memory cached snapshot (`GetLatestHealth()`) pre-computed by the background `HealthWorker`.
- Response latency is **sub-millisecond (<1ms)** regardless of platform status.

```
                                OPERATOR (Postman) / SRE DASHBOARD
                                                │
                                                │ 1. GET /api/v1/admin/system/health
                                                │    (Bearer JWT with role="admin")
                                                ▼
                               ┌──────────────────────────────────┐
                               │          HEALTH HANDLER          │
                               │  - Never runs network probes!    │
                               │  - Non-blocking read lock        │
                               └────────────────┬─────────────────┘
                                                │
                                                │ 2. healthWorker.GetLatestHealth()
                                                ▼
                               ┌──────────────────────────────────┐
                               │           HEALTH WORKER          │
                               │  Acquires RLock() on memory cache│
                               └────────────────┬─────────────────┘
                                                │
                                                │ 3. Reads pre-computed snapshot copy
                                                ▼
                               ┌──────────────────────────────────┐
                               │         IN-MEMORY CACHE          │
                               │  (Refreshed every 15s by worker) │
                               └────────────────┬─────────────────┘
                                                │
                                                │ 4. Return complete cluster status (<1ms)
                                                ▼
                               ┌──────────────────────────────────┐
                               │           HTTP 200 OK            │
                               │ Full 9-Service Map, Outbox/Saga  │
                               │ Queue Depths, Latency Metrics    │
                               └──────────────────────────────────┘
```

### 4.3 Diagnostic JSON Response Structure
```json
{
  "overall_status": "HEALTHY",
  "timestamp": "2026-09-14T12:37:04Z",
  "services": {
    "auth": { "name": "auth", "status": "UP", "latency_ms": 2 },
    "wallet": { "name": "wallet", "status": "UP", "latency_ms": 1 },
    "trade": { "name": "trade", "status": "UP", "latency_ms": 7, "http_status": 200 },
    "portfolio": { "name": "portfolio", "status": "UP", "latency_ms": 6, "http_status": 200 },
    "liquidity_engine": { "name": "liquidity_engine", "status": "UP", "latency_ms": 4, "http_status": 200 },
    "notification": { "name": "notification", "status": "UP", "latency_ms": 8, "http_status": 200 }
  },
  "admin": {
    "status": "UP",
    "postgres": { "name": "postgres", "status": "UP", "latency_ms": 5 },
    "kafka": { "name": "kafka", "status": "UP", "latency_ms": 2 },
    "outbox": {
      "pending_count": 0,
      "oldest_age": 0
    },
    "saga": {
      "pending_count": 0,
      "retrying_count": 0,
      "exhausted_count": 0
    }
  }
}
```

### 4.4 Where It Is Implemented in Code
* Endpoint Handler: [`services/admin/internal/handler/health_handler.go:171-188`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/health_handler.go#L171-L188)
* Snapshot Retrieval: [`services/admin/internal/service/health_worker.go:167-180`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/health_worker.go#L167-L180)

---

## 5. Autonomous HealthWorker Engine

### 5.1 Overview
The [`HealthWorker`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/health_worker.go#L63) is an autonomous background engine that runs on a configurable interval (default: 15s). It orchestrates concurrent probing across all external dependencies.

```
                                   TICKER (Every 15s)
                                           │
                                           ▼
                       ┌──────────────────────────────────────┐
                       │ 1. Acquire probeMu                   │
                       │    Strict single-flight serialization│
                       └───────────────────┬──────────────────┘
                                           │
                                           ▼
                       ┌──────────────────────────────────────┐
                       │ 2. Launch Parallel Worker Probes     │
                       │    ├── HTTP GET /ready [1.5s timeout]│
                       │    │   (Trade, Portfolio, Liq, Notif)│
                       │    ├── gRPC Transport Pings          │
                       │    │   (Auth, Wallet)                │
                       │    ├── Kafka Broker Socket Dials     │
                       │    ├── PostgreSQL Connection Ping    │
                       │    └── Queue Depths (Outbox & Sagas) │
                       └───────────────────┬──────────────────┘
                                           │
                                           ▼
                       ┌──────────────────────────────────────┐
                       │ 3. Evaluate Platform Health Rules    │
                       │    ├── Postgres DOWN  ──► UNHEALTHY  │
                       │    ├── Core DOWN      ──► UNHEALTHY  │
                       │    ├── Secondary DOWN ──► DEGRADED   │
                       │    └── All OK         ──► HEALTHY    │
                       └───────────────────┬──────────────────┘
                                           │
                                           ▼
                       ┌──────────────────────────────────────┐
                       │ 4. Update Prometheus Telemetry       │
                       │    - Record 1-Hot Gauges (UP/DEG/DOWN│
                       │    - Record Latency Histograms       │
                       │    - Increment Failure Counters      │
                       └───────────────────┬──────────────────┘
                                           │
                                           ▼
                       ┌──────────────────────────────────────┐
                       │ 5. Automated Incident Transitions    │
                       │    ├── DOWN / TIMEOUT ──► Open P1    │
                       │    ├── DEGRADED >= 3  ──► Open P2    │
                       │    └── UP             ──► Auto-heal  │
                       └───────────────────┬──────────────────┘
                                           │
                                           ▼
                       ┌──────────────────────────────────────┐
                       │ 6. Commit Snapshot to In-Memory Cache│
                       │    - Writes defensive copy under lock│
                       │    - Release probeMu                 │
                       └──────────────────────────────────────┘
```

### 5.2 Probing Strategies by Target Type

#### 1. HTTP Microservices (Trade, Portfolio, Liquidity, Notification)
- **Protocol:** HTTP GET with a strict `1500ms` client timeout.
- **Classification:**
  - `HTTP 200 OK` &rarr; `UP`
  - Client timeout &rarr; `TIMEOUT`
  - Connection refused / DNS failure &rarr; `DOWN`
  - `HTTP 503` / unexpected code &rarr; `DEGRADED`

#### 2. Auth gRPC Transport
- Executes an active gRPC probe (`authCli.Ping(ctx)`).
- Dials connection state; if `TRANSIENT_FAILURE` or `SHUTDOWN`, reports `DOWN`.

#### 3. Wallet gRPC Application Health
- Executes an application-level ping against the Wallet microservice gRPC endpoint.
- Verifies ledger service responsiveness.

#### 4. PostgreSQL Connection Pool
- Executes `dbPool.Ping(ctx)`.
- If ping fails, Admin marks the platform **`UNHEALTHY`** because PostgreSQL is the mandatory system of record.

#### 5. Apache Kafka Broker Availability
- Loops through configured brokers (`KAFKA_BROKERS`) and executes TCP socket dials.
- If at least one broker responds within timeout, Kafka is marked `UP`.

#### 6. Asynchronous Backlog Queues
- Queries `admin_outbox` for count of pending events and oldest unpublished age.
- Queries `admin_saga_tasks` for pending, retrying, and exhausted task counts.

### 5.3 Overall Platform Health Calculation Rules
In [`health_worker.go:310-330`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/health_worker.go#L310-L330):
- If **PostgreSQL** is `DOWN`, `UNKNOWN`, or `TIMEOUT` &rarr; Overall is **`UNHEALTHY`**.
- If core trading services (**Auth**, **Wallet**, **Trade**, **Portfolio**, **Liquidity Engine**) are `DOWN` or `TIMEOUT` &rarr; Overall is **`UNHEALTHY`**.
- If secondary services (e.g. **Notification**) are `DOWN`, or if Kafka is degraded &rarr; Overall is **`DEGRADED`**.
- If all services and infrastructure report `UP` &rarr; Overall is **`HEALTHY`**.

---

## 6. Prometheus Telemetry & 1-Hot Health Gauges

### 6.1 The 1-Hot Gauge Pattern
To eliminate metric race conditions where a service simultaneously reports `healthy=1` and `unhealthy=1`, the Admin Service implements the **1-hot gauge pattern**:

```prometheus
# HELP admin_dependency_health_state 1-hot gauge indicating dependency health state (1=active, 0=inactive)
# TYPE admin_dependency_health_state gauge
admin_dependency_health_state{service="trade",state="UP"} 1
admin_dependency_health_state{service="trade",state="DEGRADED"} 0
admin_dependency_health_state{service="trade",state="DOWN"} 0

admin_dependency_health_state{service="auth",state="UP"} 1
admin_dependency_health_state{service="auth",state="DEGRADED"} 0
admin_dependency_health_state{service="auth",state="DOWN"} 0
```
For any given service, **exactly one** label combination is `1`; all other states are explicitly set to `0`.

### 6.2 Key Prometheus Metrics Exported
| Metric Name | Type | Description |
| :--- | :---: | :--- |
| `admin_dependency_health_state` | Gauge | 1-hot dependency state (`UP`, `DEGRADED`, `DOWN`). |
| `admin_dependency_health_latency_seconds` | Histogram | Latency histogram of health checks per dependency. |
| `admin_dependency_health_check_timestamp_seconds` | Gauge | Unix timestamp of the last executed probe. |
| `admin_dependency_health_failures_total` | Counter | Total probe failures partitioned by reason (`timeout`, `http_5xx`, etc.). |
| `admin_platform_overall_status` | Gauge | 1-hot gauge for overall platform health (`HEALTHY`, `DEGRADED`, `UNHEALTHY`). |
| `admin_outbox_backlog_depth` | Gauge | Number of unpublished outbox events awaiting Kafka delivery. |
| `admin_saga_queue_depth` | Gauge | Number of active saga retry tasks partitioned by state. |

---

## 7. Automated Incident Lifecycle & Recovery Detection

### 7.1 Lifecycle State Machine
The [`processIncidentTransitions`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/incident_transitions.go#L24) engine runs after every health probe, automatically translating infrastructure status changes into persistent operational incidents:

```
                            PROBE EXECUTES ACROSS SERVICE
                                          │
                   ┌──────────────────────┼──────────────────────┐
                   ▼                      ▼                      ▼
            Status: UP            Status: DEGRADED        Status: DOWN / TIMEOUT
                   │                      │                      │
                   │               degradedCount++               │
                   │                      │                      │
                   │           Is degradedCount >= 3?            │
                   │           (3 probes ≈ 45 seconds)           │
                   │                      │                      │
                   │               ┌──────┴──────┐               │
                   │               ▼             ▼               │
                   │             >= 3           < 3              │
                   │               │             │               │
                   │               │          No-op              │
                   │               ▼             │               ▼
                   │      ┌──────────────────┐   │      ┌──────────────────┐
                   │      │ Open P2 Incident │   │      │ Open P1 Incident │
                   │      │ (Degradation)    │   │      │ (Outage)         │
                   │      └────────┬─────────┘   │      └────────┬─────────┘
                   │               │             │               │
                   │               └─────────────┼───────────────┘
                   │                             │
                   ▼                             ▼
       ┌────────────────────────┐    ┌────────────────────────┐
       │ Active Incident Found? │    │ Subsequent DOWN Probes │
       │ ├── YES ──► Auto-heal: │    │ └── Update heartbeat:  │
       │ │   - Calculate MTTR   │    │     probe_failure_cnt++│
       │ │   - Status: RESOLVED │    │     last_seen_at = NOW │
       │ └── NO  ──► Normal OK  │    └────────────────────────┘
       └────────────────────────┘
```

### 7.2 Transition Rules
1. **Outage Detection (`DOWN` or `TIMEOUT`):**
   - Opens a `P1_CRITICAL` incident in PostgreSQL (`admin_incidents`).
   - If an incident for this service is already open, it increments `probe_failure_count` and updates `last_seen_at` (heartbeat).
2. **Sustained Degradation (`DEGRADED` &times; &ge; 3 probes):**
   - A single slow request does not trigger an alert.
   - If a service remains `DEGRADED` for **3 consecutive probes (approx. 45 seconds)**, it opens a `P2_HIGH` incident.
3. **Automatic Recovery (`UP`):**
   - When the service returns `UP`, the worker locates the active incident.
   - Calculates **MTTR** (Mean Time to Resolution in seconds):
     $$\text{MTTR} = \text{now} - \text{triggered\_at}$$
   - Marks the incident status as `RESOLVED` with root cause: `"Automatic recovery detected by HealthWorker"`.

---

## 8. Service Dependency Topology Engine

### 8.1 Overview
The **Topology Engine** ([`topology_engine.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/topology_engine.go)) generates an interactive architectural graph of the TradeDrift cluster.

* **Endpoint:** `GET /api/v1/admin/topology`
* **Handler:** `TopologyHandler.HandleGetTopology`
* **Integration:** Injected with `HealthWorker` to overlay live status onto structural graph nodes.

```
                                OPERATOR (Postman) / FRONTEND
                                              │
                                              │ 1. GET /api/v1/admin/topology
                                              │    (Bearer JWT with role="admin")
                                              ▼
                             ┌─────────────────────────────────┐
                             │        TOPOLOGY HANDLER         │
                             └────────────────┬────────────────┘
                                              │
                                              │ 2. topologyEng.GetTopology(ctx)
                                              ▼
                             ┌─────────────────────────────────┐
                             │         TOPOLOGY ENGINE         │
                             └────────────────┬────────────────┘
                                              │
                                              │ 3. healthWorker.GetLatestHealth()
                                              ▼
                             ┌─────────────────────────────────┐
                             │      HEALTH WORKER SNAPSHOT     │
                             │  (Live latencies & node status) │
                             └────────────────┬────────────────┘
                                              │
                                              ▼
                             ┌─────────────────────────────────┐
                             │ 4. Merge Live State with Model  │
                             │    - 9 Microservices (4 Tiers)  │
                             │    - 18 Directional Comm Edges  │
                             │    - Dynamic Degraded Overlays  │
                             └────────────────┬────────────────┘
                                              │
                                              │ 5. Return Complete Graph JSON (<5ms)
                                              ▼
                             ┌─────────────────────────────────┐
                             │           HTTP 200 OK           │
                             │ Interactive Architectural Graph │
                             └─────────────────────────────────┘
```

### 8.2 Graph Structure
The graph categorizes all 9 microservices into 4 functional architectural tiers:
1. `control_plane`: `admin`
2. `core_accounts`: `auth`, `wallet`
3. `trading_engine`: `trade`, `liquidity_engine`
4. `edge_notification`: `portfolio`, `notification`
5. `infrastructure`: `postgres`, `kafka`

Edges represent communication protocols (`gRPC`, `Kafka Pub/Sub`, `TCP (pgx)`, `HTTP REST`) and display the operational status between nodes.

---

## 9. Codebase Implementation Reference Table

| Functional Area | Source File | Core Functions / Types |
| :--- | :--- | :--- |
| **HTTP Health Handler** | [`internal/handler/health_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/health_handler.go) | `HandleLiveness`, `HandleReadiness`, `HandleSystemHealth`, `SetShuttingDown` |
| **Health Worker Engine** | [`internal/service/health_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/health_worker.go) | `RunProbe`, `probeHTTPService`, `probeAuthGRPC`, `probeWalletGRPC`, `GetLatestHealth` |
| **Incident Transitions** | [`internal/service/incident_transitions.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/incident_transitions.go) | `processIncidentTransitions`, `handleDownTransition`, `handleRecovery` |
| **Incident Management** | [`internal/service/incident_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/incident_service.go) | `GetIncident`, `GetCorrelatedIncident`, `ResolveIncident`, `GetIncidentStats` |
| **Topology Engine** | [`internal/service/topology_engine.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/topology_engine.go) | `GetTopology`, node and edge graph generation |
| **Prometheus Metrics** | [`internal/metrics/metrics.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/metrics/metrics.go) | `RecordHealthProbe`, `RecordDependencyHealthState`, `SetSystemOverallStatus` |
| **Incident DB Schema** | [`migrations/00005_create_admin_incidents.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00005_create_admin_incidents.sql) | Table definitions for incident tracking and resolution audit |
| **Server Bootstrap** | [`cmd/server/main.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/server/main.go#L126-L195) | Starts `HealthWorker`, wires topology engine, handles graceful unreadiness drainage |
