# Metrics & Telemetry Subsystem (`internal/metrics`)

This document provides a comprehensive architectural and operational manual for the Prometheus telemetry instrumentation engine located in [`services/admin/internal/metrics/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/metrics/).

---

## Table of Contents

1. [Package Overview & Purpose](#1-package-overview--purpose)
2. [What Problems This Package Solves](#2-what-problems-this-package-solves)
3. [Metric Architecture & Design Invariants](#3-metric-architecture--design-invariants)
   - [Strict Label Cardinality Bounds](#strict-label-cardinality-bounds)
   - [The 1-Hot Boolean Gauge Invariant](#the-1-hot-boolean-gauge-invariant)
   - [Sub-Millisecond Latency Precision](#sub-millisecond-latency-precision)
   - [Paired In-Flight Operations Tracking](#paired-in-flight-operations-tracking)
4. [Complete Metric Registry Catalog](#4-complete-metric-registry-catalog)
   - [1. HTTP Ingress Metrics](#1-http-ingress-metrics)
   - [2. Administrative Operations Metrics](#2-administrative-operations-metrics)
   - [3. Transactional Outbox Worker Metrics](#3-transactional-outbox-worker-metrics)
   - [4. Distributed Saga Worker Metrics](#4-distributed-saga-worker-metrics)
   - [5. Platform Health & Diagnostics Metrics](#5-platform-health--diagnostics-metrics)
5. [Instrumentation Helper Functions](#5-instrumentation-helper-functions)
6. [Architectural & Execution Flows](#6-architectural--execution-flows)
   - [Flow 1: 1-Hot Status Invariant State Transition Flow](#flow-1-1-hot-status-invariant-state-transition-flow)
   - [Flow 2: In-Flight Operation Tracking Lifecycle](#flow-2-in-flight-operation-tracking-lifecycle)
   - [Flow 3: Autonomous Queue Backlog Telemetry Sweep](#flow-3-autonomous-queue-backlog-telemetry-sweep)
7. [Prometheus Scraping & Grafana Integration](#7-prometheus-scraping--grafana-integration)

---

## 1. Package Overview & Purpose

The `internal/metrics` package is the **centralized Prometheus telemetry registry** for the TradeDrift Admin Service.

In high-concurrency trading platforms, operational observability must provide instant, unambiguous alerting before minor glitches escalate into outages. `metrics.go` registers all Prometheus collectors under the canonical namespace:
```
tradedrift_admin_<metric_name>
```

It acts as a cross-cutting subsystem consumed by:
- **HTTP Transport Layer**: Measures request rates, status codes, and latency histograms.
- **Admin Service Layer**: Tracks active in-flight mutations, success/failure counts, and execution durations.
- **Outbox Publisher Worker**: Measures Kafka publish latency, retry counts, queue backlog depth, and oldest unpublished event age.
- **Saga Worker**: Measures downstream task durations, queue depths partitioned by state (`PENDING`, `RETRYING`, `EXHAUSTED`), and terminal retry failures.
- **Health Worker**: Telemetry on downstream microservice reachability, individual probe latencies, and overall platform health.

---

## 2. What Problems This Package Solves

| Problem | Failure Scenario Without Centralized Metrics | How `internal/metrics` Solves It |
| :--- | :--- | :--- |
| **Prometheus Cardinality Explosion** | Adding high-cardinality labels (such as `user_id`, raw URLs, or arbitrary error messages) creates millions of metric series, exhausting Prometheus memory and crashing scraper nodes. | Enforces **strictly bounded label sets** using predefined constant slices (`allowedStatuses`, `ServiceAuth`, `OpSuspendUser`, `TopicMarketHalted`). |
| **Gauge State Ghosting / Stale Values** | Storing a service status in a single gauge that flips values leaves old states lingering in Prometheus or causes confusing alert evaluation. | Implements the **1-Hot Boolean Invariant**: explicitly sets the active state to `1.0` and resets all other 4 states to `0.0`. |
| **Sub-Millisecond Truncation** | Computing latency using integer division (e.g. `float64(time.Since(start).Milliseconds()) / 1000.0`) truncates high-speed probes (< 1ms becomes `0.000s`). | Uses high-precision `time.Duration.Seconds()`, capturing microsecond and nanosecond precision. |
| **Silent Backlog Buildup** | Kafka publisher slows down or halts due to network degradation. Without queue depth metrics, engineers don't know the outbox table has accumulated 50,000 unhandled events until the disk fills up. | Exposes `tradedrift_admin_outbox_backlog_depth` and `tradedrift_admin_outbox_oldest_unpublished_seconds` for immediate alerting. |
| **Unbounded In-Flight Operations** | Spikes in slow database operations exhaust server memory without warning. | Gauges `tradedrift_admin_operations_in_flight` track active concurrent mutations in real time. |

---

## 3. Metric Architecture & Design Invariants

### Strict Label Cardinality Bounds
All label dimensions in `metrics.go` are guaranteed to have low, finite cardinality:
- **`method`**: `GET`, `POST` (2 values)
- **`route`**: Normalized static route templates (10 values)
- **`status`**: HTTP status codes (`200`, `400`, `401`, `403`, `409`, `500`, `503`)
- **`service`**: `auth`, `wallet`, `trade`, `portfolio`, `liquidity_engine`, `notification`, `postgres`, `kafka` (8 values)
- **`status` (health)**: `UP`, `DEGRADED`, `DOWN`, `TIMEOUT`, `UNKNOWN` (5 values)
- **`operation_type`**: `SUSPEND_USER`, `UNSUSPEND_USER`, `FREEZE_WALLET`, `UNFREEZE_WALLET`, `HALT_MARKET`, `RESUME_MARKET` (6 values)
- **`task_type`**: `AUTH_INVALIDATE_SESSIONS` (1 value)

---

### The 1-Hot Boolean Gauge Invariant
When representing categorical states across dependencies, setting a single gauge to an integer enum (e.g. `1=UP`, `2=DOWN`) makes alerting queries difficult and confuses PromQL rate calculations.

Instead, `SystemHealthStatus` uses a **1-Hot Boolean Gauge Matrix**:
$$\forall s \in \text{Services}, \quad \sum_{st \in \text{Statuses}} \text{SystemHealthStatus}(s, st) = 1.0$$

Whenever a service status is reported, `RecordHealthProbe` loops over all allowed statuses (`UP`, `DEGRADED`, `DOWN`, `TIMEOUT`, `UNKNOWN`), setting the active state to `1.0` and the remaining 4 states to `0.0`.

---

### Sub-Millisecond Latency Precision
Latency measurements across all histograms and gauges preserve sub-millisecond precision:
```go
HealthProbeLatencySeconds.WithLabelValues(service).Set(latency.Seconds())
HealthProbeDurationSeconds.WithLabelValues(service).Observe(latency.Seconds())
```
A 450-microsecond probe records as `0.000450` seconds rather than truncating to zero.

---

### Paired In-Flight Operations Tracking
Every administrative operation is bracketed:
1. `RecordOperationStart(opType)` increments `OperationsInFlight`.
2. `RecordOperationComplete(opType, success, duration)` decrements `OperationsInFlight` and observes `OperationDurationSeconds` in a `defer` block.
This ensures `OperationsInFlight` returns to zero even if an unhandled error or panic occurs.

---

## 4. Complete Metric Registry Catalog

### 1. HTTP Ingress Metrics
| Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- |
| `tradedrift_admin_http_requests_total` | CounterVec | `method`, `route`, `status` | Total HTTP requests processed by the Admin Service. |
| `tradedrift_admin_http_request_duration_seconds` | HistogramVec | `method`, `route` | Latency distribution of HTTP requests across 11 buckets (5ms to 10s). |

---

### 2. Administrative Operations Metrics
| Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- |
| `tradedrift_admin_operations_total` | CounterVec | `operation_type`, `result` | Total admin mutation commands completed or failed. |
| `tradedrift_admin_operations_in_flight` | GaugeVec | `operation_type` | Current number of mutation operations actively executing. |
| `tradedrift_admin_operation_duration_seconds` | HistogramVec | `operation_type` | Latency of administrative mutation transactions across 11 buckets. |

---

### 3. Transactional Outbox Worker Metrics
| Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- |
| `tradedrift_admin_outbox_events_total` | CounterVec | `topic`, `result` | Total events published to Kafka (`PUBLISHED`) or terminally failed (`FAILED`). |
| `tradedrift_admin_outbox_publish_duration_seconds` | HistogramVec | `topic` | Kafka synchronous broker ACK latency per event across 10 buckets (1ms to 2.5s). |
| `tradedrift_admin_outbox_retries_total` | CounterVec | `topic` | Count of transient publishing retries scheduled due to broker errors. |
| `tradedrift_admin_outbox_backlog_depth` | Gauge | None | Number of pending or processing events waiting in `admin_outbox`. |
| `tradedrift_admin_outbox_oldest_unpublished_seconds` | Gauge | None | Age in seconds of the oldest unpublished outbox event. |
| `tradedrift_admin_outbox_worker_errors_total` | Counter | None | Total transient errors encountered in the outbox polling loop. |

---

### 4. Distributed Saga Worker Metrics
| Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- |
| `tradedrift_admin_saga_tasks_total` | CounterVec | `task_type`, `result` | Total saga tasks resolved (`COMPLETED` or `EXHAUSTED`). |
| `tradedrift_admin_saga_task_duration_seconds` | HistogramVec | `task_type` | Duration of downstream saga execution across 10 buckets (5ms to 5s). |
| `tradedrift_admin_saga_pending_queue_depth` | Gauge | None | Number of active tasks currently pending or retrying. |
| `tradedrift_admin_saga_queue_count` | GaugeVec | `status` | Count of tasks partitioned by status (`PENDING`, `RETRYING`, `EXHAUSTED`). |
| `tradedrift_admin_saga_worker_errors_total` | Counter | None | Total worker loop errors in the saga background processor. |

---

### 5. Platform Health & Diagnostics Metrics
| Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- |
| `tradedrift_admin_system_overall_status` | Gauge | None | Numerical health score: `2 = HEALTHY`, `1 = DEGRADED`, `0 = UNHEALTHY`. |
| `tradedrift_admin_system_health_status` | GaugeVec | `service`, `status` | 1-Hot boolean gauge (`1.0` active, `0.0` inactive) across 8 services. |
| `tradedrift_admin_health_probe_latency_seconds` | GaugeVec | `service` | High-precision latency of the most recent probe. |
| `tradedrift_admin_health_probe_duration_seconds` | HistogramVec | `service` | Latency distribution of probes across 10 buckets (1ms to 2s). |
| `tradedrift_admin_health_probe_last_run_timestamp_seconds` | GaugeVec | `service` | Unix epoch timestamp of the last executed probe. |
| `tradedrift_admin_health_probe_failures_total` | CounterVec | `service`, `reason` | Probe failure counts by service and reason (`timeout`, `http_5xx`, etc.). |

---

## 5. Instrumentation Helper Functions

| Function | Signature | Purpose & Behavior |
| :--- | :--- | :--- |
| `RecordHTTPRequest` | `(method, route string, statusCode int, duration time.Duration)` | Records HTTP count and observes latency. |
| `RecordOperationStart` | `(opType string)` | Increments `OperationsInFlight` for the given operation type. |
| `RecordOperationComplete` | `(opType string, success bool, duration time.Duration)` | Decrements in-flight, increments total, and observes duration. |
| `RecordOutboxPublish` | `(topic string, duration time.Duration)` | Increments outbox published counter and records ACK latency. |
| `RecordOutboxRetry` | `(topic string)` | Increments outbox retry counter. |
| `RecordOutboxFailed` | `(topic string)` | Increments outbox terminal failure counter. |
| `RecordSagaComplete` | `(taskType string, duration time.Duration)` | Increments `COMPLETED` counter and observes duration. |
| `RecordSagaExhausted` | `(taskType string, duration time.Duration)` | Increments `EXHAUSTED` dead-letter counter. |
| `RecordSagaQueueStats` | `(pending, retrying, exhausted int)` | Updates aggregate and partitioned queue depth gauges. |
| `RecordHealthProbe` | `(service string, currentStatus string, latency time.Duration, runTime time.Time)` | Updates 1-hot gauges, latency gauge, histogram, and timestamp. |
| `RecordHealthFailure` | `(service, reason string)` | Increments health failure counter with bounded reason tag. |
| `SetSystemOverallStatus` | `(statusStr string)` | Sets `system_overall_status` gauge (`2`, `1`, or `0`). |

---

## 6. Architectural & Execution Flows

### Flow 1: 1-Hot Status Invariant State Transition Flow

```
                         HealthWorker Probe
                  (service="postgres", status="UP")
                                  │
                                  ▼
                        RecordHealthProbe()
                                  │
               Loop Over Status Enum Set:
               [UP, DEGRADED, DOWN, TIMEOUT, UNKNOWN]
                                  │
        ┌─────────────────────────┴─────────────────────────┐
 (st == "UP")                                          (st != "UP")
        ▼                                                   ▼
┌───────────────────────────────┐   ┌───────────────────────────────┐
│ SystemHealthStatus            │   │ SystemHealthStatus            │
│  {service="postgres",         │   │  {service="postgres",         │
│   status="UP"}.Set(1.0)       │   │   status=st}.Set(0.0)         │
└───────────────────────────────┘   └───────────────────────────────┘
                                  │
                                  ▼
     Invariant Guaranteed: Exactly One Status Label = 1.0; All Others = 0.0
```

---

### Flow 2: In-Flight Operation Tracking Lifecycle

```
                          Admin Handler
                                │
                        AdminService.HaltMarket()
                                │
                                ▼
                    RecordOperationStart("HALT_MARKET")
                                │
                                ▼
            OperationsInFlight{op="HALT_MARKET"}.Inc()
                                │
                                ▼
               Execute Database Tx & Outbox Logic
                                │
        ┌───────────────────────┴───────────────────────┐
 (Success)                                           (Failure)
        ▼                                                   ▼
 RecordOperationComplete(true)        RecordOperationComplete(false)
        │                                                   │
        └───────────────────────┬───────────────────────────┘
                                │
                                ▼
            OperationsInFlight{op="HALT_MARKET"}.Dec()
            OperationDurationSeconds{op="HALT_MARKET"}.Observe()
```

---

### Flow 3: Autonomous Queue Backlog Telemetry Sweep

```
                          HealthWorker
                        (Every 15s Sweep)
                                │
        ┌───────────────────────┴───────────────────────┐
        ▼                                               ▼
  Sweep Outbox Table                              Sweep Saga Queue
        │                                               │
 OutboxRepo.GetOutboxBacklogStats()            SagaRepo.GetSagaQueueStats()
        │ (count=4, oldest=1.2s)                        │ (pending=1, retrying=2)
        ▼                                               ▼
 OutboxBacklogDepth.Set(4)                     RecordSagaQueueStats(1, 2, 0)
 OutboxOldestUnpublished.Set(1.2)                       │
                                                        ▼
                                               SagaQueueCount{status}.Set(...)
```

---

## 7. Prometheus Scraping & Grafana Integration

The metrics registered in `internal/metrics` are exposed at `GET /metrics` via `promhttp.Handler()`.

### Key PromQL Alert Queries

#### 1. Outbox Publishing Lag Alert
```promql
tradedrift_admin_outbox_oldest_unpublished_seconds > 60
```
*Alerts when any administrative event has been stuck in the outbox for more than 60 seconds.*

#### 2. Downstream Dependency Outage Alert
```promql
tradedrift_admin_system_health_status{status="DOWN"} == 1
```
*Triggers immediately when any platform microservice or database transitions to DOWN.*

#### 3. High HTTP Error Rate Alert
```promql
sum(rate(tradedrift_admin_http_requests_total{status=~"5.."}[5m]))
/
sum(rate(tradedrift_admin_http_requests_total[5m])) > 0.05
```
*Triggers when 5xx errors exceed 5% of total administrative traffic over 5 minutes.*

#### 4. Saga Tasks Exhausted Alert
```promql
increase(tradedrift_admin_saga_tasks_total{result="EXHAUSTED"}[5m]) > 0
```
*Triggers when a distributed saga consumes all 10 retry attempts without success, indicating potential permanent divergence requiring operator intervention.*
