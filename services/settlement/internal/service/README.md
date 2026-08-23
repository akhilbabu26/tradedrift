# Settlement Service — Service Layer (`internal/service`)

> **Package:** `tradedrift/services/settlement/internal/service`  
> **File:** `service.go`  
> **Design Patterns:** 3-Phase Commit, Idempotent Consumer, Short-Lived Transactions, Interface-Driven Dependencies

---

## 1. Purpose

The `service` package is the **core business logic** of the Settlement Service. It has no knowledge of Kafka, `pgx`, or gRPC internals — it depends only on interfaces (`Repository`, `WalletSettler`) and is fully unit-testable with lightweight mocks.

It owns two public responsibilities:
1. `Settle()` — the 3-phase settlement pipeline for a single trade event
2. `RecoverStalePending()` — the 60-second background recovery loop

---

## 2. Files

```
services/settlement/internal/service/
├── service.go       ← Service struct + all business logic
├── service_test.go  ← 16 unit tests (no DB, no gRPC required)
└── README.md        ← This file
```

---

## 3. Type: `TradeExecutedEvent`

```go
type TradeExecutedEvent struct {
    TradeID      string `json:"trade_id"`
    MarketID     string `json:"market_id"`
    MakerOrderID string `json:"maker_order_id"`
    TakerOrderID string `json:"taker_order_id"`
    BuyOrderID   string `json:"buy_order_id"`
    SellOrderID  string `json:"sell_order_id"`
    BuyerUserID  string `json:"buyer_user_id"`
    SellerUserID string `json:"seller_user_id"`
    Price        string `json:"price"`
    Quantity     string `json:"quantity"`
    ExecutedAt   string `json:"executed_at"` // RFC3339Nano
}
```

**Why all fields are `string`?**  
This struct is the direct deserialization target for the Kafka JSON payload published by the Matching Engine. All UUIDs and decimals are carried as strings to avoid precision loss during JSON encoding. The service validates and parses each field explicitly.

---

## 4. Interface: `WalletSettler`

```go
type WalletSettler interface {
    SettleTrade(ctx context.Context, req client.SettleRequest) error
}
```

**Purpose:** Abstracts the Wallet gRPC call so the service can be tested without dialing a real gRPC server.  
**Why defined here (not in `client/`)?**  
Following Go's convention: *"accept interfaces, return structs"* — the consumer of an interface defines it, not the producer. `*client.WalletClient` satisfies this interface automatically because Go uses structural typing.

---

## 5. Struct: `Service`

```go
type Service struct {
    repo        repository.Repository
    wallet      WalletSettler
    log         *zap.Logger
    grpcTimeout time.Duration
}
```

| Field | Type | Role |
|---|---|---|
| `repo` | `repository.Repository` | All DB reads/writes — interface for testability |
| `wallet` | `WalletSettler` | Outbound gRPC — interface for testability |
| `log` | `*zap.Logger` | Structured logging at each phase transition |
| `grpcTimeout` | `time.Duration` | Per-RPC deadline — prevents hung Wallet from stalling consumer |

---

## 6. Function: `NewService`

```go
func NewService(repo repository.Repository, wallet WalletSettler, log *zap.Logger, grpcTimeout time.Duration) *Service
```

**Purpose:** Dependency injection constructor. Called once in `main.go` after all infrastructure is wired.  
**Why `grpcTimeout` is injected not hardcoded?** A 5-second timeout may be correct for production but too slow for tests and too fast for a degraded Wallet under load. The value comes from `WALLET_GRPC_TIMEOUT` env var so it can be tuned without recompiling.

---

## 7. Function: `Settle`

```go
func (s *Service) Settle(ctx context.Context, event TradeExecutedEvent) error
```

**Purpose:** Entry point for each `TradeExecuted` Kafka message. Runs all three settlement phases in sequence.  
**Returns:** `nil` on success (Kafka offset will be committed). `error` on any failure (offset NOT committed, Kafka redelivers).

### Validation (before any DB or gRPC call)

All validation fires before any side effects:

| Check | Why |
|---|---|
| `uuid.Parse(event.TradeID)` | A malformed UUID would panic with `MustParse` and corrupt all future settlements |
| `uuid.Parse(event.BuyerUserID)` | Same — all 4 user/order IDs validated explicitly |
| `buyerID == sellerID` | Self-trade is a trading rule violation — reject before touching the ledger |
| `decimal.NewFromString(event.Price)` | A non-numeric string would pass validation silently and corrupt Wallet balances |
| `price.LessThanOrEqual(decimal.Zero)` | Zero or negative price is economically invalid |
| `decimal.NewFromString(event.Quantity)` | Same as price |
| `quantity.LessThanOrEqual(decimal.Zero)` | Same as price |
| `parseMarketID(event.MarketID)` | Extracts `BaseAsset`/`QuoteAsset` — must succeed before building `SettledTrade` |

