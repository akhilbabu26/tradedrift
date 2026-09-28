# Controlled Taker Service — Architecture & Component Decomposition

> **Status:** 📐 Designed (V1.1 — Updated with Architectural Feedback)  
> **Service:** Controlled Taker Service (`services/controlled-taker`)  
> **Document:** `02_Architecture.md`  
> **Last Updated:** September 2026  

---

## 1. System Context & Minimal External Coupling

The Controlled Taker Service integrates cleanly with the TradeDrift platform with **strictly minimized runtime dependencies**:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       Controlled Taker Service                              │
│                                                                             │
│  ┌──────────────────┐    ┌──────────────────┐    ┌───────────────────────┐  │
│  │ Self-Contained   │    │ Per-Market Loops │    │  Safety & Rate Limit  │  │
│  │ Market Config    │    │ (BTC, ETH, SOL)  │    │  (Circuit Breakers)   │  │
│  └────────┬─────────┘    └────────┬─────────┘    └───────────┬───────────┘  │
│           │                       │                          │              │
└───────────┼───────────────────────┼──────────────────────────┼──────────────┘
            │                       │                          │
            │                       ▼                          │
            │           ┌──────────────────────┐               │
            │           │ Redis Client (Depth) │               │
            │           └───────────┬──────────┘               │
            │                       │                          │
            ▼                       ▼                          ▼
 ┌──────────────────────┐ ┌───────────────────┐    ┌───────────────────────┐
 │ Static Config / Env  │ │ Redis: depth:{id} │    │  Order Service gRPC   │
 │ (Zero Market Svc     │ │ (Top-of-book L2)  │    │  (CreateOrder API)    │
 │  Runtime Coupling)   │ └───────────────────┘    └───────────┬───────────┘
 └──────────────────────┘                                      │
                                                               ▼
                                                   ┌───────────────────────┐
                                                   │  Wallet Service gRPC  │
                                                   │  (ReserveFunds)       │
                                                   └───────────┬───────────┘
                                                               │
                                                               ▼
                                                   ┌───────────────────────┐
                                                   │  Order Service Outbox │
                                                   └───────────┬───────────┘
                                                               │
                                                               ▼
                                                   ┌───────────────────────┐
                                                   │ Kafka: orders.commands│
                                                   │ (Key = MarketID)      │
                                                   └───────────┬───────────┘
                                                               │
                                                               ▼
                                                   ┌───────────────────────┐
                                                   │    Matching Engine    │
                                                   └───────────────────────┘
```

### Decoupling Decision: Elimination of Market Service Runtime Dependency
In earlier design drafts, CTS was proposed to query `MarketService.ListMarkets` over gRPC. However, analyzing the repository shows that:
1. The **Liquidity Engine** ([`config.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/liquidity-engine/internal/config/config.go)) defines its target markets (`BTC-USDT`, `ETH-USDT`, `SOL-USDT`), tick sizes, and lot sizes statically via configuration.
2. Market definitions are standard platform constants (`BTC_PARTITION=0`, `ETH_PARTITION=1`, `SOL_PARTITION=2`).
3. Adding a runtime gRPC call to Market Service creates unnecessary failure coupling: if Market Service is restarting or running migrations, CTS would fail to boot.

**Architectural Decision:** CTS will mirror the Liquidity Engine's proven pattern by defining its market configurations directly within its configuration layer. This reduces runtime dependencies to **only two essential services**:
1. **Redis:** Fast in-memory inspection of `depth:{market_id}`.
2. **Order Service:** Canonical gRPC `CreateOrder` entrypoint.

---

## 2. Per-Market Worker Isolation Architecture

CTS treats each market as a completely isolated domain:

```
                          Controlled Taker Service
                                     │
            ┌────────────────────────┼────────────────────────┐
            ▼                        ▼                        ▼
    BTC-USDT Worker          ETH-USDT Worker          SOL-USDT Worker
    ├── Jitter Timer         ├── Jitter Timer         ├── Jitter Timer
    ├── Circuit Breaker      ├── Circuit Breaker      ├── Circuit Breaker
    └── Volume Accumulator   └── Volume Accumulator   └── Volume Accumulator
```

### Benefits of Per-Market Isolation:
1. **Fault Quarantine:** If ETH-USDT experiences an unexpected error, a price filter rejection, or trips its circuit breaker, **BTC-USDT and SOL-USDT continue trading normally**.
2. **Independent Calibration:** Each market operates on its own calibrated lot sizes, tick steps, notional caps, and interval profiles.
3. **No Lock Contention:** Workers do not share locks or synchronization channels; each loop executes autonomously.

---

## 3. Internal Package Decomposition

Following TradeDrift repository conventions:

```
services/controlled-taker/
├── cmd/
│   └── server/
│       └── main.go                 # App bootstrap, lifecycle, signal handling
├── internal/
│   ├── account/
│   │   └── identity.go             # CT-001 canonical UUID and constants
│   ├── config/
│   │   └── config.go               # Static market configs, limits, profile parameters
│   ├── engine/
│   │   ├── engine.go               # Master orchestrator managing market workers
│   │   ├── worker.go               # Per-market loop: timer, depth evaluation, execution
│   │   ├── profile.go              # Low, Mid, High cumulative depth sizing logic
│   │   ├── selector.go             # Direction (BUY/SELL) selection with balance bias
│   │   └── safety.go               # Rate limiters, volume accumulators, circuit breaker
│   ├── clients/
│   │   ├── orderservice/           # gRPC client for Order Service (CreateOrder)
│   │   └── redisdepth/             # High-speed Redis reader for depth:{market_id}
│   ├── metrics/
│   │   └── metrics.go              # Prometheus collectors registration
│   └── health/
│       └── server.go               # HTTP /healthz and /readyz server
├── Dockerfile                      # Multi-stage container build
├── go.mod
└── go.sum
```

---

## 4. Readiness Probe Definition

The service readiness check (`GET /readyz`) reflects the per-market isolation architecture:

$$\mathbf{Ready} \iff \text{Redis Connected} \land \text{Order Service Connected} \land (\ge 1\ \text{Configured Market Has Valid Depth})$$

If SOL-USDT depth is temporarily missing or unseeded, but BTC-USDT and ETH-USDT are healthy, CTS reports **Ready (HTTP 200)** because it can perform useful work on active markets.
