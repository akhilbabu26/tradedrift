# TradeDrift Audit — 06. Admin & Governance

> **Status:** ✅ Validated (V1.0)
> **Document:** 06_Admin_Platform_Audit.md
> **Domain:** Platform Control, Circuits, and Emergency Interventions

---

## 1. Scope

This audit reviews governance controls: account suspension lifecycle, wallet freezing actions, matching engine trading halts, and manual reconciliation workflows.

---

## 2. Scenario Validations

### 2.1 User Suspension Lifecycle
* **Workflow:** Admin suspends a user profile.
* **Audit Resolution:**
  - Sets user status to `SUSPENDED` in Postgres (User Service).
  - Auth Service invalidates session states (adds refresh tokens to Redis blacklist).
  - User Service writes a `UserSuspended` event to the outbox.
  - Order Service consumes `UserSuspended` and cancels all resting orders for that user, ensuring Wallet Service releases reserved balances.

### 2.2 Wallet Freeze Checks
* **Workflow:** Admin sets `is_frozen = TRUE` on a specific wallet.
* **Service Actions:**
  - `ReserveFunds` gRPC calls fail with `WALLET_FROZEN` errors.
  - `ReleaseFunds` calls continue to permit fund return during cancel execution.
  - `SettleTrade` calls fail if either counterparty's wallet is frozen, sending the trade event to the Settlement Dead Letter Queue (DLQ).

### 2.3 Emergency Trading halts
* **Workflow:** SRE issues a `HaltMarket` command for a trading symbol.
* **Engine Response:**
  - Sets symbol status to `HALTED` in memory.
  - Rejects incoming `OrderCreated` events with reason `market_halted`.
  - Processes `OrderCancelRequested` events to allow users to exit positions.
  - Publishes `MarketHalted` event. Order Service caches this state to reject placements at the edge.

### 2.4 Manual DLQ Settlement Retry
* **Workflow:** Stuck or frozen settlements are sent to the Settlement DLQ.
* **Audit Resolution:**
  - Settlement Service exposes `/admin/settlement/retry` endpoint.
  - SREs invoke retry manually once the root blocker (e.g. frozen wallet) is resolved.
  - Forces Phase 2 gRPC execution, commits Phase 3, and clears the DLQ item.

---

## 3. Discovered Inconsistencies & Resolutions

* **Missing Admin Workflows:** Platform manuals lacked detailed specs for governance controls and emergency shutdown paths. This was resolved by creating `24_Admin_Workflows.md` mapping out these transaction boundaries.
