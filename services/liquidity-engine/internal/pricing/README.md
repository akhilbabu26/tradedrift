# package `pricing`

## Purpose

Generates the **desired MM ladder** — the complete set of limit orders the Liquidity Engine wants to maintain in the order book at all times.

## Problem It Solves

The LE needs a deterministic, repeatable description of exactly what the order book should look like at any point in time. Without this:

- The reconciler would have no target to diff against.
- Reference price changes would require manual order updates.
- Tick/lot rounding violations would cause the ME to silently reject all orders.

## How It Solves It

`GenerateLadder()` produces a symmetric ladder around a reference price using a geometric spread formula. Every price is tick-rounded and every quantity is lot-rounded to comply with ME validation rules. Level IDs are stable and deterministic — `MM-BTC-USDT-BID-01` always refers to the closest bid, regardless of what price it's currently at.

---

## Ladder Layout

```
             BID side (below ref)          ASK side (above ref)
             ─────────────────────         ─────────────────────
  BID-12  ...  BID-02  BID-01 | ref | ASK-01  ASK-02  ...  ASK-12

  Spread formula:
    Bid_i = referencePrice / (1 + spreadBps × i / 10000)
    Ask_i = referencePrice × (1 + spreadBps × i / 10000)

  Inner spread (BID-01 to ASK-01) ≈ 2 × spreadBps basis points around reference.
  Default spreadBps = 4 → inner spread ≈ 8 bps.
```

---

## Flow: From Config to Ladder to Diff

```
config.MarketConfig
  (referencePrice, spreadBps, levelCount, tickSize, lotSize)
         │
         ▼
  GenerateLadder(mc, bidCount=12, askCount=12)
         │
         ├── BID levels: for i=1..12:
         │     price = roundToTick(ref / (1 + bps×i/10000))
         │     qty   = roundToLot(uniform)
         │     level = PriceLevel{"MM-BTC-USDT-BID-01", "BUY", ...}
         │
         └── ASK levels: for i=1..12:
               price = roundToTick(ref × (1 + bps×i/10000))
               qty   = roundToLot(uniform)
               level = PriceLevel{"MM-BTC-USDT-ASK-01", "SELL", ...}
         │
         ▼
  []PriceLevel (24 entries)
         │
         ▼
  order.Diff(desired, tracker, marketID, cfg)
         │
         ▼
  []DiffEntry → CREATE / CANCEL / CORRECT commands
```

---

## Files

### [`ladder.go`](./ladder.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `PriceLevel` | `struct` | One desired MM order: `LevelID`, `MarketID`, `Side`, `Price` (tick-rounded), `Quantity` (lot-rounded). |
| `GenerateLadder(mc, bidCount, askCount)` | `func` | Generates the full desired ladder. `bidCount`/`askCount` come from `ComputeSkew()` — reduced when inventory is low. Returns `[]PriceLevel` ordered BID-01→BID-N, then ASK-01→ASK-N. |
| `bpsMultiplier(bps)` | `func` (internal) | Returns `(1 + bps/10000)` as a decimal. Computes the price multiplier for level `i`. |
| `roundToTick(price, tickSize)` | `func` (internal) | Floors price to the nearest tick. Uses truncation (not rounding) so bid prices never exceed the intended level — a rounded-up bid could cross the reference price and be rejected by ME. |
| `roundToLot(qty, lotSize)` | `func` (internal) | Floors quantity to the nearest lot. Avoids over-quoting by rounding down. |
| `levelQuantity(mc, side, levelIndex)` | `func` (internal) | Returns the per-level order size for a market. V1 is uniform across levels. `side` and `levelIndex` are reserved for V2 tapered/skewed sizing. |

---

## Market Quantities (V1)

| Market | Quantity per level |
|:---|:---|
| BTC-USDT | 0.85000 BTC |
| ETH-USDT | 1.5000 ETH |
| SOL-USDT | 20.00 SOL |

---

## Level ID Convention

```
MM - {MARKET_BASE} - {MARKET_QUOTE} - {SIDE} - {NN}

Examples:
  MM-BTC-USDT-BID-01  →  closest bid to reference price
  MM-BTC-USDT-ASK-12  →  furthest ask from reference price
  MM-ETH-USDT-BID-06  →  6th bid level for ETH
```
