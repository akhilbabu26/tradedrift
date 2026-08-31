# Liquidity Engine — Inventory Management

> **Service:** `services/liquidity-engine`
> **Version:** V1.1

---

## Inventory Thresholds

Inventory thresholds govern quoting behaviour for each asset:

```
BTC Thresholds:
  Target   = 100 BTC   (normal operation)
  Low      =  30 BTC   (reduce sell-side depth)
  Critical =  10 BTC   (stop selling entirely)

USDT Thresholds:
  Target   = $10,000,000   (normal operation)
  Low      =  $2,000,000   (reduce buy-side depth)
  Critical =    $500,000   (stop buying entirely)
```

---

## Inventory-Aware Quoting (Skew)

When base inventory is depleted, the engine **skews its liquidity depth** on the ask side:

```
BTC Inventory: 100 → 80 → 50 → 30 → 10

Stage:     NORMAL     →   LOW    →   CRITICAL
Bids:         12            12             12
Asks:         12             6              0

(Reduces asks to prevent selling BTC the MM does not hold)
```

When quote (USDT) inventory is depleted, the engine skews the bid side:

```
USDT Inventory: $10M → $5M → $2M → $500K

Stage:     NORMAL     →   LOW    →   CRITICAL
Bids:         12             6              0
Asks:         12            12             12
```

This ensures the MM never promises assets it cannot deliver, even before settlement completes.

---

## Automatic Replenishment

When MM inventory drops below `Low` threshold, the engine issues a **replenishment request** to the System Treasury:

```
MM BTC  = 25 (below Low: 30)
Target  = 100

Replenishment = 100 - 25 = 75 BTC

Flow:
  Liquidity Engine
       │  Replenishment Request
       ▼
  System Treasury
       │
       ▼
  Settlement / Wallet Service
       │
       ▼
  MM Wallet → BTC = 100
```

> ⚠️ The Liquidity Engine **never** directly edits wallet balances. All replenishment flows
> through Settlement, which applies the actual balance change.

### Replenishment Triggers

| Asset | Trigger Condition | Replenishment Amount |
| :--- | :--- | :--- |
| Base (BTC/ETH/SOL) | balance < `MinBase` | `TargetBase - currentBase` |
| Quote (USDT) | balance < `MinQuote` | `TargetQuote - currentQuote` |
