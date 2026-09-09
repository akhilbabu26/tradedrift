# Notification Service — Observability & Metrics Guide

This document provides a technical explanation of the `internal/metrics` package in the **TradeDrift Notification Service** located in `services/notification/internal/metrics/`.

It details the package's purpose, the operational and observability problems it solves, the Prometheus metrics registry, how metrics are instrumented across components, and the end-to-end telemetry scraping flow.

---

## Purpose of `internal/metrics`

The `internal/metrics` package defines the **Prometheus instrumentation registry** for the Notification Service. It instruments the three critical operational stages of the notification pipeline:
1. **Ingress**: Asynchronous Kafka domain event consumption and gRPC direct creation.
2. **Storage**: User notifications created by category.
3. **Egress**: Transactional Outbox publishing to Redis Pub/Sub channels and network error rates.

```
services/notification/internal/metrics/
└── metrics.go       # Prometheus CounterVec definitions using promauto
```

### Core Responsibilities
- **Standardized Naming**: Follows Prometheus naming conventions (`namespace_subsystem_name_unit`). All metrics are scoped under `tradedrift_notification_*`.
- **Automatic Registration**: Uses `github.com/prometheus/client_golang/prometheus/promauto` to automatically register metrics to `prometheus.DefaultRegisterer` upon package initialization, preventing registration race conditions.
- **Zero-Allocation Counter Bumping**: Uses Prometheus vector labels (`CounterVec`) to segment counters by event type, topic, destination channel, and status without runtime schema changes.

---

## Problems Solved by `metrics.go`

| Problem | Failure Without `metrics.go` | How `metrics.go` Solves It |
|---|---|---|
| **Silent Outbox Stalling** | If Redis Pub/Sub fails or experiences authentication timeouts, the publisher pauses silently. Users stop receiving real-time WebSocket alerts with no indication in log levels. | `OutboxPublishErrorsTotal` increments immediately on every Redis failure, triggering alerts before users report missing notifications. |
| **Undetected Poison Messages** | If an upstream service (Trade or Order) changes its Kafka schema without notice, events fail validation and route to DLQ. Without metrics, engineers only notice when customers complain. | `KafkaEventsConsumedTotal` labeled with `status="poison"` spikes, allowing Prometheus Alertmanager to fire a critical page within 30 seconds. |
| **Channel Congestion Blindspots** | Different channels carry different data: `user:notifications:*` carries user trade alerts; `user:portfolio:*` carries high-frequency snapshots. | Labels outbox publication metrics by `channel`, allowing operators to distinguish whether errors are isolated to a single stream or affecting all Redis connections. |
| **Double-Registration Panics** | In Go, manually calling `prometheus.MustRegister` across multiple packages or during unit test execution causes panics if a metric is registered twice. | Uses `promauto.NewCounterVec`, which guarantees idempotent, thread-safe registration to the default registry on package load. |

---

## Prometheus Metric Registry Reference

All metrics share the common namespace `tradedrift` and subsystem `notification`:

| Metric Name | Full Prometheus Identifier | Type | Labels | Description |
|---|---|---|---|---|
| `NotificationsCreatedTotal` | `tradedrift_notification_created_total` | `CounterVec` | `type` | Total number of notifications created in PostgreSQL by type (`INFO`, `TRADE_FILL`, `SYSTEM`, `ACCOUNT`). |
| `OutboxEventsPublishedTotal` | `tradedrift_notification_outbox_published_total` | `CounterVec` | `channel` | Total outbox events successfully broadcast to Redis Pub/Sub channels. |
| `OutboxPublishErrorsTotal` | `tradedrift_notification_outbox_publish_errors_total` | `CounterVec` | `channel` | Total publication attempts that failed due to Redis network or timeout errors. |
| `KafkaEventsConsumedTotal` | `tradedrift_notification_kafka_events_consumed_total` | `CounterVec` | `topic`, `status` | Total Kafka domain events processed, segmented by topic and status (`success`, `poison`). |

---

## Detailed Instrumentation Breakdown

### 1. `NotificationsCreatedTotal`
```go
var NotificationsCreatedTotal = promauto.NewCounterVec(
    prometheus.CounterOpts{
        Namespace: "tradedrift",
        Subsystem: "notification",
        Name:      "created_total",
        Help:      "Total number of notifications created by type",
    },
    []string{"type"},
)
```
- **Where Instrumented**: `internal/service/inbox.go` inside `CreateNotification`:
  ```go
  metrics.NotificationsCreatedTotal.WithLabelValues(string(notifType)).Inc()
  ```
