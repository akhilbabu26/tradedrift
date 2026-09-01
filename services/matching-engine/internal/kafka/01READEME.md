# `internal/kafka` — Kafka Ingestion, Validation & Command Dispatch

**Package:** `kafka`  
**Service:** Matching Engine  
**File Covered:** `consumer.go` (and `consumer_test.go`)  
**Documentation:** `02READEME.md`  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Purpose

The `internal/kafka` package contains the **live ingestion, validation, serialization translation, and dispatch layer** of the Matching Engine.

It serves as the front gateway connecting the external asynchronous messaging infrastructure (Kafka) to the ultra-fast, in-memory concurrency model of the matching engine. Specifically, it:
1. **Subscribes** to the `orders.commands` Kafka topic across consumer group partitions.
2. **Synchronizes Offsets** on startup against PostgreSQL (`kafka_checkpoints`) before consuming live events, preventing message skipping or redundant reprocessing.
3. **Registers In-Flight Offsets** with the Checkpoint Coordinator (`internal/checkpoint`) to maintain contiguous checkpoint watermarks.
4. **Validates & Sanitizes** event envelopes, partition keys, schema versions, UUIDs, and decimal price/quantity values.
5. **Translates & Dispatches** validated commands into internal domain types (`market.InputEvent`) directly into each market's dedicated FIFO input channel (`MarketEngine.InputQueue`).
6. **Enforces Fail-Closed Safety**: Immediately triggers graceful engine shutdown upon encountering corrupted or unrecoverable command payloads to prevent state divergence.

---

## 2. Core Problems Solved & Why This File Is Needed

### 2.1 Bridging External Asynchronous I/O with In-Memory Concurrency
Upstream services (like the Order Service Outbox publisher) communicate using JSON payloads over Kafka. The Matching Engine’s core execution loop requires strictly typed Go structs, channel-based event queues, and deterministic decimal representations (`shopspring/decimal`). `consumer.go` encapsulates the entire serialization and translation boundary.

### 2.2 Partition Key & Market Affinity Guarantee
Kafka guarantees message ordering **only within a single partition**. To prevent race conditions between orders for the same market, all order commands for a market **must** share the same partition key (`msg.Key == market_id`).
`consumer.go` enforces this invariant:
```go
if string(msg.Key) != env.MarketID {
    return false, fmt.Errorf("partition key mismatch: key=%q envelope.market_id=%q", string(msg.Key), env.MarketID)
}
```
Any command violating this rule is rejected before reaching an engine.

### 2.3 Startup Group Offset Realignment (Issue #1)
When the Matching Engine restarts after a crash or redeployment, the Kafka broker's consumer group committed offset may lag behind or desynchronize with the engine's recovery baseline. `seekToPostgresCheckpoints()` dynamically inspects all topic partitions and queries PostgreSQL `kafka_checkpoints` to commit the exact database baseline to the Kafka broker *before* live consumption begins. Live intake starts seamlessly from `checkpoint + 1`.

### 2.4 Fail-Closed Security & Integrity Policy (Issue #10)
In financial matching engines, silently dropping corrupted commands or endlessly looping on malformed payloads is catastrophic. If `HandleOrderCommand` encounters an invalid message (unsupported schema version, malformed UUID, unparseable decimal), `consume()` logs a `FATAL` error and calls `cancelCtx()`. This initiates a clean, fail-closed shutdown, alerting operators while preventing order book corruption.

### 2.5 Financial Precision & Type Safety
Standard IEEE 754 floating-point numbers cannot represent exact financial decimals (e.g. `0.1 + 0.2 != 0.3`). `consumer.go` validates and converts all price and quantity strings into `decimal.Decimal` fixed-point representations before routing.

---

## 3. End-to-End Ingestion Flow

