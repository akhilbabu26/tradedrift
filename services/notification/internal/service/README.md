# Notification Service — Core Domain Service Guide

This document provides an in-depth technical explanation of the `internal/service` package in the **TradeDrift Notification Service** located in `services/notification/internal/service/`.

It details the business logic orchestration, financial exchange counterparty privacy enforcement, duplicate event handling, gRPC retry idempotency, input validation, and end-to-end domain execution flows.

---

## Architecture Overview & Role of `internal/service`

In Domain-Driven Design (DDD) and Clean Architecture, `internal/service` contains the **core business rules** of the notification domain. It orchestrates interactions between Kafka event ingestion, gRPC synchronous alerts, and persistent storage without coupling to transport protocols or specific database drivers.

```
services/notification/internal/service/
├── service.go        # Service struct, sentinel errors, event models & unified UUID validation
├── events.go         # Asynchronous Kafka domain handlers (TradeSettled, OrderCancelled, PortfolioUpdated)
├── inbox.go          # Synchronous gRPC business logic, keyset pagination & idempotency recovery
└── service_test.go   # Comprehensive unit test suite with mock repository
```

### Core Responsibilities
1. **Counterparty Privacy Isolation**: Sanitizes trade execution fills so that buyers and sellers receive isolated notifications that never disclose each other's identity or order IDs.
2. **Idempotent Ingestion & Safe Retries**: Deduplicates incoming Kafka events and recovers existing notifications for retried gRPC calls via `processed_events`.
3. **Write Amplification Protection**: Distinguishes between persistent inbox alerts (stored in PostgreSQL) and ephemeral streaming snapshots (portfolio balances routed only to the outbox).
4. **Input Defense-in-Depth**: Validates UUIDs, closed-set enums, and mandatory fields before data touches PostgreSQL.

---

## Problems Solved by the Service Layer

| Problem | Risk Without Service Layer Rules | How the Service Layer Solves It |
|---|---|---|
| **Counterparty Information Leakage** | In a central limit orderbook (CLOB), revealing a counterparty's user ID or order ID allows predatory traders to deanonymize accounts and track trading strategies. | `HandleTradeSettled` splits a single trade into two independent, sanitized notifications. The buyer sees only `BUY`, quantity, price, and market; the seller sees only `SELL`. |
| **Kafka Redelivery Spam** | If Kafka redelivers an already-processed trade event, a user would receive duplicate notifications, corrupting unread badge counts. | Intercepts `repository.ErrAlreadyProcessed` from `CreateTradeSettledTx` and `CreateWithDedupTx`, logging a clean debug message and returning `nil` to acknowledge the offset. |
| **gRPC Retry Duplication** | If a Wallet service deposit notification RPC times out over the network, retrying the call without idempotency generates duplicate notifications. | Uses `IdempotencyKey` as the `processed_events` key. On duplicate retry (`ErrAlreadyProcessed`), `lookupByIdempotencyKey` recovers and returns the original notification. |
| **Database Write Amplification** | Portfolio updates occur continuously (after every price tick, deposit, and trade). Writing every snapshot to `notifications` would generate millions of dead rows daily. | `HandlePortfolioUpdated` writes directly to `notification_outbox` for Redis Pub/Sub broadcast, completely bypassing `notifications` and `processed_events`. |
| **Silent Failures on Corrupted UUIDs** | Discarding UUID generation errors (`id, _ := uuid.New()`) results in empty strings reaching PostgreSQL, failing with generic database constraint errors. | Explicitly checks and wraps all `platformuuid.New()` and `json.Marshal()` calls, bubbling descriptive errors up immediately. |

---

## File-by-File & Function-by-Function Breakdown

### 1. `service.go` — Entities, Validation & Sentinel Errors

#### Sentinel Domain Errors
```go
var (
    ErrInvalidUserID   = errors.New("invalid user_id: must be a valid UUID")
    ErrInvalidEventID  = errors.New("invalid event_id: must be a valid UUID")
    ErrInvalidObjectID = errors.New("invalid id: must be a valid UUID")
    ErrEmptyTitle      = errors.New("notification title cannot be empty")
    ErrEmptyMessage    = errors.New("notification message cannot be empty")
)
```

#### Shared UUID Validator
```go
func validateUUID(field, value string) error
```
- Matches canonical 8-4-4-4-12 hexadecimal UUID formats (case-insensitive).
- Rejects non-UUID strings early, returning wrapped errors indicating the exact failing field name.

