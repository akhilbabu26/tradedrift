# Liquidity Engine — Architecture & Responsibilities

> **Service:** `services/liquidity-engine`
> **Version:** V1.1

---

## 1. Core Objective

The Liquidity Engine provides **continuous two-sided liquidity** for all supported markets:

- `BTC-USDT`
- `ETH-USDT`
- `SOL-USDT`

It operates a **dedicated Market Maker account**:

```
account_id = MM-001
role       = MARKET_MAKER
type       = SYSTEM
status     = ACTIVE
```

The MM has one wallet containing:

| Asset | Initial Balance |
| :--- | :--- |
| `BTC` | `100` |
| `ETH` | `500` |
| `SOL` | `5,000` |
| `USDT` | `10,000,000` |

**The Liquidity Engine DOES NOT:**
- Execute trades itself
- Modify wallet balances directly
- Bypass the Matching Engine
- Directly settle trades
- Self-trade to create fake volume

---

## 2. Final Architecture

```
                         ┌──────────────────────────────┐
                         │      Liquidity Engine        │
                         │                              │
                         │  One MM Account: MM-001      │
                         └──────────────┬───────────────┘
                                        │
                    ┌───────────────────┼───────────────────┐
                    │                   │                   │
                    ▼                   ▼                   ▼
              BTC-USDT Strategy   ETH-USDT Strategy   SOL-USDT Strategy
                    │                   │                   │
                    └───────────────────┼───────────────────┘
                                        │
                                        ▼
                                Order Manager
                                        │
                                        ▼
                              Kafka: orders.commands
                                        │
                                        ▼
                              ┌─────────────────┐
                              │ Matching Engine  │
                              └────────┬────────┘
                                       │
                         ┌─────────────┴─────────────┐
                         │                           │
                    No Match                     Match
                         │                           │
                         ▼                           ▼
                    Resting Order              TradeExecuted
                                                     │
                                    ┌────────────────┼──────────────┐
                                    ▼                ▼              ▼
                               Settlement        Market Service   Events
                                    │                │
                                    ▼                ▼
                               MM Wallet        Candles/Ticker
```

---

## 3. Responsibility Boundaries

This separation is **critical** to system correctness.

### Liquidity Engine — Owns:
- Pricing & reference price tracking
- Ladder generation
- Order placement / cancellation / replacement
- Inventory management
- Risk limits
- Reconciliation
- MM account management

### Matching Engine — Owns:
- Order validation
- Price-time priority
- Matching logic
- Partial fills
- Trade creation
- Self-Trade Prevention (STP)
- Order book state

### Settlement — Owns:
- User balance changes
- MM balance changes
- Asset transfers
- Fee deductions

### Market Service — Owns:
- Trade history
- Candlestick bars (1m, 5m, 15m, 1h, 1d)
- 24h volume, 24h high, 24h low
- Ticker feeds

> **⚠️ Architectural Invariant — No Database. No Wallet Writes.**
>
> The Liquidity Engine has **no database of its own**. It owns no schema, no migrations, and no Postgres connection.
>
> | Dependency | Role | Access Mode |
> | :--- | :--- | :--- |
> | **Kafka** (`orders.commands`) | Publish order commands | Write |
> | **Kafka** (`orders.events`, `trades.executed`) | Consume ME feedback | Read |
> | **Redis** (`depth:{market_id}`) | Read current book depth | Read-only |
> | **Wallet Service gRPC** | Read MM balances for inventory | Read-only |
> | **Config / env** | Market configs, reference prices | Static |
> | **In-memory** (`order.Tracker`) | Live MM order state | Ephemeral |
>
> All balance changes flow exclusively through: **Matching Engine → TradeExecuted → Settlement → Wallet**.
> The Liquidity Engine never touches a balance table. This is a **hard boundary**, not a soft guideline.

---

## 3a. LE & ME Recovery Interaction Problem

> **This is one of the most important interactions to get right.**

### The Problem

The Matching Engine recovers by replaying `orders.commands` events from `lastCheckpoint` to the Kafka High-Water Mark (HWM). The Liquidity Engine is the **dominant contributor** to this topic.

The worst-case event count depends on how the LE is implemented. A poorly written LE that unconditionally re-sends orders on a timer produces thousands of replay events per hour of ME downtime.

**The correct baseline behaviour of a healthy LE:**

```
              LE
               |
         Desired State
               |
               v
         State Diff
               |
      .--------+--------.
      |                 |
   No diff           Diff found
      |                 |
      v                 v
   NOTHING           Commands
                         |
                 .-------+-------.
                 v               v
            OrderCreate     OrderReplace
```

