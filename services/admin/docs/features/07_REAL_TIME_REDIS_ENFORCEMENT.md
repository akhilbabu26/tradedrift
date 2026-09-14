# Feature 07: Real-Time Redis Enforcement

## 1. What This Feature Does
**Real-Time Redis Enforcement** acts as the high-speed operational cache for platform circuit breakers:
* **Sub-Millisecond Guarding**: Rather than forcing the critical path of trading (Order Service) or authentication (API Gateway) to query relational databases, Admin maintains hot enforcement keys in Redis.
* **Instant Enforcement Keys**:
  - `market:halted:{marketID} = "1"`: Halts trading immediately for that pair.
  - `user:suspended:{userID} = "1"`: Rejects user API requests at the gateway.
* **Synchronous Retry Pipeline**: When an admin halts a market or suspends a user, Admin synchronously writes to Redis with a 3-attempt exponential backoff retry loop (50ms, 150ms, 300ms).

---

## 2. Why We Need It
In a distributed financial platform processing thousands of operations per second:
1. **Database Bottleneck Prevention**: If every incoming order required querying PostgreSQL to check whether a market is halted, the relational database connection pool would saturate, adding 10–50ms to order latency.
2. **Eliminating Kafka Consumer Lag**: Relying on asynchronous Kafka consumers to update order engine memory creates a vulnerability window: during consumer lag or rebalances, bad orders can execute against a halted market.
3. **Sub-Millisecond Lookups**: Redis in-memory lookups take `<1ms`, allowing the Order Service and API Gateway to enforce security controls on every request with zero perceptible performance degradation.

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **Market Service** | [`services/admin/internal/service/market_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/market_service.go#L121-L135) | Synchronous `SET market:halted:{id} = "1"` & `DEL` with 3 retries |
| **User Service** | [`services/admin/internal/service/user_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/user_service.go#L115-L130) | Synchronous `SET user:suspended:{id} = "1"` & `DEL` with 3 retries |
| **Order MarketGuard** | [`services/order/internal/service/guard.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/guard.go#L48-L58) | Evaluates `GET market:halted:{marketID}` before order reservation |
| **Gateway Auth Guard**| [`services/gateway/internal/middleware/auth.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/middleware/auth.go) | Evaluates `EXISTS user:suspended:{userID}` on every incoming JWT |

---

## 4. How We Achieve This Feature

1. **Synchronous Write Strategy with Retries**:
   When an admin mutation commits to PostgreSQL, Admin executes an immediate write loop:
   ```go
   var err error
   for attempt := 0; attempt < 3; attempt++ {
       err = r.redisCli.Set(ctx, key, "1", 0).Err()
       if err == nil {
           return nil
       }
       time.Sleep(time.Duration(50*(1<<attempt)) * time.Millisecond) // 50ms, 150ms, 300ms
   }
   ```
2. **Order Ingress Evaluation**:
   Before an order reserves funds or reaches the matching engine:
   ```go
   val, err := r.client.Get(ctx, "market:halted:"+marketID).Result()
   if err == nil && val == "1" {
       return ErrMarketHalted // Returns HTTP 409 Conflict
   }
   ```
3. **Gateway Ingress Evaluation**:
   After validating the cryptographic JWT signature:
   ```go
   exists, _ := r.client.Exists(ctx, "user:suspended:"+claims.UserID).Result()
   if exists == 1 {
       http.Error(w, `{"error":"user account is suspended"}`, http.StatusForbidden)
       return
   }
   ```

---

## 5. Execution Flow

```
 ADMIN SERVICE                           REDIS CACHE                        ORDER / GATEWAY
       │                                      │                                    │
       │── 1. SET market:halted:BTC = "1" ───►│                                    │
       │   (Retry 1: 50ms)                    │                                    │
       │   (Retry 2: 150ms)                   │                                    │
       │   (Retry 3: 300ms)                   │                                    │
       │◄─ 2. OK ─────────────────────────────│                                    │
       │                                      │                                    │
       │                                      │   [USER SENDS INCOMING REQUEST]    │
       │                                      │◄── 3. GET market:halted:BTC ───────│
       │                                      │─── 4. Returns "1" (Halted!) ──────►│
       │                                      │                                    │
       │                                      │                                    │── Reject with HTTP 409
       │                                      │                                    │   (Conflict)
```
