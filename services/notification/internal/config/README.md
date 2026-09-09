# Notification Service — Internal Configuration Guide

This document provides a technical explanation of the `internal/config` package in the **TradeDrift Notification Service** located in `services/notification/internal/config/`.

It details the package's purpose, the specific architectural and operational problems it solves, the design of the `Config` struct, a function-by-function breakdown, and the configuration loading flow.

---

## Purpose of `internal/config`

The `internal/config` package serves as the **single source of truth for runtime configuration** in the Notification Service. It acts as a typed boundary between raw operating system environment variables and the microservice's internal components.

```
services/notification/internal/config/
└── config.go       # Struct definition, environment parsing, validation & broker splitter
```

### Core Responsibilities
1. **Strongly-Typed Contract**: Converts untyped string environment variables into validated Go types (`string`, `int`, `[]string`).
2. **Fail-Fast Boot Validation**: Rejects invalid or incomplete configurations during application startup before connection pools, listeners, or consumers are created.
3. **Sensible Local Defaults**: Provides immediate plug-and-play capability for local Docker Compose and development workflows without requiring extensive `.env` setup.

---

## Problems Solved by `config.go`

| Problem | Failure Without `config.go` | How `config.go` Solves It |
|---|---|---|
| **Magic Strings & Sprawl** | `os.Getenv("SOME_VAR")` scattered across handlers and repositories makes tracking environment dependencies impossible. | Centralizes all 12 configurable parameters into a single typed `Config` struct. |
| **Late Runtime Failures** | If a database DSN is missing, the service might boot, listen on gRPC, and crash only when the first user request hits the database. | Validates critical fields (`PostgresDSN`, `KafkaBrokers`) during `config.Load()`, returning descriptive errors that halt boot immediately. |
| **Broker List Formatting** | Cloud and Kubernetes Kafka setups provide comma-separated strings with irregular whitespace (e.g. `"broker1:9092, broker2:9092, "`). | `parseBrokers()` tokenizes, trims, and filters empty entries, preventing connection parser panics. |
| **Silent Queue Blocking** | A missing Dead-Letter Queue (DLQ) topic causes consumer groups to stall on the first poison message. | Defines `NOTIFICATION_DLQ_TOPIC` with a sensible default (`notifications.dlq`) and surfaces it directly to `main.go` for startup verification. |

---

## `Config` Struct Field Reference

The `Config` struct maps directly to system environment variables:

| Field | Go Type | Environment Variable | Default Value | Description & Target Component |
|---|---|---|---|---|
| `PostgresDSN` | `string` | `NOTIFICATION_POSTGRES_DSN` | `postgres://postgres:postgres@localhost:5432/tradedrift_notification?sslmode=disable` | PostgreSQL connection string used by `pgxpool.Pool` and Goose migrations. |
| `MigrationsDir` | `string` | `NOTIFICATION_MIGRATIONS_DIR` | `services/notification/migration` | Relative or absolute path to Goose SQL migrations executed at startup. |
| `RedisAddr` | `string` | `REDIS_ADDR` | `localhost:6379` | Host and port of the Redis broker for real-time WebSocket Pub/Sub broadcasting. |
| `RedisSentinelMaster` | `string` | `REDIS_SENTINEL_MASTER` | `""` (Empty string) | Master name when deploying behind Redis Sentinel high availability. |
| `RedisPassword` | `string` | `REDIS_PASSWORD` | `""` (Empty string) | Authentication secret for secured Redis environments. |
| `RedisDB` | `int` | *(Hardcoded: `0`)* | `0` | Redis logical database index for notification channels. |
| `KafkaBrokers` | `[]string`| `KAFKA_BROKERS` | `["localhost:9092"]` | Comma-separated list of Kafka seed broker endpoints. |
| `KafkaGroupID` | `string` | `NOTIFICATION_KAFKA_GROUP_ID` | `notification-service-group` | Kafka consumer group identifier for horizontal partition balancing. |
| `KafkaDLQTopic` | `string` | `NOTIFICATION_DLQ_TOPIC` | `notifications.dlq` | Kafka topic destination for malformed or unprocessable domain messages. |
| `GRPCPort` | `string` | `NOTIFICATION_GRPC_PORT` | `:50059` | TCP listen port for the internal gRPC notification server. |
| `MetricsPort` | `string` | `NOTIFICATION_METRICS_PORT` | `:9092` | TCP listen port for HTTP Prometheus `/metrics`, `/healthz`, and `/ready`. |
| `LogLevel` | `string` | `LOG_LEVEL` | `info` | Uber Zap log severity threshold (`debug`, `info`, `warn`, `error`). |

---

## Function-by-Function Breakdown

### 1. `Load() (Config, error)`

```go
func Load() (Config, error)
```

#### Purpose
Orchestrates reading environment variables via `platform/config.GetEnv`, applies defaults, executes boundary validation, and returns the assembled `Config` object.

#### What Problem It Solves
- Eliminates silent startup failures. If mandatory settings cannot be constructed, it returns an explicit error with clear instructions (e.g. `"KAFKA_BROKERS must contain at least one valid broker address"`).

#### Validation Logic
1. **PostgreSQL DSN Check**:
   ```go
   dsn := config.GetEnv("NOTIFICATION_POSTGRES_DSN", "...")
   if dsn == "" {
       return Config{}, fmt.Errorf("NOTIFICATION_POSTGRES_DSN is required")
   }
   ```
2. **Kafka Broker List Parsing**:
   ```go
   rawBrokers := config.GetEnv("KAFKA_BROKERS", "localhost:9092")
   brokers := parseBrokers(rawBrokers)
   if len(brokers) == 0 {
       return Config{}, fmt.Errorf("KAFKA_BROKERS must contain at least one valid broker address")
   }
   ```

---

### 2. `parseBrokers(raw string) []string`

```go
func parseBrokers(raw string) []string
```

#### Purpose
Sanitizes and tokenizes a comma-delimited broker string into a clean slice of broker addresses.

#### What Problem It Solves
- In Kubernetes ConfigMaps or Docker Compose `.env` files, multi-broker configurations often contain trailing commas, newlines, or uneven spaces:
  ```
  KAFKA_BROKERS="kafka-1:9092, kafka-2:9092, , kafka-3:9092"
  ```
  Passing raw unsanitized strings directly to Kafka client libraries causes DNS resolution failures or socket connection crashes on the empty string `""`.

#### Implementation Strategy
- Splits on commas: `strings.Split(raw, ",")`.
- Trims whitespace from each token: `strings.TrimSpace(p)`.
- Discards empty strings, returning only non-empty, clean broker endpoints.

---

## Configuration Loading & Initialization Flow

```
OS Environment / .env File / Kubernetes ConfigMap
                      │
                      ▼
            platform/config.LoadEnv()
                      │
                      ▼
         notificationconfig.Load()
                      │
     ┌────────────────┴────────────────┐
     ▼                                 ▼
Read & Validate DSN               Read & Sanitize KAFKA_BROKERS
(Must be non-empty)               parseBrokers() (Split & Trim)
     │                                 │
     └────────────────┬────────────────┘
                      │
                      ▼
               Validation Check
             (Fail-Fast Boundary)
                      │
          ┌───────────┴───────────┐
          │                       │
     [Valid Config]        [Invalid Config]
          │                       │
          ▼                       ▼
   Return Config{}, nil     Return Config{}, err
          │                       │
          ▼                       ▼
    cmd/server/main.go      Panic / Fatal Shutdown
(Continues Bootstrap)     (Halts Container Loop)
```
