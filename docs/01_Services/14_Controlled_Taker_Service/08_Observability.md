# Controlled Taker Service — Observability, Metrics & Health

> **Status:** 📐 Designed (V1.1 — Updated with Architectural Feedback)  
> **Service:** Controlled Taker Service (`services/controlled-taker`)  
> **Document:** `08_Observability.md`  
> **Last Updated:** September 2026  

---

## 1. Prometheus Metrics Catalog

The service exposes an HTTP metrics scrape endpoint on port `:9090` (host port `:9097`):

```
GET http://controlled-taker:9090/metrics
```

| Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- |
| `cts_orders_submitted_total` | Counter | `market_id`, `side`, `profile` | Total orders sent to Order Service |
| `cts_orders_failed_total` | Counter | `market_id`, `reason` | Total failed or rejected orders |
| `cts_trades_executed_total` | Counter | `market_id`, `profile` | Estimated fill executions |
| `cts_notional_volume_usdt_total`| Counter | `market_id` | Cumulative synthetic notional volume |
| `cts_skipped_cycles_total` | Counter | `market_id`, `reason` | Skipped executions (e.g. wide spread, thin depth) |
| `cts_circuit_breaker_tripped` | Counter | `market_id` | Count of circuit breaker trip events |
| `cts_order_latency_seconds` | Histogram | `step` (`redis_read`, `grpc_order`) | Internal processing latency |
| `cts_worker_status` | Gauge | `market_id` | Status (1 = Active, 0 = Paused/Tripped) |

---

## 2. Structured Logging Standards

CTS uses `go.uber.org/zap` with JSON encoding. Every log entry includes standardized contextual keys:

```json
{
  "level": "info",
  "ts": "2026-09-27T23:30:15.892Z",
  "caller": "engine/worker.go:128",
  "msg": "controlled taker order submitted",
  "market_id": "BTC-USDT",
  "profile": "LOW",
  "side": "BUY",
  "quantity": "0.0035",
  "capped_price": "96515.00",
  "best_ask": "96510.00",
  "order_id": "019234af-1122-7788-99aa-123456789abc",
  "idempotency_key": "CTS-BTC-USDT-019234af-1122",
  "latency_ms": 18
}
```

---

## 3. Health & Readiness Probes

Exposed via standard HTTP server on `HEALTH_PORT` (default `:8080`, host port `:8086`):

### Liveness Probe (`GET /healthz`)
- **Status:** HTTP 200 `{"status":"ok"}`
- Confirms the Go runtime and process goroutines are alive.

### Readiness Probe (`GET /readyz`)
- **Condition:**  
  $$\mathbf{Ready} \iff \text{Redis Reachable} \land \text{Order Service Reachable} \land (\ge 1\ \text{Configured Market Has Valid Depth})$$
- **Semantic:**  
  CTS reports **Ready (HTTP 200)** as long as it has sufficient dependencies to perform useful work on **at least one** configured active market.
- **Why this matches per-market isolation:**  
  If `SOL-USDT` has an unseeded book or temporarily missing depth snapshot, but `BTC-USDT` and `ETH-USDT` have valid depth and Order Service is connected, CTS remains ready to trade on BTC and ETH rather than failing cluster health checks.
