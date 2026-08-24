# TradeDrift — Real-Time WebSocket Architecture & Data Flow (`WEBSOCKET_FLOW.md`)

> **Document Version:** `1.0.0`  
> **Route:** `GET /ws` (HTTP/1.1 Upgrade to RFC 6455)  
> **Backend Host & Port:** `services/gateway` (`:8080`)  
> **Frontend Client:** `frontend/src/api/ws.ts` & `frontend/src/pages/TradePage.tsx`  
> **Primary Role:** Real-Time Multicast Hub, Redis Depth & Ticker Fanout, Kafka Trades Streamer

---

## 1. System Architecture & Topology

TradeDrift uses an event-driven, decoupled streaming architecture where the **API Gateway** acts as a stateless real-time fan-out proxy between the exchange backend and trading clients.

```
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                                1. TRADING CLIENTS                                      │
│                                                                                        │
│   Web Browser (React / TradePage.tsx)        Mobile Apps / Programmatic Bots          │
│   └── src/api/ws.ts                          └── RFC 6455 Client                       │
└───────────────────────────────────────────┬────────────────────────────────────────────┘
                                            │
                                            │ 🌐 Single Persistent WebSocket (TCP)
                                            │    URL: ws://localhost:8080/ws?token=<JWT>
                                            │    Multiplexes all channels over 1 connection
                                            ▼
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                                2. API GATEWAY (:8080)                                  │
│   services/gateway/internal/ws/                                                        │
│                                                                                        │
│   ┌────────────────────────────────────────────────────────────────────────────────┐   │
│   │ HTTP Handshake & Security Layer (handler.go)                                    │   │
│   │ ├── CORS Origin Validation (CORS_ORIGIN: http://localhost:5173)                │   │
│   │ ├── JWT Signature & Claims Extractor (HMAC-SHA256 via JWT_SECRET)              │   │
│   │ └── HTTP Response Hijacker (RFC 6455 Upgrade -> 101 Switching Protocols)       │   │
│   └───────────────────────────────────────┬────────────────────────────────────────┘   │
│                                           │                                            │
│   ┌───────────────────────────────────────▼────────────────────────────────────────┐   │
│   │ Hub — Central Client & Subscription Registry (hub.go)                           │   │
│   │ ├── 6-Step Pre-Registration Subscription Validation Pipeline                   │   │
│   │ ├── Private Stream Authorization Guard (claims.UserID == target_user_id)       │   │
│   │ ├── On-Demand Active Market Tracker (Hub.GetActiveMarketIDs())                 │   │
│   │ └── Multicast Channel Router (Hub.Broadcast())                                 │   │
│   └───────────────────▲───────────────────────────────────────▲────────────────────┘   │
│                       │ (Initial Depth/Ticker Snapshot)       │ (Stream Data)          │
│   ┌───────────────────┴───────────────────────────────────────┴────────────────────┐   │
│   │ Streamer — Data Ingestion Engine (streamer.go)                                 │   │
│   │ ├── On-Demand Redis Depth Poller (250ms + xxhash Deduplication)                │   │
│   │ ├── Redis Ticker Fanout (1000ms interval)                                      │   │
│   │ └── Kafka Trade Streamer (Best-Effort Fanout via "gateway-websocket-group")    │   │
│   └───────────────────▲───────────────────────────────────────▲────────────────────┘   │
└───────────────────────┼───────────────────────────────────────┼────────────────────────┘
                        │                                       │
                        │ 🗄️ Redis TCP Pool                     │ 📨 Kafka TCP Consumer
                        │    (redis:6379)                       │    (kafka:29092)
                        │    - depth:{market_id}                │    - Topic: trades.executed
                        │    - ticker:{market_id}               │    - Group: gateway-websocket-group
                        ▼                                       ▼
┌───────────────────────────────────────────────┐   ┌────────────────────────────────────┐
│ 3. REDIS (In-Memory Data Store)               │   │ 4. KAFKA (Distributed Event Log)   │
│    ├── depth:BTC-USDT (L2 Depth Snapshots)    │   │    └── Topic: trades.executed      │
│    └── ticker:BTC-USDT (24h Market Stats)     │   │        (Executed Trade Events)     │
└───────────────────────▲───────────────────────┘   └─────────────────▲──────────────────┘
                        │                                             │
                        │ (Writes L2 depth every                      │ (Publishes executed
                        │  orderbook mutation)                        │  matches instantly)
                        │                                             │
┌───────────────────────┴─────────────────────────────────────────────┴──────────────────┐
│ 5. MATCHING ENGINE (services/matching-engine)                                          │
│    Processes limit/market orders, crosses books, generates matches                     │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Where & How the WebSocket Connects

### Step 1: Handshake & Connection Upgrade (`handler.go`)
1. When the client loads the trading UI, `frontend/src/api/ws.ts` initiates an HTTP GET request to `ws://localhost:8080/ws?token=<JWT>`.
2. **CORS Origin Check**: The Gateway inspects the incoming `Origin` header (`http://localhost:5173`) against `CORS_ORIGIN`. Unauthorized origins are rejected immediately.
3. **JWT Authentication**: If present, the Gateway verifies the token signature against `JWT_SECRET` and extracts the user's `user_id`.
4. **HTTP Hijacking (`http.Hijacker`)**:
   - The Gateway takes raw control of the TCP socket.
   - It sends `HTTP/1.1 101 Switching Protocols`.
   - The connection transforms into a **persistent, full-duplex RFC 6455 WebSocket**.
