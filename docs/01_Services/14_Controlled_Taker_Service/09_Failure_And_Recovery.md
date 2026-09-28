# Controlled Taker Service — Failure Modes & Recovery Procedures

> **Status:** 📐 Designed (V1.1 — Updated with Architectural Feedback)  
> **Service:** Controlled Taker Service (`services/controlled-taker`)  
> **Document:** `09_Failure_And_Recovery.md`  
> **Last Updated:** September 2026  

---

## 1. Failure Scenarios & Self-Healing Behaviors

| Component Failure | Observed Symptom | CTS Response & Recovery |
| :--- | :--- | :--- |
| **Order Service Down** | gRPC `Unavailable` / connection refused | Worker increments error counter; after 5 failures, circuit breaker trips for 60s. Backs off exponentially. |
| **Matching Engine Down** | Redis depth `snapshot_at` grows stale ($>5\text{s}$) | Worker logs depth staleness warning; skips order generation until fresh depth resumes. Zero orders submitted. |
| **Redis Down** | `GET depth:{id}` returns connection timeout | Worker bypasses cycle safely; falls back to 5-second retry. Never sends blind orders without depth. |
| **Thin/Empty Order Book** | `len(depth.Bids) == 0` or `len(depth.Asks) == 0` | Worker detects unseeded book; skips execution to avoid crossing non-existent prices. |
| **Excessive Spread** | Spread $> 1.5\%$ of MidPrice | Book is illiquid or ladder is recalculating; skips cycle until spread compresses. |
| **Insufficient CTS Balance** | Order Service returns `ErrInsufficientFunds` | Worker logs error; shifts direction to opposite side to replenish balance, or enters quiescent sleep. |
| **Network Timeout on Submit** | gRPC context deadline exceeded | Retries with the **same IdempotencyKey**; Order Service `FindByIdempotencyKey` returns existing order safely. |

---

## 2. Cold Restart Protection (Anti-Burst Rule)

A common bug in periodic background generators is firing all scheduled jobs immediately upon service restart, causing an uncoordinated traffic burst.

### CTS Startup Sequence:
1. **Warm-up Grace Period:** On boot, CTS enters a mandatory **15-second warm-up phase**.
2. **Sequential Staggering:**
   - Worker 1 (`BTC-USDT`): Starts after 15s + random jitter (0–5s).
   - Worker 2 (`ETH-USDT`): Starts after 20s + random jitter (0–5s).
   - Worker 3 (`SOL-USDT`): Starts after 25s + random jitter (0–5s).
3. This prevents a synchronized cluster of simultaneous orders across all markets upon container restart.

---

## 3. Verified Idempotency & Crash Resilience

- CTS is strictly **stateless**: it does not rely on a local database or persistent volume.
- Every order carries a unique `IdempotencyKey`:
  $$\text{IdempotencyKey} = \text{"CTS-" + MarketID + "-" + UUIDv7}$$
- **Verified Code Semantics:** In `services/order/internal/service/service.go:74-86`, Order Service queries `repo.FindByIdempotencyKey`. If an order was already inserted prior to a network disconnect, calling `CreateOrder` with identical parameters returns the existing order record cleanly without double-reserving funds or duplicate Kafka publishing.
- Upon restart, CTS begins fresh cycles without any replay backlog.
