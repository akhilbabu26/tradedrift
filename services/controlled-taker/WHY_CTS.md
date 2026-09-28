# Controlled Taker Service (CTS) — Purpose, Problem Solving & Architecture

This document explains **why** the Controlled Taker Service (CTS) is an essential component of the TradeDrift exchange platform, **what specific problems** it solves, and provides a concise **directory guide** explaining the role of every package in the codebase.

---

## 1. Why TradeDrift Needs CTS

In any modern financial exchange, liquidity exists as a two-sided equation:
1. **Liquidity Makers (Passive):** Place resting limit bids and asks on the order book (in TradeDrift, managed by the **Liquidity Engine / Market Making Service**).
2. **Liquidity Takers (Active):** Cross the spread by submitting aggressive crossing orders that execute immediately against resting maker quotes.

```
┌─────────────────────────────────┐               ┌─────────────────────────────────┐
│        Liquidity Engine         │               │     Controlled Taker (CTS)      │
│            (Makers)             │               │            (Takers)             │
│                                 │               │                                 │
│  • Posts resting Bids & Asks    │               │  • Reads live depth             │
│  • Provides book depth          │               │  • Crosses top-of-book levels   │
│  • Tightens spreads             │               │  • Generates execution flow     │
└────────────────┬────────────────┘               └────────────────┬────────────────┘
                 │                                                 │
                 ▼                                                 ▼
        Passive Maker Quotes                              Aggressive Crossing Orders
                 │                                                 │
                 └───────────────────────┬─────────────────────────┘
                                         ▼
                             Matching Engine (Executes)
                                         │
                                         ▼
                            Trade Executed Event (Kafka)
                                         │
               ┌─────────────────────────┼─────────────────────────┐
               ▼                         ▼                         ▼
        Trade Service             Market Service            Settlement Service
       (Persists Trades)        (Updates Candles)          (Transfers Balances)
```

### The "Dead Exchange" Cold-Start Problem
Without active takers crossing the spread:
* **The order book stays frozen:** Resting quotes sit motionless on the matching engine.
* **Price discovery halts:** No trades are executed, preventing market data streams and candle charts (`1m`, `5m`, `1h`) from updating.
* **Downstream services stay untested:** The asynchronous pipeline (Order Service $\rightarrow$ Kafka $\rightarrow$ Matching Engine $\rightarrow$ Trade Service $\rightarrow$ Settlement $\rightarrow$ Wallet) remains unexercised.
* **Liquidity replenishment is never triggered:** The Liquidity Engine only recalculates and replenishes quotes when its resting depth is consumed.

CTS solves this by acting as a **controlled, safe, and autonomous taker (`CT-001`)** that continuously drives real trading activity across all configured markets (`BTC-USDT`, `ETH-USDT`, `SOL-USDT`).

---

## 2. What CTS Solves

A common mistake in exchange design is using naive trading bots or static scripts to generate taker volume. Naive scripts fail catastrophically: they blow through thin books, accumulate runaway directional risk, crash under network drops, or accidentally rest on the book as makers.

CTS implements an institutional-grade architecture specifically engineered to solve these problems:

---

### Problem 1: Static Order Sizing Fragility
* **The Problem:** Hardcoded order sizes (e.g., always buying $0.1$ BTC) cause havoc. In thin markets, a fixed $0.1$ BTC order sweeps illiquid levels and causes massive slippage. In deep markets, $0.1$ BTC has zero impact.
* **What CTS Solves:** **Pure Dynamic Depth Sizing (`profile.go`).** CTS strictly prohibits fixed quantities. It inspects live L2 depth in Redis and scales orders dynamically:
  - `LOW` profile consumes **5% to 15%** of L1 depth (gentle heartbeat).
  - `MID` profile consumes **15% to 35%** of L1 depth (liquidity probing).
  - `HIGH` profile sweeps multiple levels, but strictly requires that reachable depth is at least **1.5× the target quantity** ($\text{Depth} \ge 1.5 \times \text{targetQty}$). If depth is thin, CTS scales down or fails closed.

---

