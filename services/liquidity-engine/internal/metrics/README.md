# package `metrics`

## Purpose

Provides **Prometheus instrumentation** for the Liquidity Engine. All counters, gauges, and histograms used across the engine, reconciler, and handlers are registered here.

## Problem It Solves

Without metrics, there's no way to answer operational questions like:
- Is the engine currently RUNNING or DEGRADED?
- How many MM levels are active per market and side?
- How often is the reconcile cycle producing NO-OPs vs creates/cancels?
- How many fills have been processed?
- Are there STALE orders building up?
- Is the ME probe succeeding or failing?

## How It Solves It

`New()` registers all metrics once at startup using `promauto` (auto-registers with the default Prometheus registry). The `*Metrics` struct implements both `engine.EngineMetrics` and `reconciler.ReconcilerMetrics` interfaces, so the engine and reconciler depend only on the interface — not on the concrete Prometheus implementation. This makes unit testing easy (mock implementations in tests).

---

## Prometheus Metrics Reference

| Metric | Type | Labels | Description |
|:---|:---|:---|:---|
| `le_engine_state` | Gauge | `state` | Current engine state. Only the active state is 1.0; all others are 0.0. |
| `le_active_levels` | Gauge | `market_id`, `side` | Target level count per market side (from `ComputeSkew`). |
| `le_reconcile_duration_ms` | Histogram | `market_id` | Reconcile cycle duration in ms. Buckets: 1, 5, 10, 25, 50, 100, 250, 500. |
| `le_reconcile_create_total` | Counter | `market_id` | OrderCreated commands published. |
| `le_reconcile_cancel_total` | Counter | `market_id` | OrderCancelRequested commands published. |
| `le_reconcile_correct_total` | Counter | `market_id` | CORRECT operations (cancel + replace) performed. |
| `le_reconcile_noop_total` | Counter | `market_id` | Reconcile cycles with zero diff (desired == actual). |
| `le_orders_filled_total` | Counter | `market_id`, `side` | MM orders fully filled. |
| `le_stale_orders_total` | Counter | `market_id` | Orders that entered STALE state (cancel retry limit exceeded). |
| `le_me_liveness_timeout_total` | Counter | `market_id` | Times ME liveness threshold was exceeded (market paused). |
| `le_duplicate_level_total` | Counter | `market_id` | Duplicate LevelIDs detected in OS active order response. |
| `le_me_health_probe_total` | Counter | `status` | ME HTTP health probe attempts (labels: `"success"`, `"failure"`). |
| `le_market_pause_total` | Counter | `market_id`, `action` | Market pause/resume events (labels: `"pause"`, `"resume"`). |

---

## Flow: Engine State Gauge Update

```
engine.setState(StateRunning)
         │
         ▼
  metrics.SetEngineState("RUNNING")
         │
         └── for each state [STARTING, SYNCING, RUNNING, DEGRADED, PAUSED, STOPPED]:
               engineState{state=X}.Set(0.0)
               if X == "RUNNING": Set(1.0)

Grafana: le_engine_state{state="RUNNING"} == 1
```

---

## Files

### [`metrics.go`](./metrics.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `Metrics` | `struct` | Holds all registered Prometheus metric objects. |
| `New()` | `func` | Registers all metrics with `promauto`. Call once at startup. |
| `SetEngineState(state)` | `func` | Sets the active state to 1.0 and all others to 0.0 (mutual-exclusion gauge pattern). |
| `SetLevelCount(marketID, side, count)` | `func` | Sets `le_active_levels` gauge. |
| `ObserveReconcileDuration(marketID, ms)` | `func` | Records reconcile duration in histogram. |
| `IncReconcileCreate(marketID)` | `func` | Increments `le_reconcile_create_total`. |
| `IncReconcileCancel(marketID)` | `func` | Increments `le_reconcile_cancel_total`. |
| `IncReconcileCorrect(marketID)` | `func` | Increments `le_reconcile_correct_total`. |
| `IncReconcileNoop(marketID)` | `func` | Increments `le_reconcile_noop_total`. |
| `IncOrdersFilled(marketID, side)` | `func` | Increments `le_orders_filled_total`. |
| `IncStaleOrders(marketID)` | `func` | Increments `le_stale_orders_total`. |
| `IncMELivenessTimeout(marketID)` | `func` | Increments `le_me_liveness_timeout_total`. |
| `IncDuplicateMMLevel(marketID)` | `func` | Increments `le_duplicate_level_total`. |
| `IncMEHealthProbe(status)` | `func` | Increments `le_me_health_probe_total` with `"success"` or `"failure"`. |
| `IncMarketPause(marketID, action)` | `func` | Increments `le_market_pause_total` with `"pause"` or `"resume"`. |

---

## Useful Grafana Queries

```promql
# Is the engine running?
le_engine_state{state="RUNNING"}

# Active bid levels for BTC-USDT
le_active_levels{market_id="BTC-USDT", side="BUY"}

# Reconcile create rate
rate(le_reconcile_create_total[5m])

# ME probe failure rate
rate(le_me_health_probe_total{status="failure"}[5m])

# STALE orders accumulating?
increase(le_stale_orders_total[1h])
```
