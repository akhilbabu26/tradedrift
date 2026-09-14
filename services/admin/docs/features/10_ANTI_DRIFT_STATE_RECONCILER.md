# Feature 10: Anti-Drift State Reconciler

## 1. What This Feature Does
The **Anti-Drift State Reconciler** is a continuous background self-healing engine:
* **Source of Truth Convergence**: Continuously compares the authoritative operational snapshot in PostgreSQL against the volatile hot-path cache in Redis.
* **Bi-Directional Healing**: Automatically adds missing halt keys to Redis and purges obsolete/stale keys that have expired or been resumed.
* **Cold-Start Synchronization**: Runs during service boot (`ReconcileOnce`) before HTTP traffic is admitted.

---

## 2. Why We Need It
In a distributed architecture where writes span relational databases and in-memory caches:
1. **Network Split-Brain / Missed Writes**: If an admin halts a market and PostgreSQL commits, but a network hiccup interrupts the Redis write after retries are exhausted, the systems drift. PostgreSQL says "HALTED" while Redis says "OPEN".
2. **Redis Memory Eviction (OOM)**: If Redis runs low on RAM and purges keys under an `allkeys-lru` policy, market halt keys could vanish without warning.
3. **Manual Human Errors**: If an operator accidentally runs `FLUSHDB` on Redis, the reconciler automatically detects the wiped state and repopulates all enforcement keys within 15 seconds.

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **Reconciler Worker** | [`services/admin/internal/service/reconciler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/reconciler.go#L35-L180) | 15s ticker loop, `reconcileMarkets`, `ReconcileOnce` |
| **Authoritative Query** | [`services/admin/internal/repository/postgres/operations_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/operations_repo.go#L164-L195) | `GetLatestMarketStates` evaluates distinct latest market operations |
| **Startup Wiring** | [`services/admin/cmd/server/main.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/cmd/server/main.go#L104-L111) | Calls `ReconcileOnce` during boot before opening HTTP listener |
| **Metrics** | [`services/admin/internal/metrics/metrics.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/metrics/metrics.go#L137-L145) | `RecordReconcileDuration`, `RecordReconcileDriftDetected` |

---

## 4. How We Achieve This Feature

1. **Authoritative SQL Snapshot**:
   ```sql
   SELECT DISTINCT ON (target_id) target_id, operation_type, status 
   FROM admin_operations
   WHERE operation_type IN ('HALT_MARKET', 'RESUME_MARKET')
     AND status = 'COMPLETED'
   ORDER BY target_id, created_at DESC;
   ```
   If the latest operation for `BTC-USDT` is `HALT_MARKET`, PostgreSQL considers it halted.
2. **Non-Blocking Redis Key Scan**:
   Uses Redis `SCAN` cursor loop (never `KEYS *`, which blocks single-threaded Redis):
   ```go
   iter := r.redisCli.Scan(ctx, 0, "market:halted:*", 0).Iterator()
   ```
3. **Bi-Directional Differential Engine**:
   - **Case 1 (Missing in Redis)**: Market is `HALTED` in PostgreSQL but absent in Redis &rarr; `SET market:halted:{id} = "1"`.
   - **Case 2 (Stale in Redis)**: Key exists in Redis but PostgreSQL says `RESUMED` or no halt record exists &rarr; `DEL market:halted:{id}`.
4. **Sentinel Assertion**:
   Sets `market:enforcement:ready = "1"` to guarantee order placement integrity.

---

## 5. Execution Flow

```
                 TICK EVENT (Every 15 Seconds)
                             │
                             ▼
  ┌──────────────────────────────────────────────────────────┐
  │ 1. Fetch Authoritative Snapshot from PostgreSQL          │
  │    GetLatestMarketStates() queries admin_operations      │
  └───────────────────────────┬──────────────────────────────┘
                              │
                              ▼
  ┌──────────────────────────────────────────────────────────┐
  │ 2. Scan Current Keys in Redis Cache                      │
  │    Non-blocking SCAN cursor: 0, MATCH market:halted:*    │
  └───────────────────────────┬──────────────────────────────┘
                              │
                              ▼
  ┌──────────────────────────────────────────────────────────┐
  │ 3. Bi-directional Convergence Engine                     │
  │    ├── Missing in Redis?  ──► SET market:halted:ID = "1" │
  │    └── Stale in Redis?    ──► DEL stale key from Redis   │
  └───────────────────────────┬──────────────────────────────┘
                              │
                              ▼
  ┌──────────────────────────────────────────────────────────┐
  │ 4. Assert Market Enforcement Sentinel                    │
  │    SET market:enforcement:ready = "1"                    │
  │    (Guarantees orders cannot pass until state verified)  │
  └──────────────────────────────────────────────────────────┘
```
