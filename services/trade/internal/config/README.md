# Trade Service Configuration (`internal/config/config.go`)

## 1. Overview & Purpose

The `services/trade/internal/config/config.go` package defines the **runtime configuration model and validation logic** for the Trade Service.

In distributed microservice architectures (especially financial exchanges), hardcoding configurations or scattering `os.Getenv` calls throughout the business logic leads to configuration drift, deployment failures, and security vulnerabilities. This package acts as the single source of truth for:
- Database connectivity strings and migration paths.
- Kafka broker topologies, consumer group identifiers, and topic routing (including Dead Letter Queues).
- Network bind ports for internal gRPC communication and external observability probes.
- Application logging verbosity.

---

## 2. Problems This Package Solves

| Problem | How `config.go` Solves It |
|---|---|
| **Scattered `os.Getenv` Calls** | Consolidates all environment parsing into a single `Config` struct, preventing runtime lookups in domain logic. |
| **Late / Cryptic Startup Crashes** | Implements **fail-fast validation**: if critical parameters (such as `TRADE_POSTGRES_DSN` or `KAFKA_BROKERS`) are missing or empty, it returns an explicit error before any socket or DB connection is opened. |
| **Kafka Broker String Inconsistencies** | In production, brokers are often specified as comma-separated strings with irregular whitespace (e.g. `"kafka1:9092, kafka2:9092"`). `parseBrokers()` sanitizes and splits this into a clean slice. |
| **Port Conflicts & Microservice Clashes** | Defines standard default ports (`:50057` for gRPC, `:9090` for metrics) that coordinate seamlessly with Docker Compose, Kubernetes manifests, and API Gateway upstream targets. |
| **Silent DLQ Misconfiguration** | Provides default values for both primary event ingestion (`trades.settled.v1`) and dead-letter queue routing (`trades.settled.dlq`), ensuring poison messages can always be safely isolated. |

---

## 3. Data Structures

### `type Config struct`

```go
type Config struct {
    PostgresDSN   string   // PostgreSQL connection URI
    MigrationsDir string   // Filesystem path to Goose SQL migrations
    KafkaBrokers  []string // List of Kafka broker addresses
    KafkaGroupID  string   // Consumer group ID for partition offset tracking
    KafkaTopic    string   // Primary topic to consume settled trades from (trades.settled.v1)
    KafkaDLQTopic string   // Dead letter queue topic for malformed/unsettled events (trades.settled.dlq)
    GRPCPort      string   // TCP port for gRPC server (e.g. :50057)
    MetricsPort   string   // TCP port for Prometheus & health HTTP probes (e.g. :9090)
    LogLevel      string   // Zap logger level: debug, info, warn, error
}
```

---

## 4. Functions & Logic Breakdown

### `Load() (Config, error)`

* **Purpose**: Primary factory function that resolves all environment variables, performs mandatory validations, and injects default fallbacks.
* **Problems Solved**:
  - **Mandatory Field Enforcement**:
    ```go
    dsn := config.GetEnv("TRADE_POSTGRES_DSN", "")
    if dsn == "" {
        return Config{}, fmt.Errorf("TRADE_POSTGRES_DSN is required")
    }
    ```
    If `TRADE_POSTGRES_DSN` is not provided, the service immediately halts with a clean error message rather than panicking on a database connection attempt.
  - **Sensible Defaults**:
    Assigns predictable defaults for optional environment variables:
    - `TRADE_MIGRATIONS_DIR` → `"migration"`
    - `KAFKA_GROUP_ID` → `"trade-service"`
    - `KAFKA_TOPIC_TRADE_SETTLED` → `"trades.settled.v1"`
    - `KAFKA_TOPIC_TRADE_DLQ` → `"trades.settled.dlq"`
    - `TRADE_GRPC_PORT` → `":50057"`
    - `TRADE_METRICS_PORT` → `":9090"`
    - `LOG_LEVEL` → `"info"`

---

### `parseBrokers(raw string) []string`

