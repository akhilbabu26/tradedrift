# Major Engineering Problems Faced While Building Wallet Service

This document provides a comprehensive post-mortem and engineering breakdown of the **22 major problems, race conditions, distributed systems edge-cases, and database concurrency challenges** encountered and solved while building the **TradeDrift Core Wallet Service** (`services/wallet`), complete with exact code references and architectural analysis.

---

## High-Level Summary: The 8 Core Engineering Pillars

```text
                           WALLET SERVICE CHALLENGES
                                      │
        ┌───────────────────┬─────────┴─────────┬───────────────────┐
        │                   │                   │                   │
        ▼                   ▼                   ▼                   ▼
  1. Concurrency       2. Deadlocks       3. Idempotency       4. Atomicity
  • Row-Level Locks    • 4-Wallet Sorting • Dual-Layer Dedup   • Single DB Tx
  • SELECT FOR UPDATE  • 40P01 Defense    • Wallet-Scoped Key  • Bal+Ledger+Outbox
        │                   │                   │                   │
        ├───────────────────┼───────────────────┼───────────────────┤
        │                   │                   │                   │
        ▼                   ▼                   ▼                   ▼
  5. Invariants        6. Reservations    7. Settlement        8. Outbox Worker
  • Total = Avail+Res  • Two-Bucket Math  • 4-Way Transfers    • SKIP LOCKED
  • Non-Negative Chk   • Release Guards   • Reservation Drain  • Fencing Tokens
```

---

## Problem 1: Preventing Double-Crediting of Wallets

### The Problem
When network timeouts or retries occur between upstream microservices (such as the **Wallet Top-Up Service**) and the Core Wallet Service:
```text
           Top-Up Service                      Core Wallet Service
                 │                                      │
                 │ 1. DepositFunds(+100 USDT)           │
                 ├─────────────────────────────────────>│
                 │                                      │ Credits +100 USDT
                 │     x Network Drops Response         │ (Balance: 100 -> 200)
                 │< - - - - - - - - - - - - - - - - - - ┤
                 │                                      │
                 │ 2. Retry DepositFunds(+100 USDT)     │
                 ├─────────────────────────────────────>│
                 │                                      │ Without idempotency:
                 │                                      │ Credits +100 AGAIN!
                 │                                      │ (Balance: 200 -> 300!)
```
Without strict idempotency, retrying an operation causes double-crediting, directly inflating customer balances and destroying financial invariants.

### How We Solved It in Code
1. **Wallet-Scoped Composite Idempotency Constraint**:
   In [migration/00001_create_wallet_core_tables.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00001_create_wallet_core_tables.sql):
   ```sql
   CONSTRAINT uq_wallet_transactions_key UNIQUE (wallet_id, reference_id, reference_type)
   ```
