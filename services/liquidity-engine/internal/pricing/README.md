# package `pricing`

## Purpose

Generates the **desired MM ladder** — the complete set of limit orders the Liquidity Engine wants to maintain in the order book at all times, structured into dynamic volatility and depth zones.

## Problem It Solves

The LE needs a deterministic, repeatable description of exactly what the order book should look like at any point in time. Without this:

- The reconciler would have no target to diff against.
- Reference price movements would require manual order updates.
- Uniform sizing would concentrate too much capital near the inside spread while leaving deep book liquidity thin.
- Tick/lot rounding violations would cause the ME to silently reject orders.

## How It Solves It

The pricing package provides:
1. **`zoned_ladder.go` (Production)**: Implements dynamic 3-zone pricing (`LOW`, `MID`, `HIGH`) with configurable spread bands, lot size multipliers, inventory skew count preservation (LOW-first allocation), deterministic slot jitter, and post-tick boundary validation.
2. **`ladder.go` (Legacy / Deprecated)**: The legacy uniform ladder generator (`GenerateLadder`), retained for backward compatibility.

Level IDs are stable and deterministic — `MM-BTC-USDT-BID-01` always refers to the closest bid slot, regardless of the order price.

---

## 3-Zone Pricing Layout

```
                  BID Side (below ref)                  │                 ASK Side (above ref)
   ───────────────────────────────────────────────────  │  ───────────────────────────────────────────────────
    HIGH Zone (Deep)     MID Zone       LOW Zone (Tight)│ LOW Zone (Tight)       MID Zone      HIGH Zone (Deep)
   BID-12 ... BID-09  BID-08 ... BID-05  BID-04 ... BID-01│ASK-01 ... ASK-04  ASK-05 ... ASK-08  ASK-09 ... ASK-12
      175 - 400 bps      50 - 150 bps      10 - 40 bps  │   10 - 40 bps       50 - 150 bps      175 - 400 bps
       2.5x lot size     1.5x lot size     1.0x lot size │  1.0x lot size      1.5x lot size     2.5x lot size
```

### Zone Parameters (Default per side)

| Zone | Slots | Spread Range | Lot Multiplier | Purpose |
|:---|:---|:---|:---|:---|
| **LOW** | 4 (levels 01–04) | 10 – 40 bps | 1.0x | Tight inside book liquidity; lowest adverse selection risk |
| **MID** | 4 (levels 05–08) | 50 – 150 bps | 1.5x | Standard depth; absorbs moderate market moves |
| **HIGH** | 4 (levels 09–12) | 175 – 400 bps | 2.5x | Deep buffer liquidity; capital-efficient backstop |

---

## Inventory Skew Policy: LOW-First Allocation

When inventory imbalance reduces quoting levels (e.g., `bidCount = 6` instead of 12 due to quote asset depletion):
- The generator allocates levels **LOW-first**, then **MID-first**, then **HIGH**.
- For `bidCount = 6`: all 4 LOW levels (BID-01..04) and 2 MID levels (BID-05..06) are created.
- This guarantees that tight inside-book liquidity is preserved first, protecting market presence.

---

## Flow: Zoned Ladder Generation to Diff

```
config.MarketConfig + Live RefPrice + RefVersion
                       │
                       ▼
   GenerateZonedDesired(mc, ref, tracker, bidZones, askZones, rng, bidCount, askCount)
                       │
                       ├── 1. LOW-first slot count allocation (respecting bidCount/askCount)
                       │
                       ├── 2. For each active slot:
                       │      • Base price from zone spread range + slot step
                       │      • Controlled jitter using stable RNG (±20% of slot step)
                       │      • Tick rounding via roundToTick(price, tickSize)
                       │      • Quantity calculation: baseQty × Zone.LotMultiplier
                       │      • Lot rounding via roundToLot(qty, lotSize)
                       │      • Sets Zone ("LOW"|"MID"|"HIGH") & RefVersion
                       │
                       ├── 3. ValidateZoneBoundaries():
                       │      Ensures tick-rounded prices never violate zone MinBps/MaxBps
                       │
                       ├── 4. Batch Price Uniqueness:
                       │      Guarantees no two desired levels share the exact same price
                       │
                       ▼
   []PriceLevel (up to 24 entries)
                       │
                       ▼
   order.Diff(desired, tracker, marketID, cfg)
                       │
                       ▼
   []DiffEntry → CREATE / CANCEL / CORRECT commands
```

---

## Files

### [`zoned_ladder.go`](./zoned_ladder.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `GenerateZonedDesired(...)` | `func` | Primary production generator. Produces desired levels across LOW, MID, and HIGH zones with stable RNG jitter, skew allocation, and RefVersion stamping. |
| `ValidateZoneBoundaries(...)` | `func` | Verifies that tick-rounded prices fall strictly within their zone's basis-point bounds. |
| `allocateZoneSlots(zones, totalCount)` | `func` (internal) | Distributes `bidCount` or `askCount` across zones using the LOW-first priority policy. |

### [`ladder.go`](./ladder.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `PriceLevel` | `struct` | Represents one desired MM order: `LevelID`, `MarketID`, `Side`, `Price`, `Quantity`, plus dynamic fields `Zone` and `RefVersion`. |
| `GenerateLadder(mc, bidCount, askCount)` | `func` (Deprecated) | Legacy V1 uniform geometric ladder. Kept for backward compatibility with existing tests. |
| `bpsMultiplier(bps)` | `func` (internal) | Calculates `(1 + bps/10000)` multiplier for pricing calculations. |
| `roundToTick(price, tickSize)` | `func` (internal) | Floors price to nearest tick size. Prevents invalid ticks and crossings. |
| `roundToLot(qty, lotSize)` | `func` (internal) | Floors quantity to nearest lot size to avoid over-quoting. |
| `levelQuantity(mc, side, levelIndex)` | `func` (internal) | Returns the base lot size for a market before zone multipliers. |

---

## Level ID Convention

```
MM - {MARKET_BASE} - {MARKET_QUOTE} - {SIDE} - {NN}

Examples:
  MM-BTC-USDT-BID-01  →  closest bid to reference price (LOW zone)
  MM-BTC-USDT-BID-08  →  outermost MID zone bid
  MM-BTC-USDT-ASK-12  →  furthest ask from reference price (HIGH zone)
```
