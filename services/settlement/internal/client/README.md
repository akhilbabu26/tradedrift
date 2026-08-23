# Settlement Service — Wallet gRPC Client (`internal/client`)

> **Package:** `tradedrift/services/settlement/internal/client`  
> **File:** `wallet_client.go`  
> **Protocol:** gRPC (insecure credentials, internal service mesh)  
> **Target RPC:** `WalletService.SettleTrade`

---

## 1. Purpose

The `client` package wraps the auto-generated `walletv1.WalletServiceClient` with a thin, Settlement-specific layer. It:
- Hides proto-generated types from the service layer
- Translates `SettleRequest` fields into the proto struct
- Logs errors with enough context to trace a failed settlement

The service layer depends on the `WalletSettler` interface (defined in `service/`), so it never imports this package directly — only `main.go` and tests do.

---

## 2. Files

```
services/settlement/internal/client/
├── wallet_client.go   ← Wallet gRPC connection + SettleTrade wrapper
└── README.md          ← This file
```

---

## 3. Type: `SettleRequest`

```go
type SettleRequest struct {
    TradeID     string
    BuyerID     string
    SellerID    string
    BuyOrderID  string
    SellOrderID string
    BaseAsset   string
    QuoteAsset  string
    Price       string
    Quantity    string
    MarketID    string
}
```

**Purpose:** Settlement-domain request struct. Maps directly to the `walletv1.SettleTradeRequest` proto message.  
**Why not use the proto struct directly in the service?**  
Importing a generated proto type in `service/` would couple the business logic to the protobuf schema. If the proto field names change (e.g. `trade_id` → `id`), the change is contained in this file only — `service.go` is unaffected.  
**Why `string` for all UUIDs?** The proto definition uses `string` fields for IDs. Keeping `SettleRequest` consistent avoids a `.String()` call at the mapping site.  
**`SellOrderID` specifically:** The Wallet Service uses this to look up the seller's reservation. It must match exactly what the Order Service recorded when the sell order was placed.

---

## 4. Struct: `WalletClient`

```go
type WalletClient struct {
    conn   *grpc.ClientConn
    client walletv1.WalletServiceClient
}
```

**Purpose:** Holds the gRPC connection and the generated service client stub.  
**Why store both `conn` and `client`?** The `client` is a thin wrapper around `conn`. Both are stored so `Close()` can properly shut down the underlying TCP connection — calling only `client`-level cleanup is insufficient.

---

## 5. Function: `NewWalletClient`

```go
func NewWalletClient(addr string) (*WalletClient, error)
```

**Purpose:** Dials the Wallet Service and returns a ready client. Called once at startup in `main.go`.  
**Why `grpc.NewClient` not `grpc.Dial`?** `grpc.Dial` is deprecated — `grpc.NewClient` is the recommended constructor in `google.golang.org/grpc v1.63+`.  
**Why `insecure.NewCredentials()`?** Services communicate inside a private Docker network (`tradedrift_net`). mTLS is the right production hardening step but is not required for the current internal mesh.  
**Why not retry in `NewWalletClient`?** `grpc.NewClient` returns immediately — it does not block on the TCP handshake. The actual connection is established lazily on the first RPC call. Startup does not fail if Wallet is temporarily unavailable.

---

## 6. Function: `SettleTrade`

```go
func (c *WalletClient) SettleTrade(ctx context.Context, req SettleRequest) error
```

**Purpose:** Calls `WalletService.SettleTrade` and returns a typed error on failure.  
**Idempotency contract:** The Wallet Service checks `wallet_transactions` for an existing row with `(reference_id=trade_id, asset=base_asset)` before processing. If found, it returns success immediately without moving any funds. This means:
- Crash after Phase 2 → restart → Phase 2 retried → Wallet absorbs duplicate → no double settlement
- Recovery goroutine races consumer → both call SettleTrade with same `trade_id` → Wallet absorbs one → no double settlement

**What happens on error?** The error is returned to `service.Settle()`, which returns it to the Kafka consumer. The consumer does not commit the offset — Kafka redelivers the event on the next poll.  
**Why `fmt.Errorf("wallet SettleTrade gRPC: %w", err)`?** Wraps the gRPC status error so callers can still use `errors.Is` / `errors.As` on the original status code, while also adding context to log output.

---

## 7. Function: `Close`

```go
func (c *WalletClient) Close() error
```

**Purpose:** Closes the underlying gRPC TCP connection, releasing OS-level resources.  
**When called:** From `main.go` via `defer walletClient.Close()` — always called during shutdown, even on fatal errors.  
**Why important?** Without `Close`, the gRPC connection's keepalive goroutine and the OS TCP socket remain open until the process exits. In a container environment this can delay graceful shutdown.

---

## 8. External Packages

| Package | Why Used |
|---|---|
| `google.golang.org/grpc` | gRPC client dialing, transport, and connection lifecycle |
| `google.golang.org/grpc/credentials/insecure` | No-op TLS for internal service mesh communication |
| `tradedrift/platform/api/gen/wallet/v1` | Auto-generated `WalletServiceClient` and `SettleTradeRequest` from protobuf definition |
