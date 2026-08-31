# `internal/inventory` — MM-001 Inventory & Balance Management

**Package:** `inventory`  
**Service:** Liquidity Engine  
**Last Updated:** August 2026

---

## 1. What This Package Does

This package tracks the **MM-001 wallet balances** and provides **effective available inventory** calculations used by the engine's skew logic to decide how many bid/ask levels to maintain per market.

It also computes **inventory skew** — when base or quote inventory falls below configured thresholds, the engine reduces the number of active levels to protect remaining inventory.

---

## 2. Files In This Package

| File | Purpose |
| :--- | :--- |
| `manager.go` | `Manager` struct, balance refresh, trade application, effective inventory calculations |
| `skew.go` | `ComputeSkew()`, `Skew` struct, `InventoryTier` tiers |
| `skew_test.go` | Unit tests for skew tier transitions and level count calculations |
| `README.md` | This documentation file |

---

## 3. Core Design

### Why the LE Computes Its Own "Effective Available"

The Wallet Service's `available_balance` does not account for MM orders already **committed** in the order book. If the LE blindly trusts the wallet balance, it would over-quote — placing more orders than it has inventory to back.

```
effective_available_base  = wallet.available_balance[BTC] - committed_base
effective_available_quote = wallet.available_balance[USDT] - committed_quote_across_all_markets

committed_base  = sum of (quantity × 1) for all RESTING/PENDING ask orders
committed_quote = sum of (price × quantity) for all RESTING/PENDING bid orders
```

The committed values are computed by `order.Tracker.CommittedBase()` and `order.Tracker.CommittedQuote()`.

---

## 4. `Manager` — Balance Tracking

```go
type Manager struct {
    authBalances map[string]decimal.Decimal // asset → authoritative wallet balance
    lastRefresh  time.Time                  // when RefreshFromWallet was last called
    tracker      *order.Tracker             // read-only reference for committed calculations
    logger       *zap.Logger
}
```

### Methods

| Method | Description |
| :--- | :--- |
| `RefreshFromWallet(balances map[string]decimal.Decimal)` | Overwrite authoritative balances from a Wallet Service response |
| `ApplyTrade(event kafka.TradeEvent)` | Fast-path update: adjust balances immediately on trade fill |
| `EffectiveAvailableBase(marketID) decimal.Decimal` | `wallet[base] - tracker.CommittedBase(marketID)` |
| `EffectiveAvailableQuote(markets []string) decimal.Decimal` | `wallet[USDT] - sum(CommittedQuote)` across all markets |
| `IsStale(maxStaleness) bool` | True if `lastRefresh` is zero or older than `maxStaleness` |
| `LastRefresh() time.Time` | Returns the timestamp of the last wallet refresh |
| `WalletBalanceFor(asset) (decimal.Decimal, bool)` | Raw authoritative balance for one asset |
| `ValidateMMAccount(ctx, balances) error` | Confirms MM-001 has BTC, ETH, SOL, USDT balances present |

### `ApplyTrade` Logic

```
MM SELL (ask filled):
    base[market]  = max(0, base[market] - trade.Quantity)
    USDT          = USDT + (trade.Quantity × trade.Price)

MM BUY (bid filled):
    USDT          = max(0, USDT - (trade.Quantity × trade.Price))
    base[market]  = base[market] + trade.Quantity
```

This gives a fast local balance view without waiting for the next `WalletRefreshInterval`. The Wallet Service refresh (every 5 minutes) provides the authoritative correction.

---

## 5. `Skew` — Inventory-Aware Level Count

### `InventoryTier`

```go
const (
    TierNormal   InventoryTier = iota // > MinBase / MinQuote
    TierLow                           // <= MinBase / MinQuote but > Critical
    TierCritical                      // <= CriticalBase / CriticalQuote
)
```

### `Skew` Struct

```go
type Skew struct {
    BidCount  int           // number of active bid levels this cycle
    AskCount  int           // number of active ask levels this cycle
    BaseTier  InventoryTier // ASK side inventory tier
    QuoteTier InventoryTier // BID side inventory tier
}
```

### `ComputeSkew(mc, effectiveBase, effectiveQuote, logger) Skew`

```
ASK side (driven by base asset):
    TierNormal   → askCount = LevelCount      (e.g. 12)
    TierLow      → askCount = LevelCount / 2  (e.g.  6)
    TierCritical → askCount = 0               (stop selling)

BID side (driven by USDT quote):
    TierNormal   → bidCount = LevelCount      (e.g. 12)
    TierLow      → bidCount = LevelCount / 2  (e.g.  6)
    TierCritical → bidCount = 0               (stop buying)
```

### Example (BTC-USDT)

| Effective Base | Effective Quote | BaseTier | QuoteTier | Bids | Asks | Engine State |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| 50 BTC | $5,000,000 | Normal | Normal | 12 | 12 | RUNNING |
| 15 BTC | $5,000,000 | Low | Normal | 12 | 6 | DEGRADED |
| 3 BTC | $5,000,000 | Critical | Normal | 12 | 0 | DEGRADED |
| 50 BTC | $500,000 | Normal | Low | 6 | 12 | DEGRADED |
| 3 BTC | $50,000 | Critical | Critical | 0 | 0 | DEGRADED |

---

## 6. Staleness Guard

If `IsStale(MaxBalanceStaleness)` returns `true`:
- `runReconcileAll` skips the cycle entirely — no orders placed on unknown inventory
- `handleWalletRefresh` transitions the engine to `PAUSED`

This prevents the LE from over-quoting if the Wallet Service gRPC endpoint is down and balance data is stale.

---

## 7. Concurrency

All `Manager` methods must be called from the **engine's single event loop goroutine**. There is no internal locking — correctness is enforced by the caller's single-threaded access guarantee.

---

## 8. What This Package Does NOT Do

- Does NOT call the Wallet Service gRPC directly — the `account` package handles gRPC and calls `RefreshFromWallet()`
- Does NOT modify wallet balances — all balance changes go through ME → TradeExecuted → Settlement → Wallet
- Does NOT have a database or persistent state — all state is ephemeral in-memory
