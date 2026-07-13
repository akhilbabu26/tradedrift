# TradeDrift — Notification Service

> **Status:** ✅ Designed (V1.3)
> **Document:** 13_Notification_Service.md
> **Service:** Notification Service
> **Version:** V1.3
> **Last Updated:** July 2026
> Revision notes: V1.3 resolves key constraints: (1) documents the TradeSettled event fan-out behavior (one trade settles two counterparties); (2) scopes at-least-once idempotency guards for DB paths, in-memory caches for public tickers, and client-side overrides for portfolios; (3) schedules processed_events data retention purges; (4) adds reference correlation fields to the gRPC CreateNotification request schema.

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
3. **Cron Role (`notification-service-cron`):** A single-replica deployment (`replicas = 1`) that runs clean-up schedules:
   - **Inbox Purge:** Deletes inbox rows from the `notifications` table older than 30 days.
   - **Processed Event Purge:** Deletes deduplication log rows from the `processed_events` table older than 7 days (which far exceeds any Kafka message redelivery window) to prevent database size bloat.

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

* **Dynamic Subscriptions:** When a client joins and subscribes to channels, the API replica maps their connection to the corresponding Redis Pub/Sub channels:
  * **Public Channels (Subscribed by anyone):**
    - `market:orderbook:{market_id}`: Sourced from API node Redis polling loop.
    - `market:trades:{market_id}`: Sourced from Worker Kafka consumer (`TradeExecuted`).
  * **Private Channels (Requires JWT authentication):**
    - `user:portfolio:{user_id}`: Sourced from Worker Kafka consumer (`PortfolioUpdated`).
    - `user:orders:{user_id}`: Sourced from Worker Kafka consumer (`OrderCreated`, `OrderCancelled`, `TradeSettled`).
    - `user:notifications:{user_id}`: Sourced from Worker Kafka consumer (Inbox events).
* **Connection Termination:** Upon disconnection, the API node immediately unsubscribes from these channels to prevent memory leaks and redundant Redis traffic.

---

## 3. Database Schema

Notifications are persisted in PostgreSQL to serve the historical notification inbox.

