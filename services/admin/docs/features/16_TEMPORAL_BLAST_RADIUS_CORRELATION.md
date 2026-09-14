# Feature 16: Temporal Blast-Radius Incident Correlation

## 1. What This Feature Does
The **Temporal Blast-Radius Incident Correlation Engine** automates root cause analysis by linking infrastructure outages to human administrative actions:
* **Endpoint**: `GET /api/v1/admin/incidents/{incident_id}/correlated`
* **Rolling Temporal Window**: Scans `admin_audit_log` within a **±15-minute time window** around the incident's `triggered_at` timestamp.
* **Causal Linking**: Detects whether an administrative mutation (e.g. mass market halt, wallet freeze, or user lockout) directly preceded or accompanied the outage.

---

## 2. Why We Need It
During major production outages, finding root causes manually wastes precious minutes:
1. **Answering "What Changed?"**: In over 80% of production incidents, outages are triggered by a configuration change, deployment, or administrative command.
2. **Preventing False External Accusations**: If an admin halts 10 markets and 2 minutes later the Trade Service reports an outage, correlation immediately proves the issue was an internal operational trigger rather than a cloud provider network failure.
3. **Post-Mortem Acceleration**: Provides incident response teams with an instant, unified timeline combining infrastructure telemetry and administrative audit trails.

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **Incident Service** | [`services/admin/internal/service/incident_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/incident_service.go#L76-L105) | `GetCorrelatedIncident` orchestrates audit window query |
| **Audit Repository** | [`services/admin/internal/repository/postgres/audit_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/audit_repo.go#L80-L110) | `GetAuditLogsInWindow` executes temporal range query |
| **HTTP Handler** | [`services/admin/internal/handler/incident_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/incident_handler.go) | `HandleGetCorrelatedIncident` handles HTTP request |
| **Router** | [`services/admin/internal/handler/router.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/router.go#L93) | Wires authenticated route with `RequireAdmin` |

---

## 4. How We Achieve This Feature

1. **Step 1: Retrieve Incident Trigger Time**:
   Fetches the target incident from `admin_incidents` by ID, extracting `triggered_at`.
2. **Step 2: Calculate 30-Minute Temporal Window**:
   ```go
   windowStart := incident.TriggeredAt.Add(-15 * time.Minute)
   windowEnd   := incident.TriggeredAt.Add(15 * time.Minute)
   ```
3. **Step 3: Query Correlated Audit Logs**:
   ```sql
   SELECT id, admin_id, action_type, target_id, reason, created_at, metadata
   FROM admin_audit_log
   WHERE created_at >= $1 AND created_at <= $2
   ORDER BY created_at ASC;
   ```
4. **Step 4: Composite Payload Delivery**:
   Returns the incident metadata alongside the chronological slice of operational actions taken before, during, and after the failure.

---

## 5. Execution Flow

```
                               OPERATOR / SRE POSTMAN
                                         │
                                         │ 1. GET /incidents/{id}/correlated
                                         ▼
                     ┌───────────────────────────────────────┐
                     │           INCIDENT HANDLER            │
                     └───────────────────┬───────────────────┘
                                         │
                                         │ 2. GetCorrelatedIncident(id)
                                         ▼
                     ┌───────────────────────────────────────┐
                     │           INCIDENT SERVICE            │
                     └───────────────────┬───────────────────┘
                                         │
                                         │ 3. Fetch Incident Metadata
                                         ▼
                     ┌───────────────────────────────────────┐
                     │          admin_incidents TABLE        │
                     │  - Service: "trade"                   │
                     │  - TriggeredAt: 2026-09-14 12:00:00   │
                     └───────────────────┬───────────────────┘
                                         │
                                         │ 4. Window: 11:45:00 to 12:15:00
                                         ▼
                     ┌───────────────────────────────────────┐
                     │          admin_audit_log TABLE        │
                     │  SELECT * WHERE created_at IN WINDOW  │
                     └───────────────────┬───────────────────┘
                                         │
                                         │ 5. Returns correlated operations:
                                         │    - 11:58:20: HALT_MARKET (BTC-USDT)
                                         │    - 11:59:10: SUSPEND_USER (bad_actor)
                                         ▼
                     ┌───────────────────────────────────────┐
                     │ HTTP 200 OK: Correlated Report        │
                     │ Instant Root Cause Identification     │
                     └───────────────────────────────────────┘
```
