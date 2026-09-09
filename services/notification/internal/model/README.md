# Notification Service — Domain Models & Entities Guide

This document provides a technical explanation of the `internal/model` package in the **TradeDrift Notification Service** located in `services/notification/internal/model/`.

It details the package's role as the shared domain layer, the architectural problems it solves, the design of constants and data structures, and the data lifecycle across the service.

---

## Purpose of `internal/model`

The `internal/model` package defines the **core domain entities, data transfer objects (DTOs), and wire envelopes** used throughout the Notification Service.

```
services/notification/internal/model/
└── model.go       # Core entities, outbox models, Redis envelopes & pagination filters
```

### Core Responsibilities
1. **Zero External Dependencies**: Pure Go with no imports of database drivers (`pgx`), protobuf generated code, Kafka libraries, or Redis clients.
2. **Circular Dependency Prevention**: Acts as the shared contract layer imported by `handler`, `service`, `repository`, and `publisher` without creating import loops.
3. **Protocol Independence**: Decouples persistent PostgreSQL database rows from transient gRPC Protobuf definitions and WebSocket streaming frames.

---

## Problems Solved by `model.go`

| Problem | Failure Scenario Without `model.go` | How `model.go` Solves It |
|---|---|---|
| **Circular Import Cycles** | `repository` needs structs returned by `service`, while `service` needs structs returned by `repository`. In Go, this causes compilation failure (`import cycle not allowed`). | Centralizes all entities in `model`, allowing all packages to import `model` unidirectionally without circular references. |
| **Lease Expiry Claim Races** | When multiple outbox publisher workers drain PostgreSQL concurrently, a slow worker whose lease expired can accidentally overwrite a re-claimed row. | Embeds `ClaimToken string` in `OutboxEvent`, ensuring workers track and verify claim ownership across method boundaries. |
| **Phantom Broadcasts on Redis** | If raw database rows are dumped directly into Redis Pub/Sub, the Gateway WebSocket streamer cannot tell if a message is a retry or a new notification. | Standardizes the **`RedisEnvelope`** format, binding each broadcast to an `(event_id, notification_id)` tuple for gateway-level deduplication. |
| **Offset Pagination Inefficiency** | `OFFSET / LIMIT` pagination suffers from $O(N)$ tuple scans and page drift when notifications are inserted while users paginate. | Encapsulates keyset cursor parameters (`CursorTime *time.Time`, `CursorID string`) in `PaginationFilter` for deterministic $O(\log N)$ seeks. |
| **State Drift vs. Closed Enums** | Hardcoding arbitrary strings across the codebase allows typo bugs like `"TradeFill"` vs `"TRADE_FILL"`. | Defines canonical constants (`TypeTradeFill`, `RefTypeTrade`, `OutboxStatusPending`) matching database check constraints. |

---

## Canonical Constants Reference

### 1. Notification Types
Categories identifying the origin and priority of user alerts:
```go
const (
    TypeInfo      = "INFO"       // General informational updates and announcements
    TypeTradeFill = "TRADE_FILL"  // Filled buy or sell trade executions
    TypeSystem    = "SYSTEM"     // Platform security notices, cancellations, and system alerts
    TypeAccount   = "ACCOUNT"    // KYC verification, password resets, and login events
)
```

### 2. Reference Types
Discriminators specifying the entity type referenced by `reference_id`:
```go
const (
    RefTypeTrade   = "TRADE"   // reference_id maps to a trade UUID
    RefTypeOrder   = "ORDER"   // reference_id maps to an order UUID
    RefTypeDeposit = "DEPOSIT" // reference_id maps to a ledger deposit transaction UUID
)
```

### 3. Outbox Lifecycle States
State machine tracking outbox records through their publishing lifecycle:
```go
const (
    OutboxStatusPending    = "PENDING"    // Newly staged record awaiting publisher pickup
    OutboxStatusProcessing = "PROCESSING" // Claimed by a worker; currently being published to Redis
    OutboxStatusProcessed  = "PROCESSED"  // Successfully published to Redis Pub/Sub
    OutboxStatusFailed     = "FAILED"     // Reserved: manual/terminal intervention for dead records
)
```

---

## Data Structures Deep Dive

### 1. `Notification` (Persistent Inbox Entity)

Represents a durable user inbox record in PostgreSQL.

```go
type Notification struct {
    ID            string     `json:"id"`
    UserID        string     `json:"user_id"`
    Title         string     `json:"title"`
    Message       string     `json:"message"`
    Type          string     `json:"type"`
    ReferenceID   string     `json:"reference_id,omitempty"`
    ReferenceType string     `json:"reference_type,omitempty"`
    IsRead        bool       `json:"is_read"`
    ReadAt        *time.Time `json:"read_at,omitempty"`
    CreatedAt     time.Time  `json:"created_at"`
}
```

