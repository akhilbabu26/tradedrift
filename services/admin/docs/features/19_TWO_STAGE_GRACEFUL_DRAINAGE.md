# Feature 19: Two-Stage Graceful Drainage Protocol

## 1. What This Feature Does
The **Two-Stage Graceful Drainage Protocol** guarantees zero dropped requests and zero corrupted database transactions during container termination (`SIGTERM` / `SIGINT`):
* **Stage 1 (Immediate Traffic Shedding)**: Trips an internal atomic unreadiness switch (`isShuttingDown.Store(true)`), causing the `/ready` probe to instantly return `HTTP 503 Service Unavailable`. Ingress balancers immediately stop routing new traffic to the instance.
* **Stage 2 (Coordinated Connection Drainage)**: The HTTP server drains existing in-flight requests with a 10-second grace period, while background asynchronous workers complete active batches before releasing database connections.

---

## 2. Why We Need It
During automated rolling deployments or Kubernetes pod auto-scaling:
1. **Preventing `502 Bad Gateway` Errors**: If a container terminates immediately upon receiving `SIGTERM`, clients with active in-flight requests receive broken TCP connections or 502 errors.
2. **Preventing Severed Kafka Outbox Batches**: If `OutboxPublisher` is killed midway through publishing a batch to Kafka, messages may be published without being marked as acknowledged in PostgreSQL, causing redundant retries upon container reboot.
3. **Graceful Downstream RPC Completion**: In-flight gRPC calls to Auth or Wallet must be given time to complete and commit their local state.

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **Server Entrypoint** | [`services/admin/cmd/server/main.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/server/main.go#L187-L210) | Captures OS signals, triggers unreadiness, drains HTTP and workers |
| **Health Handler** | [`services/admin/internal/handler/health_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/health_handler.go#L48-L52) | `SetShuttingDown` sets atomic boolean |
| **Readiness Check** | [`services/admin/internal/handler/health_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/health_handler.go#L85-L88) | Checks `isShuttingDown.Load()` and returns `HTTP 503` |

---

## 4. How We Achieve This Feature

In [`services/admin/cmd/server/main.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/server/main.go):
```go
// Wait for termination signal
<-rootCtx.Done()
logger.Info("Shutdown signal received, initiating graceful drainage...")

// 1. Stage 1: Tell load balancers we are shutting down
healthHdr.SetShuttingDown() // /ready now returns 503

// 2. Stage 2: Drain in-flight HTTP connections with 10s budget
shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

if err := srv.Shutdown(shutdownCtx); err != nil {
    logger.Error("HTTP server forced to shutdown", zap.Error(err))
}

// 3. Stage 3: Background workers (Outbox, Saga, Health, Recon) 
// detect rootCtx.Done(), complete current batch, and terminate.
```

---

## 5. Execution Flow

```
                  KUBERNETES / OS SENDS SIGTERM OR SIGINT
                                     │
                                     ▼
                   ┌───────────────────────────────────┐
                   │  1. Signal Captured by Admin Main │
                   └─────────────────┬─────────────────┘
                                     │
                                     ▼
                   ┌───────────────────────────────────┐
                   │  2. healthHdr.SetShuttingDown()   │
                   │     isShuttingDown.Store(true)    │
                   └─────────────────┬─────────────────┘
                                     │
                                     ▼
                   ┌───────────────────────────────────┐
                   │  3. GET /ready returns HTTP 503   │
                   │     Load Balancer steers traffic  │
                   │     to other healthy instances    │
                   └─────────────────┬─────────────────┘
                                     │
                                     ▼
                   ┌───────────────────────────────────┐
                   │  4. In-Flight HTTP Requests Drain │
                   │     10-second timeout budget      │
                   └─────────────────┬─────────────────┘
                                     │
                                     ▼
                   ┌───────────────────────────────────┐
                   │  5. Background Workers Terminate  │
                   │     - Outbox finishes Kafka batch │
                   │     - Sagas finish active retry   │
                   │     - HealthWorker releases lock  │
                   └─────────────────┬─────────────────┘
                                     │
                                     ▼
                   ┌───────────────────────────────────┐
                   │  6. Database Pools Closed & Exit  │
                   │     Zero dropped requests!        │
                   └───────────────────────────────────┘
```
