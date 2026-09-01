# package `config`

## Purpose

Loads and validates the **complete runtime configuration** for the Liquidity Engine from environment variables. It is the single source of truth for all per-market parameters (tick size, lot size, Kafka partition) and all timing/threshold knobs.

## Problem It Solves

The LE and ME must agree on a set of critical values at runtime:

1. **Kafka partition assignments** — if the LE publishes to the wrong partition, the ME's recovery pipeline will never see the command.
2. **Tick and lot sizes** — the ME hard-rejects orders that violate its configured precision; if the LE uses different values it will produce permanently-rejected orders.
3. **Timing parameters** — timeouts for pending/cancelling orders, wallet refresh intervals, ME liveness thresholds, and readiness thresholds all need to be tunable without code changes.

Embedding these values as hardcoded constants makes testing and environment-specific tuning impossible.

## How It Solves It

`config.go` reads all values from environment variables with sensible defaults, validates them early (parsing errors are returned at startup, not at runtime), and exposes them through a typed `Config` struct. The same `BTC_PARTITION`, `ETH_PARTITION`, `SOL_PARTITION` env vars are read by **both** the ME and LE — ensuring a shared source of truth.

---

## Files

### [`config.go`](./config.go)

#### Types

| Type | Purpose |
|:---|:---|
| `MarketConfig` | Per-market parameters: tick/lot sizes, Kafka partition, level count, inventory thresholds, spread in basis points, and reference price. |
| `Config` | Full LE configuration: Kafka brokers, service addresses, all market configs, all timing parameters, health ports, and readiness thresholds. |

#### Functions

| Function | Problem It Solves |
|:---|:---|
| `Load() (Config, error)` | Reads all required and optional env vars, parses them into correct types (decimal, int, duration), and returns a fully validated `Config`. Returns an error on the first invalid or missing value — **fails fast at startup** so problems are caught before the engine processes any events. |
| `(c *Config) ForMarket(marketID string) *MarketConfig` | Provides lookup of a `MarketConfig` by market ID string. Returns `nil` for unknown markets. Used pervasively throughout the reconciler and engine to avoid re-scanning all markets from a raw string. |
| `(c *Config) ValidatePartitions() error` | Detects duplicate Kafka partition assignments across markets — a partition collision would cause the ME to route two markets' commands to the same consumer group partition, corrupting recovery. Called once at startup. |
| `(c *Config) PartitionFor(marketID string) int` | Returns the Kafka partition number for a market. Returns `-1` for unknown markets. Used by the Kafka producer to route commands to the correct partition. |
| `getEnvDecimal(key, fallback string) (decimal.Decimal, error)` | (internal) Parses a decimal environment variable. Wraps `decimal.NewFromString` with a useful error message. Used by `Load()` for all price/size values. |

---

## Key Design Decisions

- **No global config**: `Config` is passed explicitly through the dependency graph. This makes tests simple — construct any `Config` directly.
- **Both ME and LE read the same partition env vars**: Prevents the most common misconfiguration (partition mismatch), which is otherwise invisible until recovery fails.
- **Tick/lot sizes mirror the ME exactly**: The values in `MarketConfig` are verified against `services/matching-engine/cmd/main.go`. Any drift is a bug.

---

## Environment Variables Reference

| Variable | Default | Description |
|:---|:---|:---|
| `KAFKA_BROKERS` | `localhost:9092` | Comma-separated Kafka broker addresses |
| `KAFKA_GROUP_ID` | `liquidity-engine-group` | Kafka consumer group ID |
| `BTC_PARTITION` | `0` | Kafka partition for BTC-USDT commands |
| `ETH_PARTITION` | `1` | Kafka partition for ETH-USDT commands |
| `SOL_PARTITION` | `2` | Kafka partition for SOL-USDT commands |
| `BTC_REFERENCE_PRICE` | `96450.00` | V1 static reference price for BTC |
| `ETH_REFERENCE_PRICE` | `2780.50` | V1 static reference price for ETH |
| `SOL_REFERENCE_PRICE` | `188.20` | V1 static reference price for SOL |
| `WALLET_GRPC_ADDR` | `localhost:50052` | Wallet Service gRPC address |
| `ORDER_GRPC_ADDR` | `localhost:50053` | Order Service gRPC address |
| `ME_HTTP_ADDR` | `http://localhost:8082` | Matching Engine HTTP health address |
| `WALLET_REFRESH_INTERVAL` | `15s` | How often to fetch MM-001 balances |
| `MAX_BALANCE_STALENESS` | `60s` | Balance age limit before pausing quoting |
| `RECONCILE_INTERVAL` | `30s` | Full reconcile cycle frequency |
| `MAX_ORDER_STATE_STALENESS` | `90s` | Order sync age limit before skipping new orders |
| `PENDING_TIMEOUT` | `10s` | Time before a PENDING order is checked in OS |
| `CANCELLING_TIMEOUT` | `30s` | Time before a CANCELLING order is re-queried |
| `CANCEL_RETRY_LIMIT` | `3` | Max cancel retries before entering STALE |
| `ME_LIVENESS_THRESHOLD` | `3` | Consecutive probe failures before pausing a market |
| `TARGETED_RECONCILE_DEBOUNCE` | `200ms` | Delay before triggering reconcile after a fill |
| `HEALTH_PORT` | `8080` | HTTP health/readiness server port |
| `METRICS_PORT` | `9090` | Prometheus metrics server port |
| `MIN_READY_BIDS` | `6` | Minimum RESTING bid levels for `/readyz` |
| `MIN_READY_ASKS` | `6` | Minimum RESTING ask levels for `/readyz` |
