# Feature 09: Readiness Sentinel Protocol

## 1. What This Feature Does
The **Readiness Sentinel Protocol** solves the cold-start vulnerability of in-memory caching:
* **The Sentinel Key**: An explicit Redis key named `market:enforcement:ready`.
* **The Invariant**: Order ingress engines treat the absence of this key as an emergency condition, immediately rejecting 100% of order submissions platform-wide until Admin sets it to `"1"`.
* **Controlled Affirmation**: Only after the Admin Service's `StateReconciler` has fully cross-checked PostgreSQL and written all active halt keys into Redis does it write `market:enforcement:ready = "1"`.

---

## 2. Why We Need It
Consider what happens during an infrastructure crash without a sentinel:
1. **The Cold-Start Memory Wipe Hazard**:
   - Redis crashes, restarts, or undergoes container failover. Its memory is completely blank.
   - The Order Service calls `GET market:halted:BTC-USDT`.
   - Redis returns `redis.Nil` (key not found).
   - The Order Service erroneously concludes: *"The key is absent, so the market is active!"*
   - **Disaster**: Orders flood into a market that was halted due to an active security exploit!
2. **Preventing Race Windows During Boot**:
   - When services start concurrently, Order Service might start accepting orders before Admin has even connected to PostgreSQL to reconstruct halt states.
   - The sentinel guarantees that Order Service **fails closed** until Admin gives the cryptographic "all clear".

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **State Reconciler** | [`services/admin/internal/service/reconciler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/reconciler.go#L160-L170) | Sets `market:enforcement:ready = "1"` after syncing halt states |
| **Order MarketGuard** | [`services/order/internal/service/guard.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/guard.go#L37-L46) | Invariant check: fails closed if sentinel != "1" |
| **Server Startup** | [`services/admin/cmd/server/main.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/server/main.go#L104-L111) | Calls `ReconcileOnce` during boot before opening HTTP server |
| **E2E Suite** | [`services/admin/test/e2e/test_control_plane.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/test/e2e/test_control_plane.go#L340-L365) | Explicitly flushes Redis to verify Order Service fails closed |

---

## 4. How We Achieve This Feature

1. **Order Guard Check**:
   ```go
   readyVal, err := g.client.Get(ctx, "market:enforcement:ready").Result()
   if err != nil || readyVal != "1" {
       return true, ErrMarketEnforcementNotReady
   }
   ```
2. **Reconciler Boot Sequence**:
   During service initialization in `cmd/server/main.go`:
   - Admin connects to PostgreSQL and queries `GetLatestMarketStates()`.
   - For every active halted market, writes `market:halted:{id} = "1"` to Redis.
   - Evicts any stale keys not in the database snapshot.
   - Only upon complete sync:
     ```go
     err = r.redisCli.Set(ctx, "market:enforcement:ready", "1", 0).Err()
     ```
3. **Automatic Protection on Redis Flush/Reboot**:
   If Redis restarts, `market:enforcement:ready` is evicted. The Order Service instantly stops all order ingress until the next 15-second Reconciler cycle restores the keys and affirms the sentinel.

---

## 5. Execution Flow

```
                REDIS CRASHES / RESTARTS (ALL KEYS ERASED)
                                     │
                                     ▼
                   User sends order: POST /api/v1/orders
                                     │
                                     ▼
                       ┌───────────────────────────┐
                       │       ORDER SERVICE       │
                       │   MarketGuard.IsHalted()  │
                       └─────────────┬─────────────┘
                                     │
                                     │ 1. GET market:enforcement:ready
                                     ▼
                               ┌───────────┐
                               │   REDIS   │
                               └─────┬─────┘
                                     │
                                     │ Returns (nil / key missing)
                                     ▼
                      ┌─────────────────────────────┐
                      │    SENTINEL MISSING CHECK   │
                      │  ErrMarketEnforcementNotReady│
                      └──────────────┬──────────────┘
                                     │
                                     ▼
                      ┌─────────────────────────────┐
                      │ REJECT: HTTP 503 / 422      │
                      │ Platform fail-closed!       │
                      └─────────────────────────────┘
                                     │
                    [ADMIN RECONCILER FIRES TICK]
                                     │
                                     ▼
                      ┌─────────────────────────────┐
                      │ 1. Query Postgres Snapshots │
                      │ 2. Restore halted keys      │
                      │ 3. SET ready = "1"          │
                      └──────────────┬──────────────┘
                                     │
                                     ▼
                      ┌─────────────────────────────┐
                      │   SENTINEL RE-ESTABLISHED   │
                      │  Orders resume processing   │
                      └─────────────────────────────┘
```
