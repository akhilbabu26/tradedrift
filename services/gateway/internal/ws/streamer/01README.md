# Streamer Package (`internal/ws/streamer`)

## 1. Purpose
The `streamer` package is the **real-time data ingestion and distribution engine** for TradeDrift's WebSocket gateway. It coordinates between backend data stores (Redis and Kafka) and connected clients via the `protocol.Broadcaster` interface.

Key responsibilities:
1. **On-Demand Level-2 Depth Polling**: Polls Redis order book depth at 250ms intervals only for markets with active subscribers.
2. **Snapshot Deduplication**: Uses 64-bit `xxhash` fingerprints to eliminate redundant broadcasts when the order book is unchanged.
3. **Failure State Machine**: Emits a single `MARKET_DATA_UNAVAILABLE` error frame upon Redis outage, preventing 4 Hz frame spam, and triggers an immediate fresh snapshot upon recovery.
4. **Resilient Kafka Trade Ingestion**: Consumes `trades.executed` events with jittered exponential backoff and `StartOffset: kafka.LastOffset` historical replay prevention.
5. **Authoritative Sequence Forwarding**: Forwards the Matching Engine's monotonically increasing sequence without generating synthetic gateway-local counters.

---

## 2. File-by-File Breakdown

| File | Responsibility |
| :--- | :--- |
| [`streamer.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/streamer/streamer.go) | **Core Streamer Coordinator**: Struct definition, lifecycle starter (`Start()`), `SnapshotProvider` implementation (`GetImmediateOrderBook`, `GetImmediateTicker`), and observability counters. |
| [`redis_poller.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/streamer/redis_poller.go) | **Redis Polling Loops & State Machine**: 250ms depth poller, 1s ticker poller, `xxhash` deduplication, and `AVAILABLE` $\leftrightarrow$ `UNAVAILABLE` failure transitions. |
| [`kafka_consumer.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/streamer/kafka_consumer.go) | **Kafka Trade Streamer**: Consumer loop (`consumeKafkaTrades`), jittered exponential backoff (`KafkaBackoff`), event validation (`ValidateTradeEvent`), and fan-out broadcasting. |
| [`schemas.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/streamer/schemas.go) | **Internal Backend Models**: Defines package-private `rawRedisDepth` and `RawTradeEvent` matching engine formats, and payload mapping helpers. |
| [`streamer_test.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/ws/streamer/streamer_test.go) | **Unit & State Machine Tests**: Validates numeric sanity checks, Kafka backoff delay curves, and state machine transitions. |

---

## 3. Packages Used & Rationale

| Package | Why It Is Used |
| :--- | :--- |
| `github.com/redis/go-redis/v9` | Low-latency Redis client for querying depth snapshots (`depth:{market}`) and 24h market stats (`ticker:{market}`). |
| `github.com/segmentio/kafka-go` | High-throughput Kafka consumer supporting manual offset commits and group balancing. |
| `github.com/cespare/xxhash/v2` | Ultra-fast 64-bit hashing algorithm (processes ~10 GB/s) for zero-overhead JSON snapshot deduplication. |
| `go.uber.org/zap` | High-performance structured logging. |
| `tradedrift/services/gateway/internal/ws/protocol` | Public DTOs and interfaces (`Broadcaster`, `SnapshotProvider`). |

---

## 4. Functions & Methods Reference

### `streamer.go`
- `NewStreamer(...) *Streamer`: Initializes the Streamer instance with deduplication maps and availability state trackers.
- `SetBroadcaster(b protocol.Broadcaster)` / `SetHub(...)`: Binds the Hub broadcast interface after construction, resolving circular setup.
- `Start(ctx context.Context)`: Launches the 3 background goroutines (`runDepthPoller`, `runTickerPoller`, `runKafkaTradeStreamer`).
- `GetImmediateOrderBook(marketID string) (*OrderBookDepthPayload, error)`: Fetches current depth snapshot directly from Redis upon subscription. Validates `raw.Sequence > 0`.
- `GetImmediateTicker(marketID string) (*TickerPayload, error)`: Fetches 24h ticker snapshot from Redis upon subscription.
- `KafkaErrorsTotal()` / `KafkaReconnectsTotal()`: Atomic observability counters.

### `redis_poller.go`
- `runDepthPoller(ctx)`: Ticks every 250ms, querying active markets.
- `pollActiveMarketDepth(ctx)`:
  1. Calls `broadcaster.GetActiveMarketIDs()` (zero subscribers = zero Redis queries).
  2. Fetches `depth:{market}` from Redis with 500ms timeout.
  3. On Redis failure, triggers `transitionDepthUnavailable` and emits 1 error frame.
  4. On Redis recovery, clears state and forces an immediate fresh broadcast.
  5. Computes `xxhash.Sum64String(val)`. If hash matches previous tick, broadcast is skipped.
  6. Broadcasts `OrderBookDepthPayload` with Matching Engine `raw.Sequence`.
- `runTickerPoller(ctx)` / `pollActiveMarketTickers(ctx)`: Ticks every 1000ms for 24h ticker updates.
- `transitionDepthUnavailable(marketID) bool`: Transitions market to `UNAVAILABLE`. Returns `true` only on the first failure.
- `clearDepthUnavailable(marketID) bool`: Resets market to `AVAILABLE` upon successful Redis read.

### `kafka_consumer.go`
- `runKafkaTradeStreamer(ctx)`: Consumes from Kafka with exponential backoff.
  - Reader configured with `StartOffset: kafka.LastOffset` to avoid historical replay for new Gateway replicas.
  - Automatically resets `attempt = 0` if connection stays healthy for $\ge 1$ minute.
- `consumeKafkaTrades(ctx, reader)`:
  1. `reader.FetchMessage(ctx)` retrieves event.
  2. Deserializes into `RawTradeEvent`.
  3. Calls `ValidateTradeEvent` to enforce non-empty IDs, `Sequence > 0`, and positive numeric prices/quantities.
  4. Broadcasts public `TradePayload` (omitting `BuyerUserID`/`SellerUserID`).
  5. `reader.CommitMessages(ctx, msg)` commits offset after broadcast (at-least-once delivery).
- `ValidateTradeEvent(e RawTradeEvent) error`: Validates non-empty fields and positive numbers (`price > 0`, `quantity > 0`, `sequence > 0`).
- `KafkaBackoff(attempt int) time.Duration`: Computes $\min(2^{\text{attempt}}\text{s}, 30\text{s}) \pm 25\%$ jitter.

---

## 5. Architectural Safeguards

### 1. On-Demand Polling
```
Active Subscribers (BTC) = 0 ──► Zero Redis GET calls (0 Hz)
Active Subscribers (BTC) = 5 ──► Polling active (4 Hz)
```

### 2. Failure State Machine
```
AVAILABLE ──► (Redis Error) ──► UNAVAILABLE (Broadcast 1 Error Frame)
                                      │
                                      ▼
                               (Still Down) ──► No spam (Silent)
                                      │
                                      ▼
AVAILABLE ◄── (Redis OK) ◄───── (Recovery) (Immediate Fresh Snapshot)
```

### 3. Horizontal Scaling Fanout
Every Gateway instance is assigned an instance-unique consumer group (`gateway-websocket-{GATEWAY_INSTANCE_ID}`) so Kafka broadcasts 100% of all executed trades to every Gateway replica for local client fanout.
