# TradeDrift — Notification Service

> **Status:** ✅ Designed (V1)
> **Document:** 13_Notification_Service.md
> **Service:** Notification Service
> **Version:** V1.0
> **Last Updated:** July 2026
> Revision notes: V1.0 initial design. Implements real-time WebSocket event streaming with a Redis Pub/Sub backplane, client heartbeat Ping/Pong frames, private PostgreSQL notification storage, and transactional outbox event publishing.

---

## Purpose

The Notification Service acts as the real-time gateway of the TradeDrift platform, bridging the asynchronous, backend event-driven pipeline (Kafka) to the client frontends (web/mobile browsers) via persistent WebSocket connections. It also provides a persistent notification inbox for historical alert retrieval.

Its responsibilities are:
1. **Manage client WebSocket connections** (upgrade, auth handshake, heartbeats, and termination).
2. **Expose public streaming feeds** (order book updates, recent public trades).
3. **Expose private streaming feeds** (personal order status, balance adjustments, portfolio changes).
4. **Manage user notification history** (store system notifications in PostgreSQL).
5. **Expose REST APIs** for reading inbox history and marking alerts read.
6. **Expose gRPC APIs** for internal services to trigger system notifications.
7. **Publish `NotificationCreated` events** via a transactional outbox.

---

## 1. Architectural Overview

The Notification Service uses a **topographic separation of concerns** split into three deployment roles (API, Worker, and Cron) using a single Go codebase:

```
                            +-----------------------+
                            |     Client Browser    |
                            +-----------------------+
                                        │ (WebSocket Connection)
                                        ▼
                            +-----------------------+
                            | notification-api      |  <--- Replicas: N
                            | (WebSocket / REST API)|
                            +-----------------------+
                                        ▲
                                        │ (Redis Pub/Sub: user:{user_id})
                                        ▼
                            +-----------------------+
                            |       Redis Cache     |
                            +-----------------------+
                                        ▲
                                        │ (Publish)
                            +-----------------------+
                            | notification-worker   |  <--- Replicas: N
                            | (Kafka Event Consumer)|
                            +-----------------------+
                                        ▲
                                        │ (Consume)
                            +-----------------------+
                            |         Kafka         |
                            +-----------------------+
```

### 1.1 Deployment Roles
1. **API Role (`notification-service-api`):** Exposes the public HTTP REST endpoints, gRPC service, and the WebSocket gateway (`/ws`). Replicas scale horizontally to manage high connection counts. It subscribes to Redis Pub/Sub backplane channels matching connected users.
2. **Worker Role (`notification-service-worker`):** Consumes events from Kafka (`TradeSettled`, `PortfolioUpdated`, `OrderCancelled`, `OrderCreated`).
   - Writes user-facing notifications to the PostgreSQL inbox database and inserts corresponding outbox records.
   - Publishes real-time event updates to the Redis Pub/Sub channels to route them to the active API nodes holding the connections.
3. **Cron Role (`notification-service-cron`):** A single-replica deployment (`replicas = 1`) that runs clean-up jobs (e.g. archiving/purging notifications older than 30 days).

---

## 2. Distributed Routing Backplane (Redis Pub/Sub)

Because clients establish WebSocket connections to random query/API replicas, a worker node consuming a Kafka event for `user_id = X` cannot know which API node holds the active TCP connection for User X. 

To bridge this, we implement a **Redis Pub/Sub routing backplane**:

```
                       Kafka Event (e.g. PortfolioUpdated)
                                   │
                                   ▼
                       notification-service-worker
                         1. Write to Postgres
                         2. Publish to Redis channel: user:portfolio:{user_id}
                                   │
                    ┌──────────────┴──────────────┐ (Redis Pub/Sub)
                    ▼                             ▼
       notification-service-api (Pod 1)   notification-service-api (Pod 2)
       [Has WebSocket connection for X]   [No connection for X]
         - Subscribed to user:portfolio:X    - Not subscribed
         - Pushes payload to WebSocket       - Ignores message
```

* **Dynamic Subscriptions:** When a client successfully authenticates on a WebSocket connection on an API replica, that replica subscribes to the Redis Pub/Sub channels:
  - `user:portfolio:{user_id}`
  - `user:orders:{user_id}`
  - `user:notifications:{user_id}`
