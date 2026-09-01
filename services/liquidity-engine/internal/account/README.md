# package `account`

## Purpose

Defines the **stable, canonical identity constants** for the `MM-001` market-maker system account — the single actor that the Liquidity Engine acts on behalf of when placing and cancelling orders.

## Problem It Solves

Multiple services need to reference the same market-maker account, but they use different identifier types:

- **Wallet Service** identifies users by a `UUID` column (`wallets.user_id`).
- **Order Service gRPC** accepts `userId` as a UUID string.
- **Matching Engine Kafka commands** embed `user_id` as a UUID in command payloads.

Without a single source of truth, each service could independently hardcode the UUID differently, causing silent identity mismatches that break fund lookups, order ownership checks, and Kafka routing.

## How It Solves It

The `account` package provides one file, `identity.go`, with:

- A **pre-parsed `uuid.UUID`** value (`WalletUUID`) ready for direct use in any UUID-typed field.
- A **raw string constant** (`WalletUUIDStr`) for services that accept a string parameter.
- A **human-readable label** (`OrderServiceID = "MM-001"`) for logs and display only — never used as a DB or Kafka field.

All services import from this package. Changing the MM account UUID requires a change in exactly one place.

---

## Files

### [`identity.go`](./identity.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `OrderServiceID` | `const string` | Human-readable label `"MM-001"` for logs/display only. **Not** a DB key or Kafka field. |
| `WalletUUIDStr` | `const string` | The canonical UUID string `"00000000-0000-0000-0000-000000000001"`. Used wherever a string parameter is required. |
| `WalletUUID` | `var uuid.UUID` | Pre-parsed form of `WalletUUIDStr`. Use wherever a `uuid.UUID` value is required (avoids repeated `uuid.MustParse` calls). |

---

## Seed Requirement

The `MM-001` account must be seeded in the Wallet Service database before the LE can operate.

- **Migration**: `00003_seed_mm001_wallet.sql` — creates the wallet row with UUID `00000000-0000-0000-0000-000000000001` and initial balances (100 BTC, 500 ETH, 5000 SOL, $10M USDT).
- **Validation**: `inventory.ValidateMMAccount()` confirms all required assets are present at engine startup.

---

## Usage Pattern

```go
// In a gRPC call that expects a UUID string:
resp, err := client.ListOrders(ctx, &pb.ListOrdersRequest{
    UserId: account.WalletUUIDStr,
})

// In a Kafka command payload that expects uuid.UUID:
cmd := &proto.OrderCreated{
    UserId: account.WalletUUID.String(),
}

// In logs (human-readable only):
logger.Info("processing MM account", zap.String("account", account.OrderServiceID))
```
