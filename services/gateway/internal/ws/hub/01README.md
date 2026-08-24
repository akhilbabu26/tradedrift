# Hub Package (`internal/ws/hub`)

## 1. Purpose
The `hub` package is the **connection authority and topic multiplexer** for TradeDrift's WebSocket gateway. It is responsible for:
1. Managing client lifecycle (handshake, read/write pumps, heartbeats, graceful teardown).
2. Authoritatively managing topic subscriptions under a single unified mutex (`Hub.mu`).
3. Enforcing stream-specific backpressure matrices to protect server memory and execution event delivery.
4. Enforcing security boundaries (JWT claims verification, CORS exact origin matching, and private user notification isolation).

---

## 2. File-by-File Breakdown

| File | Responsibility |
| :--- | :--- |
| [`client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/hub/client.go) | **WebSocket Client Session**: Owns connection state, read/write loops, ping/pong timers, inbound token-bucket rate limiter, and backpressure buffer. |
| [`hub.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/hub/hub.go) | **Central Router & Broadcaster**: Implements `protocol.Broadcaster`. Atomically registers and unregisters clients and subscriptions, and routes broadcast messages to active subscribers. |
| [`handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/hub/handler.go) | **HTTP Upgrade Handler**: Handles `GET /ws`, verifies exact-match CORS origins, parses query/header JWT tokens, and initializes new client sessions. |
| [`hub_test.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/hub/hub_test.go) | **Unit & Concurrency Tests**: Stress tests for multi-client concurrent subscribe/unsubscribe/close races, backpressure matrices, private authorization, and snapshot ordering. |

---

## 3. Packages Used & Rationale

| Package | Why It Is Used |
| :--- | :--- |
| `github.com/gorilla/websocket` | Industry-standard RFC 6455 WebSocket implementation for Go. Handles frame masking, ping/pong control frames, and connection upgrades. |
| `github.com/golang-jwt/jwt/v5` | Cryptographically parses and validates HMAC-SHA256 user authentication tokens during handshake. |
| `go.uber.org/zap` | Structured, low-latency logging without memory allocations on hot broadcast paths. |
| `sync` & `sync/atomic` | Thread synchronization primitives (`sync.RWMutex`, `sync.Mutex`, `atomic.Int64`) used for lock-free metrics and deterministic concurrency guards. |
| `tradedrift/services/gateway/internal/ws/protocol` | Wire frame DTOs (`InboundFrame`, `OutboundEnvelope`), stream constants, and interface contracts (`Broadcaster`, `SnapshotProvider`). |

---

## 4. Functions & Methods Reference

### `client.go`
- `NewClient(hub *Hub, conn *websocket.Conn, userID string, logger *zap.Logger) *Client`: Instantiates a client session with a 256-message send buffer and a 100 req/sec token bucket rate limiter.
- `NewTestClient(hub *Hub, userID string, logger *zap.Logger) *Client`: Creates a test client with an in-memory buffer without requiring an active TCP socket.
- `UserID() string`: Returns authenticated user ID (empty for anonymous).
- `HasSubscription(stream string) bool`: Queries `Hub` for active subscription status.
- `Subscriptions() []string`: Returns a slice copy of all active subscriptions.
- `AllowInboundFrame() bool`: Token-bucket rate limiter (100 frames/sec, burst 150) protecting against client message flooding.
- `Send(streamType string, msg []byte) bool`: Drops message or disconnects client based on the **Stream-Specific Backpressure Matrix** if `c.send` is full. Also enforces max 512 KB outbound payload limit.
- `Close()`: Idempotent connection closer. Closes socket and triggers `hub.Unregister(c)`.
- `ReadPump()`: Continuously reads frames from the socket, resets read deadlines on pongs, and forwards frames to `hub.HandleClientFrame`.
- `WritePump()`: Writes queued messages to the socket. Sends periodic ping frames (54s). Symmetrically invokes `c.Close()` upon socket write error.
- `SendControlError(code, message string)`: Sends structured error frames (`{"event":"error","code":...}`).
- `SendPong()`: Sends application-level `{"event":"pong"}` control frame.

### `hub.go`
- `NewHub(logger *zap.Logger, provider protocol.SnapshotProvider) *Hub`: Initializes the Hub registry with subscriber maps and atomic metric counters.
- `Register(c *Client)`: Atomically registers a client in `h.clients` under `Hub.mu`.
- `Unregister(c *Client)`: Atomically removes client, cleans up all subscribed streams in `h.subs`, and decrements `h.marketSubs` counters under `Hub.mu`.
- `HandleClientFrame(c *Client, frame protocol.InboundFrame)`: Dispatches client actions (`ping`, `subscribe`, `unsubscribe`).
- `handleSubscribe(c *Client, stream string)`: Validates stream format, checks private channel authorization, atomically updates `h.clientSubs`, `h.subs`, and `h.marketSubs` under `Hub.mu`, then dispatches initial depth/ticker snapshots. Duplicate subscriptions are idempotent no-ops.
- `handleUnsubscribe(c *Client, stream string)`: Atomically removes subscription from `h.clientSubs`, `h.subs`, and decrements `h.marketSubs`.
- `Broadcast(channel string, payload []byte, streamType string)`: Concurrently fans out a payload to all active clients subscribed to `channel`.
- `GetActiveMarketIDs() []string`: Returns list of markets with $\ge 1$ subscriber (used by Streamer for on-demand Redis polling).
- `HasSubscribers(channel string) bool`: Checks if channel has active listeners.

### `handler.go`
- `NewHandler(...) *Handler`: Creates the HTTP upgrade handler.
- `checkOrigin(r *http.Request) bool`: Strict origin validator. Accepts non-browser clients (empty origin) and exact match configured `CORS_ORIGIN`.
- `ServeWS(w http.ResponseWriter, r *http.Request)`: Extracts JWT token from query `?token=...` or `Authorization: Bearer ...`, validates HMAC signature, upgrades connection, and launches `ReadPump` and `WritePump` goroutines.

---

## 5. Concurrency Architecture & Backpressure Matrix

### Single-Lock Concurrency Model
```
┌────────────────────────────────────────────────────────┐
│                        Hub.mu                          │
├───────────────────┬───────────────────┬────────────────┤
│    h.clients      │   h.clientSubs    │     h.subs     │
│ (Active Sessions) │ (Per-Client Sets) │ (Channel Sets) │
└───────────────────┴───────────────────┴────────────────┘
```
All state transitions (connect, disconnect, subscribe, unsubscribe) are protected exclusively by `Hub.mu`. This prevents race conditions where a disconnected client could leave ghost subscriptions in topic maps.

### Stream-Specific Backpressure Matrix
When a client's 256-slot outbound channel is saturated:

| Stream Type | Action on Saturated Buffer | Rationale |
| :--- | :---: | :--- |
| **OrderBook (`orderbook`)** | **Drop Frame** | Stale snapshots can be safely dropped; next tick delivers latest state. |
| **Ticker (`ticker`)** | **Drop Frame** | Rolling 24h stats can be dropped without harming trade execution. |
| **Trades (`trades`)** | **Disconnect Client** | Financial execution data must never be silently lost. Slow clients are terminated. |
| **Notifications (`notification`)** | **Disconnect Client** | Private order fill and account alerts must not be silently skipped. |
| **Control (`control`)** | **Drop Frame** | Non-critical status/error frame dropped. |
