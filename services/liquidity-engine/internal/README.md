# Liquidity Engine Internal Architecture (`internal/`)

This document provides a comprehensive overview of the `internal/` packages that make up the **TradeDrift Liquidity Engine (LE)**. It details the purpose of each package, the files within them, and why each file is required.

---

## 1. Overall Purpose of `internal/`

In Go, the `internal/` directory enforces package encapsulation: packages within `internal/` can only be imported by code inside the `liquidity-engine` service. 

The `internal/` layer contains all the core domain logic, concurrency management, price generation, state reconciliation, and infrastructure clients needed to run the automated market maker.

---

## 2. Directory Structure

```text
services/liquidity-engine/internal/
├── account/         # MM-001 canonical identity constants
├── config/          # Environment configuration loading & validation
├── engine/          # Top-level orchestrator & single-threaded event loop
├── health/          # HTTP /healthz, /readyz, and /status probe endpoints
├── inventory/       # Projected balances, effective capital, & skew tiers
├── kafka/           # Kafka producer (orders.commands) & consumer (trades.executed)
├── meclient/        # Direct HTTP health client for Matching Engine probing
├── metrics/         # Prometheus instrumentation & metrics registry
├── order/           # In-memory order tracker & two-pass diffing algorithm
├── orderservice/    # gRPC client for Order Service discovery & registration
├── pricing/         # Geometric spread ladder generation & tick/lot rounding
├── reconciler/      # Order creation/cancellation executor & timeout handlers
└── walletservice/   # gRPC client for Wallet Service authoritative balances
```

---

## 3. Package & File Breakdown

---

### 📁 `account/`
**Purpose:** Provides the single source of truth for the Market Maker system account identity.

| File | Why It Is Needed |
| :--- | :--- |
| [`constants.go`](./account/constants.go) | Defines the canonical UUID `00000000-0000-0000-0000-000000000001` and identifier `MM-001`. Eliminates identity mismatches across gRPC requests, Kafka payloads, and PostgreSQL queries. |

---

### 📁 `config/`
**Purpose:** Parses, validates, and provides structured configuration from environment variables.

| File | Why It Is Needed |
| :--- | :--- |
| [`config.go`](./config/config.go) | Reads env vars (`KAFKA_BROKERS`, `BTC_REFERENCE_PRICE`, etc.), sets production defaults, validates thresholds (e.g., `spreadBps > 0`, `lotSize > 0`), and maps partition assignments per trading pair. Fails fast on startup if configuration is invalid. |

---

### 📁 `engine/`
**Purpose:** The central orchestrator that coordinates all subsystems and runs the single-threaded event loop.

| File | Why It Is Needed |
| :--- | :--- |
| [`engine.go`](./engine.go) | Defines `Engine` struct, lifecycle states (`STARTING → SYNCING → RUNNING → DEGRADED → STOPPED`), and the main `Run()` select loop. Spawns ticker pumps and coordinates startup. |
| [`handlers.go`](./handlers.go) | Event handlers executed by the event loop: processes trade fills (`handleTrade`), refreshes wallet balances (`handleWalletRefresh`), and runs timeout checks (`handlePendingCheck`, `handleCancellingCheck`). |
| [`reconcile.go`](./reconcile.go) | Executes full (`runReconcileAll`) and targeted (`runReconcileMarket`) reconciliation cycles. Coordinates order state resynchronization with the Order Service. |
| [`snapshot.go`](./snapshot.go) | Implements the lock-free read interface. Publishes immutable `StatusSnapshot` instances via `atomic.Pointer` for safe concurrent access by HTTP health handlers. |
| [`engine_test.go`](./engine/engine_test.go) | Automated unit and integration tests verifying event loop behavior, concurrent reads without data races, and ME liveness threshold logic. |

---

### 📁 `health/`
**Purpose:** Exposes HTTP endpoints for container orchestration (Kubernetes), load balancers, and monitoring.

| File | Why It Is Needed |
| :--- | :--- |
| [`health.go`](./health.go) | Implements `/healthz` (liveness probe), `/readyz` (readiness probe based on strictly `RESTING` orders), and `/status` (detailed JSON diagnostics including uptime, market states, and inventory freshness). Reads atomically from engine snapshots. |

---

### 📁 `inventory/`
**Purpose:** Manages the market maker's capital balances and dynamically adjusts quoting depth.

| File | Why It Is Needed |
| :--- | :--- |
| [`manager.go`](./inventory/manager.go) | Maintains projected balances (`BTC`, `ETH`, `SOL`, `USDT`) updated immediately upon receiving trade fills. Calculates effective available capital after subtracting committed in-flight orders. |
| [`skew.go`](./inventory/skew.go) | Implements `ComputeSkew()`. Evaluates effective inventory against `Min` and `Critical` thresholds to automatically scale quote levels between Normal (12 levels), Low (6 levels), and Critical (0 levels). |

---

### 📁 `kafka/`
**Purpose:** Handles all Kafka communication for order commands and trade execution events.

