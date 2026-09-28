# Controlled Taker Service (CTS) Documentation Directory

> **Status:** 📐 Architecture & Design Specification (V1.1 — Updated with Architectural Feedback)  
> **Directory:** `docs/01_Services/14_Controlled_Taker_Service/`  
> **Service:** Controlled Taker Service (`services/controlled-taker`)  
> **Last Updated:** September 2026  

---

## 1. Purpose

This directory contains the authoritative architecture and technical design specifications for the **Controlled Taker Service (CTS)** in TradeDrift.

The primary mission of CTS is to solve the **Dormant Market Problem**: in an exchange where liquidity is synthetically provided by the Liquidity Engine (LE), resting maker bids and asks do not produce executions unless an opposing counterparty crosses the spread. Without executions:
- `trades.executed` events are not emitted.
- Downstream `market_trades` receives zero records.
- 24-hour rolling statistics (High, Low, Volume, % Change) return `nil` or `$0.00`.
- Candlestick bars (1m, 5m, 15m, 1h, 4h, 1d) stagnate.

CTS injects calibrated, synthetic **taker demand** into the existing order pipeline via **aggressive taker limit orders with a slippage price cap**, ensuring continuous, authentic price discovery and trading activity.

---

## 2. Invariant: Demand vs. Execution

The foundational architectural invariant across all CTS design documents is:

$$\mathbf{CTS\ generates\ demand;\ Matching\ Engine\ determines\ execution.}$$

* **CTS DOES NOT** decide execution price, match orders, write to `market_trades`, fabricate ticker statistics, or modify balances directly.
* **CTS SUBMITS** aggressive limit orders priced at a conservative slippage cap (for BUY) or floor (for SELL).
* **Matching Engine ALWAYS** dictates whether orders match, fill prices (maker price priority), matched quantities, and sequential trade IDs.

---

## 3. The LE ↔ CTS Closed-Loop Replenishment Dynamic

CTS and the Liquidity Engine form a self-sustaining financial feedback loop:

```
        ┌────────────────────────────────────────────────────────┐
        │            Liquidity Engine (MM-001)                   │
        │   - Maintains resting bids & asks across 12 levels     │
        └──────────────────────────┬─────────────────────────────┘
                                   │
                                   ▼
                             Order Book
                                   ▲
                                   │ Aggressive Crossing Limit Order
        ┌──────────────────────────┴─────────────────────────────┐
        │        Controlled Taker Service (CT-001)               │
        │   - Evaluates cumulative depth                         │
        │   - Submits lot-aligned aggressive limit order         │
        └──────────────────────────┬─────────────────────────────┘
                                   │
                                   ▼
                       ┌───────────────────────┐
                       │    Matching Engine    │
                       │ (Executes at MM price)│
                       └───────────┬───────────┘
                                   │
                                   ▼
                             TradeExecuted
                                   │
                                   ▼
        ┌────────────────────────────────────────────────────────┐
        │            Liquidity Engine (Consumer)                 │
        │   - Observes fill via trades.executed                  │
        │   - Adjusts internal MM inventory                      │
        │   - Triggers targeted reconciliation to replenish level│
        └──────────────────────────┬─────────────────────────────┘
                                   │
                                   ▼
                             Order Book (Restored)
```

---

## 4. Document Catalog

| Document | Title | Description |
| :--- | :--- | :--- |
| **[`01_Overview.md`](01_Overview.md)** | Executive Summary & Core Concept | System context, core thesis, responsibilities, and closed-loop feedback. |
| **[`02_Architecture.md`](02_Architecture.md)** | System & Internal Architecture | Component decomposition, market worker loops, decoupled config, and data flow. |
| **[`03_Identity_And_Wallet.md`](03_Identity_And_Wallet.md)** | Identity, Wallet & Idempotency | Dedicated `CT-001` account, wallet funding, and verified Order Service idempotency. |
| **[`04_Activity_Profiles.md`](04_Activity_Profiles.md)** | Low / Mid / High Activity Profiles | Mathematical models, cumulative depth sweeping, and explicit interval timing. |
| **[`05_Order_Flow_And_Engine.md`](05_Order_Flow_And_Engine.md)** | Order Submission & Matching Pipeline | Sequence diagram with slippage-capped aggressive limit orders & LE replenishment. |
| **[`06_Kafka_And_Redis.md`](06_Kafka_And_Redis.md)** | Kafka & Redis Integration | Topic topologies, partition keys, and Redis cumulative depth inspection. |
| **[`07_Safety_And_Circuit_Breaker.md`](07_Safety_And_Circuit_Breaker.md)** | Safety Limits & Circuit Breaker | Cumulative depth guard, rate limits, notional caps, and fail-closed trip rules. |
| **[`08_Observability.md`](08_Observability.md)** | Observability, Metrics & Health | Prometheus metrics, structured Zap logging, and per-market readiness probes. |
| **[`09_Failure_And_Recovery.md`](09_Failure_And_Recovery.md)** | Failure Modes & Recovery | Resilience policies for outages, restarts, and verified idempotency on retry. |
| **[`10_Implementation_Plan.md`](10_Implementation_Plan.md)** | Implementation & Testing Roadmap | Phased engineering milestones, file inventory, and verification matrix. |
