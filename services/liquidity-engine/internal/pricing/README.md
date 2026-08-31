# `internal/pricing` — MM Ladder Generator

**Package:** `pricing`  
**Service:** Liquidity Engine  
**Last Updated:** August 2026

---

## 1. What This Package Does

This package generates the **desired MM order ladder** — the set of limit orders the Liquidity Engine wants to maintain in the order book at all times. Given a market config and bid/ask level counts (from inventory skew), it produces a deterministic list of `PriceLevel` structs with tick-rounded prices and lot-rounded quantities.

The reconciler calls `GenerateLadder` on every reconcile cycle to compute the desired state. The diff between this desired state and the actual tracker state drives all order create / cancel decisions.

---

## 2. Files In This Package

| File | Purpose |
| :--- | :--- |
| `ladder.go` | `PriceLevel`, `GenerateLadder`, price/quantity rounding helpers |
| `ladder_test.go` | Unit tests for ladder generation, tick/lot rounding, and level ID format |
| `README.md` | This documentation file |

---

## 3. Ladder Structure

```
BID-12 ... BID-01 | referencePrice | ASK-01 ... ASK-12

Symmetric around the reference price (static V1 mid-price).
Each level is separated by SpreadBps basis points from the previous.
```

### Level ID Convention

```
MM-{MarketID}-{SIDE}-{NN:02d}

MM-BTC-USDT-BID-01   ← closest bid to reference price
MM-BTC-USDT-BID-02
...
MM-BTC-USDT-BID-12   ← furthest bid from reference price

MM-BTC-USDT-ASK-01   ← closest ask to reference price
MM-BTC-USDT-ASK-02
...
MM-BTC-USDT-ASK-12   ← furthest ask from reference price
```

Level IDs are **stable logical identities** that never change for the life of a market. They are used as keys in the `order.Tracker` and as the base of `client_order_id` generation.

---

## 4. `PriceLevel` Struct

```go
type PriceLevel struct {
    LevelID  string          // e.g. "MM-BTC-USDT-ASK-01"
    MarketID string
    Side     string          // "BUY" | "SELL"
    Price    decimal.Decimal // tick-rounded (ME rejects non-tick-aligned prices)
    Quantity decimal.Decimal // lot-rounded  (ME rejects non-lot-aligned quantities)
}
```

---

## 5. Price Calculation

### Bid Levels (below reference price)

$$\text{Price}_{\text{BID}, i} = \frac{\text{ReferencePrice}}{1 + \frac{\text{SpreadBps} \times i}{10000}}$$

Each level `i` divides the reference price by a multiplier that grows with level index, placing bids progressively deeper below mid.

### Ask Levels (above reference price)

$$\text{Price}_{\text{ASK}, i} = \text{ReferencePrice} \times \left(1 + \frac{\text{SpreadBps} \times i}{10000}\right)$$

Each level `i` multiplies the reference price by a growing factor, placing asks progressively higher above mid.

### Example (BTC-USDT, SpreadBps=4, ReferencePrice=96450.00)

| Level | Side | Formula | Raw Price | Tick-Rounded |
| :--- | :--- | :--- | :--- | :--- |
| BID-01 | BUY | 96450 / (1 + 0.0004) | 96411.47... | 96411.47 |
| BID-02 | BUY | 96450 / (1 + 0.0008) | 96372.97... | 96372.97 |
| ASK-01 | SELL | 96450 × (1 + 0.0004) | 96488.58 | 96488.58 |
| ASK-02 | SELL | 96450 × (1 + 0.0008) | 96527.16 | 96527.16 |

---

## 6. Quantity (V1 — Uniform Per Market)

V1 uses a fixed quantity at every level. Per-level size variation is reserved for V2.

| Market | Quantity Per Level |
| :--- | :--- |
| `BTC-USDT` | `0.85000 BTC` |
| `ETH-USDT` | `1.5000 ETH` |
| `SOL-USDT` | `20.00 SOL` |

Quantities are floor-rounded to the market's `LotSize` before being set on the `PriceLevel`.

---

## 7. Rounding Rules

### `roundToTick(price, tickSize)`

Uses **floor (truncation)** rounding. This avoids generating a price *above* the intended level, which could cause the order to immediately match against the opposite side or be rejected by the ME.

```
96411.47384... with tickSize=0.01
→ steps = floor(96411.47384 / 0.01) = 9641147
→ price = 9641147 × 0.01 = 96411.47
```

### `roundToLot(quantity, lotSize)`

Also uses **floor (truncation)** to avoid over-quoting inventory.

```
0.85000 with lotSize=0.00001
→ passes through unchanged (already aligned)
```

---

## 8. `GenerateLadder` Signature

```go
func GenerateLadder(mc *config.MarketConfig, bidCount, askCount int) []PriceLevel
```

- `bidCount` and `askCount` are provided by `inventory.ComputeSkew()` — they can be 0 (critical tier), half of `LevelCount` (low tier), or full `LevelCount` (normal tier).
- Returns an empty slice when both counts are 0 (full inventory exhaustion).
- Order of returned levels: BID-01 ... BID-N, then ASK-01 ... ASK-N.

---

## 9. What This Package Does NOT Do

- Does NOT read from Kafka or Redis — purely a pure function over market config
- Does NOT track state — every call is stateless and deterministic for the same inputs
- Does NOT set quantities based on inventory — that is `inventory.ComputeSkew`'s job
- Does NOT validate prices against a live order book — ME is the price authority
