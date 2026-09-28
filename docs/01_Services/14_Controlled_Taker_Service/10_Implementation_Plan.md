# Controlled Taker Service — Implementation Plan & Testing Matrix

> **Status:** 📐 Designed (V1.2 — Dynamic Depth Sizing Invariant Enforced)  
> **Service:** Controlled Taker Service (`services/controlled-taker`)  
> **Document:** `10_Implementation_Plan.md`  
> **Last Updated:** September 2026  

---

## 1. Verified Infrastructure Contracts

Before finalizing the implementation plan, we verified all existing contracts directly against the active codebase:

| Contract Surface | Target File in Codebase | Verified Invariant |
| :--- | :--- | :--- |
| **`CreateOrderRequest` Proto** | `platform/api/gen/order/v1/order.pb.go` | Accepts `UserId`, `MarketId`, `Side`, `OrderType`, `Price`, `Quantity`, `IdempotencyKey`. |
| **Order Service gRPC** | `services/order/cmd/server/main.go:40` | Listens on `ORDER_GRPC_PORT` (`:50053`). |
| **Order Idempotency** | `services/order/internal/service/service.go:74-86` | Queries `FindByIdempotencyKey`; returns existing order on identical retry. |
| **Wallet Fund Reservation** | `services/order/internal/service/service.go:240-255` | Calls `wallet.ReserveFunds` for non-MM accounts (`CT-001`). |
| **Outbox Kafka Emission** | `services/order/internal/service/service.go:203-228` | Generates `OrderCreated` envelope with `market_id` partition key. |
| **Redis Depth Schema** | `services/order/internal/service/price_filter.go:68-72` | Key is `depth:{market_id}`; JSON contains `bids` & `asks` arrays with `price` and `quantity`. |
| **Matching Engine Invariant** | `services/matching-engine/internal/matcher/matcher.go:231` | Execution price is **always maker's price**; crossable logic supports limit orders with price caps. |
| **Settlement SettleTrade** | `services/wallet/internal/service/settle_trade.go:108-160` | Requires reservation for non-MM buyer/seller; debits reserved and credits available. |

---

## 2. Non-Negotiable Implementation Invariant: Dynamic Depth Sizing

> [!CAUTION]
> **CODE REVIEW REJECTION CRITERIA:**  
> The developer **MUST NOT** implement static or hardcoded order sizes:
> ```go
> // ❌ STRICTLY FORBIDDEN:
> lowOrderQuantity  := 0.01  // REJECT
> midOrderQuantity  := 0.05  // REJECT
> highOrderQuantity := 0.10  // REJECT
> ```
> Every order quantity **MUST** be computed dynamically from the **current live order-book depth snapshot** at the exact moment the taker decision is executed:
> - **LOW:** $\text{Current L1 Quantity} \times (5\% - 15\%)$
> - **MID:** $\text{Current L1 Quantity} \times (20\% - 50\%)$
> - **HIGH:** Dynamically calculated from **cumulative reachable live depth** across levels $1, \dots, m$ within the slippage band.

---

## 3. Phased Engineering Roadmap

```
Phase 1: Database Migration (Wallet Seeding)
  └── services/wallet/migration/00011_seed_ct001_wallet.sql
      - Seed CT-001 (00000000-0000-0000-0000-000000000002) with USDT, BTC, ETH, SOL

Phase 2: Service Scaffolding & Configuration
  └── services/controlled-taker/
      - go.mod, cmd/server/main.go
      - internal/account/identity.go, internal/config/config.go (self-contained market configs)

Phase 3: Service Clients
  └── internal/clients/
      - orderservice: gRPC CreateOrder
      - redisdepth: GET depth:{market_id}

Phase 4: Sizing & Policy Engine
  └── internal/engine/
      - profile.go: Dynamic live-depth sizing formulas (LOW/MID on L1, HIGH on cumulative)
      - selector.go: Inventory mean-reverting BUY/SELL bias
      - safety.go: Notional caps, rate limiters, circuit breaker

Phase 5: Market Worker Orchestration
  └── internal/engine/
      - worker.go: Per-market jitter timer loop with explicit min/base/max intervals
      - engine.go: Manager lifecycle, cold-start staggering, graceful shutdown

Phase 6: Health & Metrics
  └── internal/health/server.go, internal/metrics/metrics.go

Phase 7: Containerization & Compose Integration
  └── Dockerfile, docker-compose.yml entry
```