```sql
CREATE TYPE notification_type AS ENUM ('INFO', 'TRADE_FILL', 'SYSTEM', 'ACCOUNT');

CREATE TABLE notifications (
    notification_id UUID PRIMARY KEY,                      -- UUIDv7 (referred to as id/notification_id)
    user_id         UUID NOT NULL,                         -- Owner of the inbox
    title           VARCHAR(100) NOT NULL,
    message         TEXT NOT NULL,
    type            notification_type NOT NULL,
    reference_id    UUID,                                  -- Associated order_id or trade_id (Correlation Standard)
    reference_type  VARCHAR(30),                           -- 'ORDER' or 'TRADE'
    is_read         BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for fast user inbox retrieval sorted by newest first
CREATE INDEX idx_notifications_user_inbox ON notifications(user_id, created_at DESC);

-- Transactional outbox table for reliable publishing
CREATE TABLE notification_outbox (
    id            UUID PRIMARY KEY,                    -- UUIDv7
    event_type    VARCHAR(50) NOT NULL,                -- 'NotificationCreated'
    payload       JSONB NOT NULL,
    partition_key VARCHAR(50) NOT NULL,                -- user_id
    status        VARCHAR(20) NOT NULL DEFAULT 'PENDING', -- 'PENDING', 'PUBLISHED', 'FAILED'
    published_at  TIMESTAMPTZ,                         -- Timestamp when successfully sent to Kafka
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Idempotency deduplication log to prevent duplicate processing of Kafka events
CREATE TABLE processed_events (
    event_id      UUID PRIMARY KEY,                    -- Kafka event's unique message ID
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
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

1. **Unauthenticated (Guest) Upgrade:** The WebSocket connection is upgraded. Guests (unauthenticated connections) are allowed to remain open indefinitely (subject to the standard connection idle timeout of e.g. 15 minutes) but are restricted to subscribing **only** to public topics (`market:orderbook:*`, `market:trades:*`). Sockets are only terminated after 5 seconds if they attempt to subscribe to a private topic (`user:*`) without first sending a valid `auth` handshake frame.
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

## 5. Event Ingestion & Streaming Pipelines

To ensure at-least-once reliability, low latency, and idempotency, the service divides processing into three pipelines:

### 5.1 Idempotency Guard (Deduplication)
Every incoming Kafka event processed by the `notification-service-worker` carries a unique **event ID** in its header/metadata.
* **Database-Persisted Paths (Inbox Writes):** Before writing a notification, the worker attempts to insert the event ID into the `processed_events` table within the same PostgreSQL transaction as the notification itself. If a duplicate index conflict occurs, the transaction is rolled back and the event discarded. This guarantees that a redelivered Kafka message will never produce duplicate inbox entries.
* **Public Ticker Feed (TradeExecuted):** To prevent duplicate public trades from showing on the live charts, the worker maintains a lightweight **Redis-based bloom filter or set cache** storing the last 10,000 processed trade IDs with a 1-hour TTL. Before publishing to the Redis Pub/Sub channel `market:trades:{market_id}`, the worker verifies the ID is not cached.
* **Portfolio Update Feed (PortfolioUpdated):** Because portfolio messages only push real-time value updates directly to the client socket, the client UI simply overrides the local display with the latest message values. Duplicate pushes are naturally idempotent on the frontend, so no extra deduplication overhead is required.

### 5.2 Event Ingestion Table (Worker Ingestion)

The `notification-service-worker` consumes events from Kafka, filters duplicates via `processed_events`, and performs the following actions:

| Event Consumed | Trigger | PostgreSQL Action (Atomically with Outbox & Dedup) | Redis Pub/Sub channel |
|---|---|---|---|
| `OrderCreated` | Client placed a limit order | Inserts `notifications` row (Type: `INFO`), links `reference_id` | `user:notifications:{user_id}` |
| `OrderCancelled` | Order cancelled by user/ME | Inserts `notifications` row (Type: `SYSTEM`), links `reference_id` | `user:notifications:{user_id}` |
| `TradeSettled` | Trade settled | Inserts **two distinct `notifications` rows** (Type: `TRADE_FILL`) and two outbox rows | `user:notifications:{buyer_id}` & `user:notifications:{seller_id}` |
| `PortfolioUpdated` | Holdings/PnL changed | *No DB write.* Publishes update payload directly to Redis. | `user:portfolio:{user_id}` |

> **TradeSettled Fan-Out Rule:** A single `TradeSettled` Kafka event has both a `buyer_id` and a `seller_id`. To ensure both counterparties receive their fill alerts, the worker must split this event into **two separate notification database writes** (one for the buyer stating "Bought X", and one for the seller stating "Sold X"), inserting both into the database and outbox tables in the same atomic transaction. It then publishes to their respective Redis Pub/Sub notification channels.

### 5.3 Public Streaming Feeds Pipeline
Unlike private feeds, public feeds are shared across all users and do not require user authentication.

#### 1. Public Trade Feed (`market:trades:{market_id}`)
* **Source:** The worker role subscribes to the `TradeExecuted` topic in Kafka.
* **Pipeline:** For every consumed trade match:
  1. The worker formats the trade detail object.
  2. It publishes it to the Redis Pub/Sub channel `market:trades:{market_id}`.
  3. Every active API replica subscribed to that channel receives the trade and pushes it to all connected WebSockets listening to that market.

#### 2. Order Book Feed (`market:orderbook:{market_id}`)
* **Source:** Matching Engine writes L2 order book snapshots directly to Redis under keys `orderbook:{market_id}`.
* **Pipeline (Option A Interim-Polling Loop):**
  1. The `notification-service-api` node maintains a lightweight background polling goroutine.
  2. For any active market with active WebSocket subscribers, the node reads the `orderbook:{market_id}` snapshot from Redis every **250ms**.
  3. If the timestamp or content has changed from the previous read, the API node publishes the updated L2 order book structure to the Redis Pub/Sub channel `market:orderbook:{market_id}` to broadcast it to all client connections across all API replicas.
* **Known V1 Architectural Limitations & Trade-offs:**
  * **Latency Floor:** Introducing a 250ms polling interval adds an artificial latency floor of up to 250ms on order book updates for clients, regardless of how fast the Matching Engine executes matches. This visible lag is an accepted V1 constraint.
  * **Polling Overhead:** The query cost scales linearly with the number of active markets, not the trade rate (e.g. an completely inactive market is still queried every 250ms). This does not scale indefinitely, but is suitable for V1's limited market set.
  * **Matching Engine V2+ Dependency:** This polling loop is an explicit interim solution. Option B (event-driven Redis Pub/Sub directly from the Matching Engine) remains the target once Matching Engine is upgraded to emit `orderbook_updates` in V2+.

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
    string user_id        = 1;
    string title          = 2;
    string message        = 3;
    string type           = 4; // "INFO"|"SYSTEM"|"ACCOUNT"
    string reference_id   = 5; // Associated order_id or trade_id (optional correlation)
    string reference_type = 6; // "ORDER" or "TRADE" (optional correlation)
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
        "notification_id": "018f60f3-b780-7798-8422-dfa6b29f44ea",
        "title": "Trade Executed",
        "message": "You bought 0.1 BTC at 58,200.00 USDT",
        "type": "TRADE_FILL",
        "reference_id": "018f60f3-a120-7798-8422-cfb6a29e11aa",
        "reference_type": "TRADE",
        "is_read": false,
        "created_at": "2026-07-10T11:00:38Z"
      }
    ],
    "unread_count": 1
  }
  ```

#### `POST /notifications/{notification_id}/read`
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
- **NI-2 (Unauthenticated Topic Protection):** Unauthenticated WebSocket connections (guests) are restricted **only** to subscribing to public feeds (`market:orderbook:*`, `market:trades:*`). If an unauthenticated socket attempts to subscribe to a private topic (`user:*`), it must authenticate via an `auth` frame within 5 seconds; otherwise, the connection is forcefully terminated.
- **NI-3 (Redis Unsubscribe):** Connections must be cleaned up and their Redis subscriptions terminated immediately upon client exit to prevent memory leaks.
- **NI-4 (Owner Inbox Isolation):** Users can only query, modify, or read notifications belonging to their own authenticated `user_id`. No cross-user access is permitted.
- **NI-5 (At-least-once Deduplication):** Every consumed Kafka event triggering a database write must have its unique message identifier checked and stored in the `processed_events` table within the same PostgreSQL transaction as the notification itself. Duplicate messages are ignored. Public streaming feeds must verify execution IDs against an in-memory/Redis cache before broadcasting to prevent client-side feed duplication.
