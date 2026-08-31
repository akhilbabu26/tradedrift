# `internal/config` — Liquidity Engine Configuration

**Package:** `config`  
**Service:** Liquidity Engine  
**Last Updated:** August 2026

---

## 1. What This Package Does

This package loads and validates the **full Liquidity Engine configuration** from environment variables. It defines per-market parameters that mirror the Matching Engine's market configuration and provides helper methods for market lookups, partition routing, and partition conflict detection.

---

## 2. Files In This Package

| File | Purpose |
| :--- | :--- |
| `config.go` | `MarketConfig`, `Config` structs, `Load()`, helper methods |
| `README.md` | This documentation file |

---

## 3. Structs

### `MarketConfig`

Holds all per-market parameters. Tick/lot sizes must **exactly match** the Matching Engine's values — the ME rejects orders that violate them.

```go
type MarketConfig struct {
    MarketID        string
    BaseAsset       string
    QuoteAsset      string
    TickSize        decimal.Decimal // minimum price increment (ME enforces this)
    LotSize         decimal.Decimal // minimum quantity increment (ME enforces this)
    Partition       int             // Kafka partition — must equal ME's partition for this market
    LevelCount      int             // number of bid + ask levels to maintain (default 12 each)
    MinOrderSize    decimal.Decimal // minimum resting quantity before treating as consumed
    MinBase         decimal.Decimal // effective available base below which skew to LOW
    CriticalBase    decimal.Decimal // effective available base below which skew to CRITICAL
    MinQuote        decimal.Decimal // effective available quote below which bid-side skew
    CriticalQuote   decimal.Decimal
    SpreadBps       int             // base spread in basis points (default 4 bps per level)
    ReferencePrice  decimal.Decimal // V1 static reference price
    MaxDeviationPct decimal.Decimal // pause if traded price deviates by this pct
}
```

### `Config`

```go
type Config struct {
    KafkaBrokers              []string
    KafkaGroupID              string
    RedisAddr                 string
    WalletGRPCAddr            string
    OrderGRPCAddr             string
    Markets                   []MarketConfig
    WalletRefreshInterval     time.Duration
    MaxBalanceStaleness       time.Duration
    ReconcileInterval         time.Duration
    MaxOrderStateStaleness    time.Duration
    PendingTimeout            time.Duration
    CancellingTimeout         time.Duration
    CancelRetryLimit          int
    MELivenessThreshold       int
    TargetedReconcileDebounce time.Duration
    HealthPort                string
    MetricsPort               string
    MinReadyBids              int
    MinReadyAsks              int
}
```

---

## 4. Default Market Configuration (V1)

| Market | Tick Size | Lot Size | Partition | Levels | SpreadBps | Reference Price |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| `BTC-USDT` | `0.01` | `0.00001` | `BTC_PARTITION` env | 12 | 4 bps | `96450.00` |
| `ETH-USDT` | `0.01` | `0.0001` | `ETH_PARTITION` env | 12 | 4 bps | `2780.50` |
| `SOL-USDT` | `0.001` | `0.01` | `SOL_PARTITION` env | 12 | 4 bps | `188.20` |

### Inventory Thresholds (BTC-USDT example)

| Threshold | Value | Effect |
| :--- | :--- | :--- |
| `MinBase` | 30 BTC | Below this → skew to LOW (6 ask levels) |
| `CriticalBase` | 5 BTC | Below this → skew to CRITICAL (0 ask levels) |
| `MinQuote` | $1,000,000 USDT | Below this → skew bid-side to LOW |
| `CriticalQuote` | $100,000 USDT | Below this → 0 bid levels |

---

## 5. Environment Variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `KAFKA_BROKERS` | `localhost:9092` | Comma-separated broker list |
| `KAFKA_GROUP_ID` | `liquidity-engine-group` | Consumer group ID |
| `REDIS_ADDR` | `localhost:6379` | Redis address (read-only usage) |
| `WALLET_GRPC_ADDR` | `localhost:50052` | Wallet Service gRPC address |
| `ORDER_GRPC_ADDR` | `localhost:50053` | Order Service gRPC address |
| `BTC_PARTITION` | `0` | Kafka partition for BTC-USDT |
| `ETH_PARTITION` | `1` | Kafka partition for ETH-USDT |
| `SOL_PARTITION` | `2` | Kafka partition for SOL-USDT |
| `BTC_REFERENCE_PRICE` | `96450.00` | Static reference mid-price for BTC-USDT |
| `ETH_REFERENCE_PRICE` | `2780.50` | Static reference mid-price for ETH-USDT |
| `SOL_REFERENCE_PRICE` | `188.20` | Static reference mid-price for SOL-USDT |
| `MAX_PRICE_DEVIATION_PCT` | `15` | Pause threshold if trade price deviates >15% |
| `WALLET_REFRESH_INTERVAL` | `5m` | How often to fetch MM-001 balances from Wallet Service |
| `MAX_BALANCE_STALENESS` | `60s` | Pause reconcile if balance not refreshed within this window |
| `RECONCILE_INTERVAL` | `30s` | How often the engine runs a full reconcile pass |
| `MAX_ORDER_STATE_STALENESS` | `90s` | Max age of order state before considered stale |
| `PENDING_TIMEOUT` | `10s` | Time before a PENDING order is checked via Order Service |
| `CANCELLING_TIMEOUT` | `30s` | Time before a CANCELLING order is retried or marked STALE |
| `CANCEL_RETRY_LIMIT` | `3` | Max cancel retries before STALE transition |
| `ME_LIVENESS_THRESHOLD` | `3` | Consecutive PENDING timeouts before transitioning to PAUSED |
| `TARGETED_RECONCILE_DEBOUNCE` | `200ms` | Debounce window for targeted reconciliation |
| `MIN_READY_BIDS` | `6` | Min RESTING bids required for readiness |
| `MIN_READY_ASKS` | `6` | Min RESTING asks required for readiness |
| `HEALTH_PORT` | `8080` | HTTP health check port |
| `METRICS_PORT` | `9090` | Prometheus metrics port |

---

## 6. Key Design Invariant — Shared Partition Config

```
Both LE and ME read BTC_PARTITION / ETH_PARTITION / SOL_PARTITION
from environment variables. This is the shared source of truth.
```

If partitions diverge (LE routes BTC orders to partition 0, ME expects partition 1), orders are silently dropped by the ME. `ValidatePartitions()` catches duplicate assignments but cannot catch cross-service divergence — it must be enforced by Kubernetes config consistency.

---

## 7. Helper Methods

| Method | Description |
| :--- | :--- |
| `ForMarket(marketID) *MarketConfig` | Returns config for a market, or `nil` if unknown |
| `PartitionFor(marketID) int` | Returns Kafka partition for a market (`-1` if unknown) |
| `ValidatePartitions() error` | Ensures no two markets share the same partition number |