### Problem 2: The Resting Maker Hazard (Taker-Only Violation)
* **The Problem:** In TradeDrift, `CT-001` is strictly authorized as a taker. If a limit crossing order partially fills and the unfilled remainder sits on the book, `CT-001` accidentally becomes a market maker, competing with real liquidity providers.
* **What CTS Solves:** **Autonomous Detached Order Cleanup (`order_cleanup.go`).** Every submitted order enters an independent cleanup loop ($\le 2.5$s timeout). After allowing 60ms for the Matching Engine to execute, CTS inspects the order status. If `OPEN` or `PARTIALLY_FILLED`, CTS immediately issues `CancelOrder` and polls until the residual is confirmed `CANCELLED`. CTS never leaves maker orders resting.

---

### Problem 3: Runaway Directional Inventory Exposure
* **The Problem:** If a taker bot randomly buys and sells, random walks can cause it to buy $100$ BTC and exhaust its USDT balance, halting the service.
* **What CTS Solves:** **USDT-Normalized Inventory Balancing (`selector.go`).** CTS tracks net notional exposure ($\Delta_{\text{USDT}}$) across all markets. When exposure exceeds configurable thresholds:
  - If Long $> +\$5,000$, bias shifts to **65% SELL / 35% BUY**.
  - If Long $> +\$25,000$, bias shifts to **75% SELL / 25% BUY**.
  - Symmetrical rules apply when Short. This mean-reverting behavior ensures long-term delta-neutral balance.

---

### Problem 4: Asynchronous Submission & Lost RPC Ambiguity
* **The Problem:** Order Service uses asynchronous transactional outboxes. If a `CreateOrder` gRPC call times out due to network congestion, CTS does not know if the order was created in PostgreSQL or lost in flight. Assuming "it failed" and doing nothing leaves an active unmonitored order on the matching engine!
* **What CTS Solves:** **Keyset Cursor Idempotency Recovery (`orderservice/client.go`).** Every order is assigned a deterministic UUIDv7 key (`CTS-<market>-<uuidv7>`). If an ambiguous error occurs, CTS queries Order Service via `FindOrderByIdempotencyKey`. If committed, it recovers the `OrderID` and cancels any residual. If uncommitted, it safely fails closed.

---

### Problem 5: Cascading Market Disruption & Flash Crashes
* **The Problem:** If market data stalls, spreads blow out, or a database experiences lock contention, an unthrottled bot will flood the matching engine with bad trades.
* **What CTS Solves:** **Multi-Layer Safety Gates & Circuit Breakers (`safety.go` & `circuit_breaker.go`).**
  - **Spread Check:** Halts trading if bid/ask spread exceeds `1.5%`.
  - **Volume & Rate Limits:** Enforces rolling 1-hour notional, 24-hour notional, and $\le 60$ trades/hour.
  - **Circuit Breaker:** Consecutive errors trip the market breaker (`CLOSED` $\rightarrow$ `OPEN`). After a 60s cooldown, it enters `HALF_OPEN` and atomically reserves **one** probe order. If the probe succeeds, it resets; if it fails, it trips back to `OPEN`.

---

## 3. Short Description for Every Folder

Below is a breakdown of every meaningful folder within `services/controlled-taker/`:

```
services/controlled-taker/
├── cmd/
│   └── server/                          # [1] Server Entrypoint & Process Lifecycle
├── docs/                                # [2] High-Level Architectural & Execution Flows
└── internal/
    ├── account/                         # [3] CT-001 System Identity & UUID Constants
    ├── clients/                         # [4] External Dependency Abstraction Layer
    │   ├── orderservice/                # [5] Order Service gRPC Client & Idempotency Recovery
    │   └── redisdepth/                  # [6] Redis L2 Depth Reader & Staleness Validator
    ├── config/                          # [7] Environment Configuration & Startup Safety Validation
    ├── engine/                          # [8] Taker Engine, Sizing Math, Selector & Safety Gate
    ├── health/                          # [9] HTTP /healthz and /readyz Probe Server
    └── metrics/                         # [10] Prometheus Metric Instrumentation & Exporter
```

### [1] `cmd/server/`
* **Entrypoint:** Contains `main.go`.
* **Role:** Auto-loads `.env`, loads configuration, initializes dependencies, starts HTTP health/metrics servers, launches market workers, and ensures graceful shutdown ordering (workers finish in-flight order cleanups before external clients disconnect).