| Field | Type | Description |
|---|---|---|
| `ID` | `string` | Canonical UUIDv7 of the notification. Also used as the pagination tiebreaker cursor. |
| `UserID` | `string` | UUID of the recipient user, enforcing multi-tenant isolation. |
| `Title` | `string` | Brief alert headline (e.g. `"Trade Executed"`). |
| `Message` | `string` | Counterparty-sanitized message body. |
| `Type` | `string` | Category (`INFO`, `TRADE_FILL`, `SYSTEM`, `ACCOUNT`). |
| `ReferenceID` | `string` | Optional foreign UUID (`trade_id`, `order_id`, `deposit_id`). |
| `ReferenceType` | `string` | Discriminator (`TRADE`, `ORDER`, `DEPOSIT`). |
| `IsRead` | `bool` | Unread badge indicator. |
| `ReadAt` | `*time.Time` | Timestamp when user marked the notification as read (`nil` if unread). |
| `CreatedAt` | `time.Time` | UTC creation timestamp used as the primary keyset cursor. |

---

### 2. `CreateNotificationInput` (Inbound Service DTO)

DTO passed from gRPC handlers and Kafka processors into `service.CreateNotification`.

```go
type CreateNotificationInput struct {
    UserID         string
    Title          string
    Message        string
    Type           string
    ReferenceID    string
    ReferenceType  string
    IdempotencyKey string
}
```

- **`IdempotencyKey`**: Opaque caller-generated UUID (e.g. generated by Wallet/Auth service). When set, retries due to network drops return the original `Notification` record instead of creating duplicates.

---

### 3. `OutboxEvent` (Staged Outbox Record)

Represents a pending or in-flight outbox row in `notification_outbox`.

```go
type OutboxEvent struct {
    ID            string
    EventType     string
    Payload       []byte
    TargetChannel string
    Status        string
    ClaimToken    string
    RetryCount    int
    LastError     string
    ClaimedAt     *time.Time
    CreatedAt     time.Time
    PublishedAt   *time.Time
}
```

- **`TargetChannel`**: Full destination channel (e.g. `user:notifications:{user_id}` or `user:portfolio:{user_id}`).
- **`ClaimToken`**: Generated UUID stamped by `FetchPendingOutbox`. Must be supplied by the publisher worker to verify row ownership during `MarkOutboxPublished` and `ReleaseOutboxClaims`.

---

### 4. `RedisEnvelope` (Streaming Pub/Sub Contract)

The standardized JSON wire envelope published to Redis Pub/Sub and relayed to WebSocket clients.

```go
type RedisEnvelope struct {
    EventID        string    `json:"event_id"`
    NotificationID string    `json:"notification_id"`
    Type           string    `json:"type"`
    Channel        string    `json:"channel"`
    Timestamp      time.Time `json:"timestamp"`
    Data           any       `json:"data"`
}
```

#### Why Both `EventID` and `NotificationID` Are Required:
- For trades, **1 domain event** (`EventID = TradeID`) produces **2 distinct notifications** (`NotificationID = BuyerNotifID` and `NotificationID = SellerNotifID`).
- Gateway WebSocket streamers use the `(EventID, NotificationID)` pair to drop duplicate frames during redelivery without confusing buyer and seller streams.
- For ephemeral portfolio updates, `NotificationID` is empty (`""`) because no persistent inbox record exists.

---

### 5. `PaginationFilter` (Keyset Query DTO)

Encapsulates cursor parameters for deterministic keyset pagination.

```go
type PaginationFilter struct {
    UserID     string
    CursorTime *time.Time
    CursorID   string
    Limit      int
    TypeFilter string
}
```

- Matches the composite database index `idx_notifications_user_inbox (user_id, created_at DESC, id DESC)`.

---

## Entity Lifecycle & Transformation Flow

```
1. Incoming Transport Call (gRPC or Kafka)
   │
   ▼
2. CreateNotificationInput
   │ (Validated in service layer)
   ▼
3. Domain Entities Constructed:
   ├── model.Notification  ──► Stored in PostgreSQL "notifications" table
   └── model.OutboxEvent    ──► Stored in PostgreSQL "notification_outbox" table
           │
           │ (Payload serialized as JSON)
           ▼
4. model.RedisEnvelope (EventID, NotificationID, Timestamp, Data)
           │
           ▼
5. Broadcast over Redis Pub/Sub:
   Target Channel: "user:notifications:{user_id}"
           │
           ▼
6. Gateway Streamer Relays Frame to WebSocket Client
```
