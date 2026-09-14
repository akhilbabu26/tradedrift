# Feature 08: Fail-Closed Architecture

## 1. What This Feature Does
**Fail-Closed Architecture** establishes a non-negotiable safety invariant across TradeDrift:
* **Default Deny on Degraded Enforcement**: If Redis becomes unreachable, network-partitioned, or returns an unexpected error, downstream enforcement layers (Order Service and Gateway) **refuse to accept operations**.
* **Zero Assumption of Safety**: If a service cannot prove that a market is active or that a user is unsuspended, it rejects the request with `HTTP 503 Service Unavailable` or `codes.FailedPrecondition` rather than assuming safety.

---

## 2. Why We Need It
When designing mission-critical distributed systems, engineers must choose between two failure modes:
1. **Fail-Open (Dangerous)**:
   - If Redis is down, assume the market is active and the user is permitted.
   - *Consequence in Financial Systems*: A malicious actor could DDoS Redis, causing all halted markets and suspended accounts to suddenly open up, executing malicious trades and withdrawing illicit funds.
2. **Fail-Closed (Secure)**:
   - If Redis is down, reject orders and block sensitive access until the enforcement cache is verified healthy.
   - *Result*: Protects platform solvency and user balances during infrastructure degradation.

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **Order MarketGuard** | [`services/order/internal/service/guard.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/guard.go#L30-L46) | Returns `ErrMarketStateUnavailable` or `ErrMarketEnforcementNotReady` on Redis errors |
| **Order Service Ingress**| [`services/order/internal/service/service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/service.go#L96-L105) | Maps guard errors to `HTTP 503` / `codes.FailedPrecondition` |
| **Gateway Auth Guard** | [`services/gateway/internal/middleware/auth.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/middleware/auth.go) | Rejects request with `HTTP 503` if Redis connection fails during suspension check |

---

## 4. How We Achieve This Feature

In [`services/order/internal/service/guard.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/guard.go):
```go
func (g *RedisMarketGuard) IsHalted(ctx context.Context, marketID string) (bool, error) {
    // 1. Invariant: Check if Admin has declared Redis enforcement ready
    readyVal, err := g.client.Get(ctx, "market:enforcement:ready").Result()
    if err != nil {
        // FAIL-CLOSED: Cannot confirm enforcement engine readiness!
        return true, ErrMarketEnforcementNotReady
    }
    if readyVal != "1" {
        return true, ErrMarketEnforcementNotReady
    }

    // 2. Check if specific market is halted
    val, err := g.client.Get(ctx, "market:halted:"+marketID).Result()
    if err == redis.Nil {
        // Safe: Key does not exist, market is genuinely active
        return false, nil
    }
    if err != nil {
        // FAIL-CLOSED: Redis network error or timeout!
        return true, ErrMarketStateUnavailable
    }

    // Key exists and equals "1"
    return val == "1", nil
}
```

---

## 5. Execution Flow

```
                                 USER ATTEMPTS TO PLACE ORDER
                                              │
                                              ▼
                                 ┌─────────────────────────┐
                                 │      ORDER SERVICE      │
                                 └────────────┬────────────┘
                                              │
                                              │ 1. IsHalted(marketID)
                                              ▼
                                 ┌─────────────────────────┐
                                 │       MARKETGUARD       │
                                 └────────────┬────────────┘
                                              │
                                              │ 2. Query Redis
                                              ▼
                                  Is Redis reachable & ready?
                                              │
                               ┌──────────────┴──────────────┐
                               ▼                             ▼
                              NO                            YES
                ┌───────────────────────────┐ ┌───────────────────────────┐
                │ FAIL-CLOSED TRIGGERED:    │ │ Check market:halted key   │
                │ Return ErrStateUnavail    │ └─────────────┬─────────────┘
                └──────────────┬────────────┘               │
                               │               ┌────────────┴────────────┐
                               ▼               ▼                         ▼
                ┌───────────────────────────┐ Key Missing ("nil")       Key == "1"
                │ Reject Order Immediately  │          │                         │
                │ HTTP 503 Service Unavail  │          ▼                         ▼
                │ (Zero balance reservation)│ ┌─────────────────┐ ┌─────────────────────┐
                └───────────────────────────┘ │ ORDER ACCEPTED  │ │ HTTP 409 Conflict   │
                                              │ Proceeds to run │ │ "Market is halted"  │
                                              └─────────────────┘ └─────────────────────┘
```
