# Configuration Subsystem (`internal/config`)

This document provides a comprehensive architectural and operational guide to the centralized configuration management subsystem located in [`services/admin/internal/config/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/config/).

---

## Table of Contents

1. [Package Overview & Purpose](#1-package-overview--purpose)
2. [What Problems This Package Solves](#2-what-problems-this-package-solves)
3. [File Deep Dive: `config.go`](#3-file-deep-dive-configgo)
   - [Config Struct Breakdown](#config-struct-breakdown)
   - [Functions & Methods](#functions--methods)
4. [Environment Variables Reference Table](#4-environment-variables-reference-table)
5. [Architectural & Execution Flows](#5-architectural--execution-flows)
   - [Flow 1: Configuration Ingestion & Distribution Flow](#flow-1-configuration-ingestion--distribution-flow)
   - [Flow 2: Kafka Broker String Parsing & Sanitization Flow](#flow-2-kafka-broker-string-parsing--sanitization-flow)
   - [Flow 3: 12-Factor Environment Precedence Flow](#flow-3-12-factor-environment-precedence-flow)
6. [Best Practices & Security Notes](#6-best-practices--security-notes)

---

## 1. Package Overview & Purpose

The `internal/config` package implements a **strongly typed, immutable, centralized configuration provider** for the TradeDrift Admin Service.

In accordance with [The Twelve-Factor App (Factor III: Config)](https://12factor.net/config), microservices must strictly separate configuration from code. Configuration varies substantially across deploys (local development, Docker Compose, CI testing, staging, and production Kubernetes clusters), while code remains identical.

`internal/config` acts as the single gateway for all external runtime configuration. It reads environment variables at service bootstrap, applies sensible defaults for local development, sanitizes complex inputs (e.g. comma-delimited Kafka broker lists), and distributes typed configuration values to downstream components.

---

## 2. What Problems This Package Solves

| Problem | Failure Scenario Without Centralized Config | How `internal/config` Solves It |
| :--- | :--- | :--- |
| **Configuration Fragmentation** | Handlers, repositories, and background workers call `os.Getenv` independently throughout the codebase. Finding all required environment variables requires inspecting hundreds of source files. | Consolidates all environment variables into a **single, strongly typed `Config` struct** in [`config.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/config/config.go). |
| **Startup Crashes in Dev / CI** | Forgetting to set a non-critical environment variable causes immediate runtime panics (`nil pointer dereference` or empty DSN error). | Implements the **Fallback Default Pattern**: every variable has a battle-tested default tailored for local development and integration testing. |
| **String Parsing Duplication & Bugs** | Multiple components (e.g. `HealthWorker`, `OutboxPublisher`, `HealthHandler`) parse comma-separated `KAFKA_BROKERS` with naive `strings.Split`, leaving unhandled whitespace or trailing commas that fail network dials. | Provides a centralized `SplitKafkaBrokers()` method that splits, trims whitespace, and filters empty strings, guaranteeing clean network targets. |
| **Runtime Mutation Risks** | Code accidentally modifies a configuration variable during execution, causing unpredictable behavior across concurrent goroutines. | `Config` is passed as a **read-only, immutable parameter** during dependency injection in `cmd/server/main.go`. |

---

## 3. File Deep Dive: `config.go`