```
                           KAFKA BROKER (`orders.commands`)
                                          │
                                          ▼
                               FetchMessage(ctx)
                                          │
                                          ▼
                              ┌───────────────────────┐
                              │  offsetTracker.Track  │ ──► Coordinator tracks in-flight offset
                              └───────────┬───────────┘
                                          │
                                          ▼
                              ┌───────────────────────┐
                              │   HandleOrderCommand  │
                              └───────────┬───────────┘
                                          │
                  ┌───────────────────────┴───────────────────────┐
                  ▼                                               ▼
         [Validation Fails]                              [Validation Succeeds]
         - Bad JSON                                      - Key == MarketID
         - Key != MarketID                               - Valid UUIDs (Event, Order, User)
         - Bad UUID/Decimal                              - Supported EventVersion (1)
         - Unknown Market                                - Valid Side (BUY/SELL)
                  │                                      - Valid OrderType (LIMIT/MARKET)
                  ▼                                               │
          Fail-Closed Trigger                                     ▼
        log.Printf("[kafka] FATAL")                      Convert to market.InputEvent
        cancelCtx() (Graceful Shutdown)                           │
                                                                  ▼
                                                      Route to MarketEngine InputQueue
                                                      queue <- market.InputEvent{...}
```

---

## 4. External Packages & Dependencies

| External Package | Why It Is Used |
| :--- | :--- |
| `context` | Manages request lifecycles, cancellation propagation during graceful shutdown, and deadline timeouts (e.g., 5-second broker commit timeouts). |
| `encoding/json` | Unmarshals Kafka JSON payloads into `CommandEnvelope` and polymorphic inner payload structs (`orderCreatedPayload`, `orderCancelPayload`) using `json.RawMessage`. |
| `fmt` | Generates structured error strings with `%w` wrapping and formats partition/topic identifiers. |
| `log` | Standard logging for startup alignment events, partition discovery errors, fetch retries, and fatal fail-closed shutdown notices. |
| `time` | Controls polling deadlines (`MaxWait: 1s`), retry backoff intervals (500ms on fetch error), commit timeouts (5s), and parses event timestamps (`time.Time`). |
| `github.com/google/uuid` | Validates and parses 128-bit UUID strings (`EventID`, `OrderID`, `UserID`). Enforces RFC 4122 compliance before messages enter the engine. |
| `github.com/jackc/pgx/v5` | High-performance PostgreSQL driver interface (`pgx.Row`), used by `dbQueryer` to retrieve partition checkpoints from `kafka_checkpoints`. |
| `github.com/segmentio/kafka-go` | High-throughput, pure Go Kafka client. Provides `Reader`, `ReaderConfig`, partition discovery (`conn.ReadPartitions`), and consumer group offset management (`CommitMessages`). |
| `github.com/shopspring/decimal` | Exact arbitrary-precision decimal arithmetic. Converts stringified prices and quantities into lossless decimal numbers for matching calculations. |
| `tradedrift/.../market` | Internal domain package supplying `MarketManager`, `InputEvent`, `OrderCreatedPayload`, and `OrderCancelPayload`. |
| `tradedrift/.../orderbook` | Internal domain package supplying `KafkaPosition`, `SideType` (`SideBuy`, `SideSell`), and `OrderType` (`OrderTypeLimit`, `OrderTypeMarket`). |

---

## 5. Detailed Component & Function Breakdown

### 5.1 Constants

```go
const TopicOrderCommands = "orders.commands"
```
- Defines the single inbound Kafka topic from which all order commands (`OrderCreated`, `OrderCancelRequested`) are consumed.

---

### 5.2 Interfaces

#### `dbQueryer`
```go
type dbQueryer interface {
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
```
- **Purpose**: Decouples database querying from concrete PostgreSQL connection pools.
- **Why Needed**: Allows passing `*pgxpool.Pool` in production while enabling mock database implementations (`mockConsumerDB`) in unit tests.

#### `offsetTracker`
```go
type offsetTracker interface {
    Track(pos orderbook.KafkaPosition)
    MarkDone(ctx context.Context, pos orderbook.KafkaPosition) error
}
```
- **Purpose**: Interacts with the Checkpoint Coordinator (`internal/checkpoint`).
- **Why Needed**: Informs the coordinator of new in-flight offsets as soon as they are fetched from Kafka.

#### `kafkaCommitter`
```go
type kafkaCommitter interface {
    CommitMessages(ctx context.Context, msgs ...kafkago.Message) error
}
```
- **Purpose**: Abstracts committing consumer group offsets to Kafka brokers.

---

### 5.3 Structs & Data Envelopes

