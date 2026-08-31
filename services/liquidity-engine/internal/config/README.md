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
}
```

### `Config`

```go
type Config struct {
    KafkaBrokers              []string
    KafkaGroupID              string
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
