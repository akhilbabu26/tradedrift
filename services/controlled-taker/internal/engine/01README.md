# Engine Package (`internal/engine`)

The `engine` package is the core execution orchestrator of the Controlled Taker Service. It manages isolated per-market worker goroutines, dynamic depth sizing, safety validation, inventory balancing, circuit breaking, and autonomous residual order cancellation.

---

## 1. Source Files & Architectural Responsibilities

| File | Primary Responsibility |
| :--- | :--- |
| [`engine.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/controlled-taker/internal/engine/engine.go) | Orchestrator managing worker lifecycle, cold-start anti-burst staggering, and graceful shutdown synchronization. |
| [`worker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/controlled-taker/internal/engine/worker.go) | Autonomous execution loop for a single market pair; manages profile selection, jitter intervals, order dispatch, and fill recording. |
| [`order_cleanup.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/controlled-taker/internal/engine/order_cleanup.go) | Autonomous post-submission verification and cancellation of unfilled order remainder (`awaitOrderCleanup`). |
| [`circuit_breaker.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/controlled-taker/internal/engine/circuit_breaker.go) | Three-state (`CLOSED`/`OPEN`/`HALF_OPEN`) circuit breaker, atomic probe reservation, and fail-closed state transitions. |
| [`errors.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/controlled-taker/internal/engine/errors.go) | Submission error classification (`classifySubmissionError`) mapping gRPC status codes to action categories. |
| [`profile.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/controlled-taker/internal/engine/profile.go) | Mathematical dynamic order sizing engine; calculates lot-aligned quantities and conservative ceiling prices directly from live depth. |
| [`selector.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/controlled-taker/internal/engine/selector.go) | Mean-reverting direction selector with inventory bias normalized by USDT notional exposure. |
| [`safety.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/controlled-taker/internal/engine/safety.go) | Pre-order safety boundaries, rolling volume/trade rate limiters, spread constraints, and 1.5x depth ratio verification. |

---

## 2. The 14-Step Execution Pipeline

Every order cycle executed by a `MarketWorker` follows this sequential pipeline:

```
 1. Read Live Depth (Redis)
        ↓
 2. Select Activity Profile (LOW / MID / HIGH)
        ↓
 3. Select Direction (BUY / SELL with Inventory Bias)
        ↓
 4. Calculate Dynamic Sizing & Price Cap (profile.go)
        ↓
 5. Determine Side-Specific Conservative Price
        ↓
 6. Validate Pre-Order Safety Constraints (safety.go)
        ↓
 7. Reserve Circuit Breaker Probe (Atomically locks HALF_OPEN)
        ↓
 8. Dispatch CreateOrder to Order Service (idempotency key: CTS-<market>-<uuidv7>)
        ↓
 9. Handle Submission Result (Classify error: Definite vs. Ambiguous vs. CallerCancelled)
        ↓
10. Execute Idempotency Recovery if Submission was Ambiguous
        ↓
11. Enter Detached Verification & Residual Cleanup Loop (≤ 2.5s)
        ↓
12. Dispatch CancelOrder for Unfilled Residual (Taker-Only Invariant)
        ↓
13. Confirm Terminal State (FILLED or CANCELLED)
        ↓
14. Record Actual Fills, Update Inventory & Prometheus Metrics
```

---

## 3. Dynamic Depth Sizing (`profile.go`)

CTS strictly prohibits static or hardcoded order quantities. Quantities are dynamically calculated from the live top-of-book depth:

### LOW Profile (Heartbeat)
* **Goal:** Continuous organic taker presence with negligible market impact.
* **Sizing:** Consumes **5% to 15%** of L1 depth.
* **Pricing:** Crosses L1 by exactly 1 tick ($P_{\text{cap}} = P_{L1} \pm \text{TickSize}$).
* **Cadence:** 45s – 120s with $\pm 25\%$ randomized jitter.

### MID Profile (Depth Exploration)
* **Goal:** Probes deeper L1 liquidity without crossing multiple levels.
* **Sizing:** Consumes **15% to 35%** of L1 depth.
* **Pricing:** Crosses L1 by exactly 1 tick ($P_{\text{cap}} = P_{L1} \pm \text{TickSize}$).
* **Cadence:** 120s – 360s with $\pm 25\%$ randomized jitter.

### HIGH Profile (Controlled Sweep)
* **Goal:** Creates realistic institutional volume bursts by sweeping multiple reachable book levels within the configured slippage limit (`MAX_SLIPPAGE_BPS`).
* **Sizing:**
  - *Single Level Reachable:* Consumes **50% to 80%** of L1 depth.
  - *Multi-Level Sweep:* Consumes **100% of L1 depth** + **30% to 80%** of subsequent reachable depth levels.
* **Cumulative Depth Guard:** Enforces that reachable depth must be at least **1.5× the target quantity** ($\text{ReachableDepth} \ge 1.5 \times \text{targetQty}$). If depth cannot satisfy this ratio, sizing fails closed with `ErrInsufficientLiquidity`.
* **Cooldown:** Enforces a mandatory cooldown (`HIGH_COOLDOWN = 300s`) between HIGH attempts, regardless of whether the order filled or cancelled with zero fills.

### Lot Alignment & Max Notional Clamping
After raw sizing is calculated:
1. Quantity is constrained to be $\ge \text{market.MinQuantity}$.
2. Quantity is aligned to market lot size:
   $$\text{qty} = \lfloor \text{rawQty} / \text{lotSize} \rfloor \times \text{lotSize}$$
