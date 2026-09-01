# package `walletservice`

## Purpose

Provides a **read-only gRPC client** for the Wallet Service. The LE uses it to fetch authoritative MM-001 asset balances, which seed the inventory manager's projected balance map.

## Problem It Solves

The LE needs to know how much BTC, ETH, SOL, and USDT MM-001 actually has in its wallet so it can decide how many bid/ask levels to place and detect critical inventory shortages. Without reading the wallet, the LE would have no ground truth to anchor its balance projections.

## How It Solves It

`GetMMBalances()` fetches all asset balances for `account.WalletUUIDStr` via gRPC and returns them as a `map[asset]decimal.Decimal`. The inventory Manager calls `RefreshFromWallet()` with this map on a configurable interval (default: 15 seconds), resetting projected balances to the authoritative value.

---

## Flow: Balance Refresh

```
walletTicker fires every 15s
         │
         ▼
  engine.handleWalletRefresh(ctx)
         │
         ▼
  walletSvc.GetMMBalances(ctx) [5s timeout]
         │
         ├── gRPC GetBalances(userId=MM-UUID)
         └── for each AssetBalance in response:
               parse available_balance as decimal
               → map[asset]decimal.Decimal
         │
         ▼
  inventory.Manager.RefreshFromWallet(balances)
         ├── projected_balances["BTC"]  = balances["BTC"]
         ├── projected_balances["ETH"]  = balances["ETH"]
         ├── projected_balances["SOL"]  = balances["SOL"]
         ├── projected_balances["USDT"] = balances["USDT"]
         └── lastRefresh = time.Now()
         │
         ▼
  Next reconcile:
    EffectiveAvailableBase  = projected["BTC"] - tracker.CommittedBase("BTC-USDT")
    EffectiveAvailableQuote = projected["USDT"] - Σ CommittedQuote(all markets)
```

---

## Files

### [`client.go`](./client.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `Client` | `struct` | Thin wrapper around the Wallet Service gRPC client. |
| `NewClient(addr, logger)` | `func` | Dials the Wallet Service (insecure). Returns error if dial fails. |
| `Close()` | `func` | Closes the gRPC connection. |
| `GetMMBalances(ctx)` | `func` | Calls `GetBalances(userId=MM-UUID)`. Parses `available_balance` strings as decimals. Skips nil or empty-asset entries. Returns `map[asset]decimal.Decimal`. |
| `IsAvailable(ctx)` | `func` | Lightweight reachability check with a 2-second deadline. Returns `false` on any error. |

---

## Important Notes

- The LE reads **only** `available_balance` — it does not read or write `reserved_balance`.
- MM-001 wallet does not reflect committed capital in `reserved_balance`. The LE accounts for committed capital via `order.Tracker.CommittedBase/CommittedQuote`.
- If `GetMMBalances` fails, existing projected balances remain. If they exceed `MaxBalanceStaleness` (60s), the engine transitions to DEGRADED and pauses new order creation.
