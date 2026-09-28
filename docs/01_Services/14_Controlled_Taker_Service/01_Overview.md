# Controlled Taker Service — Overview & Core Concept

> **Status:** 📐 Designed (V1.1 — Updated with Architectural Feedback)  
> **Service:** Controlled Taker Service (`services/controlled-taker`)  
> **Document:** `01_Overview.md`  
> **Last Updated:** September 2026  

---

## 1. Executive Summary

In TradeDrift, market liquidity is bootstrapped and maintained by the **Liquidity Engine (LE)** ([`docs/01_Services/12_Automated_Liquidity_Engine_Bot`](../12_Automated_Liquidity_Engine_Bot/01_Overview.md)), which places resting limit bids and asks into the Matching Engine.

However, maker liquidity alone does not result in trades:
1. In simulated, demo, or low-traffic environments, human users may not trade continuously against every market.
2. When resting quotes are not crossed, zero `TradeExecuted` events are published.
3. [Market Service](../06_Market_Service/10_Market_Service.md) receives zero trades; its 24-hour rolling SQL queries (`WHERE executed_at >= NOW() - INTERVAL '24 hours'`) return `nil` or `$0.00`.
4. As a result, 24h High/Low appear as `---`, 24h Volume displays `$0.00`, and candlestick charts stop producing new bars.

The **Controlled Taker Service (CTS)** solves this by introducing controlled, synthetic taking activity that enters the existing TradeDrift execution pipeline as **aggressive taker limit orders with a slippage price cap**.

```
┌──────────────────────────┐          ┌──────────────────────────┐
│  Liquidity Engine (LE)   │          │ Controlled Taker Service │
│   (Provides Maker Bids)  │          │   (Provides Taker Buys)  │
└────────────┬─────────────┘          └────────────┬─────────────┘
             │                                     │
             ▼                                     ▼
        Resting Asks                          Crossing Buys (Limit w/ Cap)
             │                                     │
             └───────────────────┬─────────────────┘
                                 │
                                 ▼
                     ┌───────────────────────┐
                     │    Matching Engine    │
                     │  (Deterministic Match)│
                     └───────────┬───────────┘
                                 │
                                 ▼
                          TradeExecuted
                                 │
                 ┌───────────────┼───────────────┐
                 ▼               ▼               ▼
           Market Service   Settlement     Trade Service
           (Candles & 24h)  (Wallet Ledger)(Trade History)
```

---

## 2. Core Operational Principles

1. **Demand vs. Execution Invariant:**  
   $$\mathbf{CTS\ generates\ demand;\ Matching\ Engine\ determines\ execution.}$$  
   CTS decides *when* to trade, *which market* to target, *which side* (BUY/SELL), and *what quantity* to submit. The Matching Engine decides *if* it executes, *which resting orders* it matches against, and *the exact execution price* (maker price priority).
2. **Aggressive Limit Orders with Slippage Caps (Not Unconstrained Market Orders):**  
   CTS submits `ORDER_TYPE_LIMIT` orders with a calculated price cap (for BUY) or floor (for SELL):
   - For a BUY: $\text{PriceCap} = \text{BestAsk} \times (1 + \text{MaxSlippageBps} \times 10^{-4})$.
   - For a SELL: $\text{PriceFloor} = \text{BestBid} \times (1 - \text{MaxSlippageBps} \times 10^{-4})$.
   - **Why this is strictly superior to raw market orders:**  
     a) It gives CTS deterministic slippage protection against thin books or sudden book movements.  
     b) It complies with Order Service's wallet reservation requirement (which requires a price cap to calculate quote asset lock for BUY orders).  
     c) It guarantees that CTS will never cross beyond its configured maximum acceptable price band.
3. **Zero Falsification:**  
   CTS never writes fake trade rows into `market_trades`, never fakes candlestick records in `ohlc_candles`, and never invents synthetic ticker data in the frontend. Every metric displayed on the Markets page is backed by an authentic execution print from the Matching Engine.
4. **Double-Entry Financial Integrity:**  
   CTS trades under a fully legitimate, dedicated system trading account (`CT-001`). Its orders undergo standard wallet balance checks, fund reservations, and ledger double-entry settlements.

---

## 3. The LE ↔ CTS Dynamic Replenishment Feedback Loop

CTS does not operate in a vacuum; it forms an active, self-stabilizing financial feedback loop with the Liquidity Engine:

```
CTS consumes MM resting liquidity
          │
          ▼
Order book depth decreases at top levels
          │
          ▼
Matching Engine emits TradeExecuted to trades.executed
          │
          ▼
Liquidity Engine consumer ingests TradeExecuted
          │
          ▼
LE updates MM internal inventory balances
          │
          ▼
LE triggers targeted reconciliation for consumed levels
          │
          ▼
LE posts new resting MM limit orders (orders.commands)
          │
          ▼
Order book depth is fully replenished
          │
          ▼
CTS observes restored depth via Redis depth:{market} and trades again
```

This interaction is the cornerstone of TradeDrift's simulation fidelity: liquidity is organically consumed by CTS and automatically restocked by LE without manual intervention.

---

## 4. Scope of Responsibilities

### What CTS Owns:
- **Periodic Activity Scheduling:** Autonomous worker loops with explicit interval configurations and randomized jitter for all configured trading pairs.
- **Liquidity & Depth Inspection:** Reading cumulative top-of-book Redis depth (`depth:{market_id}`) before sending orders.
- **Order Parameter Sizing:** Calibrating order quantities according to LOW, MID, or HIGH profiles while strictly respecting lot size steps, minimum quantities, and cumulative depth.
- **Direction Determination:** Selecting BUY or SELL using an inventory-balancing mean-reversion algorithm.
- **Order Submission:** Forwarding requests to Order Service via canonical gRPC (`orderv1.OrderServiceClient.CreateOrder`).
- **Safety & Circuit Breaking:** Enforcing per-order notional caps, hourly volume ceilings, slippage thresholds, and automatic tripping on consecutive errors.
- **Observability:** Publishing Prometheus metrics and structured Zap logs for all operational cycles.

### What CTS Does NOT Own:
- **Order Matching:** Matching is exclusively owned by the Matching Engine.
- **Price Determination:** CTS never dictates execution prices.
- **Order Book State:** CTS does not maintain or publish the authoritative order book.
- **Trade Settlement:** Settle operations are owned by Settlement Service and Wallet Service.
- **Portfolio Accounting:** CTS does not calculate user PnL or manage user accounts.
- **Market Statistics:** Rolling 24h stats and OHLC candles are computed solely by Market Service.
- **Frontend Presentation:** Frontend consumes existing backend APIs; CTS has no direct UI integration.
