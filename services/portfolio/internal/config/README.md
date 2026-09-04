# Portfolio Configuration Engine (`services/portfolio/internal/config`)

## 1. Overview & System Role

The `services/portfolio/internal/config` package manages environment-driven configuration for the **Portfolio Service**.

Adhering to the **12-Factor App** configuration principles (III. Config):
* It loads and parses environment variables at service boot.
* It guarantees strict **fail-fast validation** for mission-critical infrastructure settings (database connection strings, Kafka broker addresses).
* It normalizes comma-delimited strings into clean slices and provides resilient default settings for local development and containerized Docker environments.

```
                      OS Environment (.env / Docker Compose / K8s)
                                          │
                                          ▼
                      ┌────────────────────────────────────────┐
                      │      platform/config.LoadEnv()         │
                      └───────────────────┬────────────────────┘
                                          │
                                          ▼
                      ┌────────────────────────────────────────┐
                      │       portfolioconfig.Load()           │
                      │                                        │
                      │  • Validate PORTFOLIO_POSTGRES_DSN     │
                      │  • Parse & Normalize KAFKA_BROKERS     │
                      │  • Apply Default Ports & Topics        │
                      └───────────────────┬────────────────────┘
                                          │
                       Valid Config Struct│ (or Panic if Invalid)
                                          ▼
                      ┌────────────────────────────────────────┐
                      │          cmd/server/main.go            │
                      │     (Database, Kafka, gRPC Boot)       │
                      └────────────────────────────────────────┘
```

---

## 2. Core Problems Solved by This Package

### 2.1 Prevention of Late Runtime Panics via Fail-Fast Validation
* **The Problem**: If a mission-critical configuration setting (such as the database DSN or Kafka broker list) is missing or empty, a poorly designed service might boot successfully and only crash minutes later when the first trade arrives or when a database query fails.
* **How It Solves It**: The `Load()` function inspects essential parameters before returning. If `PORTFOLIO_POSTGRES_DSN` is empty or `KAFKA_BROKERS` has no valid addresses, `Load()` returns an explicit descriptive error immediately. This triggers `panic("invalid configuration: " + err.Error())` in `main.go` on step 0, stopping the deployment before the service declares readiness to Kubernetes or Docker.

### 2.2 Whitespace & Comma-Separated Broker Normalization
* **The Problem**: Infrastructure definitions frequently specify multiple brokers with irregular spacing, e.g. `kafka1:9092, kafka2:9092 , kafka3:9092`. Direct splitting by comma preserves trailing spaces, causing TCP dial errors (`dial tcp: lookup " kafka2": no such host`).
* **How It Solves It**: The helper function `parseBrokers` trims whitespace from every entry and filters out empty tokens:
  ```go
  func parseBrokers(raw string) []string {
      parts := strings.Split(raw, ",")
      out := make([]string, 0, len(parts))
      for _, p := range parts {
          if trimmed := strings.TrimSpace(p); trimmed != "" {
              out = append(out, trimmed)
          }
      }
      return out
  }
  ```

### 2.3 Single Source of Truth for Network Topology
* **The Problem**: Hardcoding service addresses (like `wallet:50052` or `market:50054`) across various handlers creates distributed configuration drift and makes testing across environments (local native vs. Docker Compose vs. Kubernetes) difficult.
* **How It Solves It**: The `Config` struct acts as a single, immutable configuration object passed into constructors across the service lifecycle.

---

## 3. Function-by-Function Breakdown

### 3.1 `Load`
```go
func Load() (Config, error)
```
* **Purpose**: Orchestrates environment variable reading and structural validation.
* **Validation Rules**:
  1. `PORTFOLIO_POSTGRES_DSN`: Mandatory. If empty, returns `fmt.Errorf("PORTFOLIO_POSTGRES_DSN is required")`.
  2. `KAFKA_BROKERS`: Mandatory. Must parse to at least 1 non-empty host:port pair.
* **Return Value**: An initialized `Config` struct containing resolved values or defaults.

---

