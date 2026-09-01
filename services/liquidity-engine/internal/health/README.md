# package `health`

## Purpose

Provides the **`/healthz`, `/readyz`, and `/status` HTTP endpoints** that expose the engine's operational state to Kubernetes, load balancers, and operators.

## Problem It Solves

Without health endpoints:
- Kubernetes can't distinguish a starting engine from a running one, causing premature traffic routing.
- Operators have no visibility into whether the ME is paused, inventory is stale, or markets are fully quoting.
- The engine's internal `atomic.Pointer[StatusSnapshot]` state is not directly queryable.

## How It Solves It

The `health.Server` wraps any `HealthChecker` (implemented by `*engine.Engine`) and reads only from the lock-free `atomic.Pointer[StatusSnapshot]`. HTTP handlers never touch the engine's internal mutable state — they read a pre-built snapshot that is always consistent and safe for concurrent access.

---

## Endpoint Reference

| Endpoint | Returns 200 when | Returns 503 when |
|:---|:---|:---|
| `GET /healthz` | Engine is in any state except STOPPED | State == STOPPED |
| `GET /readyz` | State is RUNNING or DEGRADED, and at least one market has sufficient RESTING orders | State is STARTING/SYNCING/PAUSED/STOPPED, or insufficient resting orders |
| `GET /status` | Always 200 | Never — always returns JSON diagnostics |

---

## Flow: Readiness Check

```
HTTP GET /readyz
         │
         ▼
  handleReadiness(w, r)
         │
         ├── checker.State()   [atomic.Load from snapshot]
         │     state == STARTING / SYNCING / PAUSED / STOPPED?
         │     → 503 state.String()
         │
         ├── checker.IsReady() [atomic.Load from snapshot]
         │     IsReady == false?
         │     → 503 "insufficient resting orders"
         │
         └── 200 "ready"
```

---

## Flow: Status Response

```
HTTP GET /status
         │
         ▼
  handleStatus(w, r)
         │
         ├── checker.State()                → "RUNNING" / "DEGRADED" / ...
         ├── checker.IsReady()              → true / false
         ├── checker.MarketStates()         → {"BTC-USDT": "RUNNING", ...}
         ├── checker.InventoryLastRefresh() → RFC3339 timestamp
         └── compute inventory_stale: time.Since(lastRefresh) > MaxBalanceStaleness
         │
         ▼
  200 JSON:
  {
    "state": "RUNNING",
    "ready": true,
    "markets": {"BTC-USDT": "RUNNING", "ETH-USDT": "PAUSED_ME"},
    "inventory_last_refresh": "2026-09-01T09:00:00Z",
    "inventory_stale": false,
    "uptime_s": 3600.5
  }
```

---

## Market Status Values

| Status | Meaning |
|:---|:---|
| `RUNNING` | Market is active and order sync is fresh |
| `PAUSED_ME` | ME liveness threshold exceeded for this market |
| `PAUSED_INVENTORY` | Wallet balance is stale (> MaxBalanceStaleness) |
| `UNSYNCHRONIZED` | No successful OS sync since startup |
| `STALE` | Last OS sync is older than MaxOrderStateStaleness |

---

## Files

### [`health.go`](./health.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `HealthChecker` | `interface` | Read-only methods the engine exposes: `State()`, `IsReady()`, `MarketStates()`, `InventoryLastRefresh()`, `MaxBalanceStaleness()`. All backed by atomic snapshot reads in `engine.snapshot.go`. |
| `Server` | `struct` | Holds a `HealthChecker` reference. |
| `New(checker)` | `func` | Creates a Server wrapping the provided HealthChecker. |
| `Handler()` | `func` | Returns an `http.ServeMux` with the three endpoints registered. |
| `handleLiveness(w, r)` | `func` (internal) | 200 unless state is STOPPED. |
| `handleReadiness(w, r)` | `func` (internal) | 503 if engine is pre-running or lacks sufficient RESTING orders on at least one market. |
| `handleStatus(w, r)` | `func` (internal) | Full JSON diagnostic: state, readiness, per-market statuses, inventory freshness, uptime. |