5. **Worker Goroutines**:
   - **`Client.ReadPump()`**: Continuously reads incoming JSON frames from the browser with a 512 KB message limit and a 60s read deadline.
   - **`Client.WritePump()`**: Flushes outbound market messages from the client's internal 256-slot channel queue (`c.send`) to the network with a 10s write deadline.

### Step 2: Channel Multiplexing
Instead of opening multiple WebSocket connections for charts, books, and trades, **a single WebSocket connection multiplexes all stream topics**:

```json
// Browser sends subscription frame:
{
  "event": "subscribe",
  "streams": [
    "market:orderbook:BTC-USDT",
    "market:trades:BTC-USDT",
    "market:ticker:BTC-USDT"
  ]
}
```

---

## 3. How Information is Ingested & Streamed (Channel-by-Channel)

### A. Level-2 Order Book Depth Ladder (`market:orderbook:{market_id}`)

```
Matching Engine
      │ (Orders matched/inserted/cancelled)
      ▼
Redis `depth:BTC-USDT`
      │
      ▼ (Gateway polls every 250ms ONLY IF active subscribers exist)
Streamer.runDepthPoller()
      │
      ├── Snapshot Deduplication: xxhash.Sum64String(rawJSON)
      │     └── If hash == previousHash -> SKIP (Zero redundant network bandwidth)
      │
      ├── Convert to OrderBookDepthPayload
      │     bids: [["64230.00", "1.25"], ["64225.00", "0.80"], ...]
      │     asks: [["64235.00", "0.50"], ["64240.00", "2.10"], ...]
      │
      ▼
Hub.Broadcast("market:orderbook:BTC-USDT")
      │
      ▼
Client.Send(StreamTypeOrderBook)
      │ (Enforces Backpressure: drops stale frame if 256-buffer is full)
      ▼
Frontend (TradePage.tsx) updates live visual depth ladder
```

---

### B. Real-Time Executed Trades Stream (`market:trades:{market_id}`)

```
Matching Engine
      │ (Order execution match occurred)
      ▼
Kafka Topic: `trades.executed`
      │
      ▼ (Consumed by dedicated group: "gateway-websocket-group")
Streamer.runKafkaTradeStreamer()
      │
      ├── Parses raw TradeExecuted event (trade_id, price, quantity, timestamp)
      ├── Wraps in TradePayload envelope:
      │     {
      │       "stream": "market:trades:BTC-USDT",
      │       "data": { "tradeId": "...", "price": "64230.50", "quantity": "0.5000", ... }
      │     }
      │
      ├── Hub.Broadcast("market:trades:BTC-USDT")
      │
      └── Instant Offset Commit: reader.CommitMessages() [Best-Effort Fanout Contract]
      │
      ▼
Frontend (TradePage.tsx) prepends trade to Recent Trades list & flashes last price
```

