# package `inventory`

## Purpose

Manages the **MM-001 balance state** and provides effective available inventory calculations used by the reconcile cycle to decide how many bid/ask levels to maintain.

## Problem It Solves

The LE places orders against MM-001's wallet balance but bypasses the Wallet Service's `ReserveFunds` mechanism — resting MM orders do NOT appear as `reserved_balance` in the wallet. Without manual accounting, the LE would over-quote: it could place 12 bids each requiring $8,000 USDT against a $10,000 balance, because the wallet API would report $10,000 available for every call.

Additionally, trade fills happen asynchronously from Kafka. If the LE only trusts the wallet API balance (refreshed every 15s), it would temporarily over-quote after each fill until the next wallet refresh.

## How It Solves It

`Manager` maintains a **projected balance** that starts from the authoritative wallet snapshot and is immediately adjusted on every fill. The reconciler subtracts the committed capital of all active orders (`CommittedBase` / `CommittedQuote` from the tracker) before deciding if more orders are needed.

```
Authoritative Wallet Balance  (refreshed every 15s from Wallet Service)
         │
         ▼
  projected_balances (in-memory, updated per-fill)
         │
         ├── EffectiveAvailableBase = projected[base] - tracker.CommittedBase(market)
         └── EffectiveAvailableQuote = projected[USDT] - Σ tracker.CommittedQuote(all markets)
                   │
                   ▼
             ComputeSkew() → bidCount, askCount
```

All methods must be called from the engine's single event loop goroutine — no locking required.

---

## Flow: Fill Applied to Inventory

```
Kafka trade event (MM SELL fill on BTC-USDT)
         │
         ▼
  ApplyTrade(event)
         │
         ├── projected_balances["BTC"] -= qty
         └── projected_balances["USDT"] += qty × price
         │
         ▼
  Next reconcile cycle:
  EffectiveAvailableBase("BTC-USDT")
         = projected["BTC"] - tracker.CommittedBase("BTC-USDT")
         │
         ▼
  ComputeSkew() → maybe askCount reduced if base is LOW
```

---

## Files

### [`manager.go`](./manager.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `Manager` | `struct` | Holds `projectedBalances` map, `lastRefresh` time, and a reference to the order tracker. |
| `NewManager(tracker, logger)` | `func` | Creates an empty Manager. |
| `RefreshFromWallet(balances)` | `func` | Replaces all projected balances with the authoritative Wallet Service snapshot. Resets `lastRefresh`. Called every 15 seconds. |
| `ApplyTrade(event)` | `func` | Immediately adjusts projected balances on a fill. `SELL` fill: base decreases, USDT increases. `BUY` fill: USDT decreases, base increases. Logs a warning if a deduction would produce a negative balance (indicates a data inconsistency). |
| `EffectiveAvailableBase(marketID)` | `func` | Returns `projected[base] - tracker.CommittedBase(market)`. Clamped to zero. Used by `ComputeSkew` to determine ask-side level count. |
| `EffectiveAvailableQuote(markets)` | `func` | Returns `projected[USDT] - Σ tracker.CommittedQuote(all markets)`. Shared USDT pool is deducted across all markets simultaneously. Used by `ComputeSkew` for bid-side level count. |
| `LastRefresh()` | `func` | Returns when balances were last fetched from the wallet. Used by `publishSnapshot()` for the health status response. |
| `IsStale(maxStaleness)` | `func` | Returns `true` if `lastRefresh` is zero or older than `maxStaleness`. Triggers DEGRADED state and skips new order creation. |
| `WalletBalanceFor(asset)` | `func` | Returns the current projected balance for an asset. Used for diagnostics. |
| `ValidateMMAccount(ctx, balances)` | `func` | Confirms MM-001 has BTC, ETH, SOL, and USDT balances. Called at startup — returns an actionable error if a migration hasn't been applied. |
| `baseAsset(marketID)` | `func` (internal) | Extracts the base asset from a market ID string (e.g., `"BTC-USDT"` → `"BTC"`). |
| `maxZero(d)` | `func` (internal) | Clamps a decimal to zero if negative. Prevents negative effective balances from propagating as valid inventory. |

---

### [`skew.go`](./skew.go)

Computes how many bid/ask levels to maintain based on effective inventory.

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `InventoryTier` | `type int` | Three tiers: `TierNormal`, `TierLow`, `TierCritical` — based on thresholds from `MarketConfig`. |
| `Skew` | `struct` | Holds `BidCount`, `AskCount`, `BaseTier`, `QuoteTier` for one reconcile cycle. |
| `ComputeSkew(mc, effectiveBase, effectiveQuote, logger)` | `func` | Evaluates base against `CriticalBase`/`MinBase` thresholds → sets `AskCount`. Evaluates USDT against `CriticalQuote`/`MinQuote` thresholds → sets `BidCount`. Logs a warning when any tier is non-normal. |

#### Skew Decision Table

```
Effective Base    │  AskCount
──────────────────┼──────────────────────────
> MinBase         │  LevelCount (12) — normal
≤ MinBase         │  LevelCount/2 (6) — low
≤ CriticalBase    │  0 — critical: no asks

Effective Quote   │  BidCount
──────────────────┼──────────────────────────
> MinQuote        │  LevelCount (12) — normal
≤ MinQuote        │  LevelCount/2 (6) — low
≤ CriticalQuote   │  0 — critical: no bids
```
