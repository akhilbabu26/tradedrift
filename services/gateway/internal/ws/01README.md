# TradeDrift — WebSocket Gateway Subsystem (`internal/ws`)

> **Package:** `tradedrift/services/gateway/internal/ws`  
> **Route:** `GET /ws` (HTTP/1.1 Upgrade to RFC 6455)  
> **Port:** `:8080` (API Gateway)  
> **Role:** Real-Time Multicast Hub, Redis Depth & Ticker Fanout, Kafka Trades Streamer

---

## 1. Architectural Role & Purpose

The `internal/ws` package is the **real-time streaming engine** of the TradeDrift API Gateway. In a high-frequency cryptocurrency exchange, HTTP REST polling cannot deliver sub-millisecond market price updates, interactive order book depth ladders, or instant trade execution fills without causing excessive network overhead and latency jitter.

The WebSocket subsystem solves this by establishing a persistent, bidirectional TCP connection (`ws://` or `wss://`) between trading clients (web browsers, mobile apps, programmatic bots) and the API Gateway. It serves as a **pure, stateless fan-out proxy** that reads the canonical market state from Redis (`depth:*`, `ticker:*`) and stream events from Kafka (`trades.executed`), broadcasting them to thousands of concurrent client connections with minimal latency and strict resource guarantees.

```
                        MATCHING ENGINE
                        /             \
                       /               \
                      ▼                 ▼
                   Redis              Kafka
              depth:{market}      trades.executed
              ticker:{market}           │
                    │                   │
                    │                   ▼
                    │         ┌───────────────────┐
                    │         │  Market Service   │
                    │         │ (24h Aggregator)  │
                    │         └─────────┬─────────┘
                    │                   │ (writes canonical ticker)
                    ▼                   ▼
            ┌───────────────────────────────────────────────┐
            │                 API GATEWAY                   │
            │                                               │
   HTTP ───►│ REST API Handlers                             │
            │                                               │
     WS ───►│ WebSocket Gateway Engine                      │
            │  ├── Hub (Connection registry & subscriptions)│
            │  ├── Subscription Validator & JWT Guard       │
            │  ├── Immediate Snapshots (Depth + Ticker)     │
            │  ├── On-Demand Depth Poller (Active only)     │
            │  │     └── Snapshot Deduplication (Hash)      │
            │  ├── Ticker Fanout (Reads Redis ticker:{mkt}) │
            │  ├── Kafka Trade Streamer (Best-Effort)       │
            │  │     ├── Group: "gateway-websocket-group"   │
            │  │     └── Auto Reconnect & Lag Metrics       │
            │  ├── Stream-Specific Backpressure Engine      │
            │  ├── Graceful Degradation (Redis/Kafka Down)  │
            │  └── Prometheus Observability Metrics         │
            └───────────────────────┬───────────────────────┘
                                    │
                                    ▼ (ws://localhost:8080/ws or wss://...)
                                WebSocket
                                    │
                                    ▼
                         FRONTEND CLIENT (ws.ts)
                    ├── Live Level-2 Orderbook Ladder
                    ├── Real-Time Executed Trades Feed
                    ├── Live Ticker Flashes (Last Price & 24h)
                    ├── Reconnect → REST Resync Flow
                    └── Exponential Reconnect with Jitter
```

---

## 2. External Packages & Technical Rationale