---

### C. 24h Ticker & Market Statistics (`market:ticker:{market_id}`)

```
Market Service (24h Aggregator Worker)
      │ (Aggregates high, low, volume, 24h % change from trades)
      ▼
Redis `ticker:BTC-USDT`
      │
      ▼ (Gateway polls every 1000ms for active markets)
Streamer.runTickerPoller()
      │
      ├── Snapshot Deduplication (xxhash)
      └── Hub.Broadcast("market:ticker:BTC-USDT")
      │
      ▼
Frontend (TradePage.tsx) updates top bar statistics (High, Low, 24h Change %, Volume)
```

---

### D. Immediate Snapshot Delivery (Zero-Lag Initial Render)

When a client first subscribes to `market:orderbook:BTC-USDT` or `market:ticker:BTC-USDT`, they do **not** wait for the next 250ms or 1000ms timer tick:
1. The `Hub` immediately calls `snapshotProvider.GetImmediateOrderBook(marketID)` and `GetImmediateTicker(marketID)`.
2. Redis is queried instantly on the client goroutine.
3. The initial snapshot is pushed directly to the new client socket before any subsequent broadcast cycles.

---

## 4. Protocol Specification & Message Schemas

### 4.1 Client Inbound Frames (Client $\rightarrow$ Server)

#### Subscription Frame
```json
{
  "event": "subscribe",
  "streams": [
    "market:orderbook:BTC-USDT",
    "market:trades:BTC-USDT",
    "market:ticker:BTC-USDT"
  ]
}
```

#### Unsubscription Frame
```json
{
  "event": "unsubscribe",
  "streams": [
    "market:orderbook:BTC-USDT"
  ]
}
```

#### Ping Heartbeat Frame
```json
{
  "event": "ping"
}
```

---

### 4.2 Server Outbound Frames (Server $\rightarrow$ Client)

#### 1. Order Book Depth Snapshot
```json
{
  "stream": "market:orderbook:BTC-USDT",
  "data": {
    "marketId": "BTC-USDT",
    "bids": [
      ["64230.00", "1.250"],
      ["64225.50", "0.850"],
      ["64220.00", "2.100"]
    ],
    "asks": [
      ["64235.00", "0.450"],
      ["64240.00", "1.000"],
      ["64245.00", "3.500"]
    ],
    "timestamp": "2026-08-23T15:08:40.123456Z"
  }
}
```

#### 2. Executed Trade Event
```json
{
  "stream": "market:trades:BTC-USDT",
  "data": {
    "tradeId": "01a02f29-3fc4-79bd-8a6d-a2470a45128b",
    "marketId": "BTC-USDT",
    "price": "64230.50",
    "quantity": "0.5000",
    "executedAt": "2026-08-23T15:08:39Z"
  }
}
```

#### 3. 24h Ticker Statistics
```json
{
  "stream": "market:ticker:BTC-USDT",
  "data": {
    "marketId": "BTC-USDT",
    "lastPrice": "64230.50",
    "high24h": "65100.00",
    "low24h": "62850.25",
    "volume24h": "45231.84",
    "quoteVolume24h": "289124012.00",
    "priceChange24hPercent": "+2.45"
  }
}
```

#### 4. Control Pong Response
```json
{
  "event": "pong"
}
```

#### 5. Error Frame
```json
{
  "stream": "control:error",
  "data": {
    "code": "UNAUTHORIZED",
    "message": "authentication required for private notifications"
  }
}
```

---

## 5. Architectural Contracts & Safety Guarantees

### 5.1 Per-Stream Backpressure Matrix

Each client session has a bounded `send` channel with a maximum capacity of **256 messages**. When a slow client's buffer fills:

| Stream | Strategy | Rationale |
| :--- | :--- | :--- |
| **`market:orderbook:*`** | **Drop stale snapshot** | A newer, complete snapshot arrives in 250ms. Dropping preserves server RAM without corrupting state. |
| **`market:ticker:*`** | **Drop stale update** | Ticker updates are continuous; latest value will arrive on next tick. |
| **`market:trades:*`** | **Disconnect slow client** | Executions represent historical fills and financial truth. They must never be silently lost. Disconnecting forces a clean reconnect. |
| **`user:notifications:*`** | **Disconnect slow client** | Private fill/order alerts must never be lost. |

