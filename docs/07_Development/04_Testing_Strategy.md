# TradeDrift — Testing Strategy

> **Status:** ✅ Frozen (V1.0)
> **Document:** 04_Testing_Strategy.md
> **Directory:** docs/07_Development/
> **Last Updated:** July 2026

---

## 1. Unit Testing & Interface Mocking

All business logic must be covered by unit tests (files named `*_test.go`) utilizing interfaces to isolate external dependencies:

* **Mocking Rule:** Code must interact with databases, Redis, and Kafka through interfaces (e.g. `KafkaProducer` or `WalletRepository`). Mocks are generated or written manually to test edge cases (such as connection timeouts or double-spending) without spinning up live processes.
* **Test Isolation:** Unit tests must be fast, concurrent, and run without external network ports.
* **Test Command:**
  ```bash
  go test -v -short ./...
  ```

---

## 2. Integration Testing Suite

Integration tests verify that services communicate correctly with live data stores and brokers.

* **Local Environment Setup:** We use the local `deployments/docker-compose.yml` configuration to provision test containers:
  - PostgreSQL database.
  - Redis Sentinel cache.
  - Kafka event broker.
* **Execution Flow:**
  - Spin up the test dependencies: `docker-compose up -d`.
  - Execute integration test suites (these are marked with separate build tags or run as standalone test packages):
  ```bash
  go test -v -tags=integration ./...
  ```
  - Tear down the dependencies: `docker-compose down -v`.

---

## 3. Stress & Performance Testing

Before deploying updates to staging or production, performance critical paths (Logins, Placements, Fills) must be validated under simulated load:
* **Load Test Tooling:** Developers use k6 or custom Go benchmark scripts to test API performance boundaries.
* **Telemetry Verification:** During load test runs, log warnings and trace spans are analyzed to verify that database lock duration and gRPC latency remain within the frozen SLAs.
