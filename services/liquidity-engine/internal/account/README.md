# `internal/account` — MM-001 Identity Constants

**Package:** `account`  
**Service:** Liquidity Engine  
**Last Updated:** August 2026

---

## 1. What This Package Does

This package defines the **identity constants for the MM-001 system account**. The Market Maker uses two different identity representations depending on which service it is talking to, and this package is the single source of truth for both.

---

## 2. Files In This Package

| File | Purpose |
| :--- | :--- |
| `identity.go` | `OrderServiceID`, `WalletUUIDStr`, `WalletUUID` constants |
| `README.md` | This documentation file |

---

## 3. Why Two Identities?

The Order Service and Wallet Service use different column types for `user_id`:

| Service | Column Type | MM-001 Value |
| :--- | :--- | :--- |
| **Order Service** | `VARCHAR` (string) | `"MM-001"` |
| **Wallet Service** | `UUID` | `"00000000-0000-0000-0000-000000000001"` |

The LE must use the correct identity when querying each service. Mixing them up would result in:
- Order Service queries returning no orders (wrong `user_id` in `ListOrders` filter)
- Wallet Service queries failing with a UUID parse error

---

## 4. Constants

```go
const (
    // OrderServiceID is the string user_id used in the Order Service.
    // Used in: ListOrders, GetOrderByClientID, and as user_id in OrderCreated payloads.
    OrderServiceID = "MM-001"

    // WalletUUIDStr is the deterministic UUID used in the Wallet Service.
    // Wallet Service wallets.user_id is UUID type.
    WalletUUIDStr = "00000000-0000-0000-0000-000000000001"
)

// WalletUUID is the parsed uuid.UUID form of the MM-001 wallet identity.
var WalletUUID = uuid.MustParse(WalletUUIDStr)
```

---

## 5. Usage Pattern

```go
// When publishing an OrderCreated command to Kafka (→ Order Service via ME):
payload := orderCreatedPayload{
    UserID: account.OrderServiceID,  // "MM-001"
    ...
}

// When querying the Wallet Service gRPC:
req := &walletv1.GetWalletRequest{
    UserId: account.WalletUUID.String(),  // "00000000-0000-0000-0000-000000000001"
}
```

---

## 6. Seed Requirement

The MM-001 account must be seeded in both databases before the LE can operate:

- **Wallet Service**: Migration `00003_seed_mm001_wallet.sql` — creates the wallet row with UUID `00000000-0000-0000-0000-000000000001` and initial asset balances (100 BTC, 500 ETH, 5000 SOL, $10M USDT).
- **Order Service**: No explicit seed required — the Order Service creates orders on first `OrderCreated` event processed by the ME.

The `inventory.ValidateMMAccount()` function confirms all required asset balances are present on startup.
