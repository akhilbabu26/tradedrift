# Health Package (`internal/health`)

The `health` package provides HTTP liveness and readiness probe endpoints for Kubernetes, container orchestrators, and monitoring agents.

---

## 1. Endpoints Overview

| Endpoint | Type | Port | Purpose |
| :--- | :--- | :--- | :--- |
| `/healthz` | **Liveness Probe** | `HEALTH_PORT` (Default: `8080`) | Confirms the HTTP server is running and the Go runtime is alive. |
| `/readyz` | **Readiness Probe** | `HEALTH_PORT` (Default: `8080`) | Evaluates end-to-end operational capability across Redis, Order Service, and live market depth. |

---

## 2. Liveness (`/healthz`)

* **HTTP Status:** Always returns `200 OK`.
* **Payload:**
  ```json
  {
    "status": "ok"
  }
  ```
* **Failure Semantics:** If `/healthz` fails to respond, the container has deadlocked, ran out of memory, or terminated, and Kubernetes restarts the pod.

---

## 3. Readiness (`/readyz`)

Readiness determines whether CTS is fully capable of trading. It executes an end-to-end health sweep across infrastructure dependencies and market depth feeds:

```
                          GET /readyz
                               │
            ┌──────────────────┴──────────────────┐
            ▼                                     ▼
 1. Check Redis Connectivity           2. Check Order Service
    • redisReader == nil ──▶ 503          • orderClient == nil ──▶ 503
    • redis.Ping() err   ──▶ 503          • client.Ping() err  ──▶ 503
            │                                     │
            └──────────────────┬──────────────────┘
                               │
                               ▼
               3. Check Market Depth (All Markets)
                  For each configured market:
                  • depth exists in Redis?
                  • len(bids) > 0 && len(asks) > 0?
                  • time.Since(snapshot_at) <= 5.0s?
                               │
            ┌──────────────────┴──────────────────┐
            ▼                                     ▼
    At least 1 market ready                 0 markets ready
    • HTTP 200 OK                          • HTTP 503 Service Unavailable
    • "ready": true                        • "ready": false
    • Per-market breakdown in JSON         • "reason": "no markets have active depth"
```

### Response Schema: All Markets Healthy (`200 OK`)
```json
{
  "ready": true,
  "markets": {
    "BTC-USDT": true,
    "ETH-USDT": true,
    "SOL-USDT": true
  }
}
```

### Response Schema: Partial Markets Healthy (`200 OK`)
If at least one market has active, fresh depth and both Redis and Order Service are reachable, the service reports `200 OK` so operational markets can trade:
```json
{
  "ready": true,
  "markets": {
    "BTC-USDT": true,
    "ETH-USDT": false,
    "SOL-USDT": false
  }
}
```

### Response Schema: Infrastructure Failure (`503 Service Unavailable`)
```json
{
  "ready": false,
  "reason": "order service unreachable: connection refused"
}
```

---

## 4. Fail-Closed Safety Protections

1. **Nil-Client Protection:** If either `redisReader` or `orderClient` is uninitialized or `nil`, `/readyz` immediately returns `503 Service Unavailable`. CTS never reports ready without active Order Service and Redis connections.
2. **5-Second Depth Freshness:** If market depth has not updated within 5.0 seconds, the market is marked unready (`false`).
3. **Timeout Protection:** The `/readyz` probe context enforces a strict `2.0s` timeout to prevent hanging health checks from causing cascading container stalls.
