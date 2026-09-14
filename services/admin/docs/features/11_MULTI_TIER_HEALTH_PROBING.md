# Feature 11: Multi-Tier Health Probing Architecture

## 1. What This Feature Does
The **Multi-Tier Health Probing Architecture** replaces simplistic single-endpoint health checks with a decoupled 3-tier operational model:
* **Tier 1: Liveness (`GET /health`)**: Verifies the Go runtime and HTTP multiplexer are responsive. Performs zero network I/O.
* **Tier 2: Readiness (`GET /ready`)**: Verifies Admin's direct connectivity to mandatory local dependencies (PostgreSQL pool, Kafka cluster, Auth gRPC, Wallet gRPC) within a strict 2-second timeout budget.
* **Tier 3: Diagnostic System Health (`GET /api/v1/admin/system/health`)**: Delivers an authenticated, in-depth diagnostic snapshot across all 9 platform microservices, database pools, and backlog queues in `<1ms`.

---

## 2. Why We Need It
In microservice architectures, conflating liveness, readiness, and diagnostics causes severe operational outages:
1. **The Restart Storm Hazard (Cascading Liveness Failure)**:
   - If `/health` pings PostgreSQL, and the database has a temporary 5-second spike, Kubernetes marks the container dead and restarts it.
   - 50 Admin pods restart simultaneously, slamming PostgreSQL with 50 cold connection bursts, causing a total platform collapse.
2. **Preventing Traffic Blackholing**:
   - Ingress load balancers need to know if an instance can accept mutations *now*. If Kafka is disconnected, Admin must return `HTTP 503` so traffic routes to healthy replicas.
3. **Preventing Dashboard Denial-of-Service**:
   - If SRE dashboards ping `/system/health` every second, running 10 live network probes per request turns the monitoring system into a DDoS weapon against internal microservices.

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **Liveness Handler** | [`services/admin/internal/handler/health_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/health_handler.go#L73-L76) | `HandleLiveness` (Zero I/O, `HTTP 200 OK`) |
| **Readiness Handler**| [`services/admin/internal/handler/health_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/health_handler.go#L80-L160) | `HandleReadiness` (Parallel 2s direct dependency checks) |
| **Diagnostic Handler**| [`services/admin/internal/handler/health_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/health_handler.go#L171-L188) | `HandleSystemHealth` (Reads `healthWorker.GetLatestHealth()`) |
| **Route Registration**| [`services/admin/internal/handler/router.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/router.go#L59-L66) | Wires public probes vs. authenticated JWT diagnostic route |

---

## 4. How We Achieve This Feature

### The Three Tiers Compared

| Tier | Endpoint | Auth | Target Consumer | Purpose |
| :--- | :--- | :---: | :--- | :--- |
| **Tier 1: Liveness** | `GET /health` | None | Kubernetes / Docker | Verifies process alive & mux responsive. |
| **Tier 2: Readiness** | `GET /ready` | None | Ingress Load Balancer | Verifies direct dependency connectivity before receiving operational traffic. |
| **Tier 3: Diagnostic** | `GET /api/v1/admin/system/health` | Bearer JWT (`admin`) | SRE / Ops Dashboard | Complete cluster health snapshot (<1ms read lock). |

---

## 5. Execution Flow

```
                                      OPERATOR / KUBERNETES
                                                │
       ┌────────────────────────────────────────┼────────────────────────────────────────┐
       ▼                                        ▼                                        ▼
Tier 1: GET /health                     Tier 2: GET /ready                      Tier 3: GET /system/health
Process Alive?                          Ready for Ingress Traffic?              Complete Platform Snapshot
       │                                        │                                        │
       ▼                                        ▼                                        ▼
┌───────────────────────────┐           ┌───────────────────────────┐           ┌───────────────────────────┐
│ Zero Network I/O          │           │ Check isShuttingDown?     │           │ RequireAdmin (JWT Auth)   │
│ Return HTTP 200 {"ok"}    │           │ If true ──► 503 Drainage  │           │ healthWorker.GetLatest()  │
└───────────────────────────┘           └─────────────┬─────────────┘           │ Sub-millisecond read lock │
                                                      │                         │ Return 9-service map      │
                                                      ▼                         └───────────────────────────┘
                                        ┌───────────────────────────┐
                                        │ Parallel 2s Health Checks │
                                        │ ├── PostgreSQL Ping       │
                                        │ ├── Kafka TCP Dial        │
                                        │ ├── Auth gRPC Ping        │
                                        │ └── Wallet gRPC Ping      │
                                        └─────────────┬─────────────┘
                                                      │
                                       ┌──────────────┴──────────────┐
                                       ▼                             ▼
                                    All OK                      Any Failure
                                       │                             │
                                       ▼                             ▼
                         ┌───────────────────────────┐ ┌───────────────────────────┐
                         │ Return HTTP 200 OK        │ │ Return HTTP 503           │
                         │ Ingress admits traffic    │ │ Ingress sheds traffic     │
                         └───────────────────────────┘ └───────────────────────────┘
```
