# TradeDrift — WebSocket Gateway Architecture Specification

> **Status:** ✅ Designed (V1.1)
> **Document:** 19_WebSocket_Architecture.md
> **Service:** Platform Architecture
> **Version:** V1.1
> **Last Updated:** July 2026
> Revision notes: V1.1 addresses platform reliability and conformance details: (1) replaces JSON heartbeats with RFC 6455 Ping/Pong control frames; (2) corrects illegal Close code 1006 to 1008 Policy Violation; (3) defines JWT token expiration behavior; (4) establishes channel subscription limits; (5) enables RFC 7692 permessage-deflate compression; (6) defines reconnection restoration semantics; (7) aligns L2 orderbook updates with the 250ms Redis polling model; (8) softens pod connection limits; (9) adds SIGTERM graceful shutdown handler lifecycle.

---

## 1. Gateway Connection Lifecycle

The WebSocket Gateway (`/ws`) provides real-time streaming capabilities to public guests and authenticated clients. 

```
Client                               WebSocket API Node                Redis Pub/Sub
  │                                          │                              │
  ├─ 1. TCP Upgrade request (/ws) ──────────►│                              │
  │  (Guest Connection Established)          │                              │
  │                                          │                              │
  ├─ 2. JSON: {"action": "subscribe"} ──────►│                              │
  │                                          ├─ 3. SUBSCRIBE channel ──────►│
  │                                          ◄─ 4. Subscribed OK ───────────┤
  ◄─ 5. JSON: {"status": "subscribed"} ──────┤                              │
```

### Connection Rules:
* **Anonymous Guest Access:** Clients are permitted to connect anonymously (without providing authorization headers during the initial HTTP upgrade handshake). Guest sockets remain open indefinitely, enabling anonymous clients to subscribe to public feeds.
* **Authentication Upgrade:** To subscribe to private feeds, a guest connection must upgrade its authorization status by sending a JSON `auth` frame containing a valid JWT.
* **Grace Termination Policy:** If a client requests subscription to any private feed (e.g., `user:*`) while unauthenticated, the WebSocket Gateway starts a **5-second grace timer**. If the client fails to submit a valid `auth` frame within this 5-second window, the Gateway terminates the TCP connection with close code `4401 Unauthorized`.
* **Multiplexed Design:** A client opens a single physical WebSocket TCP connection, multiplexing multiple public and private subscription channels over this single link.
* **Permessage-Deflate Compression:** Sockets support RFC 7692 `permessage-deflate` compression to reduce outbound network payload egress, particularly for highly volatile orderbook streams.
* **Reconnection Restoration Semantics:** WebSocket connection drops do not persist subscription states. When a client reconnects, it initiates as a guest socket. The client application is responsible for resubmitting authentication frames and channel subscriptions.
* **JWT Expiration Handling:** If the client's JWT expires while the socket is open, the connection **remains open**. Public subscriptions (`market:*`) continue streaming. However, the server revokes all private subscriptions (`user:*`), stops sending updates, and publishes a JSON status frame to the client:
  ```json
  {
    "status": "auth_expired",
    "message": "JWT has expired. Send a fresh auth frame to restore private subscriptions."
  }
  ```
  The client can restore private streams by sending a fresh `auth` frame containing a renewed JWT, avoiding the need to reconnect the socket.

---

## 2. Framing & Subscription Protocol

All communications over the WebSocket connection utilize text frames containing JSON payloads.

### 2.1 Client Request Frames

#### 2.1.1 Authorization Upgrade
* **Description:** Upgrades the connection state from guest to authenticated user.
* **Payload:**
  ```json
  {
    "action": "auth",
    "token": "eyJhbGciOiJIUzI1NiIsIn..."
  }
  ```

#### 2.1.2 Channel Subscription
* **Description:** Requests real-time updates for a public or private feed.
* **Payload:**
  ```json
  {
    "action": "subscribe",
    "channel": "market:trades:BTC-USDT"
  }
  ```

#### 2.1.3 Channel Unsubscription
* **Description:** Terminates updates for a specific feed.
* **Payload:**
  ```json
  {
    "action": "unsubscribe",
    "channel": "market:trades:BTC-USDT"
  }
  ```

---

### 2.2 Server Response Frames

#### 2.2.1 Status Acknowledgement
* **Description:** Confirms success or error for client requests.
* **Payload Examples:**
  ```json
  {
    "status": "subscribed",
    "channel": "market:trades:BTC-USDT"
  }
  ```
  ```json
  {
    "status": "unauthorized",
    "channel": "user:notifications:018f60f3-a120-7798-8422-cfb6a29e11aa",
    "message": "Valid authentication token required for private streams"
  }
  ```

#### 2.2.2 Event Data Broadcast
* **Description:** Real-time stream ticks published to subscribers. **Contains a standard `event_version` attribute matching Kafka schemas.**
* **Payload Examples:**
  ```json
  {
    "channel": "market:trades:BTC-USDT",
    "event": "trade",
    "event_version": 1,
    "data": {
      "trade_id": "018f60f3-c540-7798-8422-efa6b29f1234",
      "price": "58200.0000000000",
      "quantity": "0.0400000000",
      "executed_at": "2026-07-10T18:07:42Z"
    }
  }
  ```

