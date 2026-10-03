# TradeDrift — Matching Engine Throughput Tuning Guide

Comprehensive guide for configuring, tuning, and benchmarking the TradeDrift Matching Engine across **Low**, **Mid**, and **High** throughput tiers.

---

## 1. System Dynamics: How Throughput Works in TradeDrift

In TradeDrift, trade execution volume is driven by the interaction between two autonomous simulator services:

```
  ┌─────────────────────────┐             ┌─────────────────────────┐
  │ Liquidity Engine (LE)   │             │ Controlled Taker (CTS)  │
  │ • Places resting bids   │             │ • Fires crossing orders │
  │ • Restocks order book   │             │ • Consumes maker depth  │
  └────────────┬────────────┘             └────────────┬────────────┘
               │                                       │
               └───────────────► ┌───────────────────┐ ◄┘
                                 │ Matching Engine   │
                                 │ • In-memory match │
                                 │ • Publishes trades│
                                 └───────────────────┘
```

> [!IMPORTANT]
> **Taker speed must stay balanced with Maker replenishment.**  
> If CTS consumes liquidity faster than LE restocks the book, the order book empties out. CTS safety guards will trip (`available reachable depth does not meet safety multiplier`) and halt trading until LE restocks.  
> To scale throughput, you must scale **both CTS firing rate and LE replenish rate**.

---

## 2. Where Code and Data Live

There are two primary configuration locations:

### Location 1: Go Source Code — Interval Delays
- **File:** [`services/controlled-taker/internal/config/config.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/controlled-taker/internal/config/config.go#L224-L238)
- **What it does:** Controls how long the taker loop sleeps between order cycles (`LowInterval`, `MidInterval`, `HighInterval`).

### Location 2: Container Environment Variables — Safety Caps & Restock
- **File:** [`docker-compose.yml`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docker-compose.yml#L400-L485)
- **Sections:**
  - `controlled-taker` (`MAX_TRADES_PER_HOUR`, `MAX_HOURLY_VOLUME_USDT`, `HIGH_COOLDOWN`)
  - `liquidity-engine` (`RECONCILE_INTERVAL`, `WALLET_REFRESH_INTERVAL`)

---

## 3. Key Parameters Reference

| Parameter | Location | Default Value | Purpose |
| :--- | :--- | :---: | :--- |
| **`LowInterval.Min / Base / Max`** | [`config.go:224-228`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/controlled-taker/internal/config/config.go#L224-L228) | `45s / 75s / 120s` | Sleep duration between taker orders on the standard profile (65% of orders). |
| **`MidInterval.Min / Base / Max`** | [`config.go:229-233`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/controlled-taker/internal/config/config.go#L229-L233) | `120s / 240s / 360s` | Sleep duration on mid-profile cycles (27% of orders). |
| **`MAX_TRADES_PER_HOUR`** | `docker-compose.yml` (`CTS`) | `60` | Circuit breaker cap. Prevents CTS from exceeding $N$ trades per rolling hour. |
| **`MAX_HOURLY_VOLUME_USDT`** | `docker-compose.yml` (`CTS`) | `100000.00` | Dollar cap. Trips if total traded notional in 1 hour exceeds this amount. |
| **`MAX_DAILY_VOLUME_USDT`** | `docker-compose.yml` (`CTS`) | `1500000.00` | Dollar cap. Trips if total traded notional in 24 hours exceeds this amount. |
| **`HIGH_COOLDOWN`** | `docker-compose.yml` (`CTS`) | `300s` (5m) | Cooldown period enforced between large sweep orders. |
| **`RECONCILE_INTERVAL`** | `docker-compose.yml` (`LE`) | `30s` | How often LE cancels stale maker quotes and places fresh depth. |
| **`WALLET_REFRESH_INTERVAL`** | `docker-compose.yml` (`LE`) | `15s` | How often LE refreshes internal balance caches to compute order sizes. |

---

## 4. Enabling Dynamic Tuning in Code

Currently, `LowInterval` in [`config.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/controlled-taker/internal/config/config.go#L224-L238) is hardcoded. Adding environment variable support allows instant profile switching via `docker-compose.yml` without recompiling Go code each time:

### Code Change in [`services/controlled-taker/internal/config/config.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/controlled-taker/internal/config/config.go#L224-L238)
```go
// Replace hardcoded values with platformconfig lookups:
lowMin  := platformconfig.GetEnvAsDuration("CTS_LOW_MIN_INTERVAL", 45*time.Second)
lowBase := platformconfig.GetEnvAsDuration("CTS_LOW_BASE_INTERVAL", 75*time.Second)
lowMax  := platformconfig.GetEnvAsDuration("CTS_LOW_MAX_INTERVAL", 120*time.Second)

cfg := Config{
    // ...
    LowInterval: IntervalConfig{
        MinInterval:  lowMin,
        BaseInterval: lowBase,
        MaxInterval:  lowMax,
    },
    // ...
}
```

---

## 5. Throughput Configuration Profiles

### 🟢 Profile A: Low Throughput (Realistic Production Simulation)
* **Pace:** ~1 trade every 60–90 seconds per market (~40–60 trades/hr total).
* **Use Case:** Realistic market simulation, clean 1m/5m/15m candlestick formation, long-term testing.

#### Settings in `docker-compose.yml`:
```yaml
  liquidity-engine:
    environment:
      RECONCILE_INTERVAL: "30s"
      WALLET_REFRESH_INTERVAL: "15s"

  controlled-taker:
    environment:
      MAX_TRADES_PER_HOUR: "60"
      MAX_HOURLY_VOLUME_USDT: "100000.00"
      MAX_DAILY_VOLUME_USDT: "1500000.00"
      HIGH_COOLDOWN: "300s"
      CTS_LOW_MIN_INTERVAL: "45s"
      CTS_LOW_BASE_INTERVAL: "75s"
      CTS_LOW_MAX_INTERVAL: "120s"
```

---

### 🟡 Profile B: Mid Throughput (Active Interactive Testing)
* **Pace:** ~1 trade every 3–8 seconds per market (~600–1,200 trades/hr total).
* **Use Case:** Frontend trade terminal development, instant order fill verification, live chart updates in real time.

#### Settings in `docker-compose.yml`:
```yaml
  liquidity-engine:
    environment:
      RECONCILE_INTERVAL: "8s"           # Restock liquidity faster
      WALLET_REFRESH_INTERVAL: "5s"

  controlled-taker:
    environment:
      MAX_TRADES_PER_HOUR: "2000"        # Allows up to ~33 trades/minute
      MAX_HOURLY_VOLUME_USDT: "2000000.00"
      MAX_DAILY_VOLUME_USDT: "20000000.00"
      HIGH_COOLDOWN: "30s"
      CTS_LOW_MIN_INTERVAL: "3s"         # 3s to 8s between taker orders
      CTS_LOW_BASE_INTERVAL: "5s"
      CTS_LOW_MAX_INTERVAL: "8s"
```

---

### 🔴 Profile C: High Throughput / Engine Stress Test
* **Pace:** 10 to 50+ trades per second (~36,000 to 180,000+ trades/hr).
* **Use Case:** Stress testing Matching Engine latency, Kafka event queue throughput, PostgreSQL persistence concurrency, and Redis depth broadcast.

#### Settings in `docker-compose.yml`:
```yaml
  liquidity-engine:
    environment:
      RECONCILE_INTERVAL: "2s"           # Rapid maker restocking
      WALLET_REFRESH_INTERVAL: "2s"

  controlled-taker:
    environment:
      MAX_TRADES_PER_HOUR: "500000"      # Uncapped trade limit
      MAX_HOURLY_VOLUME_USDT: "500000000.00"
      MAX_DAILY_VOLUME_USDT: "5000000000.00"
      HIGH_COOLDOWN: "5s"
      CTS_LOW_MIN_INTERVAL: "20ms"       # Millisecond rapid-fire firing
      CTS_LOW_BASE_INTERVAL: "50ms"
      CTS_LOW_MAX_INTERVAL: "100ms"
```

---

## 6. How to Monitor Capacity & Verify TPS

### 1. View Live Matching Engine Match Rates:
```bash
docker compose logs -f matching-engine | Select-String "MATCH"
```

### 2. View CTS Fills in Real Time:
```bash
docker compose logs -f controlled-taker | Select-String "Controlled taker order fill processed"
```

### 3. Check Real Trades Count in the Database:
```sql
-- Count trades executed in the last 60 seconds
SELECT COUNT(*) 
FROM market_trades 
WHERE executed_at > NOW() - INTERVAL '1 minute';
```

### 4. Check Engine Prometheus Metrics:
The Matching Engine exposes native throughput and latency metrics at:
- `http://localhost:8082/metrics`
- Key metrics to observe:
  - `me_orders_processed_total`
  - `me_trades_matched_total`
  - `me_match_duration_seconds`