---

## 4. File-Level Change Inventory

| Path | Action | Type | Purpose |
| :--- | :--- | :--- | :--- |
| `services/wallet/migration/00011_seed_ct001_wallet.sql` | **NEW** | SQL Migration | Seed `CT-001` wallet account. |
| `services/controlled-taker/go.mod` | **NEW** | Go Module | Go module definition. |
| `services/controlled-taker/cmd/server/main.go` | **NEW** | Go Source | Process entrypoint and signal handler. |
| `services/controlled-taker/internal/account/identity.go` | **NEW** | Go Source | `CT-001` UUID and system constants. |
| `services/controlled-taker/internal/config/config.go` | **NEW** | Go Source | Self-contained market configs, limits, profile parameters. |
| `services/controlled-taker/internal/clients/orderservice/client.go` | **NEW** | Go Source | gRPC client calling Order Service. |
| `services/controlled-taker/internal/clients/redisdepth/reader.go` | **NEW** | Go Source | Redis reader for depth snapshots. |
| `services/controlled-taker/internal/engine/profile.go` | **NEW** | Go Source | Dynamic depth-derived sizing logic (zero hardcoded sizes). |
| `services/controlled-taker/internal/engine/selector.go` | **NEW** | Go Source | Direction selection with balance bias. |
| `services/controlled-taker/internal/engine/safety.go` | **NEW** | Go Source | Circuit breaker, cumulative depth guard, notional caps. |
| `services/controlled-taker/internal/engine/worker.go` | **NEW** | Go Source | Per-market timer and execution loop. |
| `services/controlled-taker/internal/engine/engine.go` | **NEW** | Go Source | Coordinator managing all market workers. |
| `services/controlled-taker/internal/health/server.go` | **NEW** | Go Source | `/healthz` and `/readyz` HTTP server. |
| `services/controlled-taker/internal/metrics/metrics.go` | **NEW** | Go Source | Prometheus metrics collector and HTTP handler. |
| `services/controlled-taker/Dockerfile` | **NEW** | Container | Multi-stage Docker build. |
| `docker-compose.yml` | **MODIFY** | Config | Add `controlled-taker` container definition. |

---

## 5. Comprehensive Verification Matrix

| Test Level | Scope | Success Criteria |
| :--- | :--- | :--- |
| **Unit Test** | `profile_test.go` (Dynamic Scaling) | Asserts that varying live L1 depth (e.g. 0.20 BTC vs. 0.50 BTC) produces proportionately scaled quantities (0.02 BTC vs. 0.05 BTC). Fails if sizes are hardcoded. |
| **Unit Test** | `profile_test.go` (Sweep Sizing) | Asserts that HIGH profile sweeps scale across cumulative depth of reachable levels. |
| **Unit Test** | `selector_test.go` | Mean-reverting bias shifts toward SELL when quote is low, and BUY when base is low. |
| **Unit Test** | `safety_test.go` | Circuit breaker trips after exactly 5 failures; cumulative depth guard enforces 1.5x depth across reachable levels. |
| **Integration Test** | Order Submission | Order Service gRPC receives valid `CreateOrderRequest` with valid slippage price cap; retries with same idempotency key return original order. |
| **Integration Test** | Redis Depth Probe | Worker handles missing, malformed, or stale depth snapshots without panicking. |
| **End-to-End Test** | Full Platform Pipeline | CTS submits order $\to$ ME matches against MM $\to$ `market_trades` receives row $\to$ `GetTicker24h` returns valid 24h High, Low, and Volume on Markets page $\to$ LE replenishes resting depth. |