* **Connection Termination:** Upon disconnection, the API node immediately unsubscribes from these channels to prevent memory leaks and redundant Redis traffic.

---

## 3. Database Schema

Notifications are persisted in PostgreSQL to serve the historical notification inbox.

```sql
CREATE TYPE notification_type AS ENUM ('INFO', 'TRADE_FILL', 'SYSTEM', 'ACCOUNT');

CREATE TABLE notifications (
    id          UUID PRIMARY KEY,                      -- UUIDv7
    user_id     UUID NOT NULL,                         -- Owner of the inbox
    title       VARCHAR(100) NOT NULL,
    message     TEXT NOT NULL,
    type        notification_type NOT NULL,
    is_read     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for fast user inbox retrieval sorted by newest first
CREATE INDEX idx_notifications_user_inbox ON notifications(user_id, created_at DESC);

-- Transactional outbox table for reliable publishing
CREATE TABLE notification_outbox (
    id            UUID PRIMARY KEY,                    -- UUIDv7
    event_type    VARCHAR(50) NOT NULL,                -- 'NotificationCreated'
    payload       JSONB NOT NULL,
    partition_key VARCHAR(50) NOT NULL,                -- user_id
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

---

## 4. WebSocket Protocol Specification

Clients connect to `ws://<api-gateway>/ws`. The connection is upgraded to WebSocket standard RFC 6455.

### 4.1 JSON Protocol Frame Structure
All communication over the WebSocket uses JSON-framed payloads:
```json
{
  "action": "auth|subscribe|unsubscribe|ping|pong",
  "topic": "market:orderbook:{market_id}|market:trades:{market_id}|user:portfolio|user:orders|user:notifications",
  "token": "JWT_TOKEN_STRING",
  "correlation_id": "client-request-id",
  "payload": {}
}
```

### 4.2 Handshake & Connection Flow

```
Client                                      notification-service-api
  │                                                    │
  ├────── 1. GET /ws (Upgrade to WebSocket) ──────────►│
  │◄───── 2. 101 Switching Protocols ──────────────────┤ (Unauthenticated socket open)
  │                                                    │
  ├────── 3. Send Frame: {"action": "auth"} ──────────►│ (JWT verification)
  │◄───── 4. Send Frame: {"status": "authenticated"} ──┤ (Binds socket to user_id)
  │                                                    │
  ├────── 5. Send Frame: {"action": "subscribe"} ─────►│
  │◄───── 6. Send Frame: {"status": "subscribed"} ─────┤ (Registers to Redis Pub/Sub)
```

1. **Unauthenticated Upgrade:** Connection is established. No subscriptions to private channels are permitted yet. Unauthenticated sockets are disconnected automatically if they do not send a valid `auth` frame within **5 seconds**.
2. **Authentication Frame:**
   * **Client Sends:**
     ```json
     {
       "action": "auth",
       "token": "ey...",
       "correlation_id": "auth_req_1"
     }
     ```
   * **Server Response (Success):**
     ```json
     {
       "status": "success",
       "action": "auth",
       "correlation_id": "auth_req_1",
       "payload": {
         "user_id": "018f60f3-b780-7798-8422-dfa6b29f44ea"
       }
     }
     ```
3. **Subscription Frame:**
   * **Client Sends:**
     ```json
     {
       "action": "subscribe",
       "topic": "user:portfolio",
       "correlation_id": "sub_req_2"
     }
     ```
   * **Server Response:**
     ```json
     {
       "status": "success",
       "action": "subscribe",
       "topic": "user:portfolio",
       "correlation_id": "sub_req_2"
     }
     ```

### 4.3 Heartbeats & Keep-Alive (Ping/Pong)
To prevent network load balancers from cutting idle TCP connections and to detect silent client disconnections:
* **Server Ping Loop:** The server sends a Ping frame to the client every **30 seconds**:
  ```json
  { "action": "ping", "timestamp": 1799298302000 }
  ```
* **Client Pong Requirement:** The client must respond with a Pong frame within **10 seconds**:
  ```json
  { "action": "pong", "timestamp": 1799298302000 }
  ```
* **Disconnection:** If the client fails to respond within the 10-second window, the server forcefully terminates the connection.