### Phase 1 — Short DB Transaction

```go
repo.Insert(ctx, &repository.SettledTrade{..., Status: StatusPending})
// DB connection released immediately after INSERT commits
```

**Why run before gRPC?** Creates a durable record that Phase 1 completed. If the process crashes before Phase 3, the `PENDING` row proves exactly where to resume.  
**Skipped when:** `FindByTradeID` returns a `PENDING` row — crash recovery scenario, skip straight to Phase 2.

### Phase 2 — gRPC Call (No DB Connection Held)

```go
rpcCtx, cancel := context.WithTimeout(ctx, s.grpcTimeout)
defer cancel()
s.wallet.SettleTrade(rpcCtx, client.SettleRequest{...})
```

**Why no DB connection during gRPC?** Holding a DB connection across a network call is the most common cause of connection pool exhaustion. The Phase 1 DB connection is committed and returned to the pool before this call begins.  
**Why `context.WithTimeout`?** A hung Wallet Service (e.g. GC pause, network partition) would otherwise block the Kafka consumer indefinitely. The timeout causes a retryable error — the consumer moves on and Kafka redelivers.  
**Idempotency:** `Wallet.SettleTrade` is idempotent on `trade_id`. Duplicate calls (e.g. crash-redeliver, consumer+recovery race) are silently absorbed.

### Phase 3 — Short DB Transaction

```go
repo.MarkSettled(ctx, tradeID)
// DB connection released immediately after UPDATE commits
```

**Why after gRPC, not before?** This is the commit point. The trade is only marked `SETTLED` once we have confirmation from the Wallet Service that funds were moved.  
**On failure:** Returns an error. Kafka redelivers → Phase 2 is retried → Wallet absorbs duplicate → Phase 3 retried → success.

---

## 8. Function: `RecoverStalePending`

```go
func (s *Service) RecoverStalePending(ctx context.Context)
```

**Purpose:** Safety net for trades stuck in `PENDING` for more than 60 seconds. Called by the background recovery goroutine in `main.go` every 60 seconds.  
**When is a trade genuinely stuck?** The Kafka consumer may have crashed between Phase 2 and Phase 3, or the service may have restarted before committing the offset. In these cases, Kafka redelivers the event. This goroutine is a belt-and-suspenders check.  
**Why `created_at` not `executed_at` for the age threshold?** `executed_at` is when the Matching Engine matched the trade — a Kafka delivery delay would make a valid trade look stale before settlement even starts. `created_at` is when *this service* registered the trade.

**Per-trade timeout pattern:**
```go
for _, t := range trades {
    rpcCtx, cancel := context.WithTimeout(ctx, s.grpcTimeout)
    err := s.wallet.SettleTrade(rpcCtx, ...)
    cancel() // immediately after RPC — not deferred — prevents 50 contexts accumulating
```

**Why `cancel()` not `defer cancel()`?**  
`defer cancel()` would fire when `RecoverStalePending` returns — but this loop processes up to 50 trades. All 50 contexts would stay open until the end of the batch. Calling `cancel()` immediately after each RPC releases resources as soon as each call completes.

---

## 9. Function: `parseMarketID` (private)

```go
func parseMarketID(marketID string) (base, quote string, err error)
```

**Purpose:** Splits `"BTC-USDT"` into `("BTC", "USDT")`. Required to populate `BaseAsset` and `QuoteAsset` on the `SettledTrade` record and in the `SettleRequest` sent to Wallet.  
**Why private?** Only the service layer needs to parse market IDs — it is an implementation detail of how this service interprets the Kafka event.  
**Format contract:** `BASE-QUOTE` where neither part is empty. Returns an error for any other format — treated as a validation failure (poison pill path in the consumer).

---

## 10. External Packages

| Package | Why Used |
|---|---|
| `github.com/google/uuid` | Parse and validate all UUID fields from the Kafka event — `uuid.Parse` returns error instead of panicking |
| `github.com/shopspring/decimal` | Parse `Price` and `Quantity` strings as exact decimals; validate they are positive; avoids IEEE 754 float imprecision |
| `go.uber.org/zap` | Structured logging at each phase transition with trade_id, market, price, quantity for traceability |
| `tradedrift/services/settlement/internal/repository` | `Repository` interface + `SettledTrade` entity |
| `tradedrift/services/settlement/internal/client` | `SettleRequest` struct passed to `WalletSettler.SettleTrade` |
