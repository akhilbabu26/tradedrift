# Controlled Taker Service — Identity, Wallet & Idempotency

> **Status:** 📐 Designed (V1.1 — Updated with Architectural Feedback)  
> **Service:** Controlled Taker Service (`services/controlled-taker`)  
> **Document:** `03_Identity_And_Wallet.md`  
> **Last Updated:** September 2026  

---

## 1. Identity Design & Canonical Account

The platform maintains strict separation between Market Maker identities and Taker identities:

```
┌──────────────────────────────────────┐       ┌──────────────────────────────────────┐
│       Market Maker Identity          │       │      Controlled Taker Identity       │
│                                      │       │                                      │
│ Code Label:  MM-001                  │       │ Code Label:  CT-001                  │
│ UUID:        00000000-0000-0000-     │       │ UUID:        00000000-0000-0000-     │
│              0000-000000000001       │       │              0000-000000000002       │
│ Service:     Liquidity Engine (LE)   │       │ Service:     Controlled Taker (CTS)  │
│ Order Mode:  Maker (Resting Limits)  │       │ Order Mode:  Taker (Crossing Limits) │
│ Reservation: Bypasses reservations   │       │ Reservation: Standard Fund Lock      │
│ Outbox Pub:  Direct Kafka commands   │       │ Outbox Pub:  Via Order Svc Outbox    │
└──────────────────────────────────────┘       └──────────────────────────────────────┘
```

---

## 2. Why MM Account Cannot Act as Taker

Investigation of the TradeDrift codebase revealed a **critical system constraint** in `services/order/internal/service/service.go:200-237`:

```go
const mmAccountUUID = "00000000-0000-0000-0000-000000000001"
isMMAccount := (p.UserID == mmAccountUUID)

if !isMMAccount {
    // Normal user orders: generate outbox envelope for Kafka publishing
    ...
} else {
    // SYSTEM MM INVARIANT:
    // Order Service acts purely as the authoritative recovery database for MM orders.
    // The Liquidity Engine holds exclusive authority to publish OrderCreated commands
    // to Kafka. Enqueuing outbox events for MM orders would cause duplicate Kafka emissions.
    s.logger.Info("MM order registration: skipping outbox enqueue")
}
```

If CTS were to use the MM identity (`00000000-0000-0000-0000-000000000001`) to submit orders via Order Service, **Order Service would intentionally suppress outbox publishing to Kafka**. The orders would never reach the Matching Engine.

Therefore, CTS **must** have its own distinct identity:
- **Account Label:** `CT-001`
- **Canonical UUID:** `00000000-0000-0000-0000-000000000002`

---

## 3. Self-Trading Prevention Invariant

In financial exchanges and matching engines, self-trading (wash trading) occurs when the same entity appears as both buyer and seller.

In TradeDrift's Settlement Service (`services/wallet/internal/service/settle_trade.go:258-285`):
1. The settlement engine acquires pessimistic locks across four wallet IDs:
   ```go
   walletRepo.LockByIDs(ctx, []string{
       sellerBaseWallet.ID,
       buyerBaseWallet.ID,
       buyerQuoteWallet.ID,
       sellerQuoteWallet.ID,
   })
   ```
2. If `BuyerUserID == SellerUserID`, the slice contains duplicate IDs, causing lock acquisition anomalies or dual simultaneous debit/credit transfers on identical wallet rows.
3. By assigning `CT-001` (`...0002`) to CTS and `MM-001` (`...0001`) to the Liquidity Engine:
   $$\mathbf{BuyerUserID} \ne \mathbf{SellerUserID}$$
   is **strictly guaranteed by architectural separation**. Self-trading collisions are impossible by construction.

---

## 4. Wallet Seeding & Balance Funding

CTS requires legitimate wallet balances in `tradedrift_wallet` to support continuous fund reservations for BUY and SELL orders.

A dedicated database migration is added to `services/wallet/migration/`:
`00011_seed_ct001_wallet.sql`

```sql
-- Seed system Controlled Taker (CT-001) wallet balances
DO $$
DECLARE
    ct_uuid UUID := '00000000-0000-0000-0000-000000000002';
BEGIN
    INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, created_at, updated_at)
    VALUES
        (gen_random_uuid(), ct_uuid, 'USDT', 10000000.0000000000, 0.0000000000, NOW(), NOW()),
        (gen_random_uuid(), ct_uuid, 'BTC',       100.0000000000, 0.0000000000, NOW(), NOW()),
        (gen_random_uuid(), ct_uuid, 'ETH',      1000.0000000000, 0.0000000000, NOW(), NOW()),
        (gen_random_uuid(), ct_uuid, 'SOL',     10000.0000000000, 0.0000000000, NOW(), NOW())
    ON CONFLICT (user_id, asset) DO NOTHING;
END $$;
```

---

## 5. Wallet Reservation Semantics for Crossing Limit Orders

When CTS submits an aggressive limit order with a slippage price cap:

### BUY Orders (Quote Asset Reservation):
- **Reservation Asset:** `USDT`
- **Reserved Amount:** $\text{ReserveQuote} = P_{\text{cap}} \times Q$
- When the order matches against the resting MM ask at $P_{\text{maker}} \le P_{\text{cap}}$:
  - Actual Quote Debit: $P_{\text{maker}} \times Q$
  - Unspent Reservation Released: $(P_{\text{cap}} - P_{\text{maker}}) \times Q$
  - Base Asset Credited: $Q$ (into `CT-001`'s available BTC/ETH/SOL balance)

### SELL Orders (Base Asset Reservation):
- **Reservation Asset:** `BTC`, `ETH`, or `SOL`
- **Reserved Amount:** $Q$
- When the order matches:
  - Base Asset Debited: $Q$
  - Quote Asset Credited: $P_{\text{maker}} \times Q$ (into `CT-001`'s available USDT balance)

---

## 6. Verified Idempotency & Retry Semantics

We inspected the actual implementation of idempotency in `services/order/internal/service/service.go:74-86`:

```go
if p.IdempotencyKey != "" {
    existing, err := s.repo.FindByIdempotencyKey(ctx, p.IdempotencyKey)
    if err != nil {
        return nil, fmt.Errorf("check idempotency key: %w", err)
    }
    if existing != nil {
        if sameRequest(existing, p) {
            s.logger.Info("Idempotency key hit with matching parameters, returning existing order",
                zap.String("order_id", existing.ID))
            return existing, nil
        }
        return nil, ErrDuplicateIdempotencyKey
    }
}
```

### Crash / Timeout Behavior Verified:
1. CTS generates an idempotency key: `CTS-{MarketID}-{UUIDv7}`.
2. If CTS sends `CreateOrder`, Order Service writes to PostgreSQL and reserves funds, but the network drops before CTS receives the response:
   - CTS retries with the **same idempotency key**.
   - Order Service finds the existing order via `FindByIdempotencyKey`.
   - `sameRequest()` confirms parameters match $\implies$ **returns the existing order cleanly without double-reserving funds or creating a second order**.
3. If an entirely different request reuses the key, Order Service rejects with `ErrDuplicateIdempotencyKey`.
4. This confirms that CTS idempotency is fully supported and safe against network retries.