### 2.3 Subscription Abuse Safeguards
To prevent resource exhaustion by malicious or misconfigured clients, the WebSocket Gateway enforces strict concurrent subscription limits per connection:
* **Max Public Channel Subscriptions:** `50` channels.
* **Max Private Channel Subscriptions:** `5` channels.
* Requests exceeding these limits are rejected with a JSON status error frame.

---

## 3. Keep-Alive & Keep-Warm Heartbeats

To detect broken connections ("zombie" sockets caused by client crashes, firewall drops, or cellular handovers), the gateway utilizes standard **RFC 6455 WebSocket control frames**:

```
WebSocket API Node                                            Client
       │                                                         │
       ├─ 1. Ping Control Frame (0x9) ──────────────────────────►│
       │                                                         │
       │  (Awaits Pong Frame - Max 10s)                          │
       │                                                         │
       ◄─ 2. Pong Control Frame (0xA) ───────────────────────────┤
```

### Protocol Mechanics:
1. **Heartbeat Frequency:** The Gateway sends an RFC 6455 Ping control frame (opcode `0x9`) every **30 seconds**.
2. **Pong Threshold:** The client must respond with a corresponding Pong control frame (opcode `0xA`) within **10 seconds**. Browser engines handle this check and response automatically at the protocol layer, while custom non-browser WebSocket clients must implement pong frame generation.
3. **Hard Termination:** If a pong control frame is not returned within the 10-second window, the server forcefully closes the socket connection with close code `1008 Policy Violation`.

---

Since WebSocket connections are stateful and sticky to the specific pod replica that accepted the TCP link, fanning out private updates requires a distributed backplane:

```
                    [ Notification Service Worker ]
                                  │
                 (Consumes TradeSettled from Kafka)
                                  │
              (Publishes update to Redis Pub/Sub channel)
                    (user:notifications:{user_id})
                                  │
                      ┌───────────┴───────────┐
                      ▼                       ▼
            [ Gateway Pod Replica 1 ] [ Gateway Pod Replica 2 ]
             (Subscribes to channel)   (Subscribes to channel)
                      │                       │
             (No active user socket)  (Active user socket present)
                      │                       │
                  (Ignored)           (Pushes to Socket client)
```

### Event Distribution & Redis Polling Rules:
1. **Notification Routing (Pub/Sub):** The Gateway replica node maps authenticated user sessions to Redis Pub/Sub channels matching `user:notifications:{user_id}` and `user:portfolio:{user_id}`. A single client connection subscribes to these channels and fans out events to memory sockets. Subscriptions are cleaned up immediately on disconnection.
2. **Order Book Routing (Direct Polling):** Consistent with the Market Service design, **the orderbook does not use Redis Pub/Sub channels**. 
   * The Matching Engine writes L2 depth snapshots to `orderbook:{market_id}`.
   * If a Gateway replica pod holds active client subscriptions for a market's orderbook, the pod runs a local **250ms polling loop** query to `orderbook:{market_id}` on Redis.
   * If the snapshot's timestamp or structure has changed since the previous loop, the pod broadcasts the updated L2 order book structure directly to its local connection sockets. This avoids fanning out bulky orderbook updates across the Pub/Sub backplane.

---

To maintain system stability under load spikes:
* **Connections Capacity:** Gateway pods are sized to support approximately **10,000 concurrent idle connections** depending on hardware resource constraints and active message throughput, rather than imposing a strict architectural ceiling. If capacity limits are reached, incoming TCP handshakes are rejected with HTTP status code `503 Service Unavailable`.
* **Outbound Buffer Allocation:** Each active socket is allocated a buffered channel of **256 messages** in application memory.
* **Slow Consumer Disconnection:** If a client's network connection is congested and the socket buffer channel overflows (capacity > 256), the server immediately drops the connection to prevent memory bloat, closing with code `4429 Slow Consumer`.

---

## 6. Service Invariants

- **WSA-1 (Public Streaming Freedom):** Guest connections must not be terminated if they restrict subscriptions to public streams (`market:*`).
- **WSA-2 (Strict Grace Authentication):** Subscription to a private stream (`user:*`) from an unauthenticated socket must trigger a 5-second termination timer, closed with status `4401` if a valid `auth` request is not completed.
- **WSA-3 (Backpressure Drop):** Pod memory allocation safety must be preserved. If a client's write buffer overflows, the socket must be forcefully closed.
- **WSA-4 (Zombie Connection Eviction):** Sockets failing to complete ping-pong heartbeat challenges within the 10-second response window must be evicted immediately using close code `1008 Policy Violation`.
- **WSA-5 (Graceful Shutdown draining):** Upon receipt of SIGTERM, Gateway nodes must stop accepting new upgrades, notify active connections, and drain existing sockets using close code `1001 Going Away`.

---

## 7. Graceful Shutdown Lifecycle (SIGTERM Handler)

To support rolling software updates in Kubernetes without disrupting client connections abruptly, the Gateway implements a structured shutdown workflow:

```
[ SIGTERM Received ]
        │
        ├─ 1. Reject new HTTP upgrades (Return 503)
        │
        ├─ 2. Broadcast JSON: {"status": "disconnecting", "reason": "server_shutdown"}
        │
        ├─ 3. Start 30-second Connection Draining Loop:
        │     - Close ~10% of connections every 3 seconds
        │     - Close code: 1001 Going Away (signals client to execute reconnect backoff)
        │
        └─ 4. Terminate shared Redis client connections & exit process
```

