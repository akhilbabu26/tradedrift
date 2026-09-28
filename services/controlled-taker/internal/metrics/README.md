# Metrics Package (`internal/metrics`)

The `metrics` package provides Prometheus monitoring instrumentation and an HTTP scrape endpoint for the Controlled Taker Service.

---

## 1. Metrics Server Configuration

* **Default Port:** `METRICS_PORT` (Default: `9090`)
* **Endpoint Path:** `/metrics`
* **Handler:** Official Prometheus `promhttp.Handler()`

The metrics server runs on an isolated port separate from the health endpoint (`8080`) to ensure observability traffic does not interfere with platform health checks.

---

## 2. Complete Metric Catalog

### Order Execution & Lifecycle Counters
| Metric Name | Type | Labels | Description & Operational Meaning |
| :--- | :--- | :--- | :--- |
| `cts_orders_submitted_total` | Counter | `market_id`, `side`, `profile` | Increments each time a crossing order is successfully dispatched to Order Service. Tracks execution distribution across profiles (`LOW`, `MID`, `HIGH`) and sides (`BUY`, `SELL`). |
| `cts_orders_fully_filled_total` | Counter | `market_id` | Increments when an order executes 100% of its requested quantity against resting quotes on the Matching Engine. |
| `cts_orders_partially_filled_total` | Counter | `market_id` | Increments when an order fills a portion of its quantity and the remaining residual is successfully cancelled. |
| `cts_orders_cancelled_unfilled_total` | Counter | `market_id` | Increments when an aggressive crossing order produces zero fills and is cancelled cleanly. |
| `cts_residuals_cancelled_total` | Counter | `market_id` | Increments whenever a `CancelOrder` request is dispatched to eliminate resting maker quantities. |

---

### Critical Safety & Alert Metrics
| Metric Name | Type | Labels | Description & Operational Meaning |
| :--- | :--- | :--- | :--- |
| `cts_unresolved_residuals_total` | Counter | `market_id` | **🔴 CRITICAL P0 ALERT:** Increments if the 2.5s post-submission cleanup deadline expires without confirming that an unfilled order residual was cancelled, or if an ambiguous submission recovery fails completely. Indicates that CT-001 may have an uncontrolled resting maker order on the Matching Engine. |
| `cts_orders_failed_total` | Counter | `market_id`, `reason` | Increments when an order cycle fails or is aborted. Reasons include: `depth_read_error`, `parameter_calc_error`, `safety_guard_rejected`, `order_service_rejected`, `submit_uncommitted_timeout`, `submit_unresolved_timeout`, `shutdown_unresolved_timeout`. |
| `cts_circuit_breaker_tripped_total` | Counter | `market_id` | Increments each time consecutive order failures trip the market's circuit breaker to the `OPEN` state. |
| `cts_depth_read_errors_total` | Counter | `market_id`, `reason` | Tracks Redis depth retrieval anomalies: `not_found`, `stale`, `future_timestamp`, `market_mismatch`, `incomplete_depth`, `crossed_book`, `invalid_level`, `unmarshal_error`. |

---

### Financial & Volumetric Tracking
| Metric Name | Type | Labels | Description & Operational Meaning |
| :--- | :--- | :--- | :--- |
| `cts_conservative_notional_usdt_total` | Counter | `market_id` | Cumulative conservative upper-bound executed volume in USDT. Calculated as: $\text{filledQty} \times \text{PriceCap}$ for BUY orders, and $\text{filledQty} \times P_{L1.\text{bid}}$ for SELL orders. Used for platform volume monitoring and risk auditing. |

---

### Latency Histograms
| Metric Name | Type | Labels | Buckets | Description |
| :--- | :--- | :--- | :--- | :--- |
| `cts_order_latency_seconds` | Histogram | `market_id`, `profile` | `[0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0]` | Total duration of the end-to-end execution cycle, from order creation through residual verification and terminal cancellation. |

---

## 3. Recommended Prometheus Alert Rules

```yaml
groups:
  - name: cts_alerts
    rules:
      - alert: CTSUnresolvedResidualDetected
        expr: increase(cts_unresolved_residuals_total[1m]) > 0
        for: 0m
        labels:
          severity: critical
        annotations:
          summary: "CTS detected an unresolved order residual on {{ $labels.market_id }}"
          description: "An order residual could not be verified cancelled before the cleanup deadline. Potential resting maker order!"

      - alert: CTSCircuitBreakerOpen
        expr: increase(cts_circuit_breaker_tripped_total[5m]) > 0
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "CTS Circuit Breaker tripped on {{ $labels.market_id }}"
          description: "Multiple consecutive failures tripped the breaker. Taker activity is paused."
```