- **Operational Value**: Tracks notification generation velocity across the exchange. A sudden drop indicates upstream order or trade service halts; a massive spike indicates runaway bot notifications.

---

### 2. `OutboxEventsPublishedTotal`
```go
var OutboxEventsPublishedTotal = promauto.NewCounterVec(
    prometheus.CounterOpts{
        Namespace: "tradedrift",
        Subsystem: "notification",
        Name:      "outbox_published_total",
        Help:      "Total number of outbox events successfully published to Redis",
    },
    []string{"channel"},
)
```
- **Where Instrumented**: `internal/publisher/publisher.go` inside `ProcessBatch`:
  ```go
  metrics.OutboxEventsPublishedTotal.WithLabelValues(ev.TargetChannel).Inc()
  ```
- **Operational Value**: Measures real-time delivery throughput to the Gateway WebSocket relay. Used to calculate delivery latency and outbox draining rates.

---

### 3. `OutboxPublishErrorsTotal`
```go
var OutboxPublishErrorsTotal = promauto.NewCounterVec(
    prometheus.CounterOpts{
        Namespace: "tradedrift",
        Subsystem: "notification",
        Name:      "outbox_publish_errors_total",
        Help:      "Total number of outbox publication errors",
    },
    []string{"channel"},
)
```
- **Where Instrumented**: `internal/publisher/publisher.go` when `publishWithRetry` exhausts retries:
  ```go
  metrics.OutboxPublishErrorsTotal.WithLabelValues(ev.TargetChannel).Inc()
  ```
- **Operational Value**: Serves as the primary indicator for Redis connection degradation, network timeouts, or outbox worker saturation.

---

### 4. `KafkaEventsConsumedTotal`
```go
var KafkaEventsConsumedTotal = promauto.NewCounterVec(
    prometheus.CounterOpts{
        Namespace: "tradedrift",
        Subsystem: "notification",
        Name:      "kafka_events_consumed_total",
        Help:      "Total number of Kafka events consumed by topic and status",
    },
    []string{"topic", "status"},
)
```
- **Where Instrumented**: `internal/kafka/consumer.go`:
  - **Success Path**:
    ```go
    metrics.KafkaEventsConsumedTotal.WithLabelValues(topic, "success").Inc()
    ```
  - **Poison Message Path (`handlePoison`)**:
    ```go
    metrics.KafkaEventsConsumedTotal.WithLabelValues(topic, "poison").Inc()
    ```
- **Operational Value**: Segregates healthy event consumption from poison drops.

---

## Recommended Prometheus Alerting Rules (PromQL)

```yaml
groups:
  - name: notification_service_alerts
    rules:
      # 1. Alert if Redis outbox publish error rate exceeds 5% over 5 minutes
      - alert: NotificationOutboxPublishHighErrorRate
        expr: |
          sum(rate(tradedrift_notification_outbox_publish_errors_total[5m]))
          /
          (sum(rate(tradedrift_notification_outbox_published_total[5m])) + sum(rate(tradedrift_notification_outbox_publish_errors_total[5m])))
          > 0.05
        for: 2m
        labels:
          severity: critical
        annotations:
          summary: "Notification outbox publish error rate > 5%"
          description: "Outbox publisher is failing to deliver events to Redis Pub/Sub."

      # 2. Alert if any poison messages are detected in Kafka topics
      - alert: NotificationKafkaPoisonMessagesDetected
        expr: sum(rate(tradedrift_notification_kafka_events_consumed_total{status="poison"}[5m])) > 0
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "Poison messages detected in notification Kafka ingestion"
          description: "Events in topic {{ $labels.topic }} failed validation and were routed to DLQ."
```

---

## End-to-End Metrics Scraping Architecture

```
 Service Component        Instrumentation Action            Prometheus Registry
 ─────────────────        ──────────────────────            ───────────────────
 Kafka Consumer     ──►   KafkaEventsConsumedTotal.Inc()    ──┐
                                                              │
 Domain Service     ──►   NotificationsCreatedTotal.Inc()   ──┼─► [promauto.DefaultRegisterer]
                                                              │
 Outbox Publisher   ──►   OutboxEventsPublishedTotal.Inc()  ──┤
                    ──►   OutboxPublishErrorsTotal.Inc()    ──┘
                                                              │
                                                              ▼
                                                   HTTP Server (:9092)
                                                   Endpoint: /metrics
                                                              │
                                                              ▼
                                                   Prometheus Scraper
                                                   (Scrape Interval: 15s)
                                                              │
                                                              ▼
                                                   Grafana Dashboards &
                                                   Alertmanager Notification
```