* **Purpose**: Parses, trims, and cleans a comma-separated list of Kafka broker addresses.
* **Problems Solved**:
  - Eliminates accidental whitespace introduced by deployment manifests or config maps (e.g., `"kafka:9092, kafka2:9092 "` → `["kafka:9092", "kafka2:9092"]`).
  - Ignores empty trailing commas or accidental empty entries (e.g., `"kafka:9092,"` does not produce an empty element `""`).
  - Ensures `len(brokers) > 0` validation succeeds only when at least one valid broker host:port exists.

---

## 5. Architectural Flows

### Flow A: Configuration Resolution Flow

```mermaid
flowchart TD
    Start([main.go Starts]) --> LoadEnv[config.LoadEnv - Read .env if present]
    LoadEnv --> CallLoad[config.Load - Read Environment Variables]
    
    CallLoad --> CheckDSN{Is TRADE_POSTGRES_DSN set?}
    CheckDSN -- No --> ErrDSN[Return error: TRADE_POSTGRES_DSN is required]
    CheckDSN -- Yes --> ReadBrokers[Read KAFKA_BROKERS]
    
    ReadBrokers --> ParseBrokers[parseBrokers: Split by comma & trim whitespace]
    ParseBrokers --> CheckBrokers{len brokers > 0?}
    CheckBrokers -- No --> ErrBrokers[Return error: KAFKA_BROKERS is required]
    CheckBrokers -- Yes --> ApplyDefaults[Apply Defaults for Ports, Topics, LogLevel]
    
    ApplyDefaults --> ReturnConfig[Return populated Config struct]
    ReturnConfig --> MainBoot[main.go proceeds with DB & Ports]
    
    ErrDSN --> Panic([Service Panics Fast on Boot])
    ErrBrokers --> Panic
```

---

### Flow B: Dependency Distribution Flow

Once `Config` is constructed in `main.go`, its fields are distributed cleanly to each architectural component:

```mermaid
graph LR
    subgraph Config [Config Struct]
        dsn[PostgresDSN / MigrationsDir]
        kafka[KafkaBrokers / GroupID / Topics]
        grpcPort[GRPCPort]
        metricsPort[MetricsPort]
        logLevel[LogLevel]
    end

    subgraph Subsystems [Trade Service Subsystems]
        PG[(PostgreSQL Pool & Goose Migrations)]
        Consumer[Kafka Consumer & DLQ Producer]
        GRPC[gRPC Server Listener]
        HTTP[Prometheus & Health Probe Server]
        Logger[Uber Zap Logger]
    end

    dsn --> PG
    kafka --> Consumer
    grpcPort --> GRPC
    metricsPort --> HTTP
    logLevel --> Logger
```

---

## 6. Environment Variables Reference Table

| Variable Name | Required | Default Value | Description |
|---|---|---|---|
| `TRADE_POSTGRES_DSN` | **Yes** | *None* | Connection URI for the Trade database (e.g., `postgres://user:pass@host:5432/tradedrift_trade?sslmode=disable`). |
| `KAFKA_BROKERS` | **Yes** | `localhost:9092` | Comma-separated list of Kafka broker endpoints (e.g., `kafka:29092,kafka2:29092`). |
| `TRADE_MIGRATIONS_DIR` | No | `migration` | Relative or absolute path to the directory containing Goose SQL migration scripts. |
| `KAFKA_GROUP_ID` | No | `trade-service` | Consumer group name used for partition offset tracking on `trades.settled.v1`. |
| `KAFKA_TOPIC_TRADE_SETTLED` | No | `trades.settled.v1` | Incoming event topic carrying settlement confirmations from the Wallet service. |
| `KAFKA_TOPIC_TRADE_DLQ` | No | `trades.settled.dlq` | Dead Letter Queue topic to route corrupted, unparseable, or sequence-invalid events. |
| `TRADE_GRPC_PORT` | No | `:50057` | TCP port on which the Trade Service serves gRPC client queries. |
| `TRADE_METRICS_PORT` | No | `:9090` | TCP port on which `/metrics`, `/healthz`, and `/ready` HTTP endpoints are served. |
| `LOG_LEVEL` | No | `info` | Minimum log severity: `debug`, `info`, `warn`, `error`. |
