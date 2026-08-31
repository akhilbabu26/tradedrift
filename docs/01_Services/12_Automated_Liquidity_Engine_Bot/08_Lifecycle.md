# Liquidity Engine — Service Lifecycle

> **Service:** `services/liquidity-engine`
> **Version:** V1.1

---

## Startup Flow

```
START
  │
  ▼
Load configuration (MarketConfig, env vars)
  │
  ▼
Connect Kafka (producer + consumer groups)
  │
  ▼
Connect Redis (depth reader)
  │
  ▼
Authenticate MM-001 (validate account + role via Account Service)
  │
  ▼
Load MM balances (from Wallet Service gRPC)
  │
  ▼
Load existing MM orders (from Order Service API)
  │
  ▼
Initialize inventory manager
  │
  ▼
Get reference prices (from config)
  │
  ▼
Generate target ladders (BTC-USDT, ETH-USDT, SOL-USDT)
  │
  ▼
Reconcile → Create missing orders only
  │
  ▼
RUNNING (event loop active)
```

---

## Runtime Loop

```
                    ┌───────────────────┐
                    │  Liquidity Engine │
                    └─────────┬─────────┘
                              │
                         Event Loop
                              │
             ┌────────────────┼─────────────────┐
             ▼                ▼                 ▼
        Order Event      Price Update      Timer Tick
        (Kafka)          (config/feed)     (30s health)
             │                │                 │
             └────────────────┼─────────────────┘
                              ▼
                       Update State
                              │
                              ▼
                       Risk Check
                   (spread invariant, STP guard)
                              │
                              ▼
                     Inventory Check
                   (Normal / Low / Critical)
                              │
                              ▼
                  Recalculate Target Ladder
                   (with inventory skew)
                              │
                              ▼
                        State Diff
                   (desired vs actual orders)
                              │
                   .----------+----------.
                   |                     |
                No diff               Diff
                   |                     |
                   v                     v
               NOTHING           Kafka Commands
                              (OrderCreate / OrderReplace)
```

---

## Shutdown Flow

```
SIGTERM received
  │
  ▼
Stop all strategy cycles
  │
  ▼
Stop generating new orders
  │
  ▼
Send batch cancel for all MM resting orders
  │
  ▼
Wait for OrderCancelled confirmations (with timeout)
  │
  ▼
Flush Kafka producer buffer
  │
  ▼
Close Redis connection
  │
  ▼
SHUTDOWN COMPLETE (zero-delta state)
```

> Unlike a crash, a **graceful shutdown** can issue cancel commands with high confidence
> that ME is alive and will process them. The batch cancel on shutdown reduces the ME's
> book state that the next startup must reconcile against.

---

## Directory Structure

```
services/liquidity-engine/
│
├── cmd/
│   └── server/
│       └── main.go                  # Entrypoint, config loading, graceful shutdown
│
├── internal/
│   ├── engine/
│   │   └── engine.go                # Main orchestrator: event loop, strategy dispatch
│   │
│   ├── config/
│   │   └── config.go                # MarketConfig, MM account ID, Kafka/Redis env
│   │
│   ├── pricing/
│   │   └── reference_price.go       # V1: static configured price; V2: external feed
│   │
│   ├── strategy/
│   │   ├── strategy.go              # MarketStrategy interface
│   │   ├── ladder.go                # Bid/Ask ladder calculation, geometric step sizes
│   │   └── inventory_skew.go        # Ask/Bid depth reduction based on inventory levels
│   │
│   ├── order/
│   │   ├── manager.go               # Desired vs actual order diff + create/cancel
│   │   ├── tracker.go               # In-memory map of live MM orders by client_order_id
│   │   └── reconciler.go            # Full reconciliation loop per market cycle
│   │
│   ├── inventory/
│   │   ├── manager.go               # Live inventory tracking from TradeExecuted events
│   │   ├── limits.go                # Target / Low / Critical threshold evaluation
│   │   └── replenishment.go         # Replenishment request to System Treasury
│   │
│   ├── events/
│   │   ├── consumer.go              # Kafka consumer for orders.events & trades.executed
│   │   └── handlers.go              # Per-event handlers routing to order/inventory mgrs
│   │
│   ├── kafka/
│   │   ├── producer.go              # orders.commands publisher (OrderCreate/Replace)
│   │   └── consumer.go              # Kafka consumer group setup
│   │
│   ├── redis/
│   │   └── depth.go                 # Reads depth:{market_id} for best bid/ask
│   │
│   └── monitor/
│       └── health.go                # /healthz + /readyz HTTP endpoints
│
├── Dockerfile
├── go.mod
└── go.sum
```
