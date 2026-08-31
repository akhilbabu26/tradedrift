# `internal/metrics` — Prometheus Instrumentation

**Package:** `metrics`  
**Service:** Liquidity Engine  
**Last Updated:** August 2026

---

## 1. What This Package Does

This package registers and exposes **Prometheus metrics** for the Liquidity Engine. The `Metrics` struct satisfies both `engine.EngineMetrics` and `reconciler.ReconcilerMetrics` interfaces, so it is wired into the engine and reconciler at startup.

---

## 2. Files In This Package

| File | Purpose |
| :--- | :--- |
| `metrics.go` | `Metrics` struct, metric registration, all interface method implementations |
| `README.md` | This documentation file |

---

## 3. Registered Metrics

All metrics use the `le_` namespace (Liquidity Engine).

| Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- |
| `le_engine_state` | Gauge | `state` | Current engine state. One label is set to `1.0`, others to `0.0` |
| `le_active_levels` | Gauge | `market_id`, `side` | Number of active MM order levels (BUY or SELL) per market |
| `le_reconcile_duration_ms` | Histogram | `market_id` | Reconcile cycle duration in milliseconds. Buckets: 1, 5, 10, 25, 50, 100, 250, 500 |
| `le_reconcile_create_total` | Counter | `market_id` | Total `OrderCreated` commands published |
| `le_reconcile_cancel_total` | Counter | `market_id` | Total `OrderCancelRequested` commands published |
| `le_reconcile_correct_total` | Counter | `market_id` | Total CORRECT operations (cancel + queued replacement) |
| `le_reconcile_noop_total` | Counter | `market_id` | Total reconcile cycles where desired == actual (no commands) |
| `le_orders_filled_total` | Counter | `market_id`, `side` | Total MM orders confirmed fully filled |
| `le_stale_orders_total` | Counter | `market_id` | Total orders that entered STALE state (cancel retry limit exceeded) |
| `le_me_liveness_timeout_total` | Counter | `market_id` | Total ME liveness threshold exceeded events |

---

## 4. Engine State Gauge

`le_engine_state` uses an **exclusive set pattern**: every state transition sets exactly one label value to `1.0` and all others to `0.0`. This makes it Grafana-friendly — you can plot `le_engine_state{state="RUNNING"}` and get a 0/1 series without needing `offset` tricks.

```
le_engine_state{state="STARTING"}  = 0
le_engine_state{state="SYNCING"}   = 0
le_engine_state{state="RUNNING"}   = 1   ← current state
le_engine_state{state="DEGRADED"}  = 0
le_engine_state{state="PAUSED"}    = 0
le_engine_state{state="STOPPED"}   = 0
```

---

## 5. Key Monitoring Queries

```promql
-- Reconcile health: noop rate should be very high in steady state
rate(le_reconcile_noop_total[5m]) / rate(le_reconcile_duration_ms_count[5m])

-- Order fill rate per market
rate(le_orders_filled_total[5m])

-- Active ask levels (should be 12 when healthy)
le_active_levels{side="SELL"}

-- Detect engine pause
le_engine_state{state="PAUSED"} == 1

-- ME liveness timeout rate (>0 means ME is degraded)
rate(le_me_liveness_timeout_total[5m])

-- Stale order accumulation (should be near 0)
increase(le_stale_orders_total[1h])
```

---

## 6. Interfaces Implemented

### `engine.EngineMetrics`

```go
SetEngineState(state string)
SetLevelCount(marketID, side string, count int)
ObserveReconcileDuration(marketID string, ms float64)
IncMELivenessTimeout(marketID string)
// + all ReconcilerMetrics
```

### `reconciler.ReconcilerMetrics`

```go
IncStaleOrders(marketID string)
IncReconcileCreate(marketID string)
IncReconcileCancel(marketID string)
IncReconcileCorrect(marketID string)
IncReconcileNoop(marketID string)
IncOrdersFilled(marketID, side string)
```

---

## 7. Prometheus Endpoint

Metrics are served at `:{METRICS_PORT}/metrics` (default port `9090`) by the standard Prometheus HTTP handler registered in `cmd/server/main.go`.