2. **Dual-Layer Idempotency in `DepositFunds`**:
   In [internal/service/deposit_funds.go:L89-L109](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/deposit_funds.go#L89-L109):
   - **Layer 1 (Fast-path memory/lookup check)**:
     ```go
     existingTxn, err := s.txnRepo.GetByWalletAndReference(ctx, wallet.ID, referenceID, referenceType)
     if existingTxn != nil {
         return &DepositResult{TransactionID: existingTxn.ID, NewBalance: wallet.AvailableBalance.StringFixed(assetInfo.Decimals)}, nil
     }
     ```
   - **Layer 2 (PostgreSQL Engine Guard)**:
     If two concurrent requests pass Layer 1 simultaneously, `INSERT INTO wallet_transactions` violates `uq_wallet_transactions_key` (PostgreSQL error `23505`). The code catches `repository.ErrDuplicate`, rolls back the transaction, and safely returns the existing balance.

---

## Problem 2: Race Conditions During Concurrent Wallet Operations

### The Problem
Multiple concurrent goroutines and microservices can operate on the same user wallet simultaneously:
```text
               CONCURRENT BALANCE RACE CONDITIONS

        Goroutine A                Goroutine B                Goroutine C
      ReserveFunds()             DepositFunds()             SettleTrade()
            │                          │                          │
            └──────────────────┬───────┴──────────────────────────┘
                               │
                               ▼
               ┌───────────────────────────────┐
               │ A naive SELECT available      │
               │ reads the same balance (100)  │
               │ before any write completes!   │
               └───────────────┬───────────────┘
                               │
            ┌──────────────────┴──────────────────┐
            │                                     │
     Goroutine A spends 80                 Goroutine C spends 80
            │                                     │
            ▼                                     ▼
     Balance = 20                          Balance = 20
                      OVER-SPENDING DISASTER!
                  ₹160 spent from ₹100 balance!
```

### How We Solved It in Code
PostgreSQL exclusive row locking via `SELECT ... FOR UPDATE`.
In [internal/repository/postgres/wallet_repository.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/repository/postgres/wallet_repository.go):
```sql
SELECT id, user_id, asset, available_balance, reserved_balance, total_balance, is_frozen
FROM wallets
WHERE id = $1
FOR UPDATE;
```
Any subsequent transaction attempting to reserve, deposit, release, or settle against that wallet is blocked until the first transaction commits.

---

## Problem 3: Deadlocks During Trade Settlement (`40P01`)

### The Problem
Trade settlement involves two distinct users and up to four wallets: Buyer Quote, Buyer Base, Seller Quote, Seller Base.
If Trade 1 (User A buying from User B) and Trade 2 (User B buying from User A) execute concurrently:
```text
                 TRADE SETTLEMENT DEADLOCK HAZARD

       Transaction 1 (Trade A)                 Transaction 2 (Trade B)
                  │                                       │
       Locks Wallet A (Buyer)                  Locks Wallet B (Buyer)
                  │                                       │
       Attempts to lock Wallet B               Attempts to lock Wallet A
                  │                                       │
                  ▼                                       ▼
       WAITS ON TRANSACTION 2                  WAITS ON TRANSACTION 1
                  │                                       │
                  └───────────────────┬───────────────────┘
                                      │
                                      ▼
                      POSTGRESQL DEADLOCK DETECTED!
                         Error Code: 40P01
```

### How We Solved It in Code
**Deterministic Lock Ordering via Lexicographical Sorting**.
In [internal/service/settle_trade.go:L114-L140](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/settle_trade.go#L114-L140):
```go
walletIDs := []string{buyerQuoteWallet.ID, buyerBaseWallet.ID, sellerQuoteWallet.ID, sellerBaseWallet.ID}
sort.Strings(walletIDs) // Lexicographical sort: W1 < W2 < W3 < W4
```
And in [internal/repository/postgres/wallet_repository.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/repository/postgres/wallet_repository.go):
```sql
SELECT id, available_balance, reserved_balance, total_balance
FROM wallets
WHERE id = ANY($1)
ORDER BY id ASC
FOR UPDATE;
```
Because every transaction in the system acquires wallet locks in the exact same sorted order ($W1 \to W2 \to W3 \to W4$), cyclic wait conditions are mathematically impossible. Deadlocks drop to zero.

---

## Problem 4: Keeping Wallet Balances and Ledger Entries Consistent

### The Problem
A financial system cannot afford partial execution:
- Balance updated ✅, but ledger insertion failed ❌ $\longrightarrow$ Balance inflated with no audit trail.
- Balance updated ✅, ledger updated ✅, but outbox event failed ❌ $\longrightarrow$ Downstream services (Portfolio, Notification) never learn of the deposit.

### How We Solved It in Code
**Unified Single-Transaction Boundary**.
In [internal/service/deposit_funds.go:L114-L185](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/deposit_funds.go#L114-L185):
```text
                      BEGIN TRANSACTION
                              │
                              ▼
                     1. Lock Wallet Row
                        (FOR UPDATE)
                              │
                              ▼
                     2. Update Balances
                        available += amt
                        total     += amt
                              │
                              ▼
                     3. Insert Ledger Entry
                        wallet_transactions
                              │
                              ▼
                     4. Insert Outbox Event
                        outbox
                              │
                              ▼
                      COMMIT TRANSACTION
```
If any error occurs during balance math, ledger creation, or outbox staging, PostgreSQL rolls back the entire transaction.

---

## Problem 5: Maintaining Balance Invariants

### The Problem
Application code can have bugs, race conditions, or unhandled panics. If balance integrity relies solely on Go `if` checks, a flaw could allow balances to become negative or total balance to diverge from available + reserved.

### How We Solved It in Code
**Storage-Engine Level Check Constraints**:
1. In [migration/00008_wallet_total_balance_check.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00008_wallet_total_balance_check.sql):
   ```sql
   ALTER TABLE wallets
       ADD CONSTRAINT chk_wallet_total_balance
       CHECK (total_balance = available_balance + reserved_balance);
   ```
2. In [migration/00001_create_wallet_core_tables.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00001_create_wallet_core_tables.sql):
   ```sql
   CONSTRAINT chk_wallets_available_nonneg CHECK (available_balance >= 0),
   CONSTRAINT chk_wallets_reserved_nonneg CHECK (reserved_balance >= 0)
   ```
Any SQL update that would violate $\text{total} = \text{available} + \text{reserved}$ or cause a negative balance is rejected by PostgreSQL.

---

## Problem 6: Correct Reservation / Release Mechanics

### The Problem
When trading, funds must be locked for pending orders without removing them from total assets:
- An order for 2,000 USDT must not reduce total user equity.
- Cancellation must restore available balance without double-releasing.
- Over-reserving beyond available funds must be impossible.

```text
                TWO-BUCKET BALANCE STATE MACHINE

            Initial State:
            Available: 10,000 USDT | Reserved: 0 USDT | Total: 10,000 USDT
                               │
                               │ Order Placed (2,000 USDT)
                               ▼
            Reserved State:
            Available:  8,000 USDT | Reserved: 2,000 USDT | Total: 10,000 USDT
                               │
            ┌──────────────────┴──────────────────┐
            │ Cancelled                           │ Trade Settled
            ▼                                     ▼
     Released State:                       Settled State:
     Available: 10,000 USDT                Available:  8,000 USDT
     Reserved:       0 USDT                Reserved:       0 USDT
     Total:     10,000 USDT                Total:      8,000 USDT (-2,000 spent)
```

### How We Solved It in Code
1. In [internal/service/reserve_funds.go:L75-L105](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/reserve_funds.go#L75-L105):
   - Atomically shifts: `available_balance -= amount`, `reserved_balance += amount`.
   - Leaves `total_balance` untouched.
2. In [internal/service/release_funds.go:L55-L80](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/release_funds.go#L55-L80):
   - Checks reservation status is `ACTIVE` or `PARTIALLY_CONSUMED`.
   - Shifts: `reserved_balance -= remaining`, `available_balance += remaining`.
   - Transitions reservation to `RELEASED`. Double releases match 0 rows.

---

## Problem 7: Atomic Wallet Initialization

### The Problem
New users receive an initial seed of 10,000 USDT. If wallet row creation and initial balance crediting happen across separate transactions, a server crash or database disconnect leaves an empty, un-seeded wallet row without an `INITIAL_ALLOCATION` ledger record.

### How We Solved It in Code
In [internal/service/initialize_wallet.go:L45-L95](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/initialize_wallet.go#L45-L95):
- All 4 asset wallets (USDT, BTC, ETH, SOL) and the 10,000 USDT starting allocation are created inside a single transaction.
- An `INITIAL_ALLOCATION` entry is written into `wallet_transactions` with `balance_before = 0` and `balance_after = 10000`.
- If called again, detects existing wallets and returns existing balances without re-seeding.

---

## Problem 8: Settlement Correctness Across Two Wallets (4 Balance Movements)

### The Problem
A trade settlement is an atomic asset exchange:
- Buyer: USDT $\downarrow$, BTC $\uparrow$
- Seller: BTC $\downarrow$, USDT $\uparrow$
If any single movement fails (e.g. buyer debited but seller credit fails), funds disappear into thin air.

### How We Solved It in Code
In [internal/service/settle_trade.go:L165-L220](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/settle_trade.go#L165-L220):
All 4 movements execute inside a single transaction:
1. `buyerQuoteWallet.ReservedBalance -= quoteAmount` & `buyerQuoteWallet.TotalBalance -= quoteAmount`
2. `buyerBaseWallet.AvailableBalance += baseAmount` & `buyerBaseWallet.TotalBalance += baseAmount`
3. `sellerBaseWallet.ReservedBalance -= baseAmount` & `sellerBaseWallet.TotalBalance -= baseAmount`
4. `sellerQuoteWallet.AvailableBalance += quoteAmount` & `sellerQuoteWallet.TotalBalance += quoteAmount`
Plus 4 double-entry ledger rows in `wallet_transactions`.

---

## Problem 9: Preventing Incorrect Settlement Amounts (Over-Consumption)

### The Problem
A buggy or compromised matching engine could submit a settlement claiming 150 USDT for an order that only had 100 USDT reserved.

### How We Solved It in Code
In [internal/service/settle_trade_validate.go:L40-L75](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/settle_trade_validate.go#L40-L75):
```go
if buyerReservation.RemainingAmount.LessThan(quoteAmount) {
    return fmt.Errorf("%w: buyer reservation remaining %s < settlement quote %s",
        repository.ErrInsufficientReservation, buyerReservation.RemainingAmount, quoteAmount)
}
```
If settlement exceeds remaining reserved funds, the transaction immediately rolls back with `ErrInsufficientReservation`.

---

## Problem 10: Making Trade Settlement Idempotent

### The Problem
The Trade/Settlement Service can retry `SettleTrade()` calls during network hiccups. The Wallet Service must never settle the same trade twice.

### How We Solved It in Code
1. **Dedicated Table `settled_trades`**:
   In [migration/00005_create_settled_trades.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00005_create_settled_trades.sql):
   ```sql
   CREATE TABLE settled_trades (
       trade_id UUID PRIMARY KEY,
       market_id VARCHAR(20) NOT NULL,
       sequence BIGINT NOT NULL,
       settled_at TIMESTAMPTZ NOT NULL
   );
   ```
2. **In-Transaction Lock & Verification**:
   In [internal/service/settle_trade.go:L55-L85](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/settle_trade.go#L55-L85):
   If `trade_id` already exists in `settled_trades`, it verifies metadata matches and returns an idempotent success receipt without re-moving balances.

---

## Problem 11: Preventing Conflicting Trade Metadata

### The Problem
An upstream bug could retry a settlement with the same `trade_id` but with different amounts, market, or sequence number. Silently accepting it would corrupt accounting.

### How We Solved It in Code
In [internal/service/settle_trade.go:L65-L80](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/settle_trade.go#L65-L80):
```go
if existing.MarketID != marketID || existing.Sequence != sequence {
    return nil, fmt.Errorf("%w: trade %s already settled with market %s seq %d (got %s seq %d)",
        repository.ErrSettlementConflict, tradeID, existing.MarketID, existing.Sequence, marketID, sequence)
}
```
Rejects with `ErrSettlementConflict`.

---

## Problem 12: Reliable Event Publishing (Dual-Write Hazard)

### The Problem
Publishing directly to Kafka after a database commit is vulnerable to crashes:
```text
           Update Database ✅ ──> Process Crashes ──x Kafka Publish NEVER HAPPENS
```
The balance changed, but the event was permanently lost.

### How We Solved It in Code
**Transactional Outbox Pattern**:
The Wallet Service writes the event to the `outbox` table in the exact same database transaction as the balance change. A resilient background worker publishes it asynchronously to Kafka.

---

## Problem 13: Multiple Workers Processing the Same Outbox Records

### The Problem
When running multiple replicas of the Wallet Service in Kubernetes, two outbox workers could scan and claim the same pending event, producing duplicate Kafka events.

### How We Solved It in Code
In [internal/repository/postgres/outbox_repository.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/repository/postgres/outbox_repository.go):
```sql
SELECT id FROM outbox
WHERE status = 'PENDING'
   OR (status = 'PROCESSING' AND claim_until < NOW())
ORDER BY created_at ASC, id ASC
LIMIT $1
FOR UPDATE SKIP LOCKED;
```
`SKIP LOCKED` instructs PostgreSQL to skip rows currently locked by another worker transaction without waiting.

---

## Problem 14: Worker Crashes While Publishing Outbox Events

### The Problem
If a worker claims an outbox event, starts sending it to Kafka, and crashes (OOM, node eviction) before marking it `PUBLISHED`, the event remains stuck in `PROCESSING` forever.

### How We Solved It in Code
**Lease-Based Worker Claims**:
In [migration/00004_add_outbox_processing_and_lease.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00004_add_outbox_processing_and_lease.sql):
Each claim has a 30-second lease:
```sql
claim_until = NOW() + INTERVAL '30 seconds'
```
If the worker dies, the lease expires. When the next worker scans `WHERE claim_until < NOW()`, it re-claims and publishes the event.

---

## Problem 15: Stale Workers Modifying Newer Claims (Fencing Tokens)

### The Problem
Worker A claims Event 101 with a 30s lease. Worker A has a 45s garbage collection pause. The lease expires. Worker B claims Event 101 and publishes it. Worker A wakes up and executes `MarkPublished()`. Without fencing, Worker A could overwrite Worker B's newer claim or corrupt state.

```text
                 OUTBOX WORKER FENCING TOKEN FLOW

       Worker A (Pod 1)                        Worker B (Pod 2)
              │                                       │
              │ Claims Event 101                      │
              │ claim_token = UUID_A                  │
              │ lease = 30s                           │
              ▼                                       │
      Worker A pauses 45s                             │
      (GC pause / CPU freeze)                         │
              │                                       │
              │ ─── 30s lease expires ───             │
              │                                       ▼
              │                               Claims Event 101
              │                               claim_token = UUID_B
              │                               lease = 30s
              │                                       │
              │                               Publishes to Kafka
              │                                       │
              ▼                                       ▼
      Wakes up at 45s                         UPDATE outbox
      Publishes to Kafka                      SET status = 'PUBLISHED'
              │                               WHERE claim_token = UUID_B
              ▼                               (Rows affected = 1)
      UPDATE outbox                                   │
      SET status = 'PUBLISHED'                        ▼
      WHERE claim_token = UUID_A                 SUCCESS!
              │
      ┌───────┴───────────────┐
      │ Result: 0 ROWS        │
      │ Token mismatch!       │
      │ WORKER A FENCED OUT!  │
      └───────────────────────┘
```

### How We Solved It in Code
In [migration/00010_outbox_claim_token.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00010_outbox_claim_token.sql) and [internal/repository/postgres/outbox_repository.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/repository/postgres/outbox_repository.go):
Status transitions require the worker's token:
```sql
UPDATE outbox
SET status = 'PUBLISHED', published_at = NOW(), updated_at = NOW()
WHERE id = $1 AND claim_token = $2;
```
Worker A's token no longer matches the row. Worker A is cleanly fenced out!

---

## Problem 16: Outbox Failure / Release Isolation

### The Problem
The same fencing vulnerability applies to error handling: if Worker A wakes up late after an error, it must not execute `ReleaseClaim` or `MarkFailed` on a record that Worker B already owns.

### How We Solved It in Code
In [internal/repository/postgres/outbox_repository.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/repository/postgres/outbox_repository.go):
Both `ReleaseClaim(ctx, id, token)` and `MarkFailed(ctx, id, token, reason)` enforce:
```sql
WHERE id = $1 AND claim_token = $2
```

---

## Problem 17: Wallet-Scoped Idempotency (Why Reference Alone Is Not Enough)

### The Problem
If idempotency was simply `UNIQUE (reference_id)`, two different users couldn't participate in operations that share a reference ID (e.g. buyer and seller both referencing the same `trade_id`), or a deposit reference could conflict with an order reference.

### How We Solved It in Code
The composite key is strictly scoped to the wallet:
$$\text{UNIQUE}(\text{wallet\_id}, \text{reference\_id}, \text{reference\_type})$$
This scopes deduplication to the exact user and exact asset.

---

## Problem 18: Validating Assets and Decimal Precision

### The Problem
Floating-point precision errors (e.g. `0.1 + 0.2 = 0.30000000000000004`) or scientific notation inputs (`"5e4"`) could corrupt ledger accounting.

### How We Solved It in Code
In [internal/service/deposit_funds.go:L50-L75](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/deposit_funds.go#L50-L75):
1. Rejects scientific notation explicitly: `if depositAmount.Exponent() > 0 { return ErrInvalidDeposit }`.
2. Verifies asset is enabled in `supported_assets`.
3. Validates decimal scale does not exceed asset limit (e.g. USDT max 10 decimals).

---

## Problem 19: Handling Missing Wallets Correctly

### The Problem
If `DepositFunds()` or `ReserveFunds()` silently auto-created wallets for non-existent users, typos in UUIDs would create ghost accounts.

### How We Solved It in Code
In [internal/service/deposit_funds.go:L77-L85](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/deposit_funds.go#L77-L85):
Returns `repository.ErrWalletNotFound`. Wallets must be explicitly initialized through `InitializeWallet()` during user registration.

---

## Problem 20: Frozen Wallet Policy (Policy B: Asymmetric Security Gate)

### The Problem
When a wallet is frozen due to fraud investigation:
- Naive policy: Block everything.
  - Problem: Inbound refunds or customer top-ups fail, stranding money.
- Naive policy: Allow everything.
  - Problem: Fraudulent user withdraws or spends the money.

### How We Solved It in Code
We implemented **Policy B (Asymmetric Gate)**:
1. `ReserveFunds()` (outgoing debits) strictly blocks frozen wallets with `ErrWalletFrozen` ([reserve_funds.go:L70](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/reserve_funds.go#L70)).
2. `DepositFunds()` (inbound credits/refunds/top-ups) explicitly permits incoming deposits into frozen wallets ([deposit_funds.go:L86](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/deposit_funds.go#L86)).

---

## Problem 21: Deterministic Outbox FIFO Partition Ordering

### The Problem
If outbox events for a single user are published out of order (e.g. order filled event published before order placed event), downstream portfolio calculations break.

### How We Solved It in Code
1. In [migration/00006_outbox_ordering_index.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00006_outbox_ordering_index.sql):
   ```sql
   CREATE INDEX idx_outbox_ordering ON outbox(created_at ASC, id ASC);
   ```
2. In [internal/publisher/publisher.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/publisher/publisher.go):
   Kafka message partition key is strictly set to `user_id`. All events for a user route to the same Kafka partition, guaranteeing deterministic FIFO delivery.

---

## Problem 22: Sequence Collisions in Trade Settlements

### The Problem
Two matching engine workers could assign the same market sequence number to two different trade IDs.

### How We Solved It in Code
In [migration/00007_settled_trades_unique_sequence.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00007_settled_trades_unique_sequence.sql):
```sql
ALTER TABLE settled_trades
    ADD CONSTRAINT uq_settled_trades_market_sequence
    UNIQUE (market_id, sequence);
```
Enforces that within any market (e.g. `BTC-USDT`), sequence numbers are strictly monotonically unique.

---

## 🎯 Top 6 Technical Interview Highlights

When discussing the Wallet Service in technical interviews, focus on these flagship distributed systems highlights:

1. **Deadlock Elimination via Deterministic Lock Ordering**: Lexicographical sorting of wallet IDs before `SELECT ... FOR UPDATE` eliminates PostgreSQL `40P01` deadlocks during multi-party settlement.
2. **Dual-Layer Financial Idempotency**: Scoped composite key `(wallet_id, reference_id, reference_type)` protecting against lost gRPC responses and reconciler retries.
3. **Atomic Balance + Ledger + Outbox Boundary**: Single PostgreSQL ACID transaction preventing balance inflation and dual-write anomalies.
4. **Transactional Outbox with `SKIP LOCKED`**: Queue-like event streaming without broker lock contention.
5. **Tokenized Lease Fencing**: `claim_token UUID` preventing stale workers with GC pauses from overwriting newer state.
6. **Storage-Layer Check Constraints**: `chk_wallet_total_balance` guaranteeing $\text{total} = \text{available} + \text{reserved}$ at the PostgreSQL engine level.