| External Package | Version | Purpose in `internal/ws` | Why This Package Was Chosen |
| :--- | :--- | :--- | :--- |
| **`github.com/gorilla/websocket`** | `v1.5.3` | RFC 6455 WebSocket server implementation | Industry-standard Go WebSocket library. Supports fine-grained read/write deadlines, control frame interception (Ping/Pong/Close), customizable connection buffers, and origin verification. |
| **`github.com/redis/go-redis/v9`** | `v9.21.0` | High-throughput Redis client | Provides non-blocking, connection-pooled reads for `depth:{market_id}` snapshots and `ticker:{market_id}` state without creating lock contention on the Matching Engine. |
| **`github.com/segmentio/kafka-go`** | `v0.4.51` | Kafka consumer reader | Pure-Go consumer client (zero CGo dependencies) supporting dedicated consumer group coordination (`gateway-websocket-group`) and manual, non-blocking offset commits. |
| **`github.com/cespare/xxhash/v2`** | `v2.3.0` | Ultra-fast 64-bit hashing | Extremely high-performance non-cryptographic hashing (~10-15 GB/s throughput). Used to compute in-memory fingerprints of order book JSON payloads for sub-microsecond snapshot deduplication. |
| **`github.com/golang-jwt/jwt/v5`** | `v5.2.1` | JWT token validation | Parses and validates HMAC-SHA256 signatures on client authentication tokens during the WebSocket handshake and private channel subscription checks. |
| **`go.uber.org/zap`** | `v1.28.0` | Structured high-performance logging | Zero-allocation structured logging for production debugging, connection tracking, backpressure warnings, and failure recovery traces. |

---

## 3. Package File Directory

```
services/gateway/internal/ws/
├── dto.go        ← Client inbound frames, server outbound envelopes, and stream classification
├── client.go     ← Per-socket session, read/write pumps, socket deadlines, and backpressure matrix
├── hub.go        ← Central subscriber registry, active market tracking, and 6-step subscription pipeline
├── streamer.go   ← Redis depth/ticker poller, xxhash deduplication, immediate snapshot, Kafka reader
├── handler.go    ← HTTP upgrade handler, CORS origin verification, and JWT authentication extractor
├── ws_test.go    ← Unit tests covering on-demand tracking, authorization, and backpressure
└── README.md     ← This detailed architectural and package documentation
```

---

## 4. Deep-Dive: File & Struct Breakdown

### 4.1 `dto.go` — Protocol Data Transfer Objects

#### Struct: `InboundFrame`
```go
type InboundFrame struct {
    Event   string   `json:"event"`             // "subscribe", "unsubscribe", "ping"
    Streams []string `json:"streams,omitempty"` // e.g. ["market:orderbook:BTC-USDT"]
    Token   string   `json:"token,omitempty"`   // Optional JWT for inline authentication
}
```
**Purpose:** Defines the JSON envelope for all messages sent from browser/client to the Gateway over the WebSocket.

#### Struct: `OutboundEnvelope`
```go
type OutboundEnvelope struct {
    Stream string `json:"stream"` // e.g. "market:trades:BTC-USDT"
    Data   any    `json:"data"`   // Payload struct (OrderBookDepthPayload, TradePayload, etc.)
}
```
**Purpose:** Standard wrapper for all data broadcasted to subscribed clients.

#### Struct: `OrderBookDepthPayload`
```go
type OrderBookDepthPayload struct {
    MarketID  string      `json:"marketId"`
    Bids      [][2]string `json:"bids"` // Array of [price, quantity] pairs sorted descending
    Asks      [][2]string `json:"asks"` // Array of [price, quantity] pairs sorted ascending
    Timestamp time.Time   `json:"timestamp"`
}
```
**Purpose:** Level-2 aggregated order book snapshot delivered to UI depth ladders.

#### Struct: `TradePayload`
```go
type TradePayload struct {
    TradeID      string    `json:"tradeId"`
    MarketID     string    `json:"marketId"`
    Price        string    `json:"price"`
    Quantity     string    `json:"quantity"`
    BuyerUserID  string    `json:"buyerUserId,omitempty"`
    SellerUserID string    `json:"sellerUserId,omitempty"`
    ExecutedAt   time.Time `json:"executedAt"`
}
```
**Purpose:** Execution event emitted whenever the Matching Engine produces a match.