### 3.2 `parseBrokers`
```go
func parseBrokers(raw string) []string
```
* **Purpose**: Parses a comma-delimited broker string into a clean slice of strings.
* **Input Example**: `"kafka1:9092, kafka2:9092, "`
* **Output**: `[]string{"kafka1:9092", "kafka2:9092"}`

---

## 4. End-to-End Bootstrapping Flow

```mermaid
sequenceDiagram
    autonumber
    participant Main as cmd/server/main.go
    participant ConfigPkg as internal/config
    participant Platform as platform/config

    Main->>Platform: LoadEnv() (Read .env file if present)
    Main->>ConfigPkg: Load()
    
    rect rgb(240, 248, 255)
        note over ConfigPkg: 1. Validate DSN
        ConfigPkg->>Platform: GetEnv("PORTFOLIO_POSTGRES_DSN", "")
        alt DSN is empty
            ConfigPkg-->>Main: Error: PORTFOLIO_POSTGRES_DSN is required
            Main->>Main: panic("invalid configuration...")
        end
    end

    rect rgb(255, 250, 240)
        note over ConfigPkg: 2. Parse Brokers
        ConfigPkg->>Platform: GetEnv("KAFKA_BROKERS", "localhost:9092")
        ConfigPkg->>ConfigPkg: parseBrokers(raw)
        alt len(brokers) == 0
            ConfigPkg-->>Main: Error: KAFKA_BROKERS must contain at least one valid broker
            Main->>Main: panic("invalid configuration...")
        end
    end

    rect rgb(240, 255, 240)
        note over ConfigPkg: 3. Resolve Addresses & Ports
        ConfigPkg->>Platform: GetEnv("WALLET_GRPC_ADDR", "localhost:50052")
        ConfigPkg->>Platform: GetEnv("MARKET_GRPC_ADDR", "localhost:50054")
        ConfigPkg->>Platform: GetEnv("PORTFOLIO_GRPC_PORT", ":50058")
        ConfigPkg->>Platform: GetEnv("PORTFOLIO_METRICS_PORT", ":9091")
    end

    ConfigPkg-->>Main: Config (Valid)
    Main->>Main: Proceed with Migrations, DB Pool, gRPC & Kafka
```

---

## 5. Configuration Reference Matrix

| Environment Variable | Required? | Default Value | Description |
|---|:---:|---|---|
| `PORTFOLIO_POSTGRES_DSN` | **YES** | *(None)* | PostgreSQL connection string (`postgres://user:pass@host:5432/tradedrift_portfolio?sslmode=disable`). |
| `PORTFOLIO_MIGRATIONS_DIR` | No | `services/portfolio/migration` | Filesystem path to Goose SQL migration scripts. |
| `KAFKA_BROKERS` | No | `localhost:9092` | Comma-separated list of Kafka broker bootstrap addresses. |
| `KAFKA_GROUP_ID` | No | `portfolio-service-group` | Kafka consumer group identifier for settled trade stream. |
| `KAFKA_TOPIC_TRADE_SETTLED` | No | `trades.settled.v1` | Inbound Kafka topic for settled trade events. |
| `KAFKA_TOPIC_PORTFOLIO_UPDATED` | No | `portfolios.updated.v1` | Outbound Kafka topic for position change events emitted from outbox. |
| `KAFKA_TOPIC_TRADE_DLQ` | No | `trades.settled.dlq` | Dead-letter queue topic for invalid/poisoned trade events. |
| `WALLET_GRPC_ADDR` | No | `localhost:50052` | Network address of the Wallet Service for querying cash balances. |
| `MARKET_GRPC_ADDR` | No | `localhost:50054` | Network address of the Market Service for querying live mark prices. |
| `PORTFOLIO_GRPC_PORT` | No | `:50058` | TCP port on which the Portfolio gRPC server listens. |
| `PORTFOLIO_METRICS_PORT` | No | `:9091` | HTTP port exposing Prometheus `/metrics` and health `/healthz`, `/ready`. |
| `LOG_LEVEL` | No | `info` | Structured Zap log level (`debug`, `info`, `warn`, `error`). |