#### Domain Event Payloads & Unified Validation
- **`TradeSettledEvent`**: Contains `EventID`, `TradeID`, `MarketID`, `BuyerUserID`, `SellerUserID`, `BuyOrderID`, `SellOrderID`, `Price`, `Quantity`, `ExecutedAt`.
  - `Validate()`: Enforces presence and UUID format on all required fields. Reused by both `kafka.Consumer` and `service.Service` to keep validation boundaries synchronized.
- **`OrderCancelledEvent`**: Contains `EventID`, `OrderID`, `UserID`, `MarketID`, `Reason`, `Timestamp`.
  - `Validate()`: Enforces UUID format on `event_id`, `order_id`, and `user_id`.
- **`PortfolioUpdatedEvent`**: Contains `EventID`, `UserID`, `TotalValue`, `CashBalance`, `RealizedPnL`, `UnrealizedPnL`, `UpdatedAt`.
  - `Validate()`: Enforces UUID format on `event_id` and `user_id`.

---

### 2. `events.go` — Kafka Domain Event Handlers

#### `HandleTradeSettled(ctx, ev *TradeSettledEvent) error`
- **Purpose**: Processes trade executions from `trades.settled.v1`.
- **Privacy Enforcement**:
  - **Buyer Notification**:
    ```go
    Message: fmt.Sprintf("Your BUY order of %s %s on %s filled at %s %s (Order: %s)", 
        ev.Quantity, ev.BaseAsset, ev.MarketID, ev.Price, ev.QuoteAsset, ev.BuyOrderID)
    ```
    *(Includes user's own `BuyOrderID`; strictly suppresses `SellerUserID` and `SellOrderID`)*.
  - **Seller Notification**:
    ```go
    Message: fmt.Sprintf("Your SELL order of %s %s on %s filled at %s %s (Order: %s)", 
        ev.Quantity, ev.BaseAsset, ev.MarketID, ev.Price, ev.QuoteAsset, ev.SellOrderID)
    ```
    *(Includes user's own `SellOrderID`; strictly suppresses `BuyerUserID` and `BuyOrderID`)*.
- **Envelope Identity**: Sets `EventID = ev.EventID` on both Redis envelopes while assigning separate `NotificationID`s.
- **Atomic Persistence**: Calls `repo.CreateTradeSettledTx`.
- **Duplicate Skip**: If `ErrAlreadyProcessed` is returned, logs debug and returns `nil`.

#### `HandleOrderCancelled(ctx, ev *OrderCancelledEvent) error`
- **Purpose**: Processes order cancellations from `orders.cancelled.v1`.
- **Logic**: Creates a `SYSTEM` category notification notifying the user of the cancellation and the reason (e.g. `"insufficient balance for fee"`).
- **Atomic Persistence**: Calls `repo.CreateWithDedupTx` using `ev.EventID` as the deduplication key.

#### `HandlePortfolioUpdated(ctx, ev *PortfolioUpdatedEvent) error`
- **Purpose**: Processes portfolio balance snapshots from `portfolios.updated.v1`.
- **Delivery Semantics (At-Least-Once, Duplicate-Tolerant)**:
  - Constructs a `model.RedisEnvelope` with `NotificationID = ""` (indicating an ephemeral state update).
  - Calls `repo.StageOutboxEvent` directly to stage an outbox row for channel `user:portfolio:{user_id}`.
  - Does **not** insert into `notifications` or `processed_events`, avoiding database write amplification.
  - Duplicate delivery is harmless because the payload is a state snapshot, not an append event.

---

### 3. `inbox.go` — Synchronous User Inbox Operations

#### `CreateNotification(ctx, input model.CreateNotificationInput) (*model.Notification, error)`
- **Purpose**: Direct gRPC entrypoint for internal microservices (Wallet, Auth, Admin).
- **Validation Pipeline**:
  1. Validates `UserID != ""` and `validateUUID("user_id", input.UserID)`.
  2. Validates `Title != ""` and `Message != ""`.
  3. Closed-set validation on `Type` (`INFO`, `TRADE_FILL`, `SYSTEM`, `ACCOUNT`).
  4. Validates `ReferenceID` (UUID) and `ReferenceType` (`TRADE`, `ORDER`, `DEPOSIT`) if present.
  5. Validates `IdempotencyKey` (UUID) if present.
- **Idempotency Strategy**:
  - Uses `IdempotencyKey` as the `sourceEventID` in `processed_events`.
  - If `CreateWithDedupTx` returns `repository.ErrAlreadyProcessed` and an `IdempotencyKey` was supplied:
    ```go
    existing, lookupErr := s.lookupByIdempotencyKey(ctx, input.UserID, input.IdempotencyKey)
    return existing, nil
    ```
  - Returns the original notification without creating a duplicate.

#### `lookupByIdempotencyKey(ctx, userID, idempotencyKey)`
- Queries `repo.GetNotificationIDByEventID(ctx, idempotencyKey)` to find the created notification UUID.
- Queries `repo.GetNotificationByID(ctx, userID, notifID)`, enforcing user ownership.

#### `GetNotifications(ctx, filter model.PaginationFilter)`
- Validates `user_id` and optional `cursor_id` (must be UUID).
- Calls `repo.GetByUserID(ctx, filter)` to fetch `limit + 1` rows for keyset traversal.

#### `MarkAsRead(ctx, userID, notificationID)`
- Validates both UUIDs.
- Calls `repo.MarkAsRead(ctx, userID, notificationID)`.

#### `MarkAllAsRead(ctx, userID)`
- Validates `user_id` and bulk-clears unread badge counts in PostgreSQL.

#### `GetUnreadCount(ctx, userID)`
- Validates `user_id` and retrieves the unread count for badge rendering.

---

## End-to-End Business Logic Flows

### Flow 1: Counterparty Privacy Dual Fan-Out (`HandleTradeSettled`)
```
TradeSettledEvent (Market: BTC-USDT, Buyer: U1, Seller: U2, BuyOrder: O1, SellOrder: O2)
       │
       ▼
service.HandleTradeSettled()
       │
       ├── 1. Generate Buyer Notification:
       │      UserID:  "U1"
       │      Title:   "Trade Executed"
       │      Message: "Your BUY order of 0.05 BTC on BTC-USDT filled at 96500 USDT"
       │      (Seller ID "U2" and Sell Order ID "O2" are completely absent)
       │
       ├── 2. Generate Seller Notification:
       │      UserID:  "U2"
       │      Title:   "Trade Executed"
       │      Message: "Your SELL order of 0.05 BTC on BTC-USDT filled at 96500 USDT"
       │      (Buyer ID "U1" and Buy Order ID "O1" are completely absent)
       │
       ├── 3. Build Staged Outbox Envelopes:
       │      Buyer Envelope  ──► Channel: "user:notifications:U1"
       │      Seller Envelope ──► Channel: "user:notifications:U2"
       │
       ▼
repo.CreateTradeSettledTx() ─── Atomic commit in PostgreSQL
```

---

### Flow 2: High-Frequency Ephemeral Portfolio Streaming
```
PortfolioUpdatedEvent (User: U1, NetWorth: $50,000, Cash: $12,000)
       │
       ▼
service.HandlePortfolioUpdated()
       │
       ├── Validate UUIDs
       ├── Generate outbox ID
       │
       ├── Build OutboxEvent:
       │      TargetChannel: "user:portfolio:U1"
       │      Payload: RedisEnvelope(EventID="ev-1", NotificationID="", Data=ev)
       │
       ▼
repo.StageOutboxEvent()
       │
       ├── Persists to notification_outbox ONLY
       ├── Bypasses "notifications" table (No dead rows)
       └── Bypasses "processed_events" table (No write amplification)
       │
       ▼
Redis Publisher streams to Gateway WebSocket relay
```

---

### Flow 3: gRPC CreateNotification Idempotency Recovery
```
Caller (Wallet Service)
       │
       ▼
CreateNotification(User="U1", Title="Deposit", IdempotencyKey="018f-key-A")
       │
       ├── Attempt 1:
       │      CreateWithDedupTx stores:
       │        - processed_events (event_id="018f-key-A", notification_id="notif-1")
       │        - notifications (id="notif-1")
       │      Returns notif-1
       │
       └── Attempt 2 (e.g. response dropped, caller retries with exact same key):
              CreateWithDedupTx returns ErrAlreadyProcessed
              Service detects IdempotencyKey is non-empty
              Calls lookupByIdempotencyKey("U1", "018f-key-A"):
                 1. GetNotificationIDByEventID("018f-key-A") ──► returns "notif-1"
                 2. GetNotificationByID("U1", "notif-1")      ──► returns notif-1
              Returns original notif-1 to caller
              (Zero duplicate notifications created)
```
