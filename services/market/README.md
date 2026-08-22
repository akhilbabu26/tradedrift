# TradeDrift — Market Service (`services/market`)

> **Service:** Market Metadata & Real-Time Ticker Service  
> **Directory:** `services/market/`  
> **gRPC Port:** `:50054`  
> **Database:** PostgreSQL (`tradedrift_market`)  
> **Message Bus:** Apache Kafka (Consumer on topic `trades.executed`)  
> **Role:** Master catalog for trading pair rules (`BTC-USDT`), rolling 24-hour ticker statistics, and multi-resolution OHLC candlestick chart aggregations.

---

## 1. Executive Summary & Core Purpose

In a high-speed cryptocurrency and financial exchange, the **Market Service** serves two vital roles:
1. **The Exchange Rule Authority (gRPC):** Stores authoritative trading rules per currency pair — base/quote assets, price `tick_size` (minimum price increment, e.g., `$0.01`), order `lot_size` (minimum quantity increment, e.g., `0.0001 BTC`), minimum order quantities, and market operational status (`ACTIVE`, `HALTED`, `MAINTENANCE`).
2. **The Read-Side Market Analytics Engine (Kafka + SQL):** Continuously consumes executed trade events (`trades.executed`) published by the matching engine, persists them with strict idempotency, and dynamically calculates **24-hour rolling price/volume stats** and **historical OHLC candlestick bars** (`1m`, `5m`, `15m`, `1h`, `1d`).

---

## 2. System Architecture & Real-Time Data Flow

```
   ┌─────────────────────────────────────────────────────────────┐
   │                     Matching Engine                         │
   │               (Executes Buy & Sell Orders)                  │
   └──────────────────────────────┬──────────────────────────────┘
                                  │ Publishes TradeExecuted
                                  ▼
   ┌─────────────────────────────────────────────────────────────┐
   │                  Apache Kafka Cluster                       │
   │               Topic: "trades.executed"                      │
   └──────────────────────────────┬──────────────────────────────┘
                                  │ Consumes message batch
                                  ▼
   ┌─────────────────────────────────────────────────────────────┐
   │             Market Service: Kafka Consumer                  │
   │            (services/market/internal/kafka)                 │
   │                                                             │
   │  1. JSON Deserialization & Payload Validation               │
   │  2. Strict UUID & Decimal Parsing (shopspring/decimal)      │
   │  3. Poison Message Offset Commit (Skip malformed events)    │
   └──────────────────────────────┬──────────────────────────────┘
                                  │ Passes TradeEventPayload
                                  ▼
   ┌─────────────────────────────────────────────────────────────┐
   │             Market Service: Domain Layer                    │
   │           (services/market/internal/service)                │
   │                                                             │
   │  1. Market Existence Check                                  │
   │  2. Coordinates Atomic Multi-Table DB Transaction           │
   └──────────────────────────────┬──────────────────────────────┘
                                  │ Executes pgx.Tx Transaction
                                  ▼
   ┌─────────────────────────────────────────────────────────────┐
   │       PostgreSQL Repository (Database: tradedrift_market)   │
   │                                                             │
   │  Step A: Insert into `market_trades`                        │
   │          (ON CONFLICT (id) DO NOTHING — Idempotent)         │
   │                                                             │
   │  Step B: Upsert 5 Timeframe OHLC Candles                    │
   │          (1m, 5m, 15m, 1h, 1d)                              │
   │          • Out-of-order execution protection via            │
   │            `open_trade_at` and `close_trade_at` timestamps  │
   │          • High/Low expansion: GREATEST() / LEAST()         │
   │          • Volume accumulator: volume + EXCLUDED.volume     │
   └──────────────────────────────┬──────────────────────────────┘
                                  │ DB Transaction Committed OK
                                  ▼
   ┌─────────────────────────────────────────────────────────────┐
   │                 Kafka Offset Committed                      │
   │            (Commit-after-DB Safety Guarantee)               │
   └─────────────────────────────────────────────────────────────┘
```

---

## 3. Comprehensive File-by-File Catalog

