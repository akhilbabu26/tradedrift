# Market Service — Configuration (`internal/config`)

> **Package:** `tradedrift/services/market/internal/config`  
> **Directory:** `services/market/internal/config/`  
> **Key File:** `config.go`

---

## 1. Purpose

The `config` package provides a centralized, type-safe struct representation of all environment variables required by the Market Service. It prevents hardcoded strings and guarantees that missing configuration values fall back to sensible production-ready defaults.

---

## 2. File Breakdown

### 📄 `config.go`
* **Struct:** `Config`
  ```go
  type Config struct {
      GRPCPort      string // Default: ":50054"
      PostgresDSN   string // Default: "postgres://user:pass@localhost:5432/tradedrift_market?sslmode=disable"
      MigrationsDir string // Default: "migration"
      KafkaBrokers  string // Default: "localhost:9092"
      KafkaGroupID  string // Default: "market-service-group"
      KafkaTopic    string // Default: "trade.executed.v1"
      LogLevel      string // Default: "info"
  }
  ```
* **Functions:**
  * `Load() *Config`: Reads system environment variables using `platform/config.GetEnv`, applies defaults, and returns a validated `*Config` pointer to `main.go`.

---

## 3. Tools & Dependencies Used

* **`tradedrift/platform/config`**: Platform-wide configuration helper that inspects environment variables and loads local `.env` files.
