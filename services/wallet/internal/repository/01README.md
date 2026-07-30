# Wallet Service — Repository Layer Guide

> **What is the repository layer?**
> The repository layer is the bridge between your business logic (service layer) and the database (PostgreSQL). It defines *what* operations are needed on data — without caring *how* those operations are physically executed. This separation is one of the most important patterns in production backend engineering.

---

## Why Split Into 5 Files?

Each file owns exactly one domain concept. They don't overlap:

| File | Owns | Database table |
|---|---|---|
| `wallet.go` | Balance state per user per asset | `wallets` |
| `reservation.go` | Fund locks per order | `wallet_reservations` |
| `transaction.go` | Immutable ledger entries | `wallet_transactions` |
| `asset.go` | Platform asset configuration | `supported_assets` |
| `outbox.go` | Pending Kafka events | `outbox` |
| `errors.go` | Domain sentinel errors | — (no table, shared by all) |
| `constants.go` | Typed string constants | — (no table, shared by all) |

**Why not one big file?**
- Each file can be read, tested, and modified independently.
- A future developer reading `reservation.go` knows exactly what database operations are available for reservations — they don't need to scan 200 lines of mixed concerns.
- The Go compiler enforces the contract: if you rename a method in the interface, every implementation that doesn't update breaks at compile time.

---

## The Interface + Struct Pattern

Every file follows the same two-part structure:

```
Struct  →  mirrors a database row (the data shape)
Interface →  defines what operations you can do with that data
```

This is intentional. The struct is *what the data looks like*. The interface is *what you can do with it*. They are separate because:

- You can swap the database from PostgreSQL to SQLite by writing a new struct that implements the same interface — the service layer never changes.
- You can write a mock implementation for unit tests without touching a real database.
- The service layer only ever sees the interface — it cannot accidentally call internal database code directly.

---

## File-by-File Breakdown

---

### `wallet.go` — Balance State

```go
type Wallet struct { ... }
type WalletRepository interface { ... }
```

**The `Wallet` struct** mirrors one row in the `wallets` table. Every field maps 1:1 to a column.

**Why are balances `string` instead of `float64`?**

This is a critical financial engineering decision. Floating-point numbers (`float64`) cannot represent all decimal values exactly:

```
0.1 + 0.2 = 0.30000000000000004   ← float64 rounding error
```

In a financial system, this is catastrophic. If a user's balance rounds incorrectly by even `0.000000001`, the accounting invariant breaks — money appears from nowhere or disappears.

Using `string` (e.g. `"10000.0000000000"`) means the service layer passes the exact decimal string to the database, and PostgreSQL's `DECIMAL(30,10)` type handles the math with full precision.

**Key methods explained:**

| Method | What it does | When it's called |
|---|---|---|
| `GetByUserAndAsset` | Fetch one (user, asset) wallet | Before any balance check |
| `GetAllByUser` | Fetch all wallets for a user | `GetBalances` endpoint |
| `Create` | Insert a new wallet row | During `InitializeWallet` |
| `CreditAvailable` | `available += amount` | Settle trade (buyer gets base asset), Release funds |
| `DebitAvailable` | `available -= amount` | Reserve funds (user places order) |
| `MoveToReserved` | `available -= X`, `reserved += X` | Reserve funds atomically — one operation, not two |
| `MoveFromReserved` | `reserved -= X`, `available += X` | Release funds — return locked money |
| `DebitReserved` | `reserved -= X` | Settlement — consumed funds leave the account |
| `FreezeWallet` | Sets `is_frozen = true` with reason | Admin/risk system blocks a suspicious wallet |
| `UnfreezeWallet` | Sets `is_frozen = false`, clears reason | Admin lifts the freeze |

**Why `MoveToReserved` instead of separate `DebitAvailable` + `CreditReserved`?**

Because two separate operations means two database round-trips. Between them, another goroutine could read a partially updated balance. `MoveToReserved` is one `UPDATE wallets SET available = available - $1, reserved = reserved + $1` — atomic at the DB level. No race window.

---

### `reservation.go` — Order Fund Locks

```go
type Reservation struct { ... }
type ReservationRepository interface { ... }
```

**The `Reservation` struct** tracks the complete lifecycle of a fund lock for one order.

**The three amount fields:**

```
reserved_amount   = locked at order creation. NEVER changes.
consumed_amount   = increases with each partial fill.
remaining_amount  = reserved_amount - consumed_amount. What gets returned on cancel.
```