```
services/market/
├── Dockerfile                                 <-- Multi-stage production container build
├── .env                                       <-- Local service environment variables
├── go.mod / go.sum                            <-- Go module dependencies
├── migration/
│   └── 00001_create_market_tables.sql         <-- SQL Schema DDL (markets, market_trades, ohlc_candles)
├── cmd/
│   └── server/
│       └── main.go                            <-- gRPC Server & Kafka Consumer Lifecycle Orchestrator
└── internal/
    ├── config/
    │   └── config.go                          <-- Typed Configuration Loader (.env)
    ├── handler/
    │   ├── grpc.go                            <-- gRPC RPC Controller implementation
    │   └── mapper.go                          <-- Domain to Protobuf DTO converters & error sanitizer
    ├── kafka/
    │   └── consumer.go                        <-- Kafka Consumer with poison-skip & commit-after-DB
    ├── service/
    │   ├── errors.go                          <-- Business validation errors
    │   └── service.go                         <-- Business service logic & trade event processing
    └── repository/
        ├── errors.go                          <-- Repository domain errors (ErrNotFound, etc.)
        ├── market.go                          <-- Interface contracts & domain model definitions
        └── postgres/
            ├── market_repository.go           <-- SQL implementation for Markets, Trades & 24h Ticker
            └── candle_repository.go           <-- SQL implementation for Multi-timeframe OHLC Candles
```

---

### 📄 1. `migration/00001_create_market_tables.sql`
* **Purpose:** Defines the relational tables and indexes required for high-throughput market data ingestion.
* **Tables Created:**
  1. `markets`: Stores currency pairs (`BTC-USDT`), precision limits, and status (`ACTIVE`, `HALTED`).
  2. `market_trades`: Historical immutable log of executed trades (`trade_id`, `price`, `quantity`, `executed_at`). Uses `PRIMARY KEY (id)` for duplicate deduplication.
  3. `ohlc_candles`: Aggregated candle bars (`market_id`, `resolution`, `start_time`, `open`, `high`, `low`, `close`, `volume`, `quote_volume`, `open_trade_at`, `close_trade_at`).
* **Indexes:** Composite index `idx_ohlc_market_res_time (market_id, resolution, start_time DESC)` for ultra-fast TradingView chart range queries.

---

### 📄 2. `cmd/server/main.go`
* **Purpose:** The entrypoint for the Market Service process.
* **Lifecycle Flow:**
  1. Loads configuration from `.env` via `platform/config`.
  2. Connects to PostgreSQL connection pool using `pgxpool.New`.
  3. Executes embedded database migrations automatically on boot.
  4. Initializes Repositories ➔ Service Layer ➔ gRPC Handler.
  5. Starts the **gRPC Server** on `:50054` in a background goroutine.
  6. Starts the **Kafka Consumer Worker** in a background goroutine.
  7. Traps OS signals (`SIGINT`, `SIGTERM`) for zero-data-loss graceful shutdown.

---

### 📄 3. `internal/config/config.go`
* **Purpose:** Loads and validates all runtime environment variables with sensible production defaults:
  * `GRPCPort` (default `:50054`)
  * `DatabaseURL` (PostgreSQL connection string)
  * `KafkaBrokers`, `KafkaGroupID`, `KafkaTopic` (`trades.executed`)
  * `LogLevel` (`info`, `debug`)

---

### 📄 4. `internal/repository/market.go` & `errors.go`
* **Purpose:** Declares clean Go interfaces and domain structs decoupling business logic from PostgreSQL:
  * `MarketRepository` interface: `ListMarkets`, `GetMarketByID`, `InsertTrade`, `GetTicker24h`, `WithTx`.
  * `CandleRepository` interface: `UpsertCandles`, `GetCandles`.
  * Domain structs: `Market`, `MarketTrade`, `Ticker24h`, `Candle`.

---

### 📄 5. `internal/repository/postgres/market_repository.go`
* **Purpose:** Implements SQL queries using `jackc/pgx/v5` connection pool:
  * `GetTicker24h`: Uses an optimized **Common Table Expression (CTE)** query to calculate 24h high, 24h low, total volume, quote volume, last trade price, and price change percentage dynamically from `market_trades` within `NOW() - INTERVAL '24 hours'`.
  * `InsertTrade`: Inserts trades using `ON CONFLICT (id) DO NOTHING` returning an `inserted` boolean for deduplication tracking.

---