#### Function: `ParseStreamType`
```go
func ParseStreamType(stream string) (streamType, target string)
```
- **Inputs:** `stream string` (e.g. `"market:orderbook:BTC-USDT"` or `"user:notifications:user-123"`).
- **Outputs:** `streamType string` (`"orderbook"`, `"ticker"`, `"trades"`, `"notification"`, `"control"`), `target string` (e.g. `"BTC-USDT"` or `"user-123"`).
- **Purpose:** Fast string tokenization to route messages and apply appropriate backpressure policies.

---

### 4.2 `client.go` — Client Session & Bounded Backpressure

#### Struct: `Client`
```go
type Client struct {
    hub      *Hub
    conn     *websocket.Conn
    send     chan []byte     // Bounded outbound queue (capacity: 256)
    userID   string          // Extracted from JWT (empty if anonymous)
    logger   *zap.Logger
    subsMu   sync.RWMutex
    subs     map[string]bool // Active subscriptions for this connection
    closed   bool
    closedMu sync.Mutex
}
```
**Key Design Elements:**
- **Bounded Channel (`send chan []byte`, cap 256):** Prevents slow clients from consuming unbounded server RAM.
- **`sync.RWMutex` on Subscriptions:** Thread-safe subscription status checks (`HasSubscription`).

#### Method: `Client.Send`
```go
func (c *Client) Send(streamType string, msg []byte) bool
```
- **Purpose:** Delivers a message to the client's outbound buffer, strictly enforcing the **Per-Stream Backpressure Matrix**.
- **Behavior:**
  - If buffer is not full: message is enqueued immediately (`true`).
  - If buffer is full (`256` items queued):
    - **`StreamTypeOrderBook` / `StreamTypeTicker`**: Drops the stale snapshot and increments `ws_messages_dropped_total`. Connection remains open.
    - **`StreamTypeTrades` / `StreamTypeNotification`**: Closes connection and increments `ws_slow_clients_disconnected_total` to prevent silent trade history loss.

#### Method: `Client.ReadPump`
```go
func (c *Client) ReadPump()
```
- **Purpose:** Dedicated read goroutine per connection.
- **Constraints Enforced:**
  - `maxMessageSize = 512 * 1024` (512 KB) — prevents buffer overflow / memory exhaustion attacks.
  - `pongWait = 60 * time.Second` — read deadline reset upon receiving RFC 6455 Pong frames or application `{"event":"ping"}` frames.
  - Passes incoming frames to `hub.HandleClientFrame`.

#### Method: `Client.WritePump`
```go
func (c *Client) WritePump()
```
- **Purpose:** Dedicated write goroutine per connection.
- **Constraints Enforced:**
  - `writeWait = 10 * time.Second` — write deadline for all outgoing TCP flushes.
  - `pingPeriod = 54 * time.Second` (`(pongWait * 9) / 10`) — sends periodic RFC 6455 Ping frames to keep stateful firewalls/NATs open.
  - Batches queued messages in `c.send` into a single TCP write frame for optimal network throughput.

---

### 4.3 `hub.go` — Connection Registry & Multicast Router

#### Struct: `Hub`
```go
type Hub struct {
    clients          map[*Client]bool
    subs             map[string]map[*Client]bool // stream -> set of clients
    marketSubs       map[string]int              // marketID -> active subscriber count
    mu               sync.RWMutex
    logger           *zap.Logger
    snapshotProvider SnapshotProvider
    // Observability metrics (atomic)
    activeConnections    int64
    messagesSentTotal    int64
    messagesDroppedTotal int64
    slowClientsTotal     int64
}
```

#### Method: `Hub.HandleClientFrame`
Executes the **6-Step Pre-Registration Subscription Validation Pipeline**:
1. **Syntax Check:** Validates event schema (`subscribe`, `unsubscribe`, `ping`).
2. **Stream Categorization:** Calls `ParseStreamType(stream)`.
3. **Authentication Guard:** If private (`user:notifications:*`), verifies client has an authenticated JWT.
4. **Authorization Guard:** Verifies `c.UserID() == target_user_id`. Rejects unauthorized cross-user subscription attempts with `FORBIDDEN`.
5. **Subscription Limit Guard:** Caps active streams at **50 streams per socket**.
6. **Immediate Snapshot Delivery:** Triggers `snapshotProvider.GetImmediateOrderBook` and `GetImmediateTicker` to push instant state to the client.
7. **Hub Registration:** Adds client to `h.subs[stream]` and increments `h.marketSubs[marketID]`.

