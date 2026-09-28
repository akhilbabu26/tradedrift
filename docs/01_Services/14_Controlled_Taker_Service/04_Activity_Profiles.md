# Controlled Taker Service — Activity Profiles (Low, Mid, High)

> **Status:** 📐 Designed (V1.2 — Dynamic Depth Sizing Invariant Enforced)  
> **Service:** Controlled Taker Service (`services/controlled-taker`)  
> **Document:** `04_Activity_Profiles.md`  
> **Last Updated:** September 2026  

---

## 1. Non-Negotiable Implementation Invariant: Dynamic Live-Depth Sizing

> [!IMPORTANT]
> **CTS MUST NEVER USE PREDEFINED FIXED ORDER QUANTITIES FOR ANY PROFILE.**  
> Order size must be dynamically calculated from the **current live order-book depth** at the exact moment the taker decision is evaluated.

### Prohibited Anti-Pattern (Reject on Code Review):
```go
// ❌ STRICTLY PROHIBITED — DO NOT WRITE HARDCODED SIZES:
lowOrderQuantity  := 0.01  // REJECTED
midOrderQuantity  := 0.05  // REJECTED
highOrderQuantity := 0.10  // REJECTED
```

### Mandatory Implementation Pattern:
```go
// ✅ MANDATORY PATTERN — DYNAMIC COMPUTATION FROM CURRENT LIVE DEPTH:
// Example 1: Inside Ask L1 has 0.20 BTC available. LOW ratio is 10%.
//   -> Q_raw = 0.20 * 0.10 = 0.02 BTC
// Example 2: Later, Inside Ask L1 has 0.50 BTC available. LOW ratio is 10%.
//   -> Q_raw = 0.50 * 0.10 = 0.05 BTC
```

Order quantities must breathe and scale naturally with the actual liquidity supplied by the Liquidity Engine:
- **LOW:** $\text{Current L1 Quantity} \times (5\% - 15\%)$
- **MID:** $\text{Current L1 Quantity} \times (20\% - 50\%)$
- **HIGH:** Dynamically calculated from **cumulative reachable live depth** across levels $1, \dots, m$ within the slippage band.

---

## 2. Dynamic Sizing & Execution Pipeline

Every taker decision follows an 8-step pipeline:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       DYNAMIC SIZING PIPELINE                               │
│                                                                             │
│ Step 1: Read Current Depth from Redis                                       │
│ └── Fetch depth:{market_id}; assert freshness and valid opposite levels      │
│                                                                             │
│ Step 2: Determine Activity Profile (LOW / MID / HIGH)                       │
│ └── Selected by scheduler policy or market rotation cycle                   │
│                                                                             │
│ Step 3: Calculate Quantity from CURRENT Live Depth                          │
│ └── LOW/MID: ratio of L1 depth; HIGH: ratio of cumulative depth             │
│                                                                             │
│ Step 4: Apply Hard Safety Caps                                              │
│ └── Clamp quantity by MAX_ORDER_NOTIONAL_USDT ($10,000 V1 default)          │
│                                                                             │
│ Step 5: Apply Precision & Lot-Size Rules                                    │
│ └── Round down to nearest LotSize step; assert >= MinQuantity               │
│                                                                             │
│ Step 6: Determine Aggressive Limit Price (Price Cap)                        │
│ └── Slippage-capped limit price reaching targeted book depth                │
│                                                                             │
│ Step 7: Submit to Order Service                                             │
│ └── gRPC CreateOrder with ORDER_TYPE_LIMIT, PriceCap, and Qty               │
│                                                                             │
│ Step 8: Matching Engine Decides Actual Execution                            │
│ └── Authoritative crossing against resting orders at maker price            │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 3. Market Precision & Baseline Parameters

All order quantities must strictly respect the lot size steps and minimum quantities defined in `services/market/migration/00001_create_market_tables.sql`:

| Parameter | `BTC-USDT` | `ETH-USDT` | `SOL-USDT` | Enforcement Service |
| :--- | :--- | :--- | :--- | :--- |
| **Base Asset** | `BTC` | `ETH` | `SOL` | Wallet / Order |
| **Quote Asset** | `USDT` | `USDT` | `USDT` | Wallet / Order |
| **Tick Size** | `0.01` ($) | `0.01` ($) | `0.001` ($) | Matching Engine / Order |
| **Lot Size** | `0.0001` | `0.001` | `0.01` | Matching Engine / Order |
| **Min Quantity** | `0.0001` | `0.001` | `0.01` | Order Service |
| **Typical Level Qty** | `0.10 - 0.50` BTC | `1.00 - 5.00` ETH | `10.0 - 50.0` SOL | Liquidity Engine |

> **Note on Ranges:**  
> The target quantity ranges shown below are illustrative derived values based on typical MM level sizes ($0.10 - 0.50\ \text{BTC}$, $1.0 - 5.0\ \text{ETH}$, $10 - 50\ \text{SOL}$). They are **NOT** hardcoded constants in the code.

---

## 4. Profile Mathematical Formulations

### 4.1 Cumulative Depth Sweep Model
Given top-of-book depth from Redis (`depth:{market_id}`):

$$\text{Opposite Levels: } \{(P_1, Q_1), (P_2, Q_2), \dots, (P_N, Q_N)\}$$

Where levels are ordered by price priority:
- For a BUY order: $P_1 < P_2 < \dots < P_N$ (Asks)
- For a SELL order: $P_1 > P_2 > \dots > P_N$ (Bids)

