# Order Service — Wallet Adapter Package (`internal/wallet`)

> **Package:** `tradedrift/services/order/internal/wallet`  
> **Directory:** `services/order/internal/wallet/`  
> **Role:** Infrastructure gRPC Client Adapter for Inter-Service Calls to Wallet Service

---

## 1. Purpose & Architectural Role

The `wallet` package provides an **infrastructure adapter** wrapping the gRPC client generated for the Wallet Service (`walletv1.WalletServiceClient`). 

Key architectural principles:
1. **Decoupling**: Prevents the Order Service business layer (`service.go`) from constructing low-level gRPC request structs (`walletv1.ReserveFundsRequest`, `walletv1.ReleaseFundsRequest`) directly.
2. **Resource Management**: Retains the underlying `*grpc.ClientConn` transport connection inside the `Client` struct, exposing a `Close()` method so `main.go` can cleanly defer transport shutdown.
3. **Pure Adapter Pattern**: Holds **zero order business rules** (does not calculate prices, quantities, or asset pairs). It simply passes asset codes and decimal amounts to the Wallet Service and returns clean Go response tuples.

---

## 2. Files in This Directory

| File | Role |
| :--- | :--- |
| [`client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/wallet/client.go) | Defines `Client` struct, `NewClient`, `Close`, `ReserveFunds`, and `ReleaseFunds` methods |

---

## 3. Packages & Dependencies Used

| Package | Purpose & Rationale |
| :--- | :--- |
| `context` | Propagates request cancellation timeouts to inter-service gRPC calls. |
| `fmt` | Error wrapping (`fmt.Errorf("failed to create wallet client: %w", err)`). |
| `go.uber.org/zap` | Structured logging for transport initialization. |
| `google.golang.org/grpc` | High-performance gRPC client framework. |
| `google.golang.org/grpc/credentials/insecure` | Insecure transport credentials for local microservice networking. |
| `tradedrift/platform/api/gen/wallet/v1` | Compiled protobuf stubs (`walletv1`) generated from `proto/wallet/v1/wallet.proto`. |

---

## 4. Struct Breakdown

```go
type Client struct {
    conn   *grpc.ClientConn              // Retained transport connection
    grpc   walletv1.WalletServiceClient  // Generated gRPC service client interface
    logger *zap.Logger                   // Application logger
}
```

* **`conn`**: Stored connection handle, allowing graceful connection termination via `Close()`.
* **`grpc`**: Pre-wired gRPC stub generated from `wallet.proto`.

---

## 5. Method Analysis (`client.go`)

### 5.1 `NewClient(addr, logger)`
* **Signature:** `func NewClient(addr string, logger *zap.Logger) (*Client, error)`
* **Purpose:** Establishes a gRPC transport connection to the Wallet Service address (e.g. `"localhost:50052"`).
* **Return**: `(*Client, error)`. Returns error if target address dialing fails.

### 5.2 `Close()`
* **Signature:** `func (c *Client) Close() error`
* **Purpose:** Closes the underlying `c.conn.Close()`.
* **Usage**: Called in `main.go` via `defer walletClient.Close()`.

### 5.3 `ReserveFunds(ctx, userID, orderID, asset, amount)`
* **Signature:** `(c *Client) ReserveFunds(ctx context.Context, userID, orderID, asset, amount string) (reservationID string, alreadyExisted bool, err error)`
* **Purpose:** Invokes `WalletService.ReserveFunds` RPC over gRPC to lock available funds for an order placement.
* **Returns**:
  - `reservationID`: Unique UUID of the created balance reservation.
  - `alreadyExisted`: `true` if this reservation was already created previously (idempotent gRPC retry).
  - `err`: gRPC transport error or business status error (`codes.FailedPrecondition` when available balance is insufficient).

### 5.4 `ReleaseFunds(ctx, orderID)`
* **Signature:** `(c *Client) ReleaseFunds(ctx context.Context, orderID string) error`
* **Purpose:** Invokes `WalletService.ReleaseFunds` RPC over gRPC to unlock reserved funds when an order is cancelled or when executing a Saga compensating transaction post-database failure.