`NO DIFF -> NO KAFKA COMMAND` is the invariant. Blindly re-sending 72 creates and 72 cancels every 5 seconds is a broken implementation.

---

### Four-Layer Mitigation Strategy

#### Layer 1 -- PAUSE on ME Disconnect (No Cancel Guarantee)

When the LE detects ME / `orders.events` consumer has gone silent, it transitions to `PAUSED`:

```
ME goes down
     |
     v
LE detects silence (heartbeat timeout or consumer lag)
     |
     v
LE -> PAUSED
  |-- stop all new order creation
  |-- stop all order replacement
  `-- stop normal reconciliation loop
     |
     v
Wait for ME_LIVE signal
```

> **Critical:** The LE does **NOT** attempt to cancel existing MM orders during this phase.
>
> If ME has already crashed, it is no longer consuming `orders.commands`. Cancel commands sit
> unprocessed in Kafka with no guarantee of being acted upon before ME restarts.
>
> The correct approach: stop producing commands entirely. Trust the post-recovery reconciliation
> pass to clean up book state authoritatively.
>
> Accurate statement of the effect:
> "By pausing command generation during ME recovery, the Liquidity Engine prevents new MM traffic
> from continuously extending the Kafka HWM that ME must replay. ME recovers its authoritative
> order book state via checkpoint replay -- the LE does not interfere with this process."

---

#### Layer 2 -- Stay PAUSED During ModeRecovery

ME publishes a system status event when it transitions `ModeRecovery -> ModeLive`. The LE subscribes to this signal and produces **zero Kafka commands** until `ME_LIVE` is received.

```
ME status:  RECOVERING
     |
     v
LE:  PAUSED -- zero command generation
     |
     v  (ME publishes ME_LIVE on system.events topic)
     |
     v
LE:  -> RECONCILING
```

This is the most important phase for protecting ME fast-boot. The LE adds nothing to the replay window while ME rebuilds from its checkpoint delta.

---

#### Layer 3 -- Reconcile First, Quote Second

On receiving `ME_LIVE`, the LE transitions to `RECONCILING` before returning to `RUNNING`:

```
ME_LIVE received
     |
     v
LE -> RECONCILING

  1. Query actual MM orders from Order Service API
  2. Calculate desired state from strategy + inventory
  3. Diff: desired vs actual

  Example:
    Desired:  12 bids, 12 asks
    Actual:   10 bids, 12 asks

  Action:   Create 2 missing bids only
            NOT: cancel 22, then create 24

     |
     v
LE -> RUNNING
```

The reconciler trusts what ME reports as actual state. It discards stale in-memory assumptions and issues the minimum necessary commands to reach desired state.

---

#### Layer 4 -- Recovery Epoch / Generation

ME increments a **recovery epoch counter** each time it completes recovery and reaches `ModeLive`:

```
Normal operation:   ME epoch = 42
After recovery:     ME epoch = 43
```

When the LE detects an epoch change:

```
LE detects: current epoch (43) != last known epoch (42)
     |
     v
Invalidate all in-memory order state
     |
     v
Treat order.Tracker as stale -- do not trust local cache
     |
     v
Fetch authoritative order state from Order Service API
     |
     v
Full reconciliation from clean slate
```

This is critical because the LE has no database. Its in-memory `order.Tracker` holds assumptions from before the ME crash. The epoch change is the signal to treat all local state as invalid.

---

### LE State Machine

```
ME LIVE
   |
   v
LE RUNNING
(event-driven quoting, no-diff = no command)
   |
   | ME failure detected
   v
LE PAUSED
(stop all command generation -- no creates, no cancels, no replaces)
   |
   | ME publishes ME_RECOVERING (LE stays PAUSED, notes epoch)
   |
   | ME publishes ME_LIVE (new epoch detected)
   v
LE RECONCILING
(invalidate stale tracker, fetch actual state, diff, minimal commands)
   |
   v
LE RUNNING
```

---

### Summary

| Layer | Mechanism | Principle |
| :--- | :--- | :--- |
| **1** | PAUSE on ME disconnect -- stop all command generation | Stop being a producer when ME cannot consume |
| **2** | Stay PAUSED during ModeRecovery until ME_LIVE | Zero LE events added to the replay window |
| **3** | No diff -> no Kafka command | Healthy LE contributes near-zero replay overhead |
| **4** | Recovery epoch: invalidate local state on epoch change | Trust ME's authoritative state, not stale in-memory tracker |

> **The core principle:**
> When ME is down, LE stops being a producer.
> When ME becomes live, LE becomes a reconciler first and a liquidity provider second.
