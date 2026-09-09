# Notification Service — gRPC Transport Layer Guide

This document provides a technical explanation of the `internal/handler` package in the **TradeDrift Notification Service** located in `services/notification/internal/handler/`.

It details the role of the gRPC transport boundary, the architectural and security problems it solves, the design of the `NotificationHandler`, a function-by-function breakdown of all 5 RPC endpoints, error mapping strategies, and end-to-end request/response flows.

---

## Architecture Overview & Role of `internal/handler`

The `internal/handler` package implements the server-side contract generated from [proto/notification/v1/notification.proto](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/proto/notification/v1/notification.proto). It acts as the **inbound transport boundary** for all synchronous communication into the service.

```
services/notification/internal/handler/
└── grpc.go       # NotificationHandler struct implementing NotificationServiceServer
```

### Core Responsibilities
1. **Wire Protocol Decoupling**: Translates Protobuf messages (`*notificationv1.CreateNotificationRequest`) into internal Go domain models (`model.CreateNotificationInput`), ensuring core business logic remains independent of gRPC code generation.
2. **Canonical Error Mapping**: Intercepts domain errors (`service.ErrInvalidUserID`, `repository.ErrNotificationNotFound`) and maps them to standard gRPC status codes (`codes.InvalidArgument`, `codes.NotFound`, `codes.Internal`).
3. **Keyset Pagination Orchestration**: Manages cursor parsing, RFC3339 timestamp decoding, and `limit + 1` sentinel row slicing to determine `has_more` without issuing expensive database count queries.

---

## Problems Solved by `grpc.go`

| Problem | Failure Without `grpc.go` | How `grpc.go` Solves It |
|---|---|---|
| **Domain Leakage & Coupling** | Calling database or service methods directly from gRPC transport couples wire protocols to database tables. | Enforces a strict boundary: handler maps wire formats, calls `service.Service`, and converts domain models to Protobuf responses. |
| **Expensive `COUNT(*)` Scans** | Traditional pagination runs `SELECT COUNT(*) WHERE user_id = $1` on every page, locking tuples and burning database CPU on large inboxes. | Implements the **`limit + 1` sentinel pattern**: requests 1 extra row from the repository. If present, sets `has_more = true` and trims the row before responding. |
| **Broken Keyset Drift** | Providing only `cursor_time` without `cursor_id` causes missed or duplicate notifications when multiple records share identical timestamps. | Enforces strict cursor pairing: `cursor_time` and `cursor_id` must both be provided or both omitted, returning `codes.InvalidArgument` on mismatch. |
| **Tenant ID Enumeration Attacks** | Returning `codes.PermissionDenied` when User B tries to mark User A's notification informs an attacker that the notification ID exists. | Returns `codes.NotFound` ("notification not found") when ownership checks fail, preventing cross-tenant ID enumeration. |
| **Internal Stack Leakage** | Returning raw PostgreSQL or Redis connection errors to external callers leaks database topology and table structures. | Catches raw errors, logs full diagnostic details via Uber Zap, and returns generic `codes.Internal` messages to the client. |

---

## Component Architecture: `NotificationHandler`

```go
type NotificationHandler struct {
    notificationv1.UnimplementedNotificationServiceServer
    svc *service.Service
    log *zap.Logger
}
```

- **`UnimplementedNotificationServiceServer`**: Embedded forward-compatibility struct ensuring safe compilation even if new RPCs are added to the `.proto` contract.
- **`svc *service.Service`**: Injected domain orchestrator containing business logic, privacy sanitization, and database transactions.
- **`log *zap.Logger`**: High-performance structured logger for operational error tracking.

---

## Function-by-Function Breakdown

### 1. `CreateNotification(ctx, req)`

```go
func (h *NotificationHandler) CreateNotification(ctx context.Context, req *notificationv1.CreateNotificationRequest) (*notificationv1.CreateNotificationResponse, error)
```

#### Purpose
Enables internal exchange microservices (Wallet, Auth, Admin, Trade) to dispatch user notifications directly over gRPC.

#### Problems Solved & Implementation
- **Parameter Extraction**: Extracts `user_id`, `title`, `message`, `type`, `reference_id`, `reference_type`, and optional `idempotency_key`.
- **Pre-Validation**: Rejects missing required parameters (`user_id`, `title`, `message`) with `codes.InvalidArgument` before touching the service layer.
- **Idempotent Retry Safety**: Passes `req.IdempotencyKey` to `svc.CreateNotification`. If the caller retries due to a network timeout, the service recovers the original notification and returns `success = true` with the original `notification_id`.

#### Error Mapping Table
| Internal Error | gRPC Status Code | Client Message |
|---|---|---|
| Missing `user_id`, `title`, `message` | `codes.InvalidArgument` | `"[field] is required"` |
| `service.ErrInvalidUserID` | `codes.InvalidArgument` | `"invalid user_id: must be a valid UUID"` |
| Other validation errors | `codes.InvalidArgument` | Detailed error string |
| Database / Redis Failure | `codes.Internal` | `"internal error creating notification"` |

---

### 2. `GetNotifications(ctx, req)`

```go
func (h *NotificationHandler) GetNotifications(ctx context.Context, req *notificationv1.GetNotificationsRequest) (*notificationv1.GetNotificationsResponse, error)
```

#### Purpose
Retrieves a paginated user inbox ordered by newest first (`created_at DESC, id DESC`), along with the unread badge count.

#### Keyset Pagination & Sentinel Slicing Logic
1. **Limit Bounds Enforcement**:
   ```go
   limit := int(req.Limit)
   if limit <= 0 { limit = 20 }
   else if limit > 100 { limit = 100 }
   ```
