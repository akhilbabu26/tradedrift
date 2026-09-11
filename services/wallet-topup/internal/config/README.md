# Configuration Layer Architecture (`internal/config`)

The `internal/config` package centralizes environment configuration management for the **Wallet Top-Up Service**.

---

## 1. Directory Structure

```
internal/config/
├── config.go              # Configuration struct and environment loader
└── README.md              # This architectural and technical documentation
```

---

## 2. File Purpose & Problem Breakdown

### File: `config.go`

#### What Problems Does This File Solve?
1. **12-Factor App Compliance**:
   All configuration is loaded from environment variables rather than hardcoded in source files, allowing the same binary to run across local development, staging, Docker Compose, and Kubernetes without rebuilding.
2. **Safe Fallback Defaults**:
   If an environment variable is missing, `Load()` supplies secure, well-tested defaults (e.g. `PORT=8084`, `DAILY_LIMIT_INR=10`, `WALLET_GRPC_ADDR=localhost:50052`). This prevents runtime panics when spinning up developer environments.
3. **Type-Safe Duration & Numeric Parsing**:
   Converts string environment variables (such as integer limits and ticker durations) into strongly-typed Go primitives (`int64`, `time.Duration`).

---

## 3. Configuration Variables Breakdown

```go
type Config struct {
	Port               string
	PostgresDSN        string
	WalletGRPCAddr     string
	DailyLimitINR      int64
	WebhookSecret      string
	ReconcilerInterval time.Duration
	ExpiryInterval     time.Duration
}
```

| Field | Environment Variable | Default Value | Purpose |
| :--- | :--- | :--- | :--- |
| `Port` | `PORT` | `"8084"` | TCP port for the HTTP REST API server. |
| `PostgresDSN` | `POSTGRES_DSN` | `"postgres://postgres:postgres@localhost:5432/tradedrift_topup?sslmode=disable"` | PostgreSQL connection string for orders, limits, and webhook logs. |
| `WalletGRPCAddr` | `WALLET_GRPC_ADDR` | `"localhost:50052"` | Address of the downstream Core Wallet gRPC service. |
| `DailyLimitINR` | `DAILY_LIMIT_INR` | `10` (paise/rupees) | Per-user daily fiat deposit ceiling (₹10 in dev, ₹50,000 in prod). |
| `WebhookSecret` | `WEBHOOK_SECRET` | `"topup_mock_secret_key_12345"` | Shared HMAC-SHA256 secret for validating payment gateway webhooks. |
| `ReconcilerInterval` | Hardcoded / Configurable | `1 * time.Second` | Frequency at which background reconciler workers claim pending orders. |
| `ExpiryInterval` | Hardcoded / Configurable | `30 * time.Second` | Frequency at which background expiry workers sweep abandoned orders. |

---

## 4. Why We Need Specific Packages

| Package | Purpose & Problem Solved |
| :--- | :--- |
| `os` | Accesses operating system environment variables (`os.Getenv`). |
| `strconv` | Safely parses decimal integers (`strconv.ParseInt`) for numeric limits with bounds checking. |
| `time` | Provides typed time durations (`time.Duration`, `time.Second`) for ticker intervals. |