Example: User places an order to buy BTC for 1000 USDT.
- `reserved_amount  = "1000.00"`
- 300 USDT fills immediately → `consumed_amount = "300.00"`, `remaining_amount = "700.00"`
- User cancels → `MoveFromReserved("700.00")` → available gets 700 USDT back

**The `Status` field state machine:**

```
ACTIVE               ← order is live, no fills yet
  ↓ partial fill
PARTIALLY_CONSUMED   ← some fills, still open
  ↓ more fills
CONSUMED             ← fully filled, nothing to return
  ↓ or cancel
RELEASED             ← cancelled, remaining returned to available
```

**Why `UpdateConsumed` instead of re-calculating in code?**

The `remaining_amount = reserved_amount - consumed_amount` calculation happens inside the SQL UPDATE, not in Go code. This prevents any race between read → calculate → write where two concurrent fills could both read the same consumed_amount and both write the same (wrong) result.

---

### `transaction.go` — Immutable Ledger

```go
type WalletTransaction struct { ... }
type TransactionRepository interface { ... }
```

**The `WalletTransaction` struct** is a permanent, never-deleted record of every balance change.

**Why immutable?** Financial audit requirements. You must be able to reconstruct any user's balance at any point in time by replaying their transaction history. Rows are never updated or deleted — only inserted.

**The three interface methods:**

| Method | Why it exists |
|---|---|
| `Create` | Insert one ledger entry. If `UNIQUE` violated → wraps `ErrDuplicate` |
| `ExistsByKey` | Check before touching any balance. Upfront idempotency guard. |
| `CreateBatch` | Insert buyer + seller rows together. Used only by `SettleTrade`. |

**Why `ExistsByKey` + `Create` instead of just `Create`?**

`SettleTrade` uses the upfront check (`ExistsByKey`) to short-circuit immediately — without locking any wallet rows — if this trade was already settled. This avoids unnecessary row locks on retried calls.

`Create` catches the `UNIQUE` violation as a last-resort safety net for race conditions (two concurrent settle calls that both passed the upfront check).

**Why `ErrDuplicate` as a sentinel error?**

Because callers must treat a duplicate as **success**, not failure. Without a typed error, the service layer would have to inspect the raw Postgres error code (`23505 = unique_violation`) — leaking database implementation details into the business logic. With `ErrDuplicate`, the service just does:

```go
err := txnRepo.Create(ctx, t)
if errors.Is(err, repository.ErrDuplicate) {
    return nil // idempotent success
}
```

Clean, readable, database-agnostic.

---

### `errors.go` — Domain Sentinel Errors

```go
var (
    ErrDuplicate           = errors.New("duplicate: already processed")
    ErrNotFound            = errors.New("not found")
    ErrInsufficientBalance = errors.New("insufficient balance")
    ErrWalletFrozen        = errors.New("wallet is frozen")
)
```

**Why a dedicated errors file?**

Sentinel errors are used with `errors.Is()` — the idiomatic Go way to check error type without inspecting raw Postgres error codes. The handler layer uses them to map domain failures to gRPC status codes:

| Sentinel error | gRPC status code | Meaning to the caller |
|---|---|---|
| `ErrNotFound` | `codes.NotFound` | Wallet or reservation doesn't exist |
| `ErrInsufficientBalance` | `codes.FailedPrecondition` | Not enough available balance |
| `ErrWalletFrozen` | `codes.FailedPrecondition` | Account is under a freeze |
| `ErrDuplicate` | `codes.AlreadyExists` | Already processed (idempotent) |

**Why not in `platform/errors`?**

`platform/errors` contains HTTP/gRPC-level structured errors (the `PlatformError` type) used by handlers to send codes to clients. These wallet sentinel errors are **database-layer domain errors** — they tell the service what went wrong in Postgres. Putting wallet-specific concepts like `ErrInsufficientBalance` in platform would make the platform package aware of wallet-domain concepts, breaking the dependency direction.

---

### `constants.go` — Typed String Constants