#### `CommandEnvelope`
```go
type CommandEnvelope struct {
    EventID      string          `json:"event_id"`
    EventType    string          `json:"event_type"`
    EventVersion int             `json:"event_version"`
    MarketID     string          `json:"market_id"`
    OccurredAt   time.Time       `json:"occurred_at"`
    Payload      json.RawMessage `json:"payload"`
}
```
- Standardized outer envelope published by the Order Service transactional outbox.
- `Payload` is deferred via `json.RawMessage` for polymorphic decoding based on `EventType`.

#### `orderCreatedPayload` & `orderCancelPayload`
```go
type orderCreatedPayload struct {
    OrderID   string `json:"order_id"`
    UserID    string `json:"user_id"`
    Side      string `json:"side"`
    OrderType string `json:"order_type"`
    Price     string `json:"price"`
    Quantity  string `json:"quantity"`
}

type orderCancelPayload struct {
    OrderID string `json:"order_id"`
    UserID  string `json:"user_id"`
}
```
- JSON mapping structs representing individual command payload schemas.

#### `Config`
```go
type Config struct {
    Brokers []string
    GroupID string
    DB      dbQueryer
}
```
- Holds bootstrap broker addresses, the Kafka consumer group ID (e.g., `"matching-engine-group"`), and the database interface.

#### `Consumer`
```go
type Consumer struct {
    commandReader          *kafkago.Reader
    manager                *market.MarketManager
    tracker                offsetTracker
    cancelCtx              context.CancelFunc
    brokers                []string
    groupID                string
    db                     dbQueryer
    discoverPartitionsFunc func(topic string) ([]int, error)
    commitMessagesFunc     func(ctx context.Context, brokers []string, topic string, groupID string, partition int, offset int64) error
}
```
- Main consumer coordinator managing the Kafka reader, routing targets, lifecycle cancellation hooks, and mockable function pointers for partition discovery and broker commits.

---

### 5.4 Functions & Methods

#### `NewConsumer(cfg Config, manager *market.MarketManager, tracker offsetTracker) *Consumer`
- **Purpose**: Constructs and configures the `Consumer`.
- **Details**:
  - Initializes `kafkago.Reader` with `TopicOrderCommands`, configured brokers, consumer group, `MinBytes: 1`, `MaxBytes: 10MB`, and `CommitInterval: 0` (manual offset commits).
  - Instantiates default `discoverPartitionsFunc` using `kafkago.Dial` and `conn.ReadPartitions`.
  - Instantiates default `commitMessagesFunc` creating a short-lived temporary reader with a 5-second timeout to commit offsets.
  - Automatically registers `c.commandReader` with the `tracker` if the tracker implements `RegisterCommitter`.

#### `Start(ctx context.Context, cancel context.CancelFunc)`
- **Purpose**: Begins live Kafka intake.
- **Workflow**:
  1. Stores `cancel` as `c.cancelCtx` for fail-closed handling.
  2. Executes `seekToPostgresCheckpoints(ctx)`. If positioning fails, logs a fatal error, cancels context, and aborts.
  3. Launches background goroutine executing `c.consume(...)`.

#### `seekToPostgresCheckpoints(ctx context.Context) error`
- **Purpose**: Realigns the Kafka consumer group on the broker to the authoritative PostgreSQL checkpoints.
- **Step-by-step Execution**:
  1. Dynamically discovers all partition IDs on topic `orders.commands`.
  2. For each partition, queries `SELECT "offset" FROM kafka_checkpoints WHERE topic = $1 AND partition = $2`.
  3. If a checkpoint exists, commits `savedOffset` to the broker via `commitMessagesFunc`.
  4. Subsequent `Reader.FetchMessage` calls start consuming strictly at `savedOffset + 1`.

#### `Close() error`
- **Purpose**: Gracefully shuts down and releases the underlying Kafka connection reader.

#### `OverrideDiscoveryAndCommit(...)`
- **Purpose**: Allows unit tests to override dynamic partition discovery and broker offset commit functions without network dependencies.

#### `consume(ctx context.Context, reader *kafkago.Reader, handler func(msg kafkago.Message) (bool, error))`
- **Purpose**: Primary message fetch and processing loop.
- **Workflow**:
  1. Calls `reader.FetchMessage(ctx)` in an infinite loop.
  2. Checks for context cancellation to exit cleanly.
  3. Backs off 500ms on transient network errors.
  4. Informs `tracker.Track(pos)` of the newly received offset.
  5. Passes the message to `handler` (`handleOrderCommand`).
  6. **Fail-Closed Check**: If `handler` returns an error, logs a fatal error and invokes `c.cancelCtx()` to shut down the engine immediately.