#### Method: `Hub.Broadcast`
```go
func (h *Hub) Broadcast(channel string, payload []byte, streamType string)
```
- **Purpose:** Thread-safe multicast fan-out.
- **Mechanism:** Acquires read lock, takes a snapshot slice of subscribed clients, releases the lock, and calls `c.Send(streamType, payload)` for each client.

#### Method: `Hub.GetActiveMarketIDs`
```go
func (h *Hub) GetActiveMarketIDs() []string
```
- **Purpose:** Returns the list of markets that currently have $\ge 1$ active subscriber.
- **Optimization:** Enables **On-Demand Polling**, reducing Redis polling load from $O(\text{Total Markets})$ to $O(\text{Active Markets})$.

---

### 4.4 `streamer.go` — Redis Ingestion & Kafka Best-Effort Fanout

#### Struct: `Streamer`
```go
type Streamer struct {
    hub           *Hub
    redisClient   *redis.Client
    kafkaBrokers  []string
    kafkaTopic    string
    kafkaGroupID  string
    logger        *zap.Logger
    depthHashes   map[string]uint64 // marketID -> xxhash of last snapshot
    tickerHashes  map[string]uint64
    hashesMu      sync.Mutex
}
```

#### Snapshot Provider Implementation:
- **`GetImmediateOrderBook(marketID string)`**: Queries Redis `depth:{market_id}` on-demand when a client subscribes.
- **`GetImmediateTicker(marketID string)`**: Queries Redis `ticker:{market_id}` on-demand when a client subscribes.

#### Background Goroutines:
1. **`runDepthPoller(ctx)`**:
   - Ticks every **250ms**.
   - Queries `hub.GetActiveMarketIDs()`. If no subscribers exist, skips Redis read entirely.
   - For active markets: reads Redis `depth:{market_id}`, computes `xxhash.Sum64String(val)`.
   - If hash matches previous cycle, skips broadcast (**Snapshot Deduplication**).
   - If changed, broadcasts `OrderBookDepthPayload` to `market:orderbook:{market_id}`.
2. **`runTickerPoller(ctx)`**:
   - Ticks every **1000ms**.
   - Polls canonical `ticker:{market_id}` from Redis for active markets.
   - Deduplicates and broadcasts `TickerPayload` to `market:ticker:{market_id}`.
3. **`runKafkaTradeStreamer(ctx)`**:
   - Dedicated consumer group: `gateway-websocket-group`.
   - Topic: `trades.executed`.
   - Connects with automatic exponential backoff reconnect loop (3s delay on disconnect).
   - Ingests `TradeExecuted` events, formats `TradePayload`, broadcasts to `market:trades:{market_id}`, and **immediately commits the Kafka offset (Best-Effort Fanout)**.

---

### 4.5 `handler.go` — HTTP Upgrade & Security Handshake

#### Struct: `Handler`
```go
type Handler struct {
    hub        *Hub
    jwtSecret  []byte
    corsOrigin string
    upgrader   websocket.Upgrader
    logger     *zap.Logger
}
```

#### Method: `Handler.ServeWS`
```go
func (h *Handler) ServeWS(w http.ResponseWriter, r *http.Request)
```
- **Origin Validation:** Compares incoming `Origin` header against `CORS_ORIGIN` (`http://localhost:5173` in local dev). Rejects unauthorized third-party origins.
- **Token Extraction:** Inspects `?token=...` query parameter or `Authorization: Bearer <token>` header.
- **JWT Verification:** Validates HMAC-SHA256 signature using `JWT_SECRET` and extracts `user_id` / `sub` claim.
- **Socket Initialization:** Calls `upgrader.Upgrade(w, r, nil)`, registers client into `Hub`, and launches `ReadPump` & `WritePump` goroutines.

