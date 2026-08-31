# `internal/reconciler` — MM Order Reconciliation Engine

**Package:** `reconciler`  
**Service:** Liquidity Engine  
**Last Updated:** August 2026

---

## 1. What This Package Does

This package is the **heart of reliable MM operation**. It takes the diff between the *desired* ladder state and the *actual* tracked state, then registers orders with Order Service and publishes the minimum necessary Kafka commands to close the gap. It also manages the full `PENDING → OS_REGISTERED → RESTING → CANCELLING → CANCELLED/STALE` order lifecycle with timeout-based resolution.

The core invariant:
> **No diff → No Kafka command.**  
> The LE never blindly re-sends orders. It only acts when the actual state diverges from desired.

---

## 2. Files In This Package

| File | Purpose |
| :--- | :--- |
| `reconciler.go` | `Reconciler` struct, `ReconcileMarket`, all lifecycle handlers |
| `reconciler_test.go` | Unit tests for reconcile cycles |
| `README.md` | This documentation file |

---

## 3. Create Ordering & Safe Lifecycle

```
1. CreateMMOrder (Order Service gRPC)
   ↳ Order persisted in PostgreSQL (authoritative UUID assigned)
   ↳ Outbox and wallet fund reservation skipped for MM UUID

2. SetPending(levelID, orderID, clientOrderID, gen, desired)
   ↳ LiveOrder created in tracker with StatusPending, KafkaPublished = false

3. PublishCreate (Kafka orders.commands)
   ↳ If success: tracker.SetKafkaPublished(levelID, true)
   ↳ If failure: logged as warning; tracker retains KafkaPublished = false

4. CheckPendingTimeouts
   ↳ If !KafkaPublished: retry Kafka publish with SAME orderID, clientOrderID, gen
   ↳ If KafkaPublished: query Order Service via GetOrderByClientID
   ↳ When OS confirms OPEN: transition to OS_REGISTERED (notFoundCount reset to 0)

5. CheckOSRegisteredTimeouts
   ↳ After ME confirmation window with healthy ME liveness: transition to RESTING
```

---

## 4. Timeout and Retry Thresholds

| Mechanism | Configuration | Default | Purpose |
| :--- | :--- | :--- | :--- |
| `PENDING` Timeout | `PENDING_TIMEOUT` | 10s | Re-checks OS or retries Kafka publish |
| Not Found Threshold | `notFoundThreshold` | 3 misses | Grace window for DB commit before marking liveness failure |
| `CANCELLING` Timeout | `CANCELLING_TIMEOUT` | 30s | Checks OS for cancel confirmation; retries up to limit |
| Cancel Retry Limit | `CANCEL_RETRY_LIMIT` | 3 | Transitions unresolvable cancel to `STALE` |

---

## 5. Architectural Contracts & Guarantees

1. **Kafka Delivery & Duplicate Suppression:**
   - Trade event consumption follows an **At-Least-Once** delivery model.
   - Kafka message offsets are committed manually only *after* all trade mutations (`inv.ApplyTrade`, tracker updates, dirty market flagging) are complete.
   - Replay duplicate protection is provided via bounded in-memory `TradeID` deduplication (LRU up to 1,000 trades).

2. **`OS_REGISTERED → RESTING` Promotion Semantics (V1):**
   - In V1, promotion from `OS_REGISTERED` to `RESTING` represents a **best-effort ME acceptance inference**:
     $$\text{OS has persisted order} \land \text{ME liveness healthy} \land \text{Elapsed confirmation window} \implies \text{Order assumed RESTING}$$
   - V2 will replace this time-based proxy with a direct `OrderRested` Kafka event emitted by the Matching Engine.

3. **Order Service Authoritative Snapshot Contract:**
   - A successful response from `ListMMOrders()` is treated as an authoritative and complete active order snapshot.
   - Missing orders in `RESTING`, `OS_REGISTERED`, or `STALE` states are cleanly removed from the tracker. Missing `CANCELLING` orders trigger creation of any `QueuedCorrection` with a new generation and new `ClientOrderID`.
   - In the event of an Order Service error, the resync aborts without mutating local tracker state.
