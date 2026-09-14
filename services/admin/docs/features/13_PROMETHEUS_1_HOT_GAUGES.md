# Feature 13: Prometheus 1-Hot Health Gauges

## 1. What This Feature Does
The **1-Hot Health Metric Representation** eliminates metric race conditions and ambiguous states across monitoring dashboards:
* **Mutual Exclusivity**: For any given service or platform indicator, **exactly one** state label has value `1`, while all alternative states are explicitly set to `0`.
* **Exported Gauge**: `admin_dependency_health_state{service="...", state="UP|DEGRADED|DOWN"}`
* **Aggregate Indicator**: `admin_platform_overall_status{state="HEALTHY|DEGRADED|UNHEALTHY"}`

---

## 2. Why We Need It
Prometheus stores time series as discrete vector data. Naive boolean metrics introduce critical visualization and alerting bugs:
1. **The Stale Gauge Hazard**:
   - If you export `service_healthy = 1` during normal times, and then during an outage you set `service_unhealthy = 1` without explicitly clearing the old series, Prometheus vector math or Grafana panels can show a service as *both healthy and unhealthy at the same timestamp*.
2. **Deterministic Alerting Expressions**:
   - Alerting rules become trivial and mathematically clean:
     ```promql
     admin_dependency_health_state{state="DOWN"} == 1
     ```
   - No guesswork regarding missing labels or `NaN` values.
3. **Grafana State-Timeline Compatibility**:
   - Modern Grafana status widgets (State Timeline, Polystat) require mutually exclusive binary signals to render discrete color bars (Green = UP, Yellow = DEGRADED, Red = DOWN).

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **Metrics Registry** | [`services/admin/internal/metrics/metrics.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/metrics/metrics.go#L70-L100) | `RecordDependencyHealthState`, `SetSystemOverallStatus` |
| **Health Worker** | [`services/admin/internal/service/health_worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/health_worker.go#L330-L355) | Loops through all services and emits 1-hot metric vectors |

---

## 4. How We Achieve This Feature

In [`services/admin/internal/metrics/metrics.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/metrics/metrics.go):
```go
func (m *Metrics) RecordDependencyHealthState(service string, activeState string) {
    allStates := []string{"UP", "DEGRADED", "DOWN"}
    for _, state := range allStates {
        val := 0.0
        if state == activeState {
            val = 1.0 // Exactly ONE state receives 1.0
        }
        m.dependencyHealthState.WithLabelValues(service, state).Set(val)
    }
}
```

### Live Prometheus Scrape Output Example
```prometheus
# HELP admin_dependency_health_state 1-hot gauge indicating dependency health state (1=active, 0=inactive)
# TYPE admin_dependency_health_state gauge
admin_dependency_health_state{service="wallet",state="UP"} 1
admin_dependency_health_state{service="wallet",state="DEGRADED"} 0
admin_dependency_health_state{service="wallet",state="DOWN"} 0

admin_dependency_health_state{service="trade",state="UP"} 0
admin_dependency_health_state{service="trade",state="DEGRADED"} 1
admin_dependency_health_state{service="trade",state="DOWN"} 0
```

---

## 5. Execution Flow

```
                         PROBE RESULTS COLLECTED BY WORKER
                                         │
                                         ▼
                     ┌──────────────────────────────────────┐
                     │ Service: "trade", Current: DEGRADED  │
                     └───────────────────┬──────────────────┘
                                         │
                                         ▼
                     ┌──────────────────────────────────────┐
                     │ Iterate all possible states:         │
                     │ ["UP", "DEGRADED", "DOWN"]           │
                     └───────────────────┬──────────────────┘
                                         │
                                         ▼
                     ┌──────────────────────────────────────┐
                     │ Set 1-Hot Prometheus Gauge Values:   │
                     │ ├── trade{state="UP"}       = 0.0    │
                     │ ├── trade{state="DEGRADED"} = 1.0    │
                     │ └── trade{state="DOWN"}     = 0.0    │
                     └───────────────────┬──────────────────┘
                                         │
                                         ▼
                     ┌──────────────────────────────────────┐
                     │ PROMETHEUS SCRAPER (GET /metrics)    │
                     │ Unambiguous vector state in Grafana  │
                     └──────────────────────────────────────┘
```
