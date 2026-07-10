# TradeDrift — Observability & Monitoring Specification

> **Status:** ✅ Designed (V1.1)
> **Document:** 21_Observability.md
> **Service:** Platform Architecture
> **Version:** V1.1
> **Last Updated:** July 2026
> Revision notes: V1.1 expands security and metrics criteria: (1) documents a sensitive data redaction policy for PII and secrets; (2) defines a probabilistic head/tail trace sampling standard; (3) introduces a Service Level Objectives (SLO) performance target catalog.

---

## 1. Structured Logging Standard

All services must emit structured logs to standard output (`stdout`) in JSON format. This enables downstream log shippers (such as Vector or FluentBit) to index log records in central repositories (Elasticsearch / Grafana Loki) without parsing regex.

### 1.1 Standard JSON Logging Envelope Schema
```json
{
  "timestamp": "2026-07-10T13:45:12.123456Z",
  "level": "ERROR",
  "service": "order-service",
  "version": "v1.1.0",
  "message": "Failed to reserve funds for buy order",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "00f067aa0ba902b7",
  "request_id": "018f60f3-a120-7798-8422-cfb6a29e11aa",
  "correlation_id": "018f60f3-c540-7798-8422-efa6b29f1234",
  "causation_id": "018f60f3-d090-7798-8422-dfb8a29f5678",
  "user_id": "018f60f3-e510-7798-8422-ffc8a29e9999",
  "error": {
    "code": "FAILED_PRECONDITION",
    "message": "insufficient wallet balance",
    "stack": "github.com/tradedrift/platform/wallet/pkg/service.(*WalletService).SettleTrade..."
  }
}
```

### 1.2 Envelope Keys:
* `timestamp`: RFC3339Nano timezone-aware timestamp in UTC.
* `level`: Must be one of `DEBUG`, `INFO`, `WARN`, `ERROR`, or `FATAL`.
* `trace_id` / `span_id`: Injected by OpenTelemetry context.
* `request_id` / `correlation_id` / `causation_id`: Injected from gRPC metadata or Kafka headers (see `docs/03_Standards/ID_Correlation_Standard.md`).

### 1.3 Sensitive Data Redaction Policy
To prevent leaks of credential or user data into logging databases:
* **Prohibited Values:** Plaintext passwords, API keys, JWT signatures, session tokens, and database connection strings are strictly prohibited from log message entries and metadata maps.
* **PII Obfuscation:** Personally Identifiable Information (PII) including email addresses, phone numbers, and real-name descriptors must be redacted or replaced with a SHA-256 hash at the application boundary before logging.
* **Automatic Gateway Masking:** Gateway middleware and logger filters parse payloads and mask values matching common sensitive key shapes (e.g. `password`, `token`, `authorization`, `signature`, `key`) or structural values resembling JWT blocks.

---

## 2. Distributed Tracing & W3C context Propagation

TradeDrift maps end-to-end request flows using OpenTelemetry (OTel). Systems propagate span details using the **W3C Trace Context** standard.

```
[ Client / Browser ]
        │
  (HTTP/WebSocket) -> Header: traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01
        ▼
  [ API Gateway ]
        │
  (gRPC Metadata) -> Key: traceparent
        ▼
  [ Order Service ]
        │
  (Kafka Headers) -> Key: traceparent
        ▼
  [ Matching Engine ]
```

### 2.1 Propagation Standards:
* **HTTP / WebSocket ingress:** Extracted from the `traceparent` HTTP header.
* **Internal gRPC RPCs:** Injected/Extracted using `metadata.MD` under the `traceparent` binary or string key.
* **Kafka Event Streams:** Injected into Kafka record headers. Event-driven consumers extract trace context before invoking downstream logic, preserving the asynchronous trace path.

### 2.2 Trace Sampling Policy
To balance network bandwidth and collector storage costs with diagnostic visibility, TradeDrift implements a hybrid tracing sampling strategy:
* **Standard API/gRPC Ingress:** Uses a probabilistic head-based sampling rate of **1%** for standard HTTP and internal gRPC requests.
* **Matching Engine Execution Loops:** Probabilistic sampling is restricted to **0.1%** to avoid telemetry overhead in high-frequency engine cycles.
* **Error Overrides (Tail-based):** To ensure diagnostic reliability, any request that encounters a failure (returning HTTP `5xx` status or any gRPC status code other than `OK`) is **100% captured** via tail-based tracing.
* **Manual Debug Overrides:** SREs can trace 100% of requests for a specific session by passing a `sampled=1` flag within the W3C `traceparent` header.

---

## 3. Platform & Business Metrics Catalog

Metrics are exposed on `/metrics` endpoints in the Prometheus format. 

### 3.1 Business & Application Metrics

