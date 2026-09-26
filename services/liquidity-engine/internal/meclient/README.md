# package `meclient`

## Purpose

Provides an **HTTP client** for probing the Matching Engine's `/status` and `/markets/{market_id}/snapshot` endpoints. Used by the engine and reconciler to verify ME health, retrieve point-in-time order book state, promote orders to RESTING, and detect orphan/missing orders.

## Problem It Solves

The LE must not place new orders when the ME is down or still recovering. Furthermore, relying purely on timeouts to assume an order is resting in the order book is prone to phantom inventory and stale generation desync. The LE requires direct, authoritative visibility into:
1. Whether the ME is live and processing orders (`/status`).
2. Exact MM order resting state, remaining quantities, and sequence progression (`/markets/{market_id}/snapshot`).

## How It Solves It

- `CheckAllMarkets()` queries `/status` once and returns a map of `marketID → bool`. The engine uses consecutive-failure counts against `MELivenessThreshold` (default: 3) to pause or unpause markets.
- `FetchSnapshot()` queries `/markets/{market_id}/snapshot` to obtain an atomic point-in-time snapshot of the ME order book, including `MMOrderSummary` items for all active market maker orders. It verifies sequence monotonicity (`lastObservedSeq`) to detect restarts or out-of-order responses.

---

## Flow: ME Health Probe

```
handlePendingCheck() [every PendingTimeout/2]
         │
         ▼
  meClient.CheckAllMarkets(ctx) [2s timeout]
         │
         ├── GET {ME_HTTP_ADDR}/status
         │
         └── response: {"ready": true, "markets": ["BTC-USDT", "ETH-USDT", "SOL-USDT"]}
               │
               ├── ready=false → all markets unhealthy
               └── ready=true  → map: BTC-USDT=true, ETH-USDT=true, SOL-USDT=true

         │
         ▼
  for each market:
    meLive = (probeErr==nil && marketHealth[market])

    meLive=true  → reset consecutiveMETimeouts, unpause market
    meLive=false → consecutiveMETimeouts++ → if >= threshold → pause market
```

---

## Flow: ME Snapshot Verification

```
reconciler.ReconcileMarket() [on each cycle]
         │
         ▼
  meClient.FetchSnapshot(ctx, marketID) [2s timeout]
         │
         ├── GET {ME_HTTP_ADDR}/markets/{market_id}/snapshot
         │
         ├── validates sequence progression against lastObservedSeq
         │
         └── response: MarketSnapshot { MarketID, State, Sequence, Orders: []MMOrderSummary }
               │
               ├── State != "LIVE" → pause reconciliation cycle
               └── State == "LIVE" → syncWithMESnapshot():
                     • Confirm resting orders and sync RemainingQty
                     • Cancel orphan orders in ME
                     • Hysteresis check for missing orders
```

---

## Files

### [`client.go`](./client.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `Client` | `struct` | Holds the base URL, an `http.Client` with 2-second timeout, and `lastObservedSeq` map for sequence validation. |
| `StatusResponse` | `struct` | ME `/status` JSON response: `ready bool`, `markets []string`. |
| `MMOrderSummary` | `struct` | Summarizes a single resting MM order in ME book (`OrderID`, `ClientOrderID`, `LevelID`, `Generation`, `Side`, `Price`, `RemainingQuantity`). |
| `MarketSnapshot` | `struct` | Atomic point-in-time order book view from ME: `MarketID`, `State`, `Sequence`, `Timestamp`, `OrderCount`, `Orders`. |
| `New(baseURL, logger)` | `func` | Creates a new client. Strips trailing slashes. Defaults to `http://localhost:8082` if empty. |
| `CheckAllMarkets(ctx)` | `func` | Calls `/status` once. Returns `map[marketID]bool`. If `ready=false`, returns an empty map (all markets unhealthy). |
| `CheckMarketHealth(ctx, marketID)` | `func` | Convenience wrapper around `CheckAllMarkets`. Returns `true` only if the specific market is in the live markets list. |
| `FetchSnapshot(ctx, marketID)` | `func` | Calls `/markets/{market_id}/snapshot`. Decodes `MarketSnapshot`, monitors for sequence regression, and returns the snapshot. |

---

## Important Notes

- `trades.executed` is **NOT** used for ME liveness detection. Only the direct HTTP probe counts.
- A single failed probe does not pause a market — only `MELivenessThreshold` consecutive failures do.
- `FetchSnapshot` tracks monotonic sequence numbers (`lastObservedSeq`) per market to log warnings if sequence regresses while in `LIVE` state.