2. **Cursor Validation**:
   - `cursor_time` and `cursor_id` form a composite key.
   - If one is provided without the other, the request is rejected immediately with `codes.InvalidArgument`.
   - Parses `cursor_time` against both `time.RFC3339Nano` and `time.RFC3339`.
3. **Sentinel Row Trimming**:
   ```go
   // Service retrieves limit + 1 rows
   hasMore := len(notifs) > limit
   if hasMore {
       notifs = notifs[:limit] // Trim extra row before building response
   }
   ```
4. **Stable Next-Cursor Generation**:
   - Points at the last **returned** item (`notifs[len(notifs)-1]`), **never** at the discarded sentinel row:
   ```go
   nextCursorTime = last.CreatedAt.Format(time.RFC3339Nano)
   nextCursorID = last.ID
   ```
5. **Badge Count Enrichment**:
   - Calls `h.svc.GetUnreadCount` in tandem, returning the badge counter within the same response to eliminate a secondary round-trip from the client.

---

### 3. `MarkAsRead(ctx, req)`

```go
func (h *NotificationHandler) MarkAsRead(ctx context.Context, req *notificationv1.MarkAsReadRequest) (*notificationv1.MarkAsReadResponse, error)
```

#### Purpose
Marks a single notification as read for the authenticated user.

#### Security & Anti-Enumeration Protection
- **Ownership Verification**: Enforces `WHERE id = $1 AND user_id = $2`.
- If User A attempts to mark User B's notification as read, the query affects 0 rows, returning `repository.ErrNotificationNotFound`.
- The handler returns `codes.NotFound` (`"notification not found"`). It deliberately avoids returning `PermissionDenied` so malicious actors cannot probe whether random UUIDs exist in other tenants' inboxes.

---

### 4. `MarkAllAsRead(ctx, req)`

```go
func (h *NotificationHandler) MarkAllAsRead(ctx context.Context, req *notificationv1.MarkAllAsReadRequest) (*notificationv1.MarkAllAsReadResponse, error)
```

#### Purpose
Bulk-updates all unread notifications belonging to `req.UserId` to `is_read = true, read_at = NOW()`.

#### Implementation & Response
- Returns `marked_count` indicating the total number of notifications updated in PostgreSQL.
- Triggers badge clearance across user dashboard interfaces.

---

### 5. `GetUnreadCount(ctx, req)`

```go
func (h *NotificationHandler) GetUnreadCount(ctx context.Context, req *notificationv1.GetUnreadCountRequest) (*notificationv1.GetUnreadCountResponse, error)
```

#### Purpose
Returns the total unread notifications for navigation bar badges and polling indicators.

#### Performance Characteristic
- Uses an indexed `COUNT(*)` query filtering on `user_id = $1 AND is_read = FALSE`, executing in sub-millisecond time.

---

## End-to-End Request/Response Flows

### Flow 1: Keyset Pagination (`GetNotifications`)
```
Gateway REST / Client
       │
       ▼
GetNotificationsRequest(user_id, cursor_time, cursor_id, limit=20)
       │
       ▼
grpc.NotificationHandler
       │ 1. Validate user_id != ""
       │ 2. Clamp limit: [1..100] (default: 20)
       │ 3. Validate cursor pairing: (hasCursorTime == hasCursorID)
       │ 4. Parse RFC3339Nano timestamp
       ▼
service.GetNotifications(PaginationFilter)
       │
       ▼
repository.GetByUserID(limit=21) ─── SELECT 21 rows
       │
       ▼
grpc.NotificationHandler receives 21 rows
       │
       ├── len(rows) > 20 ──► hasMore = true
       │                      Trim rows to [:20]
       │                      nextCursorTime = rows[19].CreatedAt
       │                      nextCursorID   = rows[19].ID
       │
       └── fetch UnreadCount
       │
       ▼
GetNotificationsResponse(20 items, nextCursorTime, nextCursorID, hasMore=true, unreadCount=5)
```

---

### Flow 2: Idempotent Direct Creation (`CreateNotification`)
```
Wallet / Internal Microservice
       │
       ▼
CreateNotificationRequest(user_id, title, message, idempotency_key="018f-key")
       │
       ▼
grpc.NotificationHandler
       │ (validates presence of required fields)
       ▼
service.CreateNotification()
       │
       ├── First Call:
       │      Persists to processed_events + notifications + notification_outbox
       │      Returns new notification ID
       │
       └── Retry Call (e.g. initial response dropped by network):
              CreateWithDedupTx returns ErrAlreadyProcessed
              Service recovers original notification from processed_events
              Returns existing notification ID
       ▼
grpc.NotificationHandler
       ▼
CreateNotificationResponse(notification_id="018f-notif-uuid", success=true)
```

---

### Flow 3: Read State & Anti-Enumeration (`MarkAsRead`)
```
User Request (MarkAsRead: user_id="User-A", notification_id="Notif-B")
       │
       ▼
grpc.NotificationHandler
       │
       ▼
service.MarkAsRead("User-A", "Notif-B")
       │
       ▼
repository.MarkAsRead()
       │ UPDATE notifications 
       │ SET is_read = TRUE, read_at = NOW() 
       │ WHERE id = 'Notif-B' AND user_id = 'User-A'
       │
       ▼
RowsAffected == 0 (Notification belongs to User-B, not User-A)
       │
       ▼
Returns repository.ErrNotificationNotFound
       │
       ▼
grpc.NotificationHandler maps to:
status.Error(codes.NotFound, "notification not found")
(Attacker cannot determine if Notif-B exists)
```
