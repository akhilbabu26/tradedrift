# TradeDrift Controlled Taker Service (CTS)

The **Controlled Taker Service (CTS)** is an autonomous algorithmic execution service that acts as a pure market taker (`CT-001`) across all supported trading pairs on the TradeDrift exchange.

---

## 1. Purpose

CTS operates as the counterpart to TradeDrift's Market Making / Liquidity Engine (LE). Its core mission is to:
1. **Provide Organic Taker Flow:** Continuously cross the spread against resting institutional maker quotes to simulate realistic retail and algorithmic taker volume.
2. **Drive Real-Time Settlement & Discovery:** Exercise the end-to-end execution pipeline (Order Service $\rightarrow$ Kafka $\rightarrow$ Matching Engine $\rightarrow$ Trade Service $\rightarrow$ Settlement $\rightarrow$ Market Data / Candles).
3. **Trigger Liquidity Engine Replenishment:** Consume resting depth so the Liquidity Engine actively repositions, replenishes, and maintains liquid spreads.
4. **Preserve the Strict Taker Invariant:** Generate taker activity without ever allowing `CT-001` to inadvertently leave unfilled remainders resting on the order book as maker quotes.

---

## 2. Core Responsibilities

* **Live Depth Monitoring:** Continuously streams and validates top-of-book L2 snapshots from Redis (`internal/clients/redisdepth`).
* **Dynamic Depth Sizing:** Dynamically calculates order quantities from current live order-book depth; **hardcoded or static order quantities are strictly forbidden**.
* **Three Taker Profiles:** Executes parameterized `LOW` (heartbeat), `MID` (depth exploration), and `HIGH` (multi-level sweep) profiles.
* **Inventory Balancing:** Tracks net USDT notional exposure and shifts execution probabilities to prevent runaway base-asset accumulation or depletion (`internal/engine/selector.go`).
* **Multi-Layer Safety Verification:** Enforces rolling hourly/daily notional caps, maximum trades per hour, max allowable spreads, and cumulative depth safety multipliers (`internal/engine/safety.go`).
* **Fault-Isolated Circuit Breaking:** Maintains isolated per-market circuit breakers (`CLOSED`, `OPEN`, `HALF_OPEN`) with atomic probe reservation to prevent cascade failures on market disruptions.
* **Idempotency Recovery:** Resolves ambiguous network drops or gRPC timeouts using cryptographic idempotency keys (`CTS-<market>-<uuidv7>`) before taking any action.
* **Autonomous Residual Cancellation:** Polls and cancels any unfilled remainder within a dedicated detached cleanup loop ($\le 2.5$s) before marking an order cycle complete.
* **Graceful Shutdown Protection:** Guarantees that in-flight submitted orders finish residual cancellation before network connections are closed, even under `SIGTERM`.
* **Enterprise Observability:** Exposes detailed per-market HTTP readiness probes (`/readyz`) and Prometheus instrumentation (`:9090/metrics`).

---

## 3. Core Design Principle: Pure Dynamic Sizing

> **Fundamental CTS Architectural Invariant:**  
> CTS does NOT use predefined, fixed, or hardcoded order quantities.

Order size is calculated strictly as a dynamic function of **current, validated order-book depth**:
* If top-of-book depth is $0.20$ BTC, a `LOW` order will take $0.01$ – $0.03$ BTC.
* If top-of-book depth deepens to $5.00$ BTC, a `LOW` order will automatically scale to $0.25$ – $0.75$ BTC.
* If depth is unseeded, stale, crossed, or insufficient to satisfy the 1.5× safety multiplier, **CTS fails closed and submits nothing**.

---

## 4. Taker Activity Profiles

| Profile | Sizing Algorithm | Price Cap ($P_{\text{cap}}$) | Cadence / Jitter | Market Impact & Purpose |
| :--- | :--- | :--- | :--- | :--- |
| **`LOW`** | **5% to 15%** of L1 depth | $P_{L1} \pm 1 \text{ tick}$ | $45\text{s} - 120\text{s}$ ($\pm 25\%$) | **Heartbeat:** Continuous low-impact taker presence; establishes base candle trades without eroding book depth. |
| **`MID`** | **15% to 35%** of L1 depth | $P_{L1} \pm 1 \text{ tick}$ | $120\text{s} - 360\text{s}$ ($\pm 25\%$) | **Liquidity Probe:** Tests market maker quote replenishment speed and depth responsiveness. |
| **`HIGH`** | • Single-level: **50% to 80%** of L1<br>• Multi-level: **100% of L1 + 30% to 80%** of subsequent depth | Deepest reachable level within `MAX_SLIPPAGE_BPS` | $900\text{s} - 2700\text{s}$ ($\pm 25\%$) | **Institutional Sweep:** Simulates aggressive taker sweeps across multiple book levels. Enforces **300s cooldown** and **1.5× depth multiplier**. |

---