- **File**: [`config.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/config/config.go)
- **Primary Type**: `type Config struct`

### Config Struct Breakdown

```go
type Config struct {
    Port           string
    PostgresDSN    string
    AuthGRPCAddr   string
    WalletGRPCAddr string
    JWTSecret      string
    KafkaBrokers   string

    TradeHealthURL string
    PortHealthURL  string
    LiqHealthURL   string
    NotifHealthURL string

    OutboxInterval time.Duration
    SagaInterval   time.Duration
    HealthInterval time.Duration
}
```

#### Category 1: Network & Ingress
- **`Port`**: The TCP port the Admin HTTP server binds to (default: `8085`).

#### Category 2: Relational Persistence
- **`PostgresDSN`**: The PostgreSQL connection string used by `jackc/pgxpool` and Goose migrations. Configured with connection limits and SSL disable flags for local development.

#### Category 3: Downstream gRPC Targets
- **`AuthGRPCAddr`**: Host and port for the Auth Service gRPC server (default: `localhost:50051`). Used for session invalidation.
- **`WalletGRPCAddr`**: Host and port for the Wallet Service gRPC server (default: `localhost:50052`). Used for asset freezing and deep health checks.

#### Category 4: Security & Authentication
- **`JWTSecret`**: The HMAC-SHA256 secret key used by `platformjwt.HMACValidator` to verify incoming administrative bearer tokens and extract caller identity (`admin_id`, `role`).

#### Category 5: Event Streaming Infrastructure
- **`KafkaBrokers`**: Comma-separated list of Kafka broker endpoints (default: `localhost:9092`). Used by the outbox publisher and health probing worker.

#### Category 6: Microservice Health Probe Endpoints
- **`TradeHealthURL`**: HTTP readiness URL for the Trade execution service (default: `http://localhost:9090/ready`).
- **`PortHealthURL`**: HTTP readiness URL for the Portfolio balance service (default: `http://localhost:9091/ready`).
- **`LiqHealthURL`**: HTTP readiness URL for the Liquidity Engine (default: `http://localhost:8080/readyz`).
- **`NotifHealthURL`**: HTTP readiness URL for the Notification dispatch service (default: `http://localhost:9095/ready`).

#### Category 7: Background Daemon Intervals
- **`OutboxInterval`**: Cadence at which `OutboxPublisher` polls `admin_outbox` (fixed: `1 * time.Second`).
- **`SagaInterval`**: Cadence at which `SagaWorker` polls `admin_saga_tasks` for retryable jobs (fixed: `5 * time.Second`).
- **`HealthInterval`**: Cadence at which `HealthWorker` performs autonomous platform-wide health sweeps (fixed: `15 * time.Second`).

---

### Functions & Methods

#### 1. `Load() *Config`
```go
func Load() *Config
```
- **Purpose**: Reads all environment variables using `getEnv()` and constructs an initialized `*Config` instance.
- **Problem Solved**: Centralizes all configuration extraction into a single invocation during the early boot sequence in `main.go`.
- **Why We Need It**: Eliminates scattered global variables and ensures the application cannot begin running with half-initialized configurations.

#### 2. `(c *Config) SplitKafkaBrokers() []string`
```go
func (c *Config) SplitKafkaBrokers() []string
```
- **Purpose**: Parses the raw `KafkaBrokers` string (e.g. `"kafka1:9092, kafka2:9092, "`), splits by commas, strips leading/trailing spaces, and filters out empty tokens.
- **Problem Solved**: Prevents network dial failures caused by whitespace (e.g. `" kafka2:9092"`) or dangling commas.
- **Why We Need It**: Both `HealthWorker` and `HealthHandler` require a clean slice of broker targets to check multi-broker cluster reachability.

#### 3. `getEnv(key, defaultValue string) string`
```go
func getEnv(key, defaultValue string) string
```
- **Purpose**: Checks `os.Getenv(key)`. If non-empty, returns the environment variable; otherwise, falls back to `defaultValue`.
- **Problem Solved**: Guarantees the application never encounters unexpected empty strings for mandatory endpoints.
- **Why We Need It**: Enables seamless local development (`go run cmd/server/main.go`) without requiring a manual `.env` file export.

---

## 4. Environment Variables Reference Table

| Environment Variable | Default Value | Go Type | Description & Consumer |
| :--- | :--- | :--- | :--- |
| `PORT` | `"8085"` | `string` | TCP port for Admin HTTP REST API and metrics exposition. Consumed by `http.Server`. |
| `POSTGRES_DSN` | `"postgres://postgres:postgres@localhost:5432/tradedrift_admin?sslmode=disable"` | `string` | PostgreSQL connection string. Consumed by Goose migrations and `pgxpool.NewWithConfig`. |
| `AUTH_GRPC_ADDR` | `"localhost:50051"` | `string` | Downstream Auth Service gRPC address. Consumed by `client.NewAuthClient`. |
| `WALLET_GRPC_ADDR` | `"localhost:50052"` | `string` | Downstream Wallet Service gRPC address. Consumed by `client.NewWalletClient`. |
| `JWT_SECRET` | `"super-secret-jwt-key-tradedrift-dev-32bytes"` | `string` | HMAC key for validating admin JWT bearer tokens. Consumed by `platformjwt.NewHMACValidator`. |
| `KAFKA_BROKERS` | `"localhost:9092"` | `string` | Comma-separated list of Kafka broker endpoints. Consumed by `OutboxPublisher` and `HealthWorker`. |
| `TRADE_HEALTH_URL` | `"http://localhost:9090/ready"` | `string` | HTTP readiness URL for Trade Service. Consumed by `HealthWorker`. |
| `PORTFOLIO_HEALTH_URL` | `"http://localhost:9091/ready"` | `string` | HTTP readiness URL for Portfolio Service. Consumed by `HealthWorker`. |
| `LIQUIDITY_HEALTH_URL` | `"http://localhost:8080/readyz"` | `string` | HTTP readiness URL for Liquidity Service. Consumed by `HealthWorker`. |
| `NOTIFICATION_HEALTH_URL` | `"http://localhost:9095/ready"` | `string` | HTTP readiness URL for Notification Service. Consumed by `HealthWorker`. |

---

## 5. Architectural & Execution Flows

### Flow 1: Configuration Ingestion & Distribution Flow

```
                       OS Environment
                             │
                             ▼
                       config.Load()
                (getEnv with Dev Defaults)
                             │
                             ▼
                     *config.Config
                             │
       ┌─────────────────────┼─────────────────────┐
       ▼                     ▼                     ▼
  pgxpool.Pool         platformjwt           Background Workers
(PostgresDSN)          (JWTSecret)        (KafkaBrokers, Intervals)
       │                     │                     │
       └─────────────────────┼─────────────────────┘
                             ▼
                        http.Server
                       (Addr :Port)
```

---

### Flow 2: Kafka Broker String Parsing & Sanitization Flow

```
    Raw Input: "localhost:9092, 10.0.0.1:9092, , localhost:9093 "
                             │
                             ▼
               strings.Split(raw, ",")
                             │
                             ▼
               Loop Over Comma-Split Tokens
                             │
                             ▼
                    strings.TrimSpace()
                             │
                 Is trimmed string empty?
                  /                    \
            (Yes)/                      \(No)
                ▼                        ▼
       ┌─────────────────┐      ┌─────────────────┐
       │ Discard Token   │      │ Append to Slice │
       └─────────────────┘      └────────┬────────┘
                                         │
                                         ▼
            Sanitized Output: ["localhost:9092", "10.0.0.1:9092", "localhost:9093"]
```

---

### Flow 3: 12-Factor Environment Precedence Flow

```
                     Operating System
                            │
               os.Getenv(ENV_VARIABLE_NAME)
                            │
              Is Value Present & Non-Empty?
               /                         \
         (Yes)/                           \(No)
             ▼                             ▼
    ┌─────────────────┐           ┌─────────────────┐
    │  Use Provided   │           │ Use Hardcoded   │
    │   Environment   │           │   Safe Local    │
    │    Override     │           │   Dev Default   │
    └────────┬────────┘           └────────┬────────┘
             │                             │
             └──────────────┬──────────────┘
                            ▼
                  *config.Config Field
```

---

## 6. Best Practices & Security Notes

1. **Production Secret Management**:
   - The default `JWT_SECRET` (`super-secret-jwt-key-tradedrift-dev-32bytes`) is for local development only.
   - In production Kubernetes clusters, `JWT_SECRET` must be injected via a sealed secret, HashiCorp Vault, or AWS Secrets Manager.
2. **Immutability**:
   - The `*Config` object should never be modified after creation. Once `Load()` returns, treat all fields as constants.
3. **Adding New Dependencies**:
   - When integrating a new microservice or queue, declare the environment variable in `Config`, document it in this README, and provide a fallback default pointing to a mock or local Docker Compose service.
