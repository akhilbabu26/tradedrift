# Feature 17: Operational Analytics & Risk Signals Engine

## 1. What This Feature Does
The **Operational Analytics & Risk Signals Engine** provides macro-level observability and automated insider-threat / operational anomaly detection:
* **24-Hour Overview (`GET /api/v1/admin/analytics/overview`)**: Aggregates operational throughput, success vs. failure rates, volume by operation type, and top operating administrator IDs.
* **Rolling Risk Signals (`GET /api/v1/admin/analytics/risk-signals`)**: Evaluates operational patterns across rolling 1-hour windows to detect 4 distinct threat anomalies.

---

## 2. Why We Need It
In high-security financial infrastructure, rogue operators or compromised credentials pose severe threats:
1. **Compromised Admin Credential Detection**: If an administrator's credentials are leaked, an attacker might execute commands from multiple remote locations simultaneously.
2. **Detecting Rogue / Panic Halts**: Repeatedly halting and resuming markets (flapping) causes chaos in the order book. Operators need automated alerts when an asset is halted frequently.
3. **Mass Suspension / Ransomware Behavior**: A rogue admin or automated script executing rapid mass suspensions can freeze valid customers out of the exchange.

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **Analytics Service** | [`services/admin/internal/service/analytics_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/analytics_service.go) | `GetOperationsOverview`, `GetRiskSignals` |
| **Analytics Repo** | [`services/admin/internal/repository/postgres/analytics_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/analytics_repo.go) | Aggregates SQL queries across `admin_operations` & `admin_audit_log` |
| **HTTP Handler** | [`services/admin/internal/handler/analytics_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/analytics_handler.go) | `HandleGetOperationsOverview`, `HandleGetRiskSignals` |
| **Router** | [`services/admin/internal/handler/router.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/router.go#L98-L101) | Authenticated routing under `RequireAdmin` |

---

## 4. How We Achieve This Feature

### The 4 Automated Risk Signals

| Signal Type | Evaluation Condition (Rolling 1-Hour Window) | Severity | Threat Mitigated |
| :--- | :--- | :---: | :--- |
| **`RAPID_USER_SUSPENSIONS`** | User suspensions $\ge 10$ within 60 minutes | `HIGH` | Rogue script or accidental mass account lockout. |
| **`FREQUENT_MARKET_HALTS`** | Market halts $\ge 3$ within 60 minutes | `MEDIUM` | Oracle flapping or runaway volatility. |
| **`ADMIN_IP_DIVERSITY`** | Single `admin_id` acting from $\ge 3$ distinct remote IP addresses | `CRITICAL` | Stolen admin JWT token or credential compromise. |
| **`EXTENDED_OUTAGE`** | Any `admin_incident` remaining unresolved for $> 30$ minutes | `HIGH` | Stalled incident resolution / unmitigated outage. |

---

## 5. Execution Flow

```
                               OPERATOR / SRE DASHBOARD
                                           │
                                           │ 1. GET /api/v1/admin/analytics/risk-signals
                                           ▼
                       ┌───────────────────────────────────────┐
                       │           ANALYTICS HANDLER           │
                       └───────────────────┬───────────────────┘
                                           │
                                           │ 2. GetRiskSignals(ctx)
                                           ▼
                       ┌───────────────────────────────────────┐
                       │           ANALYTICS SERVICE           │
                       └───────────────────┬───────────────────┘
                                           │
                                           │ 3. Evaluate 1-Hour Rolling Windows
                                           ▼
                       ┌───────────────────────────────────────┐
                       │         ANALYTICS REPOSITORY          │
                       │ ├── Count Suspensions in Last 1h      │
                       │ ├── Count Market Halts in Last 1h     │
                       │ ├── Count Distinct IPs per Admin      │
                       │ └── Check Incidents with Duration>30m │
                       └───────────────────┬───────────────────┘
                                           │
                                           │ 4. Threshold Evaluation:
                                           │    - Distinct IPs >= 3? -> CRITICAL
                                           │    - Suspensions >= 10? -> HIGH
                                           │    - Halts >= 3?        -> MEDIUM
                                           ▼
                       ┌───────────────────────────────────────┐
                       │ HTTP 200 OK: Structured Signal Report │
                       │ Instant Threat Surface Assessment     │
                       └───────────────────────────────────────┘
```
