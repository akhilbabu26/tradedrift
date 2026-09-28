# Controlled Taker Service (CTS) — Core Execution & Lifecycle Flows

This document details the eight core architectural flows implemented in the TradeDrift **Controlled Taker Service (CTS)**. Every flow is grounded in the active codebase and describes how CTS safely executes autonomous market taking against resting liquidity without ever violating the platform's strict **Taker-Only Invariant**.

---

## Table of Contents
1. [Service Startup & Initialization Flow](#1-service-startup--initialization-flow)
2. [Market Depth → Taker Decision Flow](#2-market-depth--taker-decision-flow)
3. [Direction / Inventory-Balance Flow](#3-direction--inventory-balance-flow)
4. [Safety Validation & Circuit-Breaker Flow](#4-safety-validation--circuit-breaker-flow)
5. [Order Submission Flow](#5-order-submission-flow)
6. [Execution Verification & Order-Cleanup Flow](#6-execution-verification--order-cleanup-flow)
7. [Ambiguous Submission / Idempotency-Recovery Flow](#7-ambiguous-submission--idempotency-recovery-flow)
8. [Observability / Failure / Shutdown Flow](#8-observability--failure--shutdown-flow)

---

## 1. Service Startup & Initialization Flow

The startup lifecycle establishes connections to platform infrastructure, asserts strict configuration safety invariants, and spawns isolated, staggered workers for each configured market pair.

### Architectural Diagram

```
                             [ OS Process Boot ]
                                      │
                                      ▼
                      1. platformconfig.LoadEnv()
                         (Reads root & service .env)
                                      │
                                      ▼
                      2. zap.NewProductionConfig()
                         (Initializes RFC3339 Logger)
                                      │
                                      ▼
                      3. config.Load() & config.Validate()
                         • Structural Invariants: MinInterval <= Base <= Max
                         • Volume Invariants: MaxDaily >= MaxHourly > 0
                         • Policy Ceiling: MaxSlippageBps <= 500
                         • Asset & Market definitions validation
                                      │
                                      ▼
                      4. redisdepth.NewReader(cfg.RedisAddr)
                         (Connects to Redis & validates Ping)
                                      │
                                      ▼
                      5. orderservice.NewClient(cfg.OrderGRPCAddr)
                         (Establishes gRPC channel & verifies Ping)
                                      │
                                      ▼
                      6. health.NewServer() & metrics.NewServer()
                         • Binds :8080 (/healthz, /readyz)
                         • Binds :9090 (/metrics)
                                      │
                                      ▼
                      7. engine.New(cfg, ordersClient, redisReader)
                         • Creates DirectionSelector with USDT thresholds
                         • Initializes per-market MarketWorker instances
                                      │
                                      ▼
                      8. engine.Start(ctx)
                         • Cold-boot stagger: delay = WarmupDelay + (i * 5s)
                         • Spawns autonomous goroutine per market
```

### Key Initialization Steps

1. **Environment & Configuration Loading (`cmd/server/main.go`):**
   - Calls `platformconfig.LoadEnv()` to auto-populate environment variables from `.env` files.
   - Executes `config.Load()`, reading environment parameters and applying defaults.
   - Invokes `cfg.Validate()`. If any volume limit is non-positive, if daily volume is less than hourly volume, if slippage exceeds `V1MaxAllowedSlippageBps (500 bps)`, or if market tick/lot sizes are invalid, the process terminates immediately (`logger.Fatal`).

2. **Client Initialization:**
   - Connects to the Redis depth instance using `redisdepth.NewReader()`. Runs an initial `Ping()` with a 3.0s timeout.
   - Connects to the Order Service gRPC server using `orderservice.NewClient()`. Dials the remote endpoint and asserts client readiness.

3. **HTTP Server Spawning:**
   - Launches `health.Server` on `:8080`, exposing `/healthz` (liveness) and `/readyz` (readiness).
   - Launches `metrics.Server` on `:9090`, exposing the Prometheus scrape handler `/metrics`.

4. **Engine Startup & Worker Staggering (`internal/engine/engine.go`):**
   - If `cfg.Enabled` is `false`, the engine logs a notice and skips worker startup.
   - When enabled, `engine.Start(ctx)` loops through all configured markets (`BTC-USDT`, `ETH-USDT`, `SOL-USDT`).
   - **Anti-Burst Staggering:** To avoid cold-boot load spikes against Redis and Order Service, each worker startup is delayed by:
     $$\text{StartupDelay} = \text{WarmupDelay} + (i \times 5\text{s})$$
     Worker 0 starts after 15s, Worker 1 after 20s, Worker 2 after 25s.

---

## 2. Market Depth → Taker Decision Flow

CTS calculates order size strictly from live, current top-of-book depth. **Hardcoded or static order quantities are strictly forbidden.**

### Architectural Diagram

```
                 [ Worker Timer Fires ]
                           │
                           ▼
          1. Read Depth from Redis ("depth:<market_id>")
             (Timeout: 500ms)
                           │
                           ▼
          2. Execute 6-Step Depth Validation:
             • Snapshot exists in Redis?
             • MarketID matches payload?
             • Timestamp non-empty & parseable?
             • Timestamp age <= 5.0s (Staleness)?
             • Clock skew <= 1.0s (Future timestamp)?
             • Bids & Asks non-empty?
             • Bids descending & Asks ascending?
             • Best Ask > Best Bid (Not crossed)?
                           │
                ┌──────────┴──────────┐
               FAIL                  PASS
                │                     │
                ▼                     ▼
         Skip Cycle &         3. Determine Profile:
         Record Failure       • HIGH: 8% (if off 300s cooldown)
                              • MID:  27%
                              • LOW:  65% (baseline)
                                      │
                                      ▼
                              4. Compute Dynamic Sizing (profile.go):
                              • LOW:  5% - 15% of L1 Depth
                              • MID:  15% - 35% of L1 Depth
                              • HIGH: Multi-level sweep (1.5x depth ratio)
                                      │
                                      ▼
                              5. Calculate Price Cap & Conservative Price:
                              • BUY:  PriceCap = L1.Price + TickSize (or sweep limit)
                                      ConservativePrice = PriceCap
                              • SELL: PriceCap = L1.Price - TickSize (or sweep limit)
                                      ConservativePrice = L1.BidPrice
                                      │
                                      ▼
                              6. Lot Align & Min Quantity Clamp:
                              • qty = floor(rawQty / LotSize) * LotSize
                              • Clamp to MAX_ORDER_NOTIONAL_USDT
```

### Detailed Decision Logic

1. **Redis Depth Retrieval & Validation (`internal/clients/redisdepth/reader.go`):**
   - Fetches key `depth:<market_id>` with a 500ms context timeout.
   - Validates that the payload is fresh ($< 5.0$s old). If the market maker or market data service stops publishing, CTS immediately aborts the cycle with `ErrStaleSnapshot`.
   - Confirms the book is uncrossed ($\text{bestAsk} > \text{bestBid}$).

2. **Profile Selection (`chooseProfile` in `worker.go`):**
   - Evaluates `lastHighAt`. If `time.Since(lastHighAt) >= 300s` (`HIGH_COOLDOWN`), HIGH profile is eligible.
   - Generates a pseudo-random float $r \in [0, 1)$:
     - If eligible and $r < 0.08$ $\rightarrow$ **`HIGH`** (8% probability).
     - Else if $r < 0.35$ $\rightarrow$ **`MID`** (27% probability).
     - Else $\rightarrow$ **`LOW`** (65% baseline heartbeat).

3. **Mathematical Sizing & Guardrails (`internal/engine/profile.go`):**
   - **`LOW`:** Multiplies L1 quantity by randomized ratio $\in [0.05, 0.15]$. Crosses book by 1 tick.
   - **`MID`:** Multiplies L1 quantity by randomized ratio $\in [0.15, 0.35]$. Crosses book by 1 tick.
   - **`HIGH`:** Sweeps all reachable levels within `MAX_SLIPPAGE_BPS` (15 bps). Calculates $\text{totalReachableDepth}$. Enforces that reachable depth satisfies the **1.5× safety multiplier**:
     $$\text{ReachableDepth} \ge 1.5 \times \text{targetQty}$$
     If not satisfied, sizing fails closed with `ErrInsufficientLiquidity`.

4. **Lot Alignment & Notional Cap:**
   - Rounds quantity down to exact multiples of `market.LotSize`.
   - Constrains quantity $\ge \text{market.MinQuantity}$.
   - Evaluates $\text{notional} = \text{qty} \times \text{ConservativePrice}$. If notional exceeds `MAX_ORDER_NOTIONAL_USDT`, quantity is capped. If capping produces a quantity below `MinQuantity`, sizing fails closed (`ErrBelowMinQuantity`).

---

## 3. Direction / Inventory-Balance Flow

CTS maintains long-term neutral positioning to avoid accumulating directional market exposure. Rather than tracking raw coin units, it normalizes inventory exposure to **net USDT notional delta**.

### Architectural Diagram

```
                 [ Sizing Calculated ]
                           │
                           ▼
          1. Query DirectionSelector.GetNetNotionalDelta(marketID)
             Delta = Sum(Notional_BUY) - Sum(Notional_SELL)
                           │
                           ▼
          2. Evaluate Against Configured USDT Thresholds:
             • Delta > +$25,000 (Heavy Long)      ──▶ 75% SELL / 25% BUY
             • Delta >  +$5,000 (Moderate Long)   ──▶ 65% SELL / 35% BUY
             • -$5,000 <= Delta <= +$5,000 (Flat) ──▶ 50% SELL / 50% BUY
             • Delta <  -$5,000 (Moderate Short)  ──▶ 65% BUY  / 35% SELL
             • Delta < -$25,000 (Heavy Short)     ──▶ 75% BUY  / 25% SELL
                           │
                           ▼
          3. Generate Random Roll (rand.Float64() < BuyProbability)
                           │
                ┌──────────┴──────────┐
              TRUE                  FALSE
                │                     │
                ▼                     ▼
          Direction = BUY       Direction = SELL
```

### Mechanics & Invariants

* **Cross-Market Normalization:** Tracking raw coin quantities (e.g., 2 BTC vs. 20 SOL) distorts financial risk. By multiplying filled quantities by the conservative price in USDT, the inventory balance thresholds (`$5,000` moderate / `$25,000` heavy) exert consistent economic risk pressure across all trading pairs.
* **Mean Reversion:** When CT-001 has bought more than it sold, the probability shifts toward `SELL` to offload base asset back into market maker quotes.
* **Atomic Tracking:** Exposure updates occur only upon verified order fills (`selector.RecordFill`), protected by a mutex.

---

## 4. Safety Validation & Circuit-Breaker Flow

Before an order can be dispatched to Order Service, it must pass through the centralized `SafetyManager` pre-trade gate.

### Architectural Diagram

```
                 [ Pre-Order Safety Check ]
                             │
                             ▼
         1. CircuitBreaker.Allow()
            • CLOSED    ──▶ Allow
            • OPEN      ──▶ Block (Cooldown check)
            • HALF_OPEN ──▶ Allow only if ProbeNotInFlight
                             │
                             ▼
         2. Rolling Window Refresh (Hourly & Daily)
                             │
                             ▼
         3. Rate Limiting Checks:
            • sm.hourlyTrades >= MAX_TRADES_PER_HOUR (60) ──▶ REJECT
            • sm.hourlyNotional + notional > MAX_HOURLY   ──▶ REJECT
            • sm.dailyNotional + notional > MAX_DAILY     ──▶ REJECT
                             │
                             ▼
         4. Spread Sanity Check:
            • Spread = (bestAsk - bestBid) / midPrice
            • Spread > MAX_SPREAD_PERCENT (1.5%)          ──▶ REJECT
                             │
                             ▼
         5. Cumulative Depth Multiplier Check:
            • ReachableDepth >= 1.5 * targetQty           ──▶ REJECT if violated
                             │
                             ▼
         6. Atomic Probe Reservation (if HALF_OPEN):
            • ReserveProbe() locks probe slot
                             │
                  ┌──────────┴──────────┐
                PASS                   FAIL
                  │                      │
                  ▼                      ▼
           Dispatch Order        Skip Cycle & Release Probe
```

### Safety Rules Enforced

1. **Spread Guard:** If market makers pull liquidity and the spread widens beyond `1.5%`, CTS halts trading to avoid crossing wide spreads.
2. **Rate Limiting:** Protects the Matching Engine from message flooding; limits taker orders to a maximum of 60 trades per hour per market.
3. **Volume Budgeting:** Prevents CTS from exceeding configured hourly (`$100,000`) and daily (`$1,500,000`) financial budgets.
4. **Cumulative Depth Multiplier:** Guarantees that CTS never sweeps more than $\frac{2}{3}$ of available liquidity at any targeted level, preventing sudden book evaporation.
5. **Atomic Probe Reservation:** In `HALF_OPEN` state, `ReserveProbe()` atomically acquires exclusive right to dispatch a single test order.

---

## 5. Order Submission Flow

Order submission transforms internal sizing parameters into a deterministic, signed gRPC request dispatched to TradeDrift's Order Service.

### Architectural Diagram

```
                 [ Sizing & Safety Approved ]
                             │
                             ▼
          1. Generate UUIDv7 & Idempotency Key:
             idempotencyKey = "CTS-" + marketID + "-" + uuidv7
                             │
                             ▼
          2. Assemble orderv1.CreateOrderRequest:
             • UserId:         account.WalletUUIDStr (CT-001)
             • MarketId:       market.MarketID
             • Side:           BUY / SELL
             • Type:           ORDER_TYPE_LIMIT
             • TimeInForce:    TIME_IN_FORCE_GTC
             • Price:          params.PriceCap.String()
             • Quantity:       params.Quantity.String()
             • IdempotencyKey: idempotencyKey
                             │
                             ▼
          3. Dispatch gRPC CreateOrder (orderCtx timeout: 3s)
                             │
                             ▼
          4. Order Service Processing:
             • Verifies CT-001 wallet balance
             • Writes to PostgreSQL 'orders' table
             • Writes to Transactional Outbox
             • Publishes order event to Kafka
                             │
                             ▼
          5. Receive orderv1.CreateOrderResponse
             • Validate response != nil && order != nil
             • Extract OrderID and initial Status (PENDING)
```

### Submission Invariants

* **System Account Identification:** The request explicitly passes `account.WalletUUIDStr` (`"00000000-0000-0000-0000-000000000002"`), linking execution to the pre-seeded platform taker balance.
* **Deterministic Idempotency Key:** Prefix `CTS-<market>-<uuidv7>` guarantees absolute uniqueness while encoding the initiating market and a millisecond timestamp in the UUIDv7.
* **Aggressive Crossing Price:** Orders are submitted as `LIMIT` crossing orders with `PriceCap`. They are designed to immediately cross existing book levels, never to sit as passive maker liquidity.

---

## 6. Execution Verification & Order-Cleanup Flow (`internal/engine/order_cleanup.go`)

> **The Taker-Only Invariant:**  
> Once an aggressive limit order is submitted, any unfilled remainder must **never** be permitted to rest on the order book as maker liquidity. CTS autonomously verifies execution and cancels residuals.

### Architectural Diagram

```
                 [ CreateOrder Succeeded ]
                             │
                             ▼
          1. Enter Detached Cleanup Context
             cleanupCtx = context.WithTimeout(Background, 2500ms)
                             │
                             ▼
          2. Match Window Delay (60ms)
             (Allows ME to cross against resting quotes)
                             │
                             ▼
          3. Polling & Status Verification Loop:
                             │
                             ▼
                     GetOrder(cleanupCtx, orderID)
                             │
              ┌──────────────┼──────────────┐
              ▼              ▼              ▼
           FILLED        CANCELLED    OPEN / PARTIAL
              │              │              │
              │              │              ▼
              │              │      Dispatch CancelOrder
              │              │      (cancelSent = true)
              │              │              │
              │              │              ▼
              │              │      Poll again (50ms)
              │              │      until CANCELLED
              │              │              │
              └──────────────┬──────────────┘
                             ▼
          4. Terminal State Confirmed (FILLED or CANCELLED)
                             │
                             ▼
          5. Financial & State Accounting:
             • filledQty = orderState.FilledQty
             • notional = filledQty * ConservativePrice
             • RecordOrderResolved(filledQty, ConservativePrice)
             • Update DirectionSelector inventory delta
             • Increment Prometheus metric counters
```

### Cleanup Pipeline Rules

1. **Detached Context:** The cleanup loop runs under `context.Background()` with a `2.5s` timeout. If the worker's parent context is cancelled due to a service `SIGTERM`, the cleanup loop **does not abort**. It continues until the order is cancelled.
2. **60ms Matching Window:** A brief initial pause ensures the Matching Engine has processed the Kafka event and matched quotes before CTS asks to cancel.
3. **Loop Polling:** Queries status every 50ms. If `OPEN` or `PARTIALLY_FILLED`, it sends `CancelOrder` and waits for terminal `CANCELLED` status.
4. **Terminal Failure Protection:** If the 2.5s cleanup deadline expires without confirming terminal status, CTS logs a critical error, increments `cts_unresolved_residuals_total`, and trips the circuit breaker to `OPEN` to prevent further trading.

---

## 7. Ambiguous Submission / Idempotency-Recovery Flow

Network drops, RPC timeouts, or proxy disconnections during `CreateOrder` present severe risk: the order may or may not have reached the database. Treating an ambiguous failure as "not created" can orphan an unmonitored maker order on the matching book.

### Architectural Diagram

```
                 [ CreateOrder RPC Error ]
                             │
                             ▼
          1. classifySubmissionError(ctx, err)
                             │
       ┌─────────────────────┼─────────────────────┐
       ▼                     ▼                     ▼
Deterministic       Caller Cancelled           Ambiguous Error
Rejection          (Shutdown triggered)      (Deadline, Unavailable,
(InvalidArgument,            │                 codes.Canceled, etc.)
Precondition)                ▼                             │
       │             Detached Check:                       ▼
       │             FindOrderByIdempotencyKey     Detached Recovery:
       │                     │                     FindOrderByIdempotencyKey
       │              ┌──────┴──────┐              (PageSize: 50, MaxPages: 3)
       │              ▼             ▼                      │
       │          Committed?   Not Committed?              │
       │              │             │                      │
       │              │             ▼                      │
       │              │       Release Probe                │
       │              │       & Clean Exit                 │
       │              │                                    │
       ▼              ▼                                    ▼
  Skip Cycle      Recover OrderID                 Inspect Recovery Result:
  & Release       & Proceed to                    • Order Found (Committed)
  Probe           Residual Cleanup                • Order Not Found (Uncommitted)
                                                  • Lookup Failed (Unknown)
                                                           │
                                   ┌───────────────────────┼───────────────────────┐
                                   ▼                       ▼                       ▼
                              Order Found            Not Found              Lookup Failed
                                   │                       │                       │
                                   ▼                       ▼                       ▼
                            Recover OrderID           Fail Closed             Fail Closed
                            & Proceed to              RecordFailure()         RecordFailure()
                            Residual Cleanup          ReleaseProbe()          Trip Breaker (OPEN)
                                                                              UnresolvedResidual++
```

### Detailed Recovery Protocol

1. **Error Classification:**
   - **Deterministic:** Status codes `InvalidArgument`, `FailedPrecondition`, `NotFound`, `PermissionDenied`. The order was definitively rejected by Order Service validation. Recovery is skipped; breaker probe is released.
   - **Caller Cancelled:** If `parentCtx.Err() != nil`, CTS shutdown caused the cancellation. A detached lookup checks if the order was committed before the signal. If committed, it cancels the residual; if not, it exits cleanly.
   - **Ambiguous:** Status codes `DeadlineExceeded`, `Unavailable`, or transport drops. The outcome is unknown.

2. **Keyset Cursor Idempotency Lookup (`FindOrderByIdempotencyKey`):**
   - Queries Order Service with `UserId: account.WalletUUIDStr` and `MarketId`.
   - Order Service sorts orders using `ORDER BY created_at DESC, id DESC`.
   - Queries up to 3 pages with `Limit: 50` (up to 150 recent orders).
   - If a matching `idempotency_key` is found $\rightarrow$ recovers the `OrderID` and seamlessly transitions into the [Residual Cleanup Flow](#6-execution-verification--residual-cleanup-flow).
   - If exhaustively absent after 3 pages $\rightarrow$ authoritatively confirms the order never reached the database. Fails closed safely.
   - If the lookup itself times out $\rightarrow$ outcome is unresolvable. Increments `cts_unresolved_residuals_total` and trips the circuit breaker to `OPEN`.

---

## 8. Observability / Failure / Shutdown Flow

The observability and lifecycle management system guarantees operational transparency and deterministic shutdown ordering.

### Architectural Diagram

```
┌────────────────────────────────────────────────────────────────────────┐
│                        Observability Infrastructure                    │
│                                                                        │
│   HTTP :8080                                     HTTP :9090           │
│   ┌───────────────────────────────┐              ┌──────────────────┐  │
│   │ /healthz  ──▶ 200 OK          │              │ /metrics         │  │
│   │ /readyz   ──▶ Multi-tier check│              │ (Prometheus)     │  │
│   │               • Redis Ping    │              └──────────────────┘  │
│   │               • OrderClient   │                                    │
│   │               • Depth Freshness                                    │
│   └───────────────────────────────┘                                    │
└────────────────────────────────────────────────────────────────────────┘

                                  Shutdown
                                     │
                                     ▼
                          SIGINT / SIGTERM Caught
                                     │
                                     ▼
                       rootCtx Cancel Triggered
                                     │
                     ┌───────────────┴───────────────┐
                     ▼                               ▼
         Workers Halt Scheduling           In-Flight Orders Complete
               New Cycles                  Detached Cleanup (≤ 2.5s)
                                                     │
                                                     ▼
                                          All Workers Exit Loop
                                                     │
                                                     ▼
                                              engine.wg.Wait()
                                                     │
                                                     ▼
                                          Health & Metrics Stop()
                                                     │
                                                     ▼
                                          ordersClient.Close()
                                                     │
                                                     ▼
                                           redisReader.Close()
                                                     │
                                                     ▼
                                              Process Exit (0)
```

### Shutdown Invariant: External Client Lifetime

A critical defect in naive worker architectures is closing network connections while in-flight cleanup is underway. In CTS:

1. `main.go` registers `defer ordersClient.Close()` and `defer redisReader.Close()`.
2. `engine.Start(ctx)` blocks until `wg.Wait()` unblocks.
3. When `SIGTERM` arrives, workers finish their active `cleanupCtx` (up to 2.5s) and call `defer wg.Done()`.
4. `engine.Start` returns.
5. Only then do the deferred client `Close()` calls execute.

**Result:** The gRPC connection to Order Service is guaranteed to remain healthy and open until every submitted crossing order has verified its terminal status and cancelled its residual.
