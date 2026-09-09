# Notification Service — Storage Repository & PostgreSQL Adapter Guide

This document provides a technical explanation of the `internal/repository` package in the **TradeDrift Notification Service** located in `services/notification/internal/repository/`.

It details the port-and-adapter storage architecture, the distributed ACID transaction and concurrency problems it solves, sentinel error contracts, and an exhaustive breakdown of all 13 repository methods implemented in `internal/repository/postgres/`.

---

## Architecture Overview: Ports & Adapters

The repository layer follows Clean Architecture principles by separating the **storage interface (Port)** from the **concrete PostgreSQL implementation (Adapter)**:

```
services/notification/internal/repository/
├── repository.go             # Storage interface (Port) & sentinel domain errors
└── postgres/                 # PostgreSQL persistence adapter using pgxpool
    ├── repository.go         # Struct definition & connection pool holder
    ├── notifications.go      # Inbox CRUD, keyset pagination & dedup transactions
    ├── outbox.go             # Outbox claims, FOR UPDATE SKIP LOCKED & claim tokens
    └── repository_test.go    # Integration tests using live test database
```

### Core Responsibilities
1. **Durable ACID Boundaries**: Encapsulates multi-table operations (`processed_events`, `notifications`, `notification_outbox`) inside atomic PostgreSQL transactions.
2. **Deterministic Keyset Pagination**: Queries inboxes using the composite B-tree `(user_id, created_at DESC, id DESC)`, eliminating $O(N)$ `OFFSET` scans.
3. **Concurrency Control & Claim Ownership**: Uses `FOR UPDATE SKIP LOCKED` and per-claim UUID tokens (`claim_token`) to prevent worker contention and race conditions.
4. **Mockability**: Exposes `NotificationRepository` as an interface, allowing unit tests in `service` and `publisher` to mock storage operations deterministically.

---

## Sentinel Errors Reference

```go
var (
    ErrAlreadyProcessed     = errors.New("event has already been processed")
    ErrNotificationNotFound = errors.New("notification not found")
    ErrOutboxClaimLost      = errors.New("outbox claim lost: claim token mismatch")
)
```

| Sentinel Error | When It Is Returned | How Callers Handle It |
|---|---|---|
| `ErrAlreadyProcessed` | A primary key unique violation occurs on `processed_events.event_id`. | **Kafka Consumer**: Safely skips duplicate message and commits offset.<br>**gRPC Service**: Queries `GetNotificationIDByEventID` to return the original notification on retry. |
| `ErrNotificationNotFound` | A notification does not exist or belongs to a different `user_id` (0 rows affected). | **gRPC Handler**: Returns `codes.NotFound` (`"notification not found"`), preventing tenant ID enumeration. |
| `ErrOutboxClaimLost` | `MarkOutboxPublished`, `ReleaseOutboxClaims`, or `IncrementOutboxRetry` affects 0 rows due to a `claim_token` mismatch. | **Outbox Publisher**: Treats claim loss as a normal concurrency condition (another worker owns the row), logs a `Warn`, and continues processing. |

---

## Problems Solved by the Repository Layer

| Problem | Failure Without This Repository | How the Repository Solves It |
|---|---|---|
| **Partial Writes on Crash** | If an inbox row inserts but the server crashes before staging the outbox event, the notification persists but the user never receives a real-time WebSocket alert. | Executes both inserts inside a single `pgx.Tx`. If any statement fails or the process crashes, PostgreSQL rolls back all changes atomically. |
| **Worker Row Contention** | Multiple publisher pods querying `WHERE status = 'PENDING'` lock the same rows, causing deadlocks or duplicate broadcasts. | Uses **`SELECT ... FOR UPDATE SKIP LOCKED`** inside a Common Table Expression (CTE). Workers claim non-overlapping batches concurrently. |
| **Lease Expiry Concurrency Races** | A worker stalls for 65s, its 60s lease expires, and a second worker re-claims the row. When the first worker finishes, it overwrites the new owner's state. | Enforces `AND claim_token = $claimToken` on all status transitions. Mismatched tokens return `ErrOutboxClaimLost`, preventing stale overwrites. |
| **Cross-Tenant Data Tampering** | User B calls `MarkAsRead` with User A's notification ID. Without tenant isolation, User A's unread state is modified. | Strictly enforces `WHERE id = $1 AND user_id = $2`. Tampering attempts affect 0 rows and return `ErrNotificationNotFound`. |
| **Page Drift & Offset Inefficiency** | Using `OFFSET 1000` scans and discards 1,000 dead rows. New items inserted at the top shift page boundaries, showing duplicate alerts. | Uses **Keyset Pagination**: seeks directly to `(created_at, id) < ($cursorTime, $cursorID)` with an index seek, delivering sub-millisecond responses on millions of rows. |

