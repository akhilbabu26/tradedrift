# TradeDrift — Automated Liquidity Engine & Market Maker Bot

> **Status:** 🚀 Designed (V1)  
> **Service:** Automated Liquidity Engine (`services/liquidity-engine`)  
> **Document:** `01_Overview.md`  
> **Version:** V1.0  
> **Last Updated:** August 2026  

---

## 1. Executive Summary & Purpose

When a new crypto trading exchange launches, or when a user logs in to a fresh environment, the order book is inherently empty ($0\ \text{Bids}, 0\ \text{Asks}$). In financial exchanges, this is known as the **Cold-Start Liquidity Problem**:
* Without counterparties, users cannot execute **Market Buy/Sell** orders.
* **Limit Orders** sit indefinitely with nothing to match against.
* Order books, TradingView candlestick charts, 24h tickers, and trade execution tapes appear completely blank.

The **Automated Liquidity Engine (AMM Bot)** solves this by operating as an autonomous, high-frequency market-making service that bootstraps, ladders, and maintains tight institutional two-sided liquidity across all supported trading pairs (`BTC-USDT`, `ETH-USDT`, `SOL-USDT`).

---

## 2. High-Level System Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                 TradeDrift Automated Liquidity Engine                       │
│                                                                             │
│  ┌────────────────────────┐  ┌────────────────────────┐  ┌───────────────┐  │
│  │   Laddering Engine     │  │   Restocking Engine    │  │  Micro-Trader │  │
│  │ (12-15 Levels Bid/Ask) │  │  (Refills Eaten Levels)│  │ (Tick Stream) │  │
│  └───────────┬────────────┘  └───────────┬────────────┘  └───────┬───────┘  │
└──────────────┼───────────────────────────┼───────────────────────┼──────────┘
               │                           │                       │
               └───────────────────────────┼───────────────────────┘
                                           ▼
                     ┌───────────────────────────────────────────┐
                     │          Kafka: orders.commands           │
                     │    (Key = MarketID, Event: OrderCreated)  │
                     └─────────────────────┬─────────────────────┘
                                           │
                                           ▼
                     ┌───────────────────────────────────────────┐
                     │          Matching Engine (Go)             │
                     │  - Matches user orders against MM orders  │
                     │  - Pushes L2 depth to Redis               │
                     │  - Emits TradeExecuted to Kafka           │
                     └─────────────────────┬─────────────────────┘
                                           │
                   ┌───────────────────────┴───────────────────────┐
                   ▼                                               ▼
         [Redis: depth:{market_id}]                     [Kafka: trades.executed]
                   │                                               │
                   ▼                                               ▼
          [WebSocket Stream]                               [Market Service]
       (Live L2 Order Book)                       (Candlestick Bars & 24h Tickers)
```

---

## 3. Core Market Configurations

The Liquidity Engine initializes with configurable mid-prices, spreads, depth levels, and lot distributions:

| Market | Reference Mid Price | Spread Target | Depth Levels | Lot Size Step | Typical Lot Range |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`BTC-USDT`** | `$96,450.00` | `0.04%` ($38.50) | 12 Bids / 12 Asks | `0.0001 BTC` | `0.0500 – 0.8500 BTC` |
| **`ETH-USDT`** | `$2,780.50` | `0.04%` ($1.10) | 12 Bids / 12 Asks | `0.0010 ETH` | `0.4000 – 4.5000 ETH` |
| **`SOL-USDT`** | `$188.20` | `0.05%` ($0.09) | 12 Bids / 12 Asks | `0.0100 SOL` | `4.0000 – 45.0000 SOL` |

---

## 4. Key Engine Components

### 4.1 Order Ladder Generator (Bid & Ask Laddering)
* **Bid Ladder (Buys):** Places 12 discrete buy limit orders at geometric price increments below the mid-price:
  $$\text{Price}_{\text{Bid}, i} = \text{MidPrice} \times \left(1 - \frac{\text{Spread}}{2} - (i \times \text{StepSize})\right)$$
* **Ask Ladder (Sells):** Places 12 discrete sell limit orders at geometric price increments above the mid-price:
  $$\text{Price}_{\text{Ask}, i} = \text{MidPrice} \times \left(1 + \frac{\text{Spread}}{2} + (i \times \text{StepSize})\right)$$
* **Lot Sizing Variation:** Randomizes order size at each level within a realistic normal distribution curve so the order book exhibits natural market depth.

### 4.2 Restocking & Order Book Health Monitor
* Connects to the Redis depth cache (`depth:{market_id}`) or subscribes to Kafka `trades.executed`.
* If a price level is completely consumed by an incoming user market or limit order, the restocking engine automatically calculates the gap and dispatches a new resting limit order within $< 50\text{ms}$.
* Prevents order book hollow-out and guarantees persistent two-sided market liquidity.

### 4.3 Micro-Trading & Continuous Candlestick Builder
* Periodically (every 4–8 seconds, with randomized jitter), submits small micro-market orders crossing the spread within a system liquidity provider loop.
* Generates live execution prints that feed:
  * **Real-time Candlestick Bars:** 1m, 5m, 15m, 1h, 1d OHLC bars constructed by `Market Service`.
  * **Recent Trades Tape:** Real-time trade ticker in the `/trade` terminal.
  * **24h Ticker Stats:** Live 24h high, 24h low, and 24h volume stats.

---

## 5. Security & Risk Controls

1. **System Dedicated Identity:** The Liquidity Engine authenticates using a designated system account (`system-market-maker-bot`) with predefined system wallet reserves.
2. **Spread Guard:** An invariant check ensures $\text{Best Ask} > \text{Best Bid}$, preventing self-cross locks.
3. **Max Cumulative Exposure Limit:** Enforces hard boundaries on maximum inventory deviation from neutral (Delta-neutral risk management).
4. **Graceful Cancellation on Shutdown:** On `SIGTERM` / shutdown, the bot cleanly sends batch cancel commands to avoid leaving orphan orders in memory.

---

## 6. Directory Structure (`services/liquidity-engine`)

```
services/liquidity-engine/
├── cmd/
│   └── server/
│       └── main.go               # Service entrypoint, config loader & graceful shutdown
├── internal/
│   ├── config/
│   │   └── config.go             # Market mid-prices, spread %, depth levels
│   ├── generator/
│   │   ├── ladder.go             # Bid/Ask ladder calculation & random lot sizing
│   │   └── microtrade.go         # Periodic tick simulator & trade generator
│   ├── monitor/
│   │   └── health.go             # Redis depth watcher & orderbook restocker
│   ├── kafka/
│   │   └── producer.go           # High-throughput orders.commands publisher
│   └── bot/
│       └── engine.go             # Orchestrator loop managing all trading pairs
├── Dockerfile
├── go.mod
└── go.sum
```