### [2] `docs/`
* **Architecture Documentation:** Contains detailed specifications such as `Flows of CTS.md`.
* **Role:** Explains the eight end-to-end execution flows (startup, depth sizing, inventory balancing, safety validation, order dispatch, residual cleanup, idempotency recovery, and observability).

### [3] `internal/account/`
* **Identity Management:** Defines system constants for the `CT-001` trading account.
* **Role:** Exposes the canonical `WalletUUIDStr` (`00000000-0000-0000-0000-000000000002`) used to identify CT-001 in Wallet Service balances and Order Service authorization.

### [4] `internal/clients/`
* **Dependency Abstraction:** Boundary layer decoupling engine logic from raw network transports.
* **Role:** Defines interfaces (`DepthReader`, `OrderSubmitter`) enabling unit testing and isolating network failures from core trading decisions.

### [5] `internal/clients/orderservice/`
* **Order Service gRPC Client:** Connects to `tradedrift.order.v1.OrderService`.
* **Role:** Dispatches aggressive `LIMIT` crossing orders, polls order status, cancels residuals, and executes multi-page keyset cursor pagination to recover orders by idempotency key.

### [6] `internal/clients/redisdepth/`
* **Redis Depth Reader:** Read-only client streaming top-of-book L2 snapshots (`depth:<market_id>`).
* **Role:** Enforces strict fail-closed validation: verifies market IDs, asserts timestamp freshness ($\le 5.0$s), checks monotonicity of bids and asks, and blocks crossed or locked books.

### [7] `internal/config/`
* **Configuration & Validation:** Uses `platform/config` to load operational parameters.
* **Role:** Applies safe defaults, configures profile timings (`LOW`, `MID`, `HIGH`), and validates startup invariants (e.g., volume hierarchies, positive tick/lot sizes, and the $500$ bps slippage ceiling).

### [8] `internal/engine/`
* **Core Execution Orchestrator:** The heart of CTS.
* **Role:**
  - `engine.go`: Spawns and synchronizes per-market worker loops with cold-boot staggering.
  - `worker.go`: Orchestrates the execution cycle, profile selection, and fill recording.
  - `order_cleanup.go`: Autonomous post-submission verification and cancellation of unfilled order remainder (`awaitOrderCleanup`).
  - `circuit_breaker.go`: Three-state circuit breaker with atomic probe reservations and fail-closed transitions.
  - `errors.go`: gRPC submission error classification (`classifySubmissionError`).
  - `profile.go`: Computes dynamic order quantities, lot alignment, and side-specific conservative pricing.
  - `selector.go`: Balances BUY/SELL direction using USDT-normalized inventory exposure.
  - `safety.go`: Enforces rolling volume limits, trade rate limits, spread checks, and 1.5x depth ratio verification.

### [9] `internal/health/`
* **HTTP Probes (`:8080`):** Exposes `/healthz` (liveness) and `/readyz` (readiness).
* **Role:** Performs multi-tier checks (Redis ping, Order Service ping, and 5-second depth freshness across all configured markets). Fails closed (`503`) if any dependency is uninitialized or unreachable.

### [10] `internal/metrics/`
* **Prometheus Exporter (`:9090/metrics`):** Observability and alerting telemetry.
* **Role:** Exposes counters and histograms tracking submitted orders, fills, cancellations, latency, and critical alerts such as `cts_unresolved_residuals_total` and `cts_circuit_breaker_tripped_total`.

---

## 4. Summary of Critical System Invariants

1. **Pure Taker:** CTS orders cross existing quotes. Unfilled quantities are cancelled within $\le 2.5$s.
2. **Dynamic Sizing:** Sizing scales with live order-book depth. Static order sizes are prohibited.
3. **Conservative Liability:** BUY uses `PriceCap`; SELL uses $P_{L1.\text{bid}}$ to guarantee notional budgets are never underestimated.
4. **Fail Closed:** Any ambiguity in market depth or RPC outcomes pauses trading rather than guessing.
5. **Safe Shutdown:** In-flight order cleanups are guaranteed to finish before network connections close.