---

### 5.2 On-Demand Polling (Zero Unnecessary Redis Load)

Instead of looping over all 1,000+ trading pairs every 250ms:
- The Gateway tracks active subscriber counts per market (`hub.marketSubs[marketID]`).
- When a user views `BTC-USDT`, the counter increments to `1` $\rightarrow$ Polling starts.
- When all users navigate away, the counter drops to `0` $\rightarrow$ Polling stops.
- **Result:** Redis CPU/Network load scales with **active viewing users ($O(\text{Active})$)**, not total listed markets ($O(\text{Total})$).

---

### 5.3 Snapshot Deduplication (xxhash)

- Depth snapshots from Redis are hashed using **`xxhash.Sum64String`** (~15 GB/s throughput).
- If the order book did not change between ticks, the hash matches the previous tick $\rightarrow$ **Broadcast is skipped**.
- Saves CPU serialization time and massive amounts of frontend network bandwidth.

---

### 5.4 Frontend Reconnect $\rightarrow$ REST Resynchronization Protocol

WebSocket streams are best-effort fanout. If a user temporarily loses internet connection (e.g. WiFi switch, sleep mode):

```
1. Socket Drops ──► ws.ts enters Exponential Reconnect Loop + Jitter
                     (Delay = min(30s, 1000 * 2^attempt) + rand(0, 1000ms))

2. Socket Reopens ─► ws.ts resubscribes to previous active channels

3. Parallel REST Resync ─► Fetches authoritative REST snapshots:
                     ├── GET /api/v1/markets/{id}/ticker
                     ├── GET /api/v1/orders
                     └── GET /api/v1/wallet/balances

4. Live Stream Resumes ──► Real-time WebSocket updates take over seamlessly
```

---

## 6. Observability & Monitoring Metrics

| Metric | Type | Purpose |
| :--- | :--- | :--- |
| `ws_connections_active` | Gauge | Current count of connected WebSocket client sockets. |
| `ws_subscriptions_active` | Gauge | Total active stream subscriptions across all clients. |
| `ws_messages_sent_total` | Counter | Total messages successfully written over WebSocket. |
| `ws_messages_dropped_total` | Counter | Stale orderbook/ticker snapshots dropped due to backpressure. |
| `ws_slow_clients_disconnected_total` | Counter | Slow clients disconnected due to trade buffer saturation. |
| `gateway_kafka_consumer_errors_total` | Counter | Errors encountered reading `trades.executed`. |
| `gateway_kafka_reconnects_total` | Counter | Kafka consumer reconnect attempts during broker outages. |

---

## 7. Package Source File Index

| File | Purpose | Key Responsibilities |
| :--- | :--- | :--- |
| [`services/gateway/internal/ws/dto.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/dto.go) | Protocol DTOs | Wire protocol frame structs, outbound envelopes, and stream type classification. |
| [`services/gateway/internal/ws/client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/client.go) | Client Session | Bounded channel queue (256), `ReadPump`, `WritePump`, deadlines, and stream-specific backpressure. |
| [`services/gateway/internal/ws/hub.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/hub.go) | Hub Registry | Thread-safe connection & subscription registry, on-demand market tracking, and 6-step subscription pipeline. |
| [`services/gateway/internal/ws/streamer.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/streamer.go) | Streamer Engine | 250ms Redis depth poller, xxhash deduplication, immediate snapshot provider, and Kafka trade fan-out. |
| [`services/gateway/internal/ws/handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/handler.go) | HTTP Upgrader | CORS validation, query/header JWT extraction, and RFC 6455 upgrade handler (`/ws`). |
| [`frontend/src/api/ws.ts`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/api/ws.ts) | Frontend Client | Reconnecting WebSocket singleton with jitter, 30s heartbeat, multi-channel routing, and REST resync hooks. |
| [`frontend/src/pages/TradePage.tsx`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/frontend/src/pages/TradePage.tsx) | Trading Terminal | Live L2 order book depth ladder, recent executed trades list, and real-time ticker statistics. |