Within the maximum allowable slippage band ($P_{\text{max\_allowed}}$):
$$\text{Cumulative Reachable Depth: } D_{\text{cum}}(k) = \sum_{i=1}^{k} Q_i \quad \text{for } P_k \le P_{\text{max\_allowed}}$$

---

### 4.2 LOW TAKER (Background Heartbeat)
- **Objective:** Keeps 1-minute and 5-minute candlestick bars alive and maintains 24h rolling volume without moving the book.
- **Dynamic Sizing Rule:** Consumes a randomized small fraction of **current Level 1 quantity**:
  $$\rho \in [0.05, 0.15], \quad Q_{\text{raw}} = Q_1 \times \rho$$
  $$Q = \max\left(\text{MinQuantity},\ \left\lfloor \frac{Q_{\text{raw}}}{\text{LotSize}} \right\rfloor \times \text{LotSize}\right)$$
- **Price Cap:** $P_{\text{cap}} = P_1$ (Strict inside spread price; zero price movement).
- **Example Calculation:**
  - If current Ask 1 has $0.35\ \text{BTC}$ and $\rho = 0.10$:  
    $Q_{\text{raw}} = 0.35 \times 0.10 = 0.035\ \text{BTC} \implies Q = 0.0350\ \text{BTC}$.
  - If current Ask 1 has $0.12\ \text{BTC}$ and $\rho = 0.10$:  
    $Q_{\text{raw}} = 0.12 \times 0.10 = 0.012\ \text{BTC} \implies Q = 0.0120\ \text{BTC}$.

---

### 4.3 MID TAKER (Standard Simulated Flow)
- **Objective:** Generates regular organic market participation; clears part or all of Level 1.
- **Dynamic Sizing Rule:** Consumes a moderate fraction of **current Level 1 quantity**:
  $$\rho \in [0.20, 0.50], \quad Q_{\text{raw}} = Q_1 \times \rho$$
  $$Q = \text{LotAlign}(Q_{\text{raw}})$$
- **Price Cap:** $P_{\text{cap}} = P_1 + \text{TickSize}$ (Allows clearing Level 1 and matching at Level 2 if book moves).
- **Example Calculation:**
  - If current Bid 1 has $3.20\ \text{ETH}$ and $\rho = 0.35$:  
    $Q_{\text{raw}} = 3.20 \times 0.35 = 1.12\ \text{ETH} \implies Q = 1.120\ \text{ETH}$.

---

### 4.4 HIGH TAKER (Active Trend & Multi-Level Sweep)
- **Objective:** Simulates aggressive institutional sweeps consuming multiple price levels to establish candle highs/lows and significant volume.
- **Dynamic Multi-Level Sizing Rule:**
  1. Determine maximum permitted price band based on slippage:
     - For BUY: $P_{\text{limit}} = P_1 \times (1 + \text{MaxSlippageBps} \times 10^{-4})$
     - For SELL: $P_{\text{limit}} = P_1 \times (1 - \text{MaxSlippageBps} \times 10^{-4})$
  2. Identify all reachable depth levels $i = 1, 2, \dots, m$ where $P_i \le P_{\text{limit}}$.
  3. Calculate sweep quantity dynamically: consumes $100\%$ of **current Level 1** plus a randomized fraction $\rho_{\text{sweep}} \in [0.30, 0.80]$ of **subsequent cumulative reachable depth**:
     $$Q_{\text{raw}} = Q_1 + \left(\sum_{i=2}^{m} Q_i\right) \times \rho_{\text{sweep}}$$
  4. Enforce hard notional ceiling:
     $$Q_{\text{capped}} = \min\left(Q_{\text{raw}},\ \frac{\text{MAX\_ORDER\_NOTIONAL\_USDT}}{P_m}\right)$$
  5. Apply lot-alignment: $Q = \text{LotAlign}(Q_{\text{capped}})$.
  6. Set price cap: $P_{\text{cap}} = P_m$ (Reaches up to Level $m$).
- **Cooldown:** Mandatory 5-minute quench period post-execution during which only LOW profile is allowed.

---

## 5. Explicit Timing & Jitter Model

The interval scheduler defines explicit triple bounds `(MinInterval, BaseInterval, MaxInterval)`:

```go
type IntervalConfig struct {
    MinInterval  time.Duration
    BaseInterval time.Duration
    MaxInterval  time.Duration
}
```

### Profile Timing Configurations:

| Profile | `MinInterval` | `BaseInterval` | `MaxInterval` |
| :--- | :--- | :--- | :--- |
| **LOW** | `45s` | `75s` | `120s` |
| **MID** | `120s` (2m) | `240s` (4m) | `360s` (6m) |
| **HIGH** | `900s` (15m) | `1800s` (30m) | `2700s` (45m) |

### Bounded Interval Calculation Algorithm:
```go
func CalculateNextInterval(cfg IntervalConfig) time.Duration {
    // Generate uniform random jitter between -25% and +25%
    jitterPercent := (rand.Float64() * 0.50) - 0.25
    interval := time.Duration(float64(cfg.BaseInterval) * (1.0 + jitterPercent))
    
    // Hard clamp within explicit [MinInterval, MaxInterval]
    if interval < cfg.MinInterval {
        return cfg.MinInterval
    }
    if interval > cfg.MaxInterval {
        return cfg.MaxInterval
    }
    return interval
}
```
