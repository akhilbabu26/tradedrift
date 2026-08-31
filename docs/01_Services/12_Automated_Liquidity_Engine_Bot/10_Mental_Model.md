# Liquidity Engine — Principles & Mental Model

> **Service:** `services/liquidity-engine`
> **Version:** V1.1

---

## Self-Trade Prevention (STP)

Enforced **exclusively by the Matching Engine**:

```
If account_id(taker) == account_id(maker):
    SELF TRADE DETECTED
    → REJECT (do not match)
```

The Liquidity Engine's spread invariant (`BestBid < BestAsk`) provides a **first line of defense**,
but the Matching Engine's STP rule is the **authoritative enforcement layer**. Both must be in place.

---

## No Micro-Trader in V1

The original Micro-Trader concept is **removed from core V1**. Periodic micro self-orders would be
self-trades (MM ↔ MM), creating artificial volume and corrupting candle data.

The correct pattern for demo activity:

```
V1 — Real Liquidity:
  Liquidity Engine → real resting orders → real user fills → real trades

V2+ — Simulation Engine (separate service):
  SIM-USER-001
  SIM-USER-002    →  Matching Engine  →  MM-001
  SIM-USER-003

  SIMULATED USER <-> MM  (real match, real settlement)
  !=  MM <-> MM          (self-trade, fake volume)
```

---

## The Final Mental Model

```
                         ┌──────────────┐
                         │   Treasury   │
                         └──────┬───────┘
                                │
                         funds / replenishes
                                │
                                ▼
                         ┌──────────────┐
                         │   MM-001     │
                         │ Account      │
                         │              │
                         │ BTC ETH SOL  │
                         │ USDT         │
                         └──────┬───────┘
                                │
                         controlled by
                                │
                                ▼
                    ┌──────────────────────┐
                    │  Liquidity Engine    │
                    │                      │
                    │ Pricing              │
                    │ Laddering            │
                    │ Order Management     │
                    │ Inventory            │
                    │ Reconciliation       │
                    └──────────┬───────────┘
                               │
                         BUY / SELL limit orders
                         (Kafka -> orders.commands)
                               │
                               ▼
                    ┌──────────────────────┐
                    │   Matching Engine    │
                    │                      │
                    │ Price-Time Priority  │
                    │ STP Enforcement      │
                    │ Matching             │
                    └──────────┬───────────┘
                               │
                         TradeExecuted
                         (Kafka -> trades.executed)
                               │
                               ▼
                    ┌──────────────────────┐
                    │ Settlement / Wallet  │
                    └──────────┬───────────┘
                               │
                    .----------+----------.
                    ▼                     ▼
                  USER                  MM-001
                Wallet                  Wallet
```

---

## The Fundamental Rules

> *The Liquidity Engine provides the counterparty.*
> *The Matching Engine performs the match.*
> *Settlement moves the assets.*

> *When ME is down, LE stops being a producer.*
> *When ME becomes live, LE becomes a reconciler first and a liquidity provider second.*

---

## Design Validation Checklist

- ✅ **Clean SoC:** LE prices and ladders. ME matches. Settlement settles.
- ✅ **No self-trading:** Micro-Trader removed. Simulation Engine is the correct V2 solution.
- ✅ **Inventory awareness:** Skew prevents overselling/overbuying beyond available reserves.
- ✅ **No crash-cancel assumption:** LE does not depend on cancel commands being processed after ME failure.
- ✅ **Recovery epoch:** LE invalidates stale in-memory state on ME epoch change.
- ✅ **Event-driven reconcile:** No diff → no Kafka command. Healthy LE is nearly silent.
- ✅ **Reconciliation-based:** Desired vs actual state is the industry-standard MM approach.
- ✅ **Phased delivery:** 10-phase rollout avoids big-bang complexity.
- ✅ **STP enforced at ME layer:** Application layer spread invariant is a hint; ME STP is the enforcer.
- ⚠️ **V1 Reference Price:** Static configured price is acceptable for V1. V2 priority is an external
  price feed. Ladder re-center must trigger a full cancel + rebuild to avoid a transient crossed book.
