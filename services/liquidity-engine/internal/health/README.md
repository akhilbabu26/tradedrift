# `internal/health` — Liveness & Readiness HTTP Server

**Package:** `health`  
**Service:** Liquidity Engine  
**Last Updated:** August 2026

---

## 1. What This Package Does

This package provides the **HTTP health check server** for the Liquidity Engine. It exposes three endpoints used by Kubernetes probes and operational monitoring to determine the live and ready state of the LE process.

---

## 2. Files In This Package

| File | Purpose |
| :--- | :--- |
| `health.go` | `Server`, `HealthChecker` interface, `/healthz`, `/readyz`, `/status` handlers |
| `README.md` | This documentation file |

---

## 3. Endpoints

| Endpoint | Probe Type | Returns 200 when | Returns 503 when |
| :--- | :--- | :--- | :--- |
| `GET /healthz` | Liveness | Engine is not `STOPPED` | Engine state is `STOPPED` |
| `GET /readyz` | Readiness | State is `RUNNING` or `DEGRADED` AND `IsReady() == true` | State is `PAUSED`, `SYNCING`, or `STARTING`; or insufficient resting orders |
| `GET /status` | Debug / monitoring | Always 200 | — |

---

## 4. `HealthChecker` Interface

The `Server` depends on this interface — the engine (`engine.Engine`) satisfies it:

```go
type HealthChecker interface {
    State() string                        // "RUNNING" | "PAUSED" | "DEGRADED" | etc.
    IsReady() bool                        // true if all markets have ≥ MinReadyBids/Asks resting
    InventoryLastRefresh() time.Time      // timestamp of last Wallet Service balance refresh
    MaxBalanceStaleness() time.Duration   // configured staleness threshold
}
```

---

## 5. Liveness (`/healthz`)

```
Engine state == "STOPPED" → 503 Service Unavailable ("stopped")
Otherwise                 → 200 OK ("ok")
```

The liveness probe is intentionally lenient — it only fails when the engine has cleanly shut down. `PAUSED` and `DEGRADED` states are considered live (recoverable without a restart).

---

## 6. Readiness (`/readyz`)

```
State == "PAUSED"   → 503 ("PAUSED")    ← ME unresponsive, not accepting traffic
State == "SYNCING"  → 503 ("SYNCING")   ← startup sync in progress
State == "STARTING" → 503 ("STARTING")  ← pre-sync initialization

IsReady() == false  → 503 ("insufficient resting orders")
                        (some markets have < MinReadyBids or MinReadyAsks)

Otherwise           → 200 OK ("ready")
```

Kubernetes will not route traffic to the LE pod while readiness fails. This is critical during:
- Initial startup (SYNCING state while discovering existing MM orders)
- ME recovery periods (PAUSED state — LE is not quoting)
- Inventory exhaustion (all levels consumed, DEGRADED with 0 active sides)

---

## 7. `/status` — Debug Endpoint

Returns a JSON object for operational monitoring:

```json
{
  "state":                  "RUNNING",
  "ready":                  true,
  "inventory_last_refresh": "2026-08-31T08:00:00Z",
  "inventory_stale":        false,
  "uptime_s":               3600.0
}
```

---

## 8. Usage

```go
// In cmd/server/main.go:
healthServer := health.New(engine)  // engine satisfies HealthChecker
mux := healthServer.Handler()
http.ListenAndServe(":"+cfg.HealthPort, mux)
```
