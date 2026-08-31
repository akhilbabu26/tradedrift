# `internal/order` — MM Order Tracker & Diff Engine

**Package:** `order`  
**Service:** Liquidity Engine  
**Last Updated:** August 2026

---

## 1. What This Package Does

This package provides:

1. **`Tracker`** — the in-memory working picture of all active MM-001 orders across all markets. It is the LE's fast local view of what orders currently exist.
2. **`Diff()`** — the core reconciliation algorithm. It compares desired ladder levels (from `pricing.GenerateLadder`) against actual tracked orders and produces the **minimum necessary set** of CREATE / CANCEL / CORRECT actions.

> **The Tracker is NOT authoritative.** The Order Service PostgreSQL database is the source of truth. The Tracker is a working cache, populated at startup via `ListMMOrders` and updated continuously by reconcile cycles and trade events.

---

## 2. Order Lifecycle & Status Progression

```
             CREATE
               │
               ▼
      Register in Order Service (Postgres)
               │
               ▼
            PENDING (KafkaPublished=false)
               │
               ├──► Kafka publish fails ──► retry SAME orderID, clientOrderID, generation
               │
               ▼ Kafka publish succeeds (KafkaPublished=true)
         OS_REGISTERED
               │
               ▼ ME confirmation window / liveness check
            RESTING ────────── reconciler issues cancel
               │
               ▼
          CANCELLING ────── CancellingTimeout expires ──► [Check via Order Service]
               │                                              │
               │                                    ┌─────── ┤
               │                                    ▼        │
               │                                 CANCELLED ──┘ → Remove
               │                                 FILLED ──────── Remove + IncFilled
               │                                 OPEN → retry cancel
               │
               └── retries >= CancelRetryLimit
                       │
                       ▼
                    STALE  ← terminal; cleared only by authoritative resync
```

### Status Rules

| Status | Blocks CREATE? | Eligible for CANCEL? | Eligible for CORRECT? |
| :--- | :---: | :---: | :---: |
| `PENDING` | ✅ Yes | ❌ No | ❌ No |
| `OS_REGISTERED` | ✅ Yes | ❌ No | ❌ No |
| `RESTING` | ✅ Yes | ✅ Yes | ✅ Yes |
| `CANCELLING` | ✅ Yes | ❌ No | ❌ No |
| `STALE` | ✅ Yes | ❌ No | ❌ No |

---

## 3. Monotonic Generation Invariant

Generations are strictly monotonic and permanent:
- `LevelID = "MM-BTC-USDT-ASK-01"`
- `Generation = 3`
- `ClientOrderID = "MM-BTC-USDT-ASK-01-G003"`

Generation numbers are committed at `SetPending` time. Even if Kafka publish fails, the generation is **never rolled back**. On engine restarts, generations are parsed from Order Service `idempotency_key` strings and restored into `generations[levelID] = max(generations[levelID], o.Generation)`.

---

## 4. Single-Threaded Event Loop Concurrency

All tracker mutations occur exclusively within the Engine's single-threaded event loop. No internal mutexes are used or required.
