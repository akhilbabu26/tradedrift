# Server Entrypoint (`cmd/server`)

The `cmd/server` package contains the entrypoint (`main.go`) for the Controlled Taker Service binary.

---

## 1. Architectural Responsibility

`main.go` coordinates:
1. Production logger initialization (`go.uber.org/zap`).
2. Environment configuration loading and validation (`platform/config` and `internal/config`).
3. External dependency connection establishment (Redis depth reader and Order Service gRPC client).
4. HTTP health (`:8080`) and Prometheus metrics (`:9090`) servers.
5. Launching the taker engine orchestrator with graceful shutdown handling.

---

## 2. Startup Lifecycle

```
OS Execution
     │
     ▼
 1. platformconfig.LoadEnv() ───────── Auto-loads .env file if present
     │
     ▼
 2. Initialize Zap Production Logger
     │
     ▼
 3. config.Load() ──────────────────── Reads environment & validates constraints
     │
     ▼
 4. redisdepth.NewReader() ─────────── Connects to Redis and pings instance
     │
     ▼
 5. orderservice.NewClient() ───────── Dials Order Service gRPC (blocking handshake)
     │
     ▼
 6. health.NewServer() & metrics.NewServer() ── Starts :8080 and :9090 listeners
     │
     ▼
 7. engine.New() & engine.Start(ctx) ─ Spawns staggered per-market workers
     │
     ▼
 Block on SIGINT / SIGTERM
```

---

## 3. Graceful Shutdown & Dependency Lifetime Invariant

> **Critical Lifetime Invariant:**  
> External clients (`ordersClient` and `redisReader`) must remain open while in-flight order cleanups resolve.

If an order was submitted to Order Service immediately before `SIGTERM` arrived, CTS must verify and cancel any unfilled resting residual before the process terminates.

```
                      SIGINT / SIGTERM Received
                                 │
                                 ▼
                   rootCtx cancellation signaled
                                 │
                 ┌───────────────┴───────────────┐
                 ▼                               ▼
    Worker stops new cycles           In-flight order enters
                                    detached cleanup (≤ 2.5s)
                                                 │
                                                 ▼
                                     CancelOrder residual sent
                                                 │
                                                 ▼
                                      Terminal state confirmed
                                                 │
                                                 ▼
                                     worker goroutine exits
                                                 │
                                                 ▼
                                         engine.wg.Wait()
                                                 │
                                                 ▼
                                     Health & Metrics Stop()
                                                 │
                                                 ▼
                                      ordersClient.Close()
                                                 │
                                                 ▼
                                      redisReader.Close()
                                                 │
                                                 ▼
                                           Process Exit (0)
```

By placing `defer ordersClient.Close()` and `defer redisReader.Close()` before `engine.Start(ctx)`, Go's LIFO defer unwinding guarantees that gRPC and Redis connections are closed **only after** all workers have completely returned from their detached cleanup loops.