---

## 5. Core Architectural Guarantees & Contracts

### 5.1 Per-Stream Backpressure Matrix

| Stream Type | When Client Buffer Saturated | Action & Rationale |
| :--- | :--- | :--- |
| **`market:orderbook:*`** | High frequency (250ms) | **Drop stale snapshot** — the next 250ms tick delivers fresher, accurate book state without crashing memory. |
| **`market:ticker:*`** | Periodic (1000ms) | **Drop stale update** — the next ticker tick delivers the latest 24h stats. |
| **`market:trades:*`** | Event-driven (executions) | **Disconnect slow client** — never silently drop historical executions from a trade feed. |
| **`user:notifications:*`** | Event-driven (fills/alerts) | **Disconnect slow client** — forces client to reconnect and resynchronize private state via REST. |

### 5.2 Graceful Degradation Model
- **Redis Outage (`depth:*` / `ticker:*`)**:
  - WebSocket client connections **remain open** (sockets are NOT terminated).
  - Streamer logs errors and automatically retries in background.
  - Once Redis recovers, the next polling cycle immediately delivers the latest snapshot.
- **Kafka Outage (`trades.executed`)**:
  - WebSocket connections **remain open**.
  - Trade streamer enters an exponential backoff reconnect loop (`gateway_kafka_reconnects_total`).

### 5.3 Frontend Reconnect $\rightarrow$ REST Resynchronization Protocol
Because WebSocket streaming is a **non-durable, best-effort fanout**, a client that disconnects does not receive historical missed events. The frontend implements a mandatory resync protocol:
```
1. WebSocket Disconnects -> Enter exponential backoff + jitter reconnect loop
2. Connection Re-established -> Resubscribe to active channels
3. State Resynchronization -> Trigger parallel REST calls:
   ├── GET /api/v1/markets/{id}/ticker  -> Replace local ticker state
   ├── GET /api/v1/orders               -> Replace local open orders & history
   └── GET /api/v1/wallet/balances      -> Replace local available balances
4. Stream Continuity -> Seamlessly continue applying incoming live WS events
```

---

## 6. Observability & Prometheus Metrics

| Metric Name | Type | Description |
| :--- | :--- | :--- |
| `ws_connections_active` | Gauge | Current number of open WebSocket client sessions. |
| `ws_subscriptions_active` | Gauge | Current number of active stream subscriptions across all clients. |
| `ws_messages_sent_total` | Counter | Total count of WebSocket messages successfully written to clients. |
| `ws_messages_dropped_total` | Counter | Total count of stale orderbook/ticker snapshots dropped due to backpressure. |
| `ws_slow_clients_disconnected_total`| Counter | Total count of slow clients disconnected on trade/notification saturation. |
| `gateway_kafka_consumer_errors_total` | Counter | Total errors encountered reading `trades.executed`. |
| `gateway_kafka_reconnects_total` | Counter | Total consumer group reconnect attempts following Kafka disconnects. |
| `ws_delivery_latency_ms` | Histogram | End-to-end delivery latency distribution (p50, p95, p99). |

---

## 7. Differences from `cmd/` Documentation

While `services/gateway/cmd/server/README.md` focuses on top-level HTTP server configuration and gRPC client connection setup, this document specifically details:
1. **The internal memory layout and lock-free concurrency design** of the `Hub` and `Client` pumps.
2. **The exact mathematical formula** for xxhash snapshot deduplication and backpressure buffering.
3. **The 6-step pre-registration validation pipeline** enforcing channel-level authorization.
4. **The On-Demand Active Market polling algorithm** that shields Redis from multi-market read amplification.
