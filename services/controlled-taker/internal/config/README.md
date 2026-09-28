# Configuration Package (`internal/config`)

The `config` package defines, loads, and strictly validates all operational, architectural, and safety parameters for the Controlled Taker Service.

---

## 1. Architectural Responsibility

CTS is governed by strict mathematical, volumetric, and timing guardrails. The `config` package:
1. Loads settings from environment variables using the shared [`platform/config`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/platform/config) module.
2. Applies safe, production-calibrated defaults for local development.
3. Performs fail-fast validation at startup, ensuring CTS refuses to boot if any invariant or safety boundary is violated.

---

## 2. Configuration Parameters & Environment Variables

### Operational & Network
| Env Variable | Default | Type | Purpose |
| :--- | :--- | :--- | :--- |
| `CTS_ENABLED` | `true` | `bool` | Master switch to enable or pause CTS worker execution. |
| `ORDER_GRPC_ADDR` | `"localhost:50053"` | `string` | Target address of the Order Service gRPC server. |
| `REDIS_ADDR` | `"localhost:6379"` | `string` | Target address of the Redis depth snapshot instance. |
| `HEALTH_PORT` | `"8080"` | `string` | HTTP port for `/healthz` and `/readyz` probes. |
| `METRICS_PORT` | `"9090"` | `string` | HTTP port for the Prometheus `/metrics` scraper. |
| `LOG_LEVEL` | `"info"` | `string` | Zap logging level (`debug`, `info`, `warn`, `error`). |

### Volumetric & Risk Boundaries
| Env Variable | Default | Type | Purpose |
| :--- | :--- | :--- | :--- |
| `MAX_ORDER_NOTIONAL_USDT` | `10000.00` | `Decimal` | Maximum single-order notional exposure in USDT. |
| `MAX_HOURLY_VOLUME_USDT` | `100000.00` | `Decimal` | Rolling 1-hour total executed notional ceiling. |
| `MAX_DAILY_VOLUME_USDT` | `1500000.00` | `Decimal` | Rolling 24-hour total executed notional ceiling. |
| `MAX_TRADES_PER_HOUR` | `60` | `int` | Maximum number of executed orders permitted per hour. |
| `MAX_SPREAD_PERCENT` | `0.015` (1.5%) | `Decimal` | Maximum allowable bid/ask spread percentage before trading halts. |
| `MAX_SLIPPAGE_BPS` | `15` (0.15%) | `int` | Crossing slippage limit applied against top-of-book quotes. |
| `CIRCUIT_BREAKER_FAILURES` | `5` | `int` | Consecutive submission failures required to trip breaker to `OPEN`. |
| `WARMUP_DELAY` | `15s` | `Duration` | Base cold-boot pause before workers begin submitting orders. |
| `HIGH_COOLDOWN` | `300s` (5m) | `Duration` | Cooldown period enforced between HIGH profile sweeps. |

### Inventory Bias Exposure Thresholds
| Env Variable | Default | Type | Purpose |
| :--- | :--- | :--- | :--- |
| `CTS_INVENTORY_MODERATE_BIAS_USDT` | `5000.00` | `Decimal` | Exposure delta threshold where direction shifts to 65% counter-bias. |
| `CTS_INVENTORY_HEAVY_BIAS_USDT` | `25000.00` | `Decimal` | Exposure delta threshold where direction shifts to 75% counter-bias. |

---

## 3. Pre-Seeded Default Markets

CTS initializes with three standard platform markets:

| Market ID | Base | Quote | Tick Size | Lot Size | Min Quantity | Partition |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| `BTC-USDT` | `BTC` | `USDT` | `0.01` | `0.0001` | `0.0001` | `0` |
| `ETH-USDT` | `ETH` | `USDT` | `0.01` | `0.001` | `0.001` | `1` |
| `SOL-USDT` | `SOL` | `USDT` | `0.001` | `0.01` | `0.01` | `2` |

---

## 4. Taker Profile Scheduling Intervals

Each profile defines triple-bounded intervals (`MinInterval`, `BaseInterval`, `MaxInterval`) used to calculate randomized jitter between order cycles:

| Profile | Min Interval | Base Interval | Max Interval | Effective Cadence |
| :--- | :--- | :--- | :--- | :--- |
| **LOW** | `45s` | `75s` | `120s` | Steady, baseline heartbeat orders. |
| **MID** | `120s` | `240s` | `360s` | Moderate liquidity-probing orders. |
| **HIGH** | `900s` (15m) | `1800s` (30m) | `2700s` (45m) | Rare, multi-level depth sweeps. |

---

## 5. Strict Startup Validation Rules (`Validate`)

When `config.Load()` is executed, `Validate()` asserts the following invariants:

1. **Volume & Notional Invariants:**
   - `MAX_ORDER_NOTIONAL_USDT > 0`
   - `MAX_HOURLY_VOLUME_USDT > 0`
   - `MAX_DAILY_VOLUME_USDT >= MAX_HOURLY_VOLUME_USDT` (daily limit cannot be smaller than hourly limit).
2. **Slippage Structural vs. Safety Policy:**
   - `MAX_SLIPPAGE_BPS >= 0` (structural requirement: negative slippage is invalid).
   - `MAX_SLIPPAGE_BPS <= V1MaxAllowedSlippageBps (500 bps)` (policy ceiling: prevents traversal into illiquid order book levels).
3. **Execution Rate & Health:**
   - `MAX_TRADES_PER_HOUR > 0`
   - `CIRCUIT_BREAKER_FAILURES > 0`
   - `MAX_SPREAD_PERCENT > 0`
   - `HEALTH_PORT != ""` and `METRICS_PORT != ""`
   - `LogLevel` must be one of `"debug"`, `"info"`, `"warn"`, or `"error"`.
4. **Inventory Bias Hierarchy:**
   - `CTS_INVENTORY_MODERATE_BIAS_USDT > 0`
   - `CTS_INVENTORY_HEAVY_BIAS_USDT > CTS_INVENTORY_MODERATE_BIAS_USDT` (heavy threshold must exceed moderate threshold).
5. **Market Definitions:**
   - `len(Markets) > 0`
   - No duplicate `MarketID`s.
   - For every market: `BaseAsset != ""`, `QuoteAsset != ""`, `TickSize > 0`, `LotSize > 0`, `MinQuantity > 0`, and `Partition >= 0`.
6. **Profile Timing Hierarchy:**
   - For every profile (`LOW`, `MID`, `HIGH`):
     $$\text{MinInterval} \le \text{BaseInterval} \le \text{MaxInterval} \quad \text{and} \quad \text{MinInterval} > 0$$