## 5. End-to-End Execution Flow

```
                      1. Read Live Depth (Redis: "depth:<market_id>")
                                     │
                                     ▼
                      2. Validate Snapshot Integrity & Freshness
                                     │
                                     ▼
                      3. Select Activity Profile (LOW / MID / HIGH)
                                     │
                                     ▼
                      4. Select Side (BUY / SELL with USDT Inventory Bias)
                                     │
                                     ▼
                      5. Compute Dynamic Size & Side-Specific Price Cap
                                     │
                                     ▼
                      6. Pre-Order Safety Validation (safety.go)
                         (Spread %, Hourly/Daily Caps, 1.5x Depth Ratio)
                                     │
                                     ▼
                      7. Reserve Circuit Breaker Probe (if HALF_OPEN)
                                     │
                                     ▼
                      8. Submit LIMIT Crossing Order to Order Service
                         (Idempotency Key: CTS-<market>-<uuidv7>)
                                     │
                         ┌───────────┴───────────┐
                         ▼                       ▼
                   CreateOrder OK          RPC Timeout / Ambiguous
                         │                       │
                         │                       ▼
                         │               Idempotency Recovery
                         │            (FindOrderByIdempotencyKey)
                         │                       │
                         │         ┌─────────────┴─────────────┐
                         │         ▼                           ▼
                         │    Order Found                Order Not Found
                         │         │                           │
                         │         │                           ▼
                         │         │                      Fail Closed
                         │         │                  (Trip Breaker / Skip)
                         │         │                           │
                         └─────────┬───────────────────────────┘
                                   │
                                   ▼
                9. Detached Post-Submission Cleanup (≤ 2.5s)
                   Wait 60ms for Matching Engine cross
                                   │
                   ┌───────────────┴───────────────┐
                   ▼                               ▼
            Status: FILLED               Status: OPEN / PARTIAL
                   │                               │
                   │                               ▼
                   │                      Dispatch CancelOrder
                   │                      for Unfilled Residual
                   │                               │
                   │                               ▼
                   │                       Poll until CANCELLED
                   │                               │
                   └───────────────┬───────────────┘
                                   │
                                   ▼
              10. Verify Terminal Status (FILLED or CANCELLED)
                                   │
                                   ▼
              11. Update Net Inventory, Record Metrics & Cooldown
```

---

## 6. BUY / SELL Conservative Pricing Invariant

CTS uses an asymmetric conservative ceiling price to account for maximum possible financial liability:

* **BUY Orders:** Executes against asks up to the price cap.
  $$\text{ConservativePrice} = P_{\text{cap}}$$
* **SELL Orders:** Crosses against bids down to the price cap; the highest fill occurs at the best bid ($P_{L1.\text{bid}}$).
  $$\text{ConservativePrice} = P_{L1.\text{bid}}$$

### Why This Invariant Matters
1. **Notional Cap Enforcement:** Guarantees `MAX_ORDER_NOTIONAL_USDT` is never breached. Using $P_{\text{cap}}$ for SELL orders would underestimate financial exposure.
2. **Safety Windows:** Hourly and daily volume accumulators conservatively track risk.
3. **Accounting Accuracy:** Inventory tracking and Prometheus metrics record exposure based on this upper bound.

---

## 7. Residual Order Invariant & Post-Submission Cleanup

> **The Taker-Only Invariant:**  
> CT-001 is strictly a taker. An unfilled order residual must **never** remain resting on the order book as a maker.

1. **Autonomous Cancellation:** Once an order is submitted, the worker enters an independent cleanup context (`2.5s` timeout).
2. **Matching Engine Cross Window:** A deliberate `60ms` initial delay allows the Matching Engine to execute against resting MM quotes.
3. **Active Residual Elimination:** If the order is `OPEN` or `PARTIALLY_FILLED`, CTS dispatches `CancelOrder` to cancel the remainder.
4. **Terminal Verification:** CTS continuously polls `GetOrder` until the order achieves a verified terminal status (`FILLED` or `CANCELLED`).
5. **Fail-Closed Alarm:** If the 2.5s deadline expires without confirmed cancellation, CTS increments the critical P0 counter `cts_unresolved_residuals_total` and trips the circuit breaker to `OPEN`.

---

## 8. Graceful Shutdown & Dependency Lifetime Invariant

When `SIGINT` or `SIGTERM` is received:
1. Root context cancellation signals all market workers to halt scheduling new cycles.
2. If an order was already submitted, it completes its detached cleanup loop ($\le 2.5$s) independently of the parent cancellation.
3. The engine orchestrator blocks on `sync.WaitGroup.Wait()` until all workers confirm zero in-flight orders.
4. Only after all workers exit are external clients (`ordersClient.Close()`, `redisReader.Close()`) terminated via LIFO defer unwinding in `main.go`.

---

## 9. Circuit Breaker State Machine

Each market worker runs an isolated three-state `CircuitBreaker`:

```
               Success (Probe filled or zero-fill cancelled)
             ┌──────────────────────────────────────────────┐
             │                                              │
             ▼                                              │
       ┌───────────┐          Failures >= 5           ┌──────────┐
       │  CLOSED   │─────────────────────────────────▶│   OPEN   │
       └───────────┘                                  └──────────┘
             ▲                                              │
             │                                              │ Cooldown expired (60s)
             │                                              ▼
             │            Probe Failed                ┌───────────┐
             └────────────────────────────────────────│ HALF_OPEN │
                                                      └───────────┘
```

* **`CLOSED`:** Normal operation.
* **`OPEN`:** Tripped after `CIRCUIT_BREAKER_FAILURES` (default: 5) consecutive errors. Order cycles are blocked. Transitions to `HALF_OPEN` after 60s cooldown.
* **`HALF_OPEN`:** Allows exactly **one** probe order to test market recovery. `ReserveProbe()` atomically locks the slot. If successful (even if cancelled with zero fills), it transitions to `CLOSED`. If it fails, it trips back to `OPEN`.

---

## 10. Depth Snapshot Integrity & Freshness Checks

Every Redis snapshot read (`depth:<market_id>`) must pass 6 fail-closed checks in `reader.go`:
1. **Market ID Match:** Payload market matches requested key.
2. **Timestamp Non-Empty & Parseable:** RFC3339 / RFC3339Nano parsed.
3. **Staleness Guard:** Timestamp age must be $\le 5.0$ seconds.
4. **Clock Skew Guard:** Future timestamp must not exceed $1.0$ second.
5. **Monotonicity:** Bids must be strictly descending ($P_i < P_{i-1}$); asks must be strictly ascending ($P_i > P_{i-1}$); all prices and quantities $> 0$.
6. **Crossed Book Guard:** Best ask must be strictly greater than best bid ($P_{\text{best\_ask}} > P_{\text{best\_bid}}$).

---

## 11. Health & Observability

* **`/healthz` (`:8080`):** Liveness probe; returns `200 OK` while process is alive.
* **`/readyz` (`:8080`):** Readiness probe; verifies Redis ping, Order Service ping, and live depth freshness across all markets. Fails closed (`503`) if any core client is nil or unreachable.
* **Prometheus Metrics (`:9090/metrics`):** Exposes counters for orders submitted, fills, failures, latency histograms, and the critical `cts_unresolved_residuals_total` alert counter.

---

## 12. External Dependencies

| Dependency | Connection | Purpose |
| :--- | :--- | :--- |
| **Order Service** | gRPC (`orderv1`) | Order creation, status polling, residual cancellation, idempotency lookup. |
| **Redis** | TCP (`go-redis/v9`) | Read-only L2 order book depth stream (`depth:<market_id>`). |
| **Matching Engine** | Indirect (Kafka) | Executes crossing limit orders against resting liquidity. |
| **Wallet Service** | Indirect (Order Service) | Authorizes and holds reserves for `CT-001` (`00000000-0000-0000-0000-000000000002`). |

---

## 13. Critical Developer Invariants

Future developers maintaining CTS **must preserve** the following invariants:

1. **NEVER use hardcoded order sizes:** Sizing must always be calculated dynamically from current depth.
2. **NEVER trade on stale depth:** If Redis depth is older than 5.0 seconds, fail closed.
3. **NEVER assume `CreateOrder` means execution:** Always verify status via `GetOrder` and cancel residuals.
4. **NEVER leave resting maker orders:** Any unfilled remainder must be cancelled via `CancelOrder`.
5. **NEVER re-issue orders on ambiguous timeouts:** Always query `FindOrderByIdempotencyKey` first.
6. **NEVER bypass Order Service:** Do not publish directly to Matching Engine or Kafka.
7. **NEVER close network clients before workers finish cleanup:** Respect the graceful shutdown lifecycle.
8. **NEVER disable circuit breaker protection:** Faults must remain isolated to their respective market pair.

---

## 14. Directory Structure

```
services/controlled-taker/
├── cmd/
│   └── server/          # Entrypoint (main.go), lifecycle, startup/shutdown orchestration
├── internal/
│   ├── account/         # CT-001 canonical system account UUID constants
│   ├── clients/         # External dependency abstraction
│   │   ├── orderservice/# Order Service gRPC client & idempotency recovery
│   │   └── redisdepth/  # Redis L2 depth reader & integrity validator
│   ├── config/          # Environment configuration, defaults, and startup validation
│   ├── engine/          # Taker engine, market workers, profiles, selector, safety & circuit breaker
│   ├── health/          # HTTP /healthz and /readyz probe server
│   └── metrics/         # Prometheus collectors & HTTP /metrics scrape server
├── Dockerfile           # Multi-stage production container build
├── go.mod               # Go module definition
└── README.md            # Main CTS documentation (this file)
```
