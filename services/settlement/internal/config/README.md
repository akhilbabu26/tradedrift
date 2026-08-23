# Settlement Service — Configuration (`internal/config`)

> **Package:** `tradedrift/services/settlement/internal/config`  
> **File:** `config.go`

---

## 1. Purpose

The `config` package is the single source of truth for all runtime configuration. It reads environment variables, applies validated defaults, and returns a typed `Config` struct to `main.go`. Every other package receives only the values it needs — nothing reads `os.Getenv` directly.

---

## 2. Files

```
services/settlement/internal/config/
├── config.go   ← Config struct + Load() + parseBrokers()
└── README.md   ← This file
```

---

## 3. Struct: `Config`

```go
type Config struct {
    PostgresDSN       string
    MigrationsDir     string
    KafkaBrokers      []string
    KafkaGroupID      string
    KafkaTopic        string
    WalletGRPCAddr    string
    WalletGRPCTimeout time.Duration
    LogLevel          string
}
```

| Field | Env Variable | Default | Used By |
|---|---|---|---|
| `PostgresDSN` | `SETTLEMENT_POSTGRES_DSN` | local DSN | `postgres.NewPool`, `postgres.RunMigrations` |
| `MigrationsDir` | `SETTLEMENT_MIGRATIONS_DIR` | `"migration"` | `postgres.RunMigrations` |
| `KafkaBrokers` | `KAFKA_BROKERS` | `"localhost:9092"` | `kafka.NewConsumer` |
| `KafkaGroupID` | `KAFKA_GROUP_ID` | `"settlement-service-group"` | `kafka.NewConsumer` |
| `KafkaTopic` | `KAFKA_TOPIC_TRADE_EXECUTED` | `"trades.executed"` | `kafka.NewConsumer` |
| `WalletGRPCAddr` | `WALLET_GRPC_ADDR` | `"localhost:50052"` | `client.NewWalletClient` |
| `WalletGRPCTimeout` | `WALLET_GRPC_TIMEOUT` | `5s` | `service.NewService` |
| `LogLevel` | `LOG_LEVEL` | `"info"` | `logger.New` |

**Why `KafkaBrokers []string` not `string`?**  
`kafka-go`'s `ReaderConfig.Brokers` accepts `[]string`. Converting at load time avoids repeated `strings.Split` calls throughout the codebase.

**Why `WalletGRPCTimeout time.Duration` not `int` seconds?**  
`context.WithTimeout` accepts `time.Duration`. Storing it as `Duration` in `Config` avoids a multiply-by-`time.Second` conversion at every call site.

---

## 4. Function: `Load`

```go
func Load() (Config, error)
```

**Purpose:** Reads all environment variables, applies defaults, validates values, and returns the populated `Config` struct.  
**Returns an error if:** `WALLET_GRPC_TIMEOUT` is set to a non-parseable duration string (e.g. `abc`, `0`, `-1s`).  
**Why return `error` instead of calling `log.Fatal`?** `Load` has no logger — the logger is created from `Config.LogLevel` *after* `Load` succeeds. Returning an error lets `main.go` panic with a clear message before the logger is constructed.

**Migration directory fallback logic:**
```go
if _, err := os.Stat(dir); os.IsNotExist(err) {
    if _, err2 := os.Stat("migration"); err2 == nil {
        dir = "migration"
    }
}
```
**Why?** When running inside Docker, the working directory may differ from the local development path. This fallback ensures the migration directory is found regardless of how the binary is invoked.

**`WALLET_GRPC_TIMEOUT` validation:**
```go
grpcTimeout, err := config.GetEnvAsDuration("WALLET_GRPC_TIMEOUT", 5*time.Second)
if err != nil {
    return Config{}, fmt.Errorf("invalid WALLET_GRPC_TIMEOUT: %w", err)
}
if grpcTimeout <= 0 {
    return Config{}, fmt.Errorf("WALLET_GRPC_TIMEOUT must be positive, got %s", grpcTimeout)
}
```
**Why explicit validation?** A zero or negative timeout would make every gRPC call immediately fail with `context deadline exceeded`. This would cause the settlement service to appear running but settle nothing — a silent correctness failure that is hard to diagnose. Failing at startup surfaces the misconfiguration immediately.

---

## 5. Function: `parseBrokers` (private)

```go
func parseBrokers(raw string) []string
```

**Purpose:** Splits a comma-separated broker list (e.g. `"kafka1:9092,kafka2:9092"`) into a `[]string` and trims whitespace from each entry.  
**Why trim whitespace?** Docker Compose environment values sometimes have trailing spaces from YAML formatting. Untrimmed spaces would cause `kafka-go` to fail DNS resolution with `"kafka1:9092 "` (note the trailing space).  
**Why private?** Only `Load` calls this. It is not part of any exported API.

---

## 6. External Packages

| Package | Why Used |
|---|---|
| `tradedrift/platform/config` | `GetEnv(key, default)` — reads env vars and auto-loads `.env` file; `GetEnvAsDuration` — parses duration string with error |
| `os` | `os.Stat` for migration directory existence check |
| `strings` | `strings.Split` and `strings.TrimSpace` for broker list parsing |
| `time` | `time.Duration` for `WalletGRPCTimeout`; `time.Second` for default value |
| `fmt` | Error formatting in `Load` |
