# Feature 12: Autonomous HealthWorker Engine

## 1. What This Feature Does
The **Autonomous HealthWorker Engine** is an independent background loop running within the Admin Service:
* **Decoupled 15-Second Ticker**: Dispatches concurrent health probes across all 9 platform microservices, database connection pools, and messaging brokers every 15 seconds.
* **Non-Blocking In-Memory Snapshot**: Computes aggregate system status and caches a thread-safe snapshot in memory (`latestHealth`) so user requests never trigger live network probes.
* **Autonomous Incident Driver**: Feeds status changes directly into the automated incident lifecycle engine, detecting outages and auto-healing services without human intervention.

---

## 2. Why We Need It
Traditional health checking architectures execute network calls on-demand when a user visits a status page:
1. **Preventing Dependency Amplification Cascades**: If an incident occurs, 100 internal engineers hit refresh on their status dashboards. If each refresh runs 10 live network probes, the monitoring dashboard delivers the final blow that crashes already-struggling services.
2. **Deterministic Prometheus Scraping**: Prometheus scrapers (`GET /metrics`) expect instant responses. HealthWorker ensures metrics are pre-calculated, eliminating scrape timeouts.
3. **Continuous Continuous Auditing**: Even if no operator is logged into the Admin portal, HealthWorker runs 24/7, continuously checking queue lag and service responsiveness.

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **Worker Engine** | [`services/admin/internal/service/health_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/health_worker.go#L63-L360) | `RunProbe`, `probeHTTPService`, `probeAuthGRPC`, `probeWalletGRPC`, `GetLatestHealth` |
| **Incident Pipeline** | [`services/admin/internal/service/incident_transitions.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/incident_transitions.go) | `processIncidentTransitions` evaluates probe deltas |
| **Server Startup** | [`services/admin/cmd/server/main.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/server/main.go#L126-L135) | Initializes worker and launches background goroutine |
| **Telemetry Export** | [`services/admin/internal/metrics/metrics.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/metrics/metrics.go#L70-L100) | `RecordHealthProbe`, `RecordDependencyHealthState` |

---

## 4. How We Achieve This Feature

1. **Strict Single-Flight Serialization**:
   Uses a mutex (`probeMu.TryLock()`) to guarantee that slow probe cycles never overlap with subsequent ticks.
2. **Concurrent Target Probing**:
   - **HTTP Targets (1500ms timeout)**: Trade, Portfolio, Liquidity Engine, Notification.
   - **gRPC Transports**: Active application pings to Auth and Wallet.
   - **Kafka Socket Dial**: Scans configured brokers (`KAFKA_BROKERS`) via TCP dial.
   - **PostgreSQL Pool**: Pings local database connection pool.
   - **Queue Depths**: Evaluates pending outbox events and active saga retry counts.
3. **Platform Status Computation**:
   - If PostgreSQL or core trading services (Auth, Wallet, Trade, Portfolio, Liq) are `DOWN` &rarr; Overall is **`UNHEALTHY`**.
   - If secondary services (Notification) or Kafka are degraded &rarr; Overall is **`DEGRADED`**.
   - If all dependencies respond normally &rarr; Overall is **`HEALTHY`**.
4. **Defensive Snapshot Commit**:
   Acquires write lock on `snapshotMu` and saves a deep copy into memory.

---

## 5. Execution Flow

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