| File | Why It Is Needed |
| :--- | :--- |
| [`producer.go`](./kafka/producer.go) | Publishes `OrderCreated` and `OrderCancelRequested` commands to `orders.commands`. Enforces partition keying (`msg.Key = marketID`) and strict envelope schemas. |
| [`consumer.go`](./kafka/consumer.go) | Reads from `trades.executed`. Wraps fills in `TradeEnvelope` with an `Ack` channel, ensuring Kafka offsets are committed **only after** state mutations complete (at-least-once guarantee). |

---

### 📁 `meclient/`
**Purpose:** Direct HTTP client for querying Matching Engine status.

| File | Why It Is Needed |
| :--- | :--- |
| [`client.go`](./meclient/client.go) | Probes `GET /status` on the Matching Engine with a 2-second timeout. Decouples liveness detection from trade activity so dead engines are detected even during zero-trade periods. |

---

### 📁 `metrics/`
**Purpose:** Prometheus telemetry and operational monitoring.

| File | Why It Is Needed |
| :--- | :--- |
| [`metrics.go`](./metrics/metrics.go) | Registers and updates Prometheus metrics: engine state gauge, active level gauges, reconcile duration histograms, fill counters, stale order counters, and probe failure counters. |

---

### 📁 `order/`
**Purpose:** In-memory order tracking and reconciliation diff algorithm.

| File | Why It Is Needed |
| :--- | :--- |
| [`tracker.go`](./order/tracker.go) | In-memory store of all MM orders. Manages the 5-state lifecycle (`PENDING → OS_REGISTERED → RESTING → CANCELLING → STALE`), generation counters (`G007`), and committed balance aggregations. |
| [`diff.go`](./order/diff.go) | Implements two-pass diffing. Compares the desired price ladder against the tracker's known set to output minimal `DiffCreate`, `DiffCancel`, and `DiffCorrect` actions. |
| [`tracker_test.go`](./order/tracker_test.go) | Unit tests verifying tracker state transitions, generation increments, and deduplication. |
| [`diff_test.go`](./order/diff_test.go) | Unit tests verifying diff generation across missing, existing, partially filled, and wrong-price orders. |

---

### 📁 `orderservice/`
**Purpose:** Read-only gRPC client for the persistent Order Service.

| File | Why It Is Needed |
| :--- | :--- |
| [`client.go`](./orderservice/client.go) | Provides `ListMMOrders` (startup recovery and periodic sync), `CreateMMOrder` (idempotent pre-registration before Kafka publish), and `GetOrderByClientID` (pending/cancelling verification). |

---

### 📁 `pricing/`
**Purpose:** Mathematical price ladder generation and decimal tick/lot compliance.

| File | Why It Is Needed |
| :--- | :--- |
| [`ladder.go`](./pricing/ladder.go) | Generates symmetric bid/ask ladders around reference prices using geometric spread formulas. Enforces tick-size flooring for prices and lot-size flooring for quantities to prevent Matching Engine rejections. |
| [`ladder_test.go`](./pricing/ladder_test.go) | Unit tests verifying spread calculations, tick rounding, and lot sizing across BTC, ETH, and SOL. |

---

### 📁 `reconciler/`
**Purpose:** Executes reconciliation diffs and resolves stuck in-flight orders.

| File | Why It Is Needed |
| :--- | :--- |
| [`reconciler.go`](./reconciler/reconciler.go) | Executes `Diff()` results. Runs the 3-step creation flow (`OS Register → SetPending → Kafka Publish`) and issues cancel commands. |
| [`sync.go`](./reconciler/sync.go) | Synchronizes the tracker with Order Service database snapshots, resolving missing orders based on their local lifecycle status. |
| [`timeouts.go`](./reconciler/timeouts.go) | Scans for stuck orders: verifies pending orders, promotes `OS_REGISTERED` to `RESTING` when ME is healthy, and retries/escalates `CANCELLING` orders to `STALE`. |
| [`reconciler_test.go`](./reconciler/reconciler_test.go) | Unit tests verifying execution of create/cancel commands, timeout resolution, and error handling. |

---

### 📁 `walletservice/`
**Purpose:** gRPC client for fetching authoritative wallet balances.

| File | Why It Is Needed |
| :--- | :--- |
| [`client.go`](./walletservice/client.go) | Calls `GetBalances` on the Wallet Service for MM-001. Seeds initial balances on startup and periodically refreshes projected balances. |

---

## 4. Package Dependency & Data Flow

```text
                             ┌───────────────┐
                             │    config     │
                             └───────┬───────┘
                                     │
                                     ▼
                             ┌───────────────┐
                             │    engine     │ ◄───── health (HTTP)
                             └───┬───┬───┬───┘
                                 │   │   │
             ┌───────────────────┘   │   └────────────────────┐
             ▼                       ▼                        ▼
     ┌───────────────┐       ┌───────────────┐        ┌───────────────┐
     │   inventory   │       │  reconciler   │        │     kafka     │
     └───────┬───────┘       └───┬───┬───┬───┘        └───────┬───────┘
             │                   │   │   │                    │
             ▼                   ▼   │   ▼                    ▼
     ┌───────────────┐   ┌───────┴┐  │ ┌───────────────┐   Matching Engine
     │ walletservice │   │ pricing│  │ │     order     │
     └───────────────┘   └────────┘  │ └───────────────┘
                                     ▼
                             ┌───────────────┐
                             │ orderservice  │
                             └───────────────┘
```
