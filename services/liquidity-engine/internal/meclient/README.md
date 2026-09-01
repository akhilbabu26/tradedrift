# package `meclient`

## Purpose

Provides an **HTTP client** for probing the Matching Engine's `/status` endpoint. Used by the engine to determine whether the ME is live and healthy before placing new orders or promoting OS_REGISTERED orders to RESTING.

## Problem It Solves

The LE must not place new orders when the ME is down or still recovering. Without a direct ME health probe, the LE has no way to distinguish between "ME is healthy but order hasn't appeared yet" and "ME is down and Kafka commands are being queued with no consumer". Placing orders during ME downtime wastes OS slots and corrupts inventory accounting.

## How It Solves It

`CheckAllMarkets()` queries ME's `/status` endpoint once and returns a map of `marketID → bool` (live/not-live). The engine calls this once per `PendingTimeout/2` tick and uses the consecutive-failure count to decide when to pause a market. A single failed probe is not enough to pause — the engine requires `MELivenessThreshold` (default: 3) consecutive failures.

---

## Flow

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

## Files

### [`client.go`](./client.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `Client` | `struct` | Holds the base URL and an `http.Client` with a 2-second timeout. |
| `StatusResponse` | `struct` | ME `/status` JSON response: `ready bool`, `markets []string`. |
| `New(baseURL, logger)` | `func` | Creates a new client. Strips trailing slashes. Defaults to `http://localhost:8082` if empty. |
| `CheckAllMarkets(ctx)` | `func` | Calls `/status` once. Returns `map[marketID]bool`. If `ready=false`, returns an empty map (all markets unhealthy). Used for the per-market liveness evaluation in `handlePendingCheck`. |
| `CheckMarketHealth(ctx, marketID)` | `func` | Convenience wrapper around `CheckAllMarkets`. Returns `true` only if the specific market is in the live markets list. |

---

## Important Notes

- `trades.executed` is **NOT** used for ME liveness detection. Only the direct HTTP probe counts.
- A single failed probe does not pause a market — only `MELivenessThreshold` consecutive failures do.
- The engine makes exactly **one** ME probe per pending-check tick, regardless of how many markets are configured. This avoids N × 2s blocking calls per tick.