---

## 5. Event Ingestion Pipeline

The `notification-service-worker` processes the following Kafka events to generate user notifications:

| Event Consumed | Trigger | Payload Action | Outbox Event |
|---|---|---|---|
| `OrderCreated` | Client placed a limit order | Writes to `notifications` DB (Type: `INFO`) | `NotificationCreated` |
| `OrderCancelled` | Order was cancelled by user/Matching Engine | Writes to `notifications` DB (Type: `SYSTEM`) | `NotificationCreated` |
| `TradeSettled` | Trade successfully settled (buy/sell filled) | Writes to `notifications` DB (Type: `TRADE_FILL`) | `NotificationCreated` |
| `PortfolioUpdated` | Cost basis or balance adjusted | Publishes directly to Redis Pub/Sub (no DB write) | None |

---

## 6. APIs

### 6.1 gRPC API (Internal)
Exposed by `notification-service-api` for downstream services:

```protobuf
syntax = "proto3";

package notification;

service NotificationService {
    rpc CreateNotification(CreateNotificationRequest) returns (CreateNotificationResponse);
}

message CreateNotificationRequest {
    string user_id  = 1;
    string title    = 2;
    string message  = 3;
    string type     = 4; // "INFO"|"SYSTEM"|"ACCOUNT"
}

message CreateNotificationResponse {
    string notification_id = 1;
    bool   success         = 2;
}
```

### 6.2 REST API (Client/Dashboard facing)
Requires a valid JWT bearer token.

#### `GET /notifications`
* **Description:** Retrieve the user's notification history.
* **Query Params:** 
  - `limit` (default: 20, max: 100)
  - `offset` (default: 0)
* **Response:**
  ```json
  {
    "notifications": [
      {
        "id": "018f60f3-b780-7798-8422-dfa6b29f44ea",
        "title": "Trade Executed",
        "message": "You bought 0.1 BTC at 58,200.00 USDT",
        "type": "TRADE_FILL",
        "is_read": false,
        "created_at": "2026-07-10T11:00:38Z"
      }
    ],
    "unread_count": 1
  }
  ```

#### `POST /notifications/{id}/read`
* **Description:** Mark a specific notification as read.
* **Response:** `200 OK`

#### `POST /notifications/read-all`
* **Description:** Mark all notifications for the authenticated user as read.
* **Response:** `200 OK`

---

## 7. Scalability & System Safeguards

Managing thousands of persistent WebSockets requires distinct safeguards to prevent service degradation:

### 7.1 Memory Footprint Management
* Persistent connections occupy memory. Go's default HTTP server allocates buffer sizes per socket.
* **Optimization:** Use connection hijacking or the `epoll` network library (e.g. `gobwas/ws` or optimized `fasthttp/websocket`) to avoid maintaining a full Go goroutine per connection. This reduces the footprint to **<4KB per socket**, allowing a single replica to easily hold 50,000+ idle connections.

### 7.2 Connection Rate Limiting
* If the API Gateway restarts, thousands of clients will try to reconnect simultaneously (reconnection storm).
* **Mitigation:** The API Gateway rate limits the `/ws` endpoint (IP/client bucket). Sockets use an **exponential backoff with jitter** reconnection algorithm in the frontend browser client code to distribute reconnection load over a wide window.

### 7.3 File Descriptor Exhaustion
* Each socket connection represents an open file descriptor (`FD`).
* **Enforcement:** Deployment container configuration templates enforce `ulimit -n 65535` limits, ensuring nodes do not reject connections due to operating system resource starvation.

---

## 8. Service Invariants

- **NI-1 (At-least-once outbox):** All modifications to the `notifications` table must write to the `notification_outbox` in the same PostgreSQL transaction.
- **NI-2 (Unauthenticated Socket Cleanup):** Unauthenticated WebSocket connections must be closed by the server if authentication fails or is not sent within 5 seconds.
- **NI-3 (Redis Unsubscribe):** Connections must be cleaned up and their Redis subscriptions terminated immediately upon client exit to prevent memory leaks.
- **NI-4 (Owner Inbox Isolation):** Users can only query, modify, or read notifications belonging to their own authenticated `user_id`. No cross-user access is permitted.
