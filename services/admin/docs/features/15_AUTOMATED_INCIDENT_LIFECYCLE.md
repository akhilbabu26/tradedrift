# Feature 15: Automated Incident Lifecycle & Recovery Engine

## 1. What This Feature Does
The **Automated Incident Lifecycle Engine** turns transient infrastructure failures into persistent, trackable operational incidents in PostgreSQL (`admin_incidents`):
* **Autonomous Outage Creation**: If a service transitions to `DOWN` or `TIMEOUT`, the worker instantly opens a `P1_CRITICAL` incident without requiring manual human alerting.
* **Flap Prevention for Degradations**: Sustained slowness (`DEGRADED`) requires **3 consecutive failed probes (~45 seconds)** before triggering a `P2_HIGH` incident.
* **Auto-Healing & MTTR**: When the service recovers (`UP`), the worker automatically closes the active incident, marks it `RESOLVED`, and calculates the exact **Mean Time to Resolution (MTTR)** in seconds.

---

## 2. Why We Need It
Manual incident reporting in fast-moving trading environments leads to delayed responses and forgotten audits:
1. **Accurate Downtime Accounting**: SLA compliance requires precise second-by-second timestamps of when an outage began and when it recovered.
2. **Preventing Alert Fatigue**: Transient network hiccups (a single 1500ms timeout) should not wake on-call SREs. The 3-probe degradation dampener prevents alert storms.
3. **Heartbeat Tracking**: If a service remains down for hours, the incident record continuously updates `last_seen_at` and `probe_failure_count`, tracking the persistence of the outage.

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **Transition Engine** | [`services/admin/internal/service/incident_transitions.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/incident_transitions.go#L24-L120) | `processIncidentTransitions`, `handleDownTransition`, `handleRecovery` |
| **Incident Service** | [`services/admin/internal/service/incident_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/incident_service.go) | `GetIncident`, `GetIncidentStats`, `ResolveIncident` |
| **Incident Repository**| [`services/admin/internal/repository/postgres/incident_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/incident_repo.go) | `CreateIncident`, `ResolveIncident`, `GetActiveIncidentByService` |
| **Database Schema** | [`services/admin/migrations/00005_create_admin_incidents.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00005_create_admin_incidents.sql) | Table schema with severity (`P1_CRITICAL`, `P2_HIGH`), MTTR, and failure counts |

---

## 4. How We Achieve This Feature

1. **Outage Detection (`DOWN` / `TIMEOUT`)**:
   - Checks if an active incident for this service already exists in PostgreSQL.
   - If **NO**: Inserts new incident (`severity = 'P1_CRITICAL'`, `status = 'OPEN'`, `triggered_at = NOW()`).
   - If **YES**: Updates `probe_failure_count++` and `last_seen_at = NOW()`.
2. **Sustained Degradation (`DEGRADED`)**:
   - Increments an in-memory `degradedCount[service]`.
   - Only when `degradedCount >= 3` does it open a `P2_HIGH` incident in the database.
3. **Automatic Recovery (`UP`)**:
   - Fetches active incident for the service.
   - If found:
     $$\text{MTTR (seconds)} = \text{now} - \text{triggered\_at}$$
     ```sql
     UPDATE admin_incidents 
     SET status = 'RESOLVED', 
         resolved_at = NOW(), 
         mttr_seconds = $1,
         root_cause = 'Automatic recovery detected by HealthWorker'
     WHERE id = $2;
     ```

---

## 5. Execution Flow

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
