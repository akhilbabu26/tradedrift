# `internal/orderservice` — Order Service gRPC Client

**Package:** `orderservice`  
**Service:** Liquidity Engine  
**Last Updated:** August 2026

---

## 1. What This Package Does

This package provides a **gRPC client** for the Order Service. The Liquidity Engine uses it for two primary purposes:
1. **MM Order Registration (`CreateMMOrder`)**: Prior to publishing an `OrderCreated` command to Kafka, the LE registers the order with the Order Service. This ensures the order is persisted in PostgreSQL with an authoritative UUID and `idempotency_key` (`client_order_id`) for crash recovery. For MM orders (`account.WalletUUIDStr`), the Order Service skips Wallet `ReserveFunds` and skips enqueuing an `outbox` record.
2. **Authoritative State Queries & Recovery (`ListMMOrders`, `GetOrderByClientID`)**: Used on startup to populate the order tracker with existing orders and recover generation numbers (`-G003`), and during periodic resync and timeout resolution.

---

## 2. Files In This Package

| File | Purpose |
| :--- | :--- |
| `client.go` | `Client` struct, `OrderState`, `CreateMMOrder`, `ListMMOrders`, `GetOrderByClientID`, `IsAvailable` |
| `README.md` | This documentation file |

---

## 3. The Architecture Contract & Authority Split

```
                       Liquidity Engine
                              │
               1. Register    │ 2. Command
               CreateMMOrder  │    Publish
                              ▼
┌──────────────────┐               ┌──────────────────┐
│  Order Service   │               │   Kafka Topic    │
│                  │               │ orders.commands  │
│  PostgreSQL      │               └────────┬─────────┘
│  (MM Order DB)   │                        │
│                  │                        ▼
│  - No Reserve    │               ┌──────────────────┐
│  - No Outbox     │               │ Matching Engine  │
└──────────────────┘               │                  │
                                   │  idempotency by  │
                                   │     orderID      │
                                   └────────┬─────────┘
                                            │
                                            ▼
                                       Order Book
```

| Responsibility | Authoritative Service |
| :--- | :--- |
| MM Order Persistence & Recovery | Order Service (PostgreSQL) |
| MM Command Publishing | Liquidity Engine (Sole Kafka publisher) |
| Live Order Book & Execution | Matching Engine |
| User Funds Reservation | Wallet Service / Order Service |
| MM Inventory Tracking | Liquidity Engine / Wallet Service |

---

## 4. Method Overview

| Method | Purpose | Called by |
| :--- | :--- | :--- |
| `CreateMMOrder` | Pre-register MM order before Kafka publish | `reconciler.applyCreate` |
| `ListMMOrders` | Startup & periodic recovery of live MM orders | `reconciler.SyncFromOrderService` |
| `GetOrderByClientID` | Query state by `client_order_id` (idempotency key) | `reconciler.CheckPendingTimeouts`, `handleCancellingTimeout` |
| `IsAvailable` | Health probe checking reachability | `health.Server` |

---

## 5. Generation Recovery Across Restarts

`client_order_id` follows the format:
```
"MM-BTC-USDT-ASK-01-G003"
    │               │
    ├── LevelID     └── Generation = 3
```
On engine startup, `ListMMOrders` extracts `Generation` and assigns it to `order.OSOrder`. The tracker initializes `generations[levelID] = max(generations[levelID], order.Generation)` so that subsequent replacements monotonically advance (e.g. `G004`).