---

## Function-by-Function Breakdown

### 1. Ingestion & Transactional Writers

#### `CreateWithDedupTx(ctx, notif, sourceEventID, outbox)`
- **File**: `postgres/notifications.go`
- **Purpose**: Atomically persists an event deduplication row, a notification inbox row, and a staged outbox event.
- **Statements Executed**:
  1. `INSERT INTO processed_events (event_id, user_id, notification_id) VALUES ($1, $2, $3)` (Checks for duplicate event ID).
  2. `INSERT INTO notifications (...) VALUES (...)` (Persists durable inbox row).
  3. `INSERT INTO notification_outbox (...) VALUES (...)` (Stages real-time event).
- **Idempotency Contract**: Stores `notif.ID` in `processed_events.notification_id` so that gRPC retries can look up and return the original notification.

#### `CreateTradeSettledTx(ctx, buyerNotif, sellerNotif, sourceEventID, buyerOutbox, sellerOutbox)`
- **File**: `postgres/notifications.go`
- **Purpose**: Implements atomic dual counterparty fan-out for trade settlements.
- **Statements Executed**:
  1. Inserts `sourceEventID` into `processed_events`.
  2. Inserts Buyer notification into `notifications` (sanitized BUY message).
  3. Inserts Seller notification into `notifications` (sanitized SELL message).
  4. Inserts Buyer outbox event into `notification_outbox` (`target_channel = user:notifications:buyer`).
  5. Inserts Seller outbox event into `notification_outbox` (`target_channel = user:notifications:seller`).
- **Guarantees**: Either both counterparties receive their private alerts, or neither does.

#### `StageOutboxEvent(ctx, outbox)`
- **File**: `postgres/outbox.go`
- **Purpose**: Directly stages an outbox record without writing an inbox row.
- **Use Case**: High-frequency portfolio snapshots (`HandlePortfolioUpdated`). Bypasses `notifications` to prevent database write amplification.

---

### 2. Inbox Keyset Pagination & Lookups

#### `GetByUserID(ctx, filter model.PaginationFilter) ([]*model.Notification, error)`
- **File**: `postgres/notifications.go`
- **Purpose**: Keyset pagination query fetching user inboxes ordered by `created_at DESC, id DESC`.
- **The `LIMIT + 1` Pattern**:
  ```sql
  SELECT id, user_id, title, message, type, reference_id, reference_type, is_read, read_at, created_at
  FROM notifications
  WHERE user_id = $1 
    AND (($2::timestamptz IS NULL) OR (created_at, id) < ($2, $3))
    AND (($5 = '') OR (type = $5))
  ORDER BY created_at DESC, id DESC
  LIMIT $4; -- where $4 is filter.Limit + 1
  ```
- **Why `limit + 1`**: Slices off the extra row to detect `has_more` without issuing a costly `COUNT(*)` query.

#### `GetNotificationByID(ctx, userID, notificationID)`
- **File**: `postgres/notifications.go`
- **Purpose**: Retrieves a single notification enforcing `WHERE id = $1 AND user_id = $2`.
- **Use Case**: Used by `service.lookupByIdempotencyKey` to return existing notifications on gRPC retries.

#### `GetNotificationIDByEventID(ctx, eventID)`
- **File**: `postgres/notifications.go`
- **Purpose**: Queries `SELECT notification_id FROM processed_events WHERE event_id = $1`.
- **Use Case**: Maps a caller's `idempotency_key` back to the created `notification_id`.

---

### 3. Read State & Badge Counters

#### `MarkAsRead(ctx, userID, notificationID)`
- **File**: `postgres/notifications.go`
- **Purpose**: Updates `is_read = TRUE, read_at = NOW()` for a single user notification.
- **Security**: Verifies `WHERE id = $1 AND user_id = $2`. Returns `ErrNotificationNotFound` if 0 rows affected.

#### `MarkAllAsRead(ctx, userID)`
- **File**: `postgres/notifications.go`
- **Purpose**: Bulk-marks all unread notifications for `user_id` as read.
- **Query**:
  ```sql
  UPDATE notifications
  SET is_read = TRUE, read_at = NOW()
  WHERE user_id = $1 AND is_read = FALSE;
  ```
- **Returns**: Total count of rows marked read.

#### `GetUnreadCount(ctx, userID)`
- **File**: `postgres/notifications.go`
- **Purpose**: Sub-millisecond indexed counter query:
  ```sql
  SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND is_read = FALSE;
  ```

---

### 4. Outbox Lifecycle & Concurrency Control