#### `handleOrderCommand(msg kafkago.Message) (bool, error)`
- **Purpose**: Resolves the target `MarketEngine` from `MarketManager` by `marketID` and passes its `InputQueue` to `HandleOrderCommand`.

#### `HandleOrderCommand(msg kafkago.Message, route routeFunc) (bool, error)`
- **Purpose**: **Core parsing, validation, and dispatch engine.**
- **Validation Pipeline**:
  1. Unmarshals JSON bytes into `CommandEnvelope`.
  2. **Partition Key Validation**: Checks `len(msg.Key) > 0` and `string(msg.Key) == env.MarketID`.
  3. **Event ID Validation**: Parses `env.EventID` via `uuid.Parse`.
  4. **Schema Version Check**: Asserts `env.EventVersion == 1`.
  5. **Routing Lookup**: Calls `route(env.MarketID)`. Rejects command if market queue is nil.
  6. **Payload Branching**:
     - **`OrderCreated`**: Parses `OrderID` (UUID), `UserID` (UUID), `Price` (decimal), `Quantity` (decimal), `Side` (`BUY`/`SELL`), and `OrderType` (`LIMIT`/`MARKET`). Dispatches `market.InputEvent` of type `EventOrderCreated`.
     - **`OrderCancelRequested`**: Parses `OrderID` (UUID) and `UserID` (UUID). Dispatches `market.InputEvent` of type `EventOrderCancel`.
     - **Unknown Event Type**: Returns error.

#### Helper Parsing Functions
- `parseSide(s string) (orderbook.SideType, error)`: Maps `"BUY"` -> `SideBuy`, `"SELL"` -> `SideSell`.
- `parseOrderType(s string) (orderbook.OrderType, error)`: Maps `"LIMIT"` -> `OrderTypeLimit`, `"MARKET"` -> `OrderTypeMarket`.

#### Test Helpers
- `TestableConsumer` & `NewTestableConsumer(route routeFunc)`: Helper struct exposing `HandleOrderCommand` for direct unit testing of routing and envelope validation.

---

## 6. Unit Test Scenarios & Invariants (`consumer_test.go`)

| Test Name | Validation / Invariant Tested |
| :--- | :--- |
| `TestHandleOrderCommand_OrderCreated_Valid` | Verifies successful parsing of `OrderCreated` payload, side conversion (`BUY`), decimal price/quantity parsing, and channel delivery. |
| `TestHandleOrderCommand_OrderCancel_Valid` | Verifies successful parsing of `OrderCancelRequested` payload, target order UUID parsing, and channel delivery. |
| `TestHandleOrderCommand_PartitionKeyMismatch` | Asserts that messages where `msg.Key != envelope.market_id` are rejected with an error. |
| `TestHandleOrderCommand_InvalidEventID` | Asserts that non-UUID event IDs fail validation. |
| `TestHandleOrderCommand_UnsupportedVersion` | Asserts that schema versions other than `1` return errors. |
| `TestHandleOrderCommand_UnknownMarket_Fails` | Asserts that commands targeted to unregistered markets return an error. |
| `TestHandleOrderCommand_MalformedJSON` | Asserts that corrupted JSON input returns an unmarshal error. |
| `TestConsumer_SeekToPostgresCheckpoints` | Verifies that startup partition discovery reads PostgreSQL checkpoints and commits matching broker offsets. |
| `TestConsumer_SeekToPostgresCheckpoints_Failure` | Asserts that if broker offset positioning fails during startup, the consumer triggers fail-stop cancellation immediately. |

---

## 7. What This Package Does NOT Do

To maintain a clean separation of concerns:
- **Does NOT execute orders or maintain order books**: Handled by `../market/` and `../matcher/`.
- **Does NOT publish trades or events to Kafka**: Handled by `../publisher/`.
- **Does NOT write checkpoints or snapshots to PostgreSQL**: Handled by `../checkpoint/`.
- **Does NOT project depth to Redis**: Handled by `../projection/`.
- **Does NOT validate user wallet balances**: Handled upstream by the Order & Wallet Services.
- **Does NOT perform startup log replay**: Startup recovery uses a dedicated partition reader in `../recovery/`.
