# Protocol Package (`internal/ws/protocol`)

## 1. Purpose
The `protocol` package defines the **public wire protocol and interface contracts** for TradeDrift's WebSocket subsystem. It is completely decoupled from network transport and databases (has zero third-party dependencies, using only the Go standard library).

It establishes:
1. Exact JSON frame specifications for all inbound and outbound client messages.
2. Interface contracts (`SnapshotProvider`, `Broadcaster`) enabling loose coupling between connection hubs and data streamers.
3. Strict stream naming grammar and pattern allowlists.
4. Privacy boundaries ensuring internal user IDs are never exposed on public market feeds.

---

## 2. File-by-File Breakdown

| File | Responsibility |
| :--- | :--- |
| [`dto.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/protocol/dto.go) | **Wire Models & Payloads**: Inbound client frames, outbound broadcast envelopes, Level-2 depth, trade execution, 24h ticker, and private notification payloads. |
| [`interfaces.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/protocol/interfaces.go) | **Decoupled Architecture Contracts**: Defines `SnapshotProvider` and `Broadcaster` interfaces to prevent circular dependencies between `hub` and `streamer`. |
| [`validator.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/protocol/validator.go) | **Stream Grammar & Allowlist**: Strict validation rules for topic paths and JSON marshaling helpers. |
| [`validator_test.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/protocol/validator_test.go) | **Protocol & Privacy Tests**: Validates stream parsing allowlists and asserts that public trade JSON never contains buyer/seller user IDs. |

---

## 3. Packages Used & Rationale

| Package | Why It Is Used |
| :--- | :--- |
| `encoding/json` | Standard Go JSON encoder/decoder for serializing and deserializing wire envelopes. |
| `strings` | High-efficiency string splitting and prefix matching for stream validation. |

*Note: The `protocol` package intentionally has **zero external third-party dependencies**, keeping the core contract pure and portable.*

---

## 4. DTOs & Payloads Reference

### Client Inbound Frames
- `InboundFrame`:
  ```json
  {
    "event": "subscribe",
    "streams": ["market:orderbook:BTC-USDT", "market:trades:BTC-USDT"]
  }
  ```
  Accepted events: `"subscribe"`, `"unsubscribe"`, `"ping"`. Authentication is exclusively performed during HTTP upgrade (no in-band credentials).

### Server Outbound Envelopes
- `OutboundEnvelope`:
  ```json
  {
    "stream": "market:orderbook:BTC-USDT",
    "data": { ... }
  }
  ```
- `OutboundEvent` (Control Frames):
  ```json
  {
    "event": "error",
    "code": "MARKET_DATA_UNAVAILABLE",
    "message": "market data temporarily unavailable for market:orderbook:BTC-USDT"
  }
  ```

### Stream Payloads & Data Contracts
- `OrderBookDepthPayload`:
  - Represents Level-2 depth snapshot.
  - `Sequence uint64`: Authoritative Matching Engine sequence stored in Redis.
  - `Bids [][2]string` & `Asks [][2]string`: Array of `[price, quantity]` tuples.
  - `Timestamp int64`: Unix milliseconds.
- `TradePayload`:
  - Represents public executed trade fills.
  - **Privacy Guarantee**: `BuyerUserID` and `SellerUserID` are strictly omitted from public feeds.
  - `TradeID string`, `MarketID string`, `Price string`, `Quantity string`, `Side string`, `Sequence uint64`, `ExecutedAt int64`.
- `TickerPayload`:
  - 24h market statistics (`LastPrice`, `High24h`, `Low24h`, `Volume24h`, `QuoteVolume24h`, `PriceChange24hPercent`, `Timestamp`).
- `NotificationPayload`:
  - Private user alerts (`ID`, `UserID`, `Title`, `Message`, `Type`, `CreatedAt`).

---

## 5. Interfaces & Architecture Contracts

```go
// SnapshotProvider allows Hub to fetch initial snapshots without depending on Streamer.
type SnapshotProvider interface {
    GetImmediateOrderBook(marketID string) (*OrderBookDepthPayload, error)
    GetImmediateTicker(marketID string) (*TickerPayload, error)
}

// Broadcaster allows Streamer to fanout data without depending on Hub implementation.
type Broadcaster interface {
    Broadcast(channel string, payload []byte, streamType string)
    HasSubscribers(channel string) bool
    GetActiveMarketIDs() []string
}
```

---

## 6. Stream Grammar & Allowlist

The `ValidateStream(stream string)` function enforces strict 3-part topic names (`prefix:type:target`):

| Pattern | Stream Type | Example | Access |
| :--- | :--- | :--- | :--- |
| `market:orderbook:{market_id}` | `StreamTypeOrderBook` | `market:orderbook:BTC-USDT` | Public |
| `market:ticker:{market_id}` | `StreamTypeTicker` | `market:ticker:ETH-USDT` | Public |
| `market:trades:{market_id}` | `StreamTypeTrades` | `market:trades:SOL-USDT` | Public |
| `user:notifications:{user_id}` | `StreamTypeNotification` | `user:notifications:usr_981` | Authenticated Owner Only |

*Any stream not matching one of the above 4 patterns is rejected immediately with `INVALID_STREAM`.*
