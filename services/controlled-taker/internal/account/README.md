# Account Package (`internal/account`)

The `account` package provides canonical identity definitions and constants for the **Controlled Taker Service (CTS)** system trading account (`CT-001`).

---

## 1. Purpose

CTS acts as an autonomous market taker operating on the TradeDrift platform. To interact with external platform services (specifically **Order Service** and **Wallet Service**), CTS requires a deterministic system identity that is recognized, authorized, and pre-seeded with liquidity.

This package centralizes that identity in a single location to prevent mismatched IDs or hardcoded string literals across the codebase.

---

## 2. CT-001 Identity Definitions

| Constant / Variable | Value | Type | Purpose |
| :--- | :--- | :--- | :--- |
| `TakerServiceID` | `"CT-001"` | `string` | Human-readable label used in logging, monitoring, and administrative diagnostics. |
| `WalletUUIDStr` | `"00000000-0000-0000-0000-000000000002"` | `string` | Canonical UUID string recognized platform-wide as the CT-001 system account. |
| `WalletUUID` | `uuid.MustParse(WalletUUIDStr)` | `uuid.UUID` | Pre-parsed `uuid.UUID` structure for type-safe identity passing. |

---

## 3. Platform Identity Mapping & Relationships

The `WalletUUIDStr` serves as the authoritative foreign key across TradeDrift services:

```
                  ┌────────────────────────────────────────┐
                  │    account.WalletUUIDStr (CT-001)      │
                  │  00000000-0000-0000-0000-000000000002  │
                  └───────────────────┬────────────────────┘
                                      │
           ┌──────────────────────────┴──────────────────────────┐
           ▼                                                     ▼
┌──────────────────────────────┐              ┌──────────────────────────────┐
│        Wallet Service        │              │        Order Service         │
│                              │              │                              │
│ wallets.user_id =            │              │ orders.user_id =             │
│   account.WalletUUIDStr      │              │   account.WalletUUIDStr      │
│ (Holds balance for trades)   │              │ (Authorizes order placement) │
└──────────────────────────────┘              └──────────────────────────────┘
```

1. **Order Service:**
   - Sent as `user_id` in `orderv1.CreateOrderRequest` via `orderservice.Client.CreateCrossingOrder`.
   - Sent as `user_id` in `orderv1.GetOrderRequest` and `orderv1.CancelOrderRequest`.
   - Sent as `user_id` in `orderv1.ListOrdersRequest` to query recent orders placed by CT-001 during idempotency recovery.
2. **Wallet Service:**
   - Identifies the wallet holding balance reserves for CT-001 in base and quote assets across all supported markets (`BTC`, `ETH`, `SOL`, `USDT`).
   - Order Service locks and deducts funds from this wallet when CTS submits crossing orders.

---

## 4. Important Identity Invariants

* **Strict Singularity:** CTS operates strictly on behalf of the `CT-001` system taker identity. It never trades on behalf of external retail users.
* **Never Modify the UUID:** The UUID `"00000000-0000-0000-0000-000000000002"` is an established platform seed constant in TradeDrift. Altering it will cause Order Service pre-trade balance checks to fail (`codes.FailedPrecondition`).
* **Zero Secrets:** No credentials, private keys, or passwords reside in this package; identity verification in the TradeDrift internal microservice mesh relies on internal service-to-service gRPC networking with UUID authorization.