3. Quantity is clamped against `MAX_ORDER_NOTIONAL_USDT` using the conservative execution price. If notional clamping forces quantity below `MinQuantity`, sizing fails closed (`ErrBelowMinQuantity`).

---

## 4. BUY / SELL Conservative Pricing Invariant

CTS uses an asymmetric conservative ceiling price to account for maximum possible financial liability:

* **BUY Orders:** The taker crosses against asks up to the price cap.
  $$\text{ConservativePrice} = P_{\text{cap}}$$
* **SELL Orders:** The taker crosses against bids down to the price cap, meaning the first (and highest) fill occurs at the best bid (L1).
  $$\text{ConservativePrice} = P_{L1.\text{bid}}$$

### Why This Invariant is Critical
1. **Notional Protection:** Prevents exceeding `MAX_ORDER_NOTIONAL_USDT`. Using $P_{\text{cap}}$ for SELL orders would underestimate notional volume exposure.
2. **Safety Budgeting:** Ensures hourly and daily volume accumulators conservatively track exposure.
3. **Inventory Tracking:** DirectionSelector and metrics record inventory exposure based on this upper-bound execution notional.

---

## 5. Residual Order Invariant & Detached Cleanup Loop

> **Fundamental CTS Invariant:**  
> CTS is a pure taker service. It must **never** intentionally leave an unfilled order remainder resting on the order book as a maker.

```
Order Service CreateOrder Response
                 │
                 ▼
Detached Cleanup Context (2.5s Timeout)
                 │
  ┌──────────────┴──────────────┐
  ▼                             ▼
Wait 60ms                 Poll GetOrder
(ME match window)               │
                 ┌──────────────┴──────────────┐
                 ▼                             ▼
        Status: FILLED               Status: OPEN / PARTIAL
                 │                             │
                 │                             ▼
                 │                     Dispatch CancelOrder
                 │                             │
                 │                             ▼
                 │                      Wait up to 2.5s
                 │                      for CANCELLED
                 │                             │
                 └──────────────┬──────────────┘
                                ▼
                   Verify Terminal Status
                  (FILLED or CANCELLED only)
```

### Shutdown Protection
If CTS receives a `SIGTERM` during order submission, the worker's parent context is cancelled. However:
* The post-submission cleanup loop executes in an **independent context** (`context.WithTimeout(context.Background(), 2500*time.Millisecond)`).
* The worker will not terminate until the resting order has been confirmed cancelled or the 2.5s deadline expires.
* In `main.go`, `ordersClient.Close()` is deferred until `engine.Start(ctx)` completes, guaranteeing that the gRPC connection remains alive while detached cleanup finishes.

---

## 6. Circuit Breaker (`circuit_breaker.go`)

Each market worker possesses an isolated `CircuitBreaker`:

```
               Success (probe filled/zero-fill resolved)
             ┌─────────────────────────────────────────┐
             │                                         │
             ▼                                         │
       ┌───────────┐     Failures >= MaxFailures  ┌──────────┐
       │  CLOSED   │─────────────────────────────▶│   OPEN   │
       └───────────┘                              └──────────┘
             ▲                                         │
             │                                         │ Cooldown expired (60s)
             │                                         ▼
             │       Probe Failed                ┌───────────┐
             └───────────────────────────────────│ HALF_OPEN │
                                                 └───────────┘
```

* **`CLOSED`:** Normal operation. Orders are allowed.
* **`OPEN`:** Tripped due to consecutive failures. All order cycles are blocked. Transitions to `HALF_OPEN` after `cooldown = 60s`.
* **`HALF_OPEN`:** Probe state. Allows exactly **one** probe order to test market recovery.
  - `ReserveProbe()` atomically locks the probe slot.
  - If the probe succeeds (even if cancelled with zero fills), the breaker transitions back to `CLOSED`.
  - If the probe fails, the breaker trips back to `OPEN`.

---

## 7. Direction Selector & Inventory Balancing (`selector.go`)

The `DirectionSelector` prevents CTS from accumulating runaway directional inventory risk. Rather than tracking raw coin units, it normalizes inventory exposure by **net USDT notional delta**:

$$\Delta_{\text{USDT}} = \sum \text{Notional}_{\text{BUY}} - \sum \text{Notional}_{\text{SELL}}$$

| Net Exposure ($\Delta_{\text{USDT}}$) | BUY Probability | SELL Probability | Operational Stance |
| :--- | :--- | :--- | :--- |
| $> +25,000$ USDT (`heavyBias`) | 25% | **75%** | Heavy sell bias to liquidate excess base asset. |
| $> +5,000$ USDT (`moderateBias`) | 35% | **65%** | Moderate sell bias. |
| Between $-5,000$ and $+5,000$ | **50%** | **50%** | Neutral random walk. |
| $< -5,000$ USDT | **65%** | 35% | Moderate buy bias. |
| $< -25,000$ USDT | **75%** | 25% | Heavy buy bias to replenish depleted base asset. |

---

## 8. Concurrency & Synchronization Model

* **Market Isolation:** Every market worker runs in its own independent goroutine. A stall or circuit trip on `SOL-USDT` has zero impact on `BTC-USDT`.
* **Cold-Start Anti-Burst Staggering:** In `engine.Start()`, worker initialization is staggered by `WarmupDelay + (i * 5s)` to prevent all workers from hitting Order Service and Redis simultaneously on startup.
* **Thread Safety:**
  - `DirectionSelector.mu` guards the cross-market net notional map.
  - `SafetyManager.mu` guards rolling volume accumulators and rate limits.
  - `CircuitBreaker.mu` guards state transitions and atomic probe reservations.
