# Liquidity Engine — Market Configuration & Pricing

> **Service:** `services/liquidity-engine`
> **Version:** V1.1

---

## Market Configuration

Each market has its own strategy configuration:

```go
type MarketConfig struct {
    MarketID       string
    BaseAsset      string
    QuoteAsset     string

    ReferencePrice decimal.Decimal

    SpreadBps      decimal.Decimal  // e.g. 4 = 0.04%
    LevelCount     int              // e.g. 12
    LevelStepBps   decimal.Decimal  // step between levels

    MinOrderSize   decimal.Decimal
    MaxOrderSize   decimal.Decimal

    // Inventory thresholds (base asset)
    TargetBase     decimal.Decimal
    MinBase        decimal.Decimal
    CriticalBase   decimal.Decimal

    // Inventory thresholds (quote asset)
    TargetQuote    decimal.Decimal
    MinQuote       decimal.Decimal
    CriticalQuote  decimal.Decimal
}
```

### V1 Default Configurations

| Field | BTC-USDT | ETH-USDT | SOL-USDT |
| :--- | :--- | :--- | :--- |
| `ReferencePrice` | `96,450.00` | `2,780.50` | `188.20` |
| `SpreadBps` | `4` (0.04%) | `4` (0.04%) | `5` (0.05%) |
| `LevelCount` | `12` | `12` | `12` |
| `MinOrderSize` | `0.0001 BTC` | `0.001 ETH` | `0.01 SOL` |
| `MaxOrderSize` | `0.85 BTC` | `4.5 ETH` | `45 SOL` |
| `TargetBase` | `100 BTC` | `500 ETH` | `5000 SOL` |
| `MinBase` | `30 BTC` | `150 ETH` | `1500 SOL` |
| `CriticalBase` | `10 BTC` | `50 ETH` | `500 SOL` |
| `TargetQuote` | `$10,000,000` | `$10,000,000` | `$10,000,000` |
| `MinQuote` | `$2,000,000` | `$2,000,000` | `$2,000,000` |
| `CriticalQuote` | `$500,000` | `$500,000` | `$500,000` |

---

## Reference Price

V1 uses a **configured reference price** (not permanently hardcoded):

```yaml
markets:
  BTC-USDT:
    reference_price: 96450.00
  ETH-USDT:
    reference_price: 2780.50
  SOL-USDT:
    reference_price: 188.20
```

> **V2 Roadmap:** External price feed → Reference Price Service → Liquidity Engine.
> The ladder recenters dynamically when the reference price moves.
>
> **V1 Note:** When the reference price is changed in config and the service restarts,
> re-centering triggers a full ladder cancel + rebuild to avoid transient crossed book states.

---

## Ladder Generation

For BTC-USDT:

```
Reference = $96,450.00
Spread    = 0.04%  →  Half-spread = 0.02%  →  $19.29

Best Ask  = $96,450.00 + $19.29 = $96,469.29
Best Bid  = $96,450.00 - $19.29 = $96,430.71
```

Generated order book ladder:

```
              BTC-USDT Order Book

ASK 12   $96,892.34  |  0.0710 BTC
ASK 11   $96,795.12  |  0.1250 BTC
...
ASK  2   $96,506.78  |  0.5500 BTC
ASK  1   $96,469.29  |  0.8100 BTC    <- Best Ask
─────────────────────────────────────
         $96,450.00                    <- Mid Price
─────────────────────────────────────
BID  1   $96,430.71  |  0.7300 BTC    <- Best Bid
BID  2   $96,412.13  |  0.4800 BTC
...
BID 11   $96,124.88  |  0.1100 BTC
BID 12   $96,028.16  |  0.0620 BTC
```

**Invariant that must always hold:**

```
BestBid < MidPrice < BestAsk
```

This spread invariant is the Liquidity Engine's first line of defense against self-trading.
The Matching Engine's STP rule is the authoritative enforcement layer.