| Metric Name | Type | Labels | Description |
|---|---|---|---|
| `matching_engine_order_process_latency_seconds` | `Histogram` | `market_id`, `order_type`, `side` | Latency to execute matching logic in-memory. |
| `matching_engine_book_depth_count` | `Gauge` | `market_id`, `side` | Count of active bids/asks resting in the book. |
| `wallet_balance_invariants_violation_total` | `Counter` | `user_id`, `asset` | **P0 Alert Trigger.** Increments if `available + reserved != total`. |
| `wallet_stale_reservations_reconciled_total` | `Counter` | `asset` | Count of reservation TTL cleanups executed by crons. |
| `websocket_active_connections` | `Gauge` | `replica_pod` | Concurrent TCP sockets active on this gateway node. |
| `websocket_buffer_overflow_drops_total` | `Counter` | `reason` | Counts sockets terminated because of client network lag. |
| `kafka_outbox_publish_latency_seconds` | `Histogram` | `service` | Duration from SQL outbox write to Kafka broker ACK. |

### 3.2 Standard Infrastructure Metrics

| Metric Name | Type | Labels | Description |
|---|---|---|---|
| `http_requests_total` | `Counter` | `handler`, `status` | Total HTTP requests handled. |
| `grpc_server_handled_total` | `Counter` | `grpc_service`, `grpc_method`, `grpc_code` | Total gRPC calls processed. |
| `go_memstats_heap_alloc_bytes` | `Gauge` | N/A | Memory allocation profiling (detects memory leaks). |
| `go_goroutines` | `Gauge` | N/A | Total active Go routines (detects routine leakage). |

---

## 4. Alerting Thresholds & SLA Matrix

Alerting rules are evaluated in Prometheus/Thanos and routed through Alertmanager:

### 4.1 P1 Alert Thresholds (Critical — PagerDuty Active Alert)
* **Balance Invariant Failure:**
  `sum(rate(wallet_balance_invariants_violation_total[1m])) > 0`
  *Action:* Page SRE and freeze transaction updates immediately (automated circuit-breaker).
* **Matching Engine Offline:**
  `up{service="matching-engine"} == 0`
  *Action:* Page SRE immediately (critical order processing downtime).
* **Kafka Egress Lag Spike:**
  `sum(kafka_consumergroup_lag) by (consumergroup) > 50000`
  *Action:* Page SRE if lag persists for > 3 minutes. Indicates consumer crash or partition lockup.

### 4.2 P2 Alert Thresholds (Warning — Slack Notification / Email)
* **API Ingress Error Rates:**
  `sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m])) > 0.01`
  *Action:* Warn developers (5xx HTTP error rates exceed 1%).
* **WebSocket Evictions:**
  `sum(rate(websocket_buffer_overflow_drops_total[5m])) > 100`
  *Action:* Alert network team of potential DDoS or ISP transit degradation.
* **Database Pool Saturation:**
  `go_db_connections_active / go_db_connections_max > 0.85`
  *Action:* Trigger warning to scale database replication pools or upgrade DB instance storage.

---

## 5. Service Level Objectives (SLOs)

To measure and report operational health against Service Level Agreements (SLAs), the platform defines the following Service Level Objectives (SLOs) over a rolling 30-day window:

| Service Area | Service Level Indicator (SLI) | Target Objective (SLO) | Operational Priority |
|---|---|---|---|
| **Order Ingestion** | Ratio of HTTP/gRPC `CreateOrder` calls responding in $< 50\text{ms}$ | **$\ge 99.9\%$** | P1 (SRE Page on violation) |
| **Matching Engine** | Ratio of order execution matching loops matching in $< 1\text{ms}$ | **$\ge 99.99\%$** | P1 (SRE Page on violation) |
| **Trade Settlement** | Elapsed time from Match execution event to Settlement status written | **$\ge 99.9\%$** in $< 200\text{ms}$ | P2 (Slack Warning) |
| **WebSocket Delivery** | Latency between Redis notification publish to socket frame write | **$\ge 99.0\%$** in $< 50\text{ms}$ | P2 (Slack Warning) |
| **System Availability** | Percentage of non-5xx responses across all public API routes | **$\ge 99.95\%$** | P1 (SRE Page on violation) |

---

## 6. Service Invariants

- **OBS-1 (Context Propagation):** Downstream microservices must extract trace contexts from parent streams (HTTP/gRPC/Kafka) and inject trace contexts into outgoing network events.
- **OBS-2 (Invariant Alert Priority):** The metric `wallet_balance_invariants_violation_total` must map directly to a critical SRE notification system.
- **OBS-3 (JSON Standard):** No plain-text logs are allowed in staging/production environments. Sockets must route records via the standard JSON logging envelope.
- **OBS-4 (Secrets Filter):** All log messages must pass through application-level masking filters to redact sensitive user information and access tokens.