### 📄 6. `internal/repository/postgres/candle_repository.go`
* **Purpose:** Upserts OHLC candlestick bars across 5 resolutions (`1m`, `5m`, `15m`, `1h`, `1d`) inside a single atomic transaction.
* **Out-of-Order Trade Protection:**
  When network delays deliver trade events out of sequence:
  ```sql
  open = CASE WHEN EXCLUDED.open_trade_at < ohlc_candles.open_trade_at THEN EXCLUDED.open ELSE ohlc_candles.open END,
  close = CASE WHEN EXCLUDED.close_trade_at >= ohlc_candles.close_trade_at THEN EXCLUDED.close ELSE ohlc_candles.close END,
  high = GREATEST(ohlc_candles.high, EXCLUDED.high),
  low = LEAST(ohlc_candles.low, EXCLUDED.low),
  volume = ohlc_candles.volume + EXCLUDED.volume,
  quote_volume = ohlc_candles.quote_volume + EXCLUDED.quote_volume
  ```
  This guarantees that Open and Close prices always reflect the earliest and latest trades chronologically, never the arrival order.

---

### 📄 7. `internal/service/service.go` & `errors.go`
* **Purpose:** Core business orchestration:
  * `ProcessTradeEvent`: Validates market existence, starts a PostgreSQL transaction (`pgx.Tx`), inserts the trade record, computes timestamps for all 5 candle resolutions, upserts the candles, and commits atomically.
  * `GetCandles`: Validates requested limit (defaults `100`, max `500`), checks market existence, and returns chronological candlestick bars.
  * `GetTicker`: Validates market existence and returns live 24h rolling price statistics.

---

### 📄 8. `internal/handler/grpc.go` & `mapper.go`
* **Purpose:** Implements the `marketv1.MarketServiceServer` Protobuf interface:
  * Handles `ListMarkets`, `GetMarket`, `GetTicker`, and `GetCandles`.
  * `mapper.go` transforms internal domain structs into Protobuf messages (`marketv1.Market`, `marketv1.Ticker24H`, `marketv1.Candle`).
  * Maps internal errors to standard gRPC status codes (`codes.NotFound`, `codes.InvalidArgument`, `codes.Internal`).

---

### 📄 9. `internal/kafka/consumer.go`
* **Purpose:** Reliable, high-throughput consumer for `trades.executed` events.
* **Key Reliability Features:**
  1. **Explicit Data Type Validation:** Parses UUIDs and Decimals explicitly. Malformed data is logged as an error and skipped.
  2. **Poison-Pill Message Skipping:** If an unparseable payload or invalid UUID arrives on the Kafka topic, `consumer.go` logs the poison message and commits its offset immediately. This prevents the partition consumer from getting stuck in an infinite retry loop.
  3. **Commit-After-DB Guarantee:** The Kafka offset is committed **only after** PostgreSQL successfully commits the transaction. If the database crashes, the message remains uncommitted and will be safely reprocessed upon restart.

---

## 4. Tools, Libraries & Technology Decisions

| Tool / Package | Why It Was Chosen |
| :--- | :--- |
| **`github.com/jackc/pgx/v5`** | Fastest pure-Go PostgreSQL driver and connection pool. Provides native support for binary protocols and atomic `pgx.Tx` transactions. |
| **`github.com/segmentio/kafka-go`** | Pure Go Kafka library with zero CGO dependencies. Allows clean manual offset commits and context cancellation. |
| **`github.com/shopspring/decimal`** | Arbitrary-precision decimal arithmetic. Prevents IEEE 754 binary floating-point rounding errors on prices and volumes. |
| **`github.com/google/uuid`** | RFC 4122 compliant UUID parsing and generation for trade and order identifiers. |
| **`go.uber.org/zap`** | Blazing-fast, zero-allocation structured JSON logging. |
| **`google.golang.org/grpc`** | Low-latency binary RPC protocol for communication with the API Gateway. |

---

## 5. Configuration & Environment Variables

| Variable | Default Value | Description |
| :--- | :--- | :--- |
| `MARKET_GRPC_PORT` | `:50054` | Port on which the Market gRPC server listens |
| `MARKET_DB_URL` | `postgres://user:pass@localhost:5432/tradedrift_market?sslmode=disable` | PostgreSQL connection string |
| `KAFKA_BROKERS` | `localhost:9092` | Comma-separated list of Apache Kafka brokers |
| `KAFKA_GROUP_ID` | `market-service-group` | Kafka consumer group ID |
| `KAFKA_TOPIC_TRADE_EXECUTED` | `trades.executed` | Kafka topic for executed trades |
| `LOG_LEVEL` | `info` | Logging verbosity (`debug`, `info`, `warn`, `error`) |

---

## 6. How to Run Locally

### Run with Local Go:
```powershell
cd services/market
go run cmd/server/main.go
```

### Run via Docker Compose:
```powershell
docker compose up -d market
```