#### `FetchPendingOutbox(ctx, limit)`
- **File**: `postgres/outbox.go`
- **Purpose**: Claims up to `limit` pending outbox records using a Common Table Expression (CTE).
- **SQL Implementation**:
  ```sql
  WITH claimed AS (
      SELECT id
      FROM notification_outbox
      WHERE status = 'PENDING'
         OR (status = 'PROCESSING' AND claimed_at < NOW() - INTERVAL '60 seconds')
      ORDER BY created_at ASC
      LIMIT $1
      FOR UPDATE SKIP LOCKED
  )
  UPDATE notification_outbox o
  SET status = 'PROCESSING', claimed_at = NOW(), claim_token = $2
  FROM claimed
  WHERE o.id = claimed.id
  RETURNING o.id, o.event_type, o.payload, o.target_channel, o.status, o.claim_token, o.retry_count, ...;
  ```
- **Mechanism**:
  - `FOR UPDATE SKIP LOCKED`: Skips rows locked by other worker instances.
  - Stamping `claim_token = $2`: Binds the row to the claiming worker.

#### `MarkOutboxPublished(ctx, id, claimToken)`
- **File**: `postgres/outbox.go`
- **Purpose**: Transitions a claimed row from `PROCESSING` to `PROCESSED`.
- **SQL Implementation**:
  ```sql
  UPDATE notification_outbox
  SET status = 'PROCESSED', published_at = NOW(), claimed_at = NULL, claim_token = NULL
  WHERE id = $1 AND status = 'PROCESSING' AND claim_token = $2;
  ```
- **Token Verification**: If `RowsAffected == 0`, returns `ErrOutboxClaimLost`.

#### `IncrementOutboxRetry(ctx, id, lastError, claimToken)`
- **File**: `postgres/outbox.go`
- **Purpose**: Increments `retry_count` and records `last_error` for an event that failed to publish to Redis.
- **SQL Implementation**:
  ```sql
  UPDATE notification_outbox
  SET retry_count = retry_count + 1, last_error = $2
  WHERE id = $1 AND status = 'PROCESSING' AND claim_token = $3;
  ```
- **Token Verification**: Checks `RowsAffected == 0` and returns `ErrOutboxClaimLost` if another worker re-claimed the row.

#### `ReleaseOutboxClaims(ctx, ids, claimToken)`
- **File**: `postgres/outbox.go`
- **Purpose**: Reverts in-flight claims back to `PENDING` when a publisher batch fails.
- **SQL Implementation**:
  ```sql
  UPDATE notification_outbox
  SET status = 'PENDING', claimed_at = NULL, claim_token = NULL
  WHERE id = ANY($1::uuid[]) AND status = 'PROCESSING' AND claim_token = $2;
  ```

#### `RecoverStaleOutboxClaims(ctx, timeout)`
- **File**: `postgres/outbox.go`
- **Purpose**: Background sweeper resetting abandoned `PROCESSING` records back to `PENDING`.
- **SQL Implementation**:
  ```sql
  UPDATE notification_outbox
  SET status = 'PENDING', claimed_at = NULL, claim_token = NULL
  WHERE status = 'PROCESSING' AND claimed_at < $1;
  ```

---

## Transaction & Keyset Pagination Flowcharts

### Flow 1: Dual Counterparty Settlement Transaction
```
service.HandleTradeSettled()
       │
       ▼
repository.CreateTradeSettledTx()
       │
       ▼
BEGIN TRANSACTION (Isolation: Read Committed)
       │
       ├── 1. INSERT INTO processed_events (event_id, trade_id)
       │      └── Primary Key check eliminates duplicate trade events
       │
       ├── 2. INSERT INTO notifications (buyer_notif, type='TRADE_FILL')
       │      └── Sanitized message (BUY side only)
       │
       ├── 3. INSERT INTO notifications (seller_notif, type='TRADE_FILL')
       │      └── Sanitized message (SELL side only)
       │
       ├── 4. INSERT INTO notification_outbox (channel="user:notifications:buyer")
       └── 5. INSERT INTO notification_outbox (channel="user:notifications:seller")
       │
       ▼
COMMIT TRANSACTION ──► All 5 records persist atomically
```

---

### Flow 2: Keyset Pagination Seek (`LIMIT + 1`)
```
GetByUserID(user_id="U1", cursor_time="T1", cursor_id="ID1", limit=20)
       │
       ▼
PostgreSQL Query Planner
       │
       ▼
B-Tree Index Seek on idx_notifications_user_inbox:
Seek to: (user_id = 'U1', created_at < 'T1', id < 'ID1')
       │
       ▼
Scan next 21 rows in index order (created_at DESC, id DESC)
       │
       ▼
Return 21 rows to caller:
  - Rows 0..19: Current page payload
  - Row 20: Sentinel row (determines has_more = true, trimmed before return)
```