```go
const (
    ReservationActive            = "ACTIVE"
    ReservationPartiallyConsumed = "PARTIALLY_CONSUMED"
    ReservationConsumed          = "CONSUMED"
    ReservationReleased          = "RELEASED"

    TxnTypeCredit = "CREDIT"
    TxnTypeDebit  = "DEBIT"

    RefInitialAllocation = "INITIAL_ALLOCATION"
    RefReservation       = "RESERVATION"
    RefRelease           = "RELEASE"
    RefSettlement        = "SETTLEMENT"
    RefDeposit           = "DEPOSIT"
    RefWithdrawal        = "WITHDRAWAL"
)
```

**Why typed constants instead of plain strings?**

Without constants, every file that sets a reservation status would write `"ACTIVE"`, `"RELEASED"` etc. as magic strings. A typo like `"RELASED"` compiles fine but writes corrupt data to the database silently. With constants, `repository.ReservationReleased` is a compile-time-checked reference — the compiler catches the typo.

**Why in `repository/` not `service/`?**

These strings are values that map directly to database column values (`status`, `reference_type`, `transaction_type`). They belong in the repository package because they represent the vocabulary of the data layer. Both the service layer and the postgres implementations import them from here.

```go
type SupportedAsset struct { ... }
type AssetRepository interface { ... }
```

**The `SupportedAsset` struct** mirrors one row from `supported_assets`.

**Why is this a separate repository?**

`InitializeWallet` needs to know: *"What assets does this platform support right now?"* It cannot hardcode `["USDT", "BTC", "ETH", "SOL"]` — that would require a code change every time a new asset is added.

Instead it calls `assetRepo.GetEnabled()`, gets the live list from the database, and creates wallet rows for each one. Adding a new asset becomes a data operation, not a code operation.

**`GetAll` vs `GetEnabled`:**

| Method | Returns | Used by |
|---|---|---|
| `GetAll` | All assets, including disabled ones | Admin UI, audit tools |
| `GetEnabled` | Only `is_enabled = true` assets | `InitializeWallet`, trading operations |

This distinction matters: if you disable an asset (e.g. SOL maintenance), new user wallets should not get a SOL wallet, but existing SOL wallets and their history must still be readable.

---

### `outbox.go` — Transactional Outbox

```go
type OutboxEvent struct { ... }
type OutboxRepository interface { ... }
```

**The `OutboxEvent` struct** is a pending Kafka event stored in the database.

**Why does `Insert` exist here instead of publishing to Kafka directly?**

Because publishing to Kafka must happen *after* the database commits — and must be *guaranteed* even if the service crashes immediately after the commit.

The repository's `Insert` method writes the event row **inside the same transaction** as the balance change. Either both commit or neither commits. A separate background goroutine (the outbox publisher) then reads `PENDING` rows and publishes to Kafka.

**The `PartitionKey` field:**

Kafka partitions events by key. Events with the same `partition_key` always go to the same partition, guaranteeing ordering. For `UserTradeSettled`:
- Buyer's event → `partition_key = buyer_id`
- Seller's event → `partition_key = seller_id`

This means all events for a given user are processed in order by downstream consumers (Portfolio Service, Notification Service).

**`Payload []byte` instead of `interface{}`?**

The payload is already serialized to JSON before being passed to the repository. The repository just stores raw bytes. This keeps the repository layer ignorant of business event schemas — it just persists bytes and lets the publisher push them as-is.

---

## How the 5 Repositories Work Together

Here's a concrete example — `ReserveFunds`:

```
1. walletRepo.GetByUserAndAsset(userID, asset)
        → Is the wallet frozen? Does user have enough available_balance?

2. reservationRepo.GetByOrderID(orderID)
        → Already reserved? (idempotency check) Return existing if found.

3. walletRepo.MoveToReserved(walletID, amount)
        → available -= amount, reserved += amount  (atomic)

4. reservationRepo.Create(reservation)
        → Insert the fund lock record

5. txnRepo.Create(walletTransaction)
        → Insert ledger entry (RESERVATION, DEBIT)

6. COMMIT
```

Five repositories, five responsibilities, one coherent operation. Each repository only sees its own table — no repository queries another repository's table.

---

## The Repository vs Service Distinction

| Repository layer | Service layer |
|---|---|
| *How* data is stored and retrieved | *Why* and *when* operations happen |
| Knows SQL, table names, column names | Knows business rules (e.g. "cannot reserve more than available") |
| No business logic | No SQL |
| Returns raw domain structs | Orchestrates multiple repository calls |
| Tested with a real database | Tested with mock repositories |

This separation means you can change the database schema without touching business logic, and you can change business rules without touching SQL.
