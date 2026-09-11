# Distributed Systems & Financial Engineering Techniques in Wallet Service (`TECHNIQUES.md`)

This document provides a comprehensive technical breakdown of the **29 distributed systems, financial correctness, and database engineering techniques** implemented across the **TradeDrift Core Wallet Service** (`services/wallet`), with direct file and line references showing where and how each technique is implemented in the codebase.

---

## High-Level Classification: The 8 Engineering Categories

```text
                           WALLET SERVICE TECHNIQUES
                                       │
        ┌────────────────────┬─────────┴─────────┬────────────────────┐
        │                    │                   │                    │
        ▼                    ▼                   ▼                    ▼
  1. Concurrency       2. Idempotency       3. Transactions      4. Financial
  • Row Locks          • Dual-Layer Dedup   • Single DB Tx       • Balance Invariants
  • 4-Wallet Sorting   • (wallet,ref,type)  • Multi-Wallet Unit  • Two-Bucket Model
  • Conditional CAS    • Sequence Barrier   • Atomicity          • Immutable Ledger
        │                    │                   │                    │
        ├────────────────────┼───────────────────┼────────────────────┤
        │                    │                   │                    │
        ▼                    ▼                   ▼                    ▼
  5. Event System      6. Worker Safety     7. Architecture      8. Validation
  • Outbox Pattern     • SKIP LOCKED        • Repository Pattern • Go + DB Defense
  • At-Least-Once      • Leases (30s)       • DBTX Interface     • Decimal Arithmetic
  • FIFO Partitioning  • Fencing Tokens     • Explicit Lifecycle • Asymmetric Gate
```

---

## 1. Concurrency & Locking Techniques

### Technique 1: Pessimistic Row Locking (`SELECT ... FOR UPDATE`)
- **Problem**: Concurrent requests (e.g. concurrent `ReserveFunds`, `DepositFunds`, and `SettleTrade`) reading stale balances can cause balance over-spending or negative balances.
- **Where in Code**:
  - [internal/repository/postgres/wallet_repository.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/repository/postgres/wallet_repository.go):
    ```sql
    SELECT id, user_id, asset, available_balance, reserved_balance, total_balance, is_frozen
    FROM wallets
    WHERE id = $1
    FOR UPDATE;
    ```
- **How It Works**: PostgreSQL locks the specific wallet row exclusively. Concurrent transactions attempting to mutate the same wallet queue deterministically until the active transaction commits.

---

### Technique 2: Deterministic Lock Ordering (Deadlock Elimination)
- **Problem**: When Trade 1 involves Buyer A and Seller B, and Trade 2 involves Buyer B and Seller A, locking wallets in arbitrary order causes cyclic wait conditions and PostgreSQL `40P01` deadlock errors.
- **Where in Code**:
  - [internal/service/settle_trade.go:L114-L140](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/settle_trade.go#L114-L140):
    ```go
    walletIDs := []string{buyerQuoteWallet.ID, buyerBaseWallet.ID, sellerQuoteWallet.ID, sellerBaseWallet.ID}
    sort.Strings(walletIDs) // Lexicographical sorting: W1 < W2 < W3 < W4
    ```
  - [internal/repository/postgres/wallet_repository.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/repository/postgres/wallet_repository.go):
    ```sql
    SELECT id, available_balance, reserved_balance, total_balance
    FROM wallets
    WHERE id = ANY($1)
    ORDER BY id ASC
    FOR UPDATE;
    ```
- **Visualization**:
```text
            DETERMINISTIC DEADLOCK-FREE LOCK ORDERING

       Trade 1 (Buyer X, Seller Y)             Trade 2 (Buyer Y, Seller X)
                   │                                       │
       Wallets: [W3, W1, W4, W2]               Wallets: [W2, W4, W1, W3]
                   │                                       │
                   ▼                                       ▼
        Lexicographical Sorting                 Lexicographical Sorting
                   │                                       │
                   ▼                                       ▼
        Lock Order:                             Lock Order:
        1. Lock W1 (FOR UPDATE)                 1. Lock W1 (FOR UPDATE)
        2. Lock W2 (FOR UPDATE)                 2. Lock W2 (FOR UPDATE)
        3. Lock W3 (FOR UPDATE)                 3. Lock W3 (FOR UPDATE)
        4. Lock W4 (FOR UPDATE)                 4. Lock W4 (FOR UPDATE)
```

---

### Technique 3: Optimistic / Conditional Updates (Compare-And-Swap)
- **Problem**: Long-running background processes should not hold open row locks across external network calls.
- **Where in Code**:
  - [internal/repository/postgres/outbox_repository.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/repository/postgres/outbox_repository.go):
    ```sql
    UPDATE outbox
    SET status = 'PUBLISHED', published_at = NOW(), updated_at = NOW()
    WHERE id = $1 AND claim_token = $2;
    ```
- **How It Works**: If another worker reclaimed the event during a network pause, `RowsAffected == 0`. The worker detects that state changed underneath it and safely aborts.

---

## 2. Idempotency & Deduplication Techniques

### Technique 4: Wallet-Scoped Composite Idempotency
- **Problem**: Global `reference_id` uniqueness breaks when multiple users participate in the same operation (e.g. buyer and seller sharing `trade_id`).
- **Where in Code**:
  - [migration/00001_create_wallet_core_tables.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00001_create_wallet_core_tables.sql):
    ```sql
    CONSTRAINT uq_wallet_transactions_key UNIQUE (wallet_id, reference_id, reference_type)
    ```
- **How It Works**: Scopes deduplication strictly to the wallet, operation type, and reference ID. For top-ups: `(wallet_id, topup_id, 'TOPUP')`.

---

### Technique 5: Dual-Layer Idempotency (Fast-Path + Database Barrier)
- **Problem**: High-throughput retries should not waste database write locks if an operation was already processed, yet concurrent identical requests must not slip past application checks.
- **Where in Code**:
  - [internal/service/deposit_funds.go:L89-L109](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/deposit_funds.go#L89-L109):
    - **Layer 1 (Fast-Path Read)**: Queries `GetByWalletAndReference(wallet_id, ref_id, ref_type)`. If present, returns existing receipt without write lock.
    - **Layer 2 (PostgreSQL Engine Barrier)**: In concurrent race conditions, `INSERT INTO wallet_transactions` triggers unique violation `23505`. `DepositFunds` intercepts `ErrDuplicate`, rolls back, and returns the existing balance.

---

### Technique 6: State & Metadata-Based Settlement Idempotency
- **Problem**: Checking only `trade_id` is insufficient. An upstream bug could resubmit the same `trade_id` with conflicting market or sequence parameters.
- **Where in Code**:
  - [internal/service/settle_trade.go:L65-L85](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/settle_trade.go#L65-L85) & [migration/00005_create_settled_trades.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00005_create_settled_trades.sql):
    ```go
    if existing.MarketID != marketID || existing.Sequence != sequence {
        return nil, fmt.Errorf("%w: trade %s conflict", repository.ErrSettlementConflict, tradeID)
    }
    ```
- **How It Works**: Exact match $\to$ returns existing success. Conflicting metadata $\to$ rejects with `ErrSettlementConflict`.

---

### Technique 7: Monotonic Market Sequence Constraints
- **Problem**: Sequence replay or sequence reuse between different trade IDs within the same order book.
- **Where in Code**:
  - [migration/00007_settled_trades_unique_sequence.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00007_settled_trades_unique_sequence.sql):
    ```sql
    ALTER TABLE settled_trades
        ADD CONSTRAINT uq_settled_trades_market_sequence
        UNIQUE (market_id, sequence);
    ```

---

## 3. Transaction & Storage Boundary Techniques

### Technique 8: Unified Database Transaction (ACID Boundary)
- **Problem**: Partial execution where balance updates commit, but ledger rows or outbox records fail.
- **Where in Code**:
  - [internal/service/deposit_funds.go:L114-L185](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/deposit_funds.go#L114-L185)
  - [internal/service/settle_trade.go:L100-L240](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/settle_trade.go#L100-L240)
- **How It Works**: Every operation runs within `tx.Begin(ctx)` ... `tx.Commit(ctx)`. Balance mutations, reservation transitions, ledger insertions, and outbox staging commit or rollback as one single atomic unit.

---

### Technique 9: Atomic Multi-Wallet Settlement
- **Problem**: In multi-asset trade execution, buyer quote debit, buyer base credit, seller quote credit, and seller base debit must never be split.
- **Where in Code**:
  - [internal/service/settle_trade.go:L165-L220](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/settle_trade.go#L165-L220):
    Executes all 4 balance shifts and 4 double-entry ledger entries within the same database transaction.

---

### Technique 10: Atomic Wallet Initialization
- **Problem**: Creating user wallet rows without their initial deposit seed creates broken empty accounts.
- **Where in Code**:
  - [internal/service/initialize_wallet.go:L45-L95](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/initialize_wallet.go#L45-L95):
    Initializes USDT, BTC, ETH, and SOL rows, assigns the 10,000 USDT seed balance, and creates the `INITIAL_ALLOCATION` ledger row in a single atomic transaction.

---

## 4. Financial Integrity & Accounting Invariants

### Technique 11: Two-Bucket Balance Model (`available` vs `reserved`)
- **Problem**: Active trading orders must lock funds without removing them from total user equity.
- **Where in Code**:
  - [internal/service/reserve_funds.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/reserve_funds.go) & [release_funds.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/release_funds.go):
    - Order placed: `available_balance -= amount`, `reserved_balance += amount`. `total_balance` is unchanged.
    - Order cancelled: `reserved_balance -= remaining`, `available_balance += remaining`. `total_balance` is unchanged.

---

### Technique 12: Database-Enforced Balance Invariant Checks
- **Problem**: Guaranteeing financial axioms cannot be broken even if application code contains a bug.
- **Where in Code**:
  - [migration/00008_wallet_total_balance_check.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00008_wallet_total_balance_check.sql):
    ```sql
    ALTER TABLE wallets
        ADD CONSTRAINT chk_wallet_total_balance
        CHECK (total_balance = available_balance + reserved_balance);
    ```
  - [migration/00001_create_wallet_core_tables.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00001_create_wallet_core_tables.sql):
    ```sql
    CONSTRAINT chk_wallets_available_nonneg CHECK (available_balance >= 0),
    CONSTRAINT chk_wallets_reserved_nonneg CHECK (reserved_balance >= 0)
    ```

---

### Technique 13: Double-Entry Immutable Ledger Recording
- **Problem**: Mutating balances without immutable audit records makes financial forensics and reconciliation impossible.
- **Where in Code**:
  - [internal/repository/postgres/transaction_repository.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/repository/postgres/transaction_repository.go):
    Every balance change writes an append-only row to `wallet_transactions` capturing `balance_before`, `balance_after`, `reference_type`, and `reference_id`. Records are never modified or deleted.

---

### Technique 14: Separation of Current State and Historical State
- **Problem**: Querying account balance by summing historical ledger rows is too slow for trading engines ($O(N)$ query time).
- **Where in Code**:
  - Fast $O(1)$ reads: `wallets` table holds pre-computed live balance.
  - Complete audit trail: `wallet_transactions` holds immutable historical ledger.
  - Consistency: Both updated synchronously inside the same transaction.

---

## 5. Event-Driven Architecture & Transactional Outbox

### Technique 15: Transactional Outbox Pattern
- **Problem**: Dual-write hazard: committing balance updates to PostgreSQL while publishing directly to Kafka can drop events on server crashes.
- **Where in Code**:
  - [internal/service/settle_trade.go:L215-L235](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/settle_trade.go#L215-L235)
  - [internal/publisher/publisher.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/publisher/publisher.go)
- **How It Works**:
```text
                  TRANSACTIONAL OUTBOX PIPELINE

                      BEGIN TRANSACTION
                              │
                              ├──────────────────────────────┐
                              ▼                              ▼
                     Update Balances &              Insert Outbox Event
                     Write Ledger Row               status: 'PENDING'
                              │                              │
                              └──────────────┬───────────────┘
                                             │
                                             ▼
                                     COMMIT TRANSACTION
                                             │
                                             ▼
                                 Background Outbox Worker
                                 claims with lease & publishes
                                             │
                                             ▼
                                     Apache Kafka Topic
```

---

### Technique 16: Non-Blocking Work Queues (`FOR UPDATE SKIP LOCKED`)
- **Problem**: Multiple replicas of the outbox worker querying the table simultaneously cause lock contention and worker stalling.
- **Where in Code**:
  - [internal/repository/postgres/outbox_repository.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/repository/postgres/outbox_repository.go):
    ```sql
    SELECT id FROM outbox
    WHERE status = 'PENDING'
       OR (status = 'PROCESSING' AND claim_until < NOW())
    ORDER BY created_at ASC, id ASC
    LIMIT $1
    FOR UPDATE SKIP LOCKED;
    ```
- **How It Works**: Skips rows currently held by other workers without waiting, allowing multi-worker concurrency with zero lock contention.

---

### Technique 17: Deterministic FIFO Partition Ordering
- **Problem**: Events for a single user must not arrive out of order (e.g. order cancelled before order placed).
- **Where in Code**:
  - [migration/00006_outbox_ordering_index.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00006_outbox_ordering_index.sql): `CREATE INDEX idx_outbox_ordering ON outbox(created_at ASC, id ASC);`
  - [internal/publisher/publisher.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/publisher/publisher.go): Sets Kafka message Key to `user_id`, guaranteeing all events for a user route to the same Kafka partition.

---

### Technique 18: At-Least-Once Delivery Semantics
- **Problem**: In distributed systems, network acknowledgements can be lost.
- **Where in Code**:
  - [internal/publisher/publisher.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/publisher/publisher.go):
    Marks events `PUBLISHED` only after receiving Kafka delivery acknowledgement. If a crash occurs before marking, the event is re-claimed and published upon recovery. Downstream services use idempotency.

---

## 6. Background Worker Safety & Fencing

### Technique 19: Lease-Based Work Processing
- **Problem**: If an outbox worker claims an event and crashes (OOM, eviction), the event must not remain locked in `PROCESSING` forever.
- **Where in Code**:
  - [migration/00004_add_outbox_processing_and_lease.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00004_add_outbox_processing_and_lease.sql):
    ```sql
    claim_until = NOW() + INTERVAL '30 seconds'
    ```
- **How It Works**: When `claim_until < NOW()`, the lease has expired. Other workers automatically reclaim the orphaned event.

---

### Technique 20: Fencing Tokens (`claim_token UUID`)
- **Problem**: Worker A claims Event 1, suffers an extended GC pause, Worker B takes over after lease expiration and finishes. Worker A wakes up and attempts to update the row.
- **Where in Code**:
  - [migration/00010_outbox_claim_token.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00010_outbox_claim_token.sql)
  - [internal/repository/postgres/outbox_repository.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/repository/postgres/outbox_repository.go):
    ```sql
    UPDATE outbox
    SET status = 'PUBLISHED', published_at = NOW(), updated_at = NOW()
    WHERE id = $1 AND claim_token = $2;
    ```
- **How It Works**:
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

---

### Technique 21: Outbox Failure & Release Isolation
- **Problem**: Stale workers must not be allowed to release or fail claims currently owned by active newer workers.
- **Where in Code**:
  - [internal/repository/postgres/outbox_repository.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/repository/postgres/outbox_repository.go):
    `ReleaseClaim` and `MarkFailed` strictly enforce `WHERE id = $1 AND claim_token = $2`.

---

## 7. Software Architecture & Design Patterns

### Technique 22: Clean Architecture & Repository Pattern
- **Problem**: Tight coupling between SQL implementation and financial business rules makes testing difficult and leads to SQL leakage.
- **Where in Code**:
  - Service Layer: [internal/service/](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/) (pure business rules, repository interfaces only).
  - Repository Layer: [internal/repository/postgres/](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/repository/postgres/) (concrete SQL implementations).
- **How It Works**: The service layer can be 100% unit-tested with in-memory mocks without launching PostgreSQL.

---

### Technique 23: Transaction Manager & `DBTX` Abstraction
- **Problem**: Repository methods need to participate in shared database transactions without knowing whether a transaction is active.
- **Where in Code**:
  - [internal/repository/dbtx.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/repository/dbtx.go):
    ```go
    type DBTX interface {
        Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
        Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
        QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
    }
    ```
- **How It Works**: Every repository method accepts a `DBTX` or transaction context, allowing atomic multi-table execution across distinct repositories.

---

### Technique 24: Explicit Wallet Lifecycle
- **Problem**: Implicitly auto-creating accounts during deposits or reservations allows UUID typos to create ghost wallets.
- **Where in Code**:
  - [internal/service/deposit_funds.go:L77-L85](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/deposit_funds.go#L77-L85):
    If wallet row does not exist, returns `ErrWalletNotFound`. Wallets must be explicitly initialized via `InitializeWallet()`.

---

## 8. Validation & Defensive Engineering

### Technique 25: Defense in Depth (Service Validation + Database Constraints)
- **Problem**: Relying solely on application checks is vulnerable to race conditions; relying solely on DB constraints yields poor user-facing API errors.
- **Where in Code**:
  - Go Layer ([internal/service/reserve_funds.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/reserve_funds.go)): Formats clear domain errors like `ErrInsufficientFunds`.
  - Database Layer ([migration/00001_create_wallet_core_tables.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00001_create_wallet_core_tables.sql)): Acts as an un-bypassable consistency barrier.

---

### Technique 26: Asymmetric Security Gating (Frozen Wallet Policy B)
- **Problem**: Frozen wallets under fraud hold must not allow withdrawals, but blocking inbound credits strands user top-ups and trade settlements.
- **Where in Code**:
  - [internal/service/reserve_funds.go:L70](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/reserve_funds.go#L70): Blocks outgoing debits with `ErrWalletFrozen`.
  - [internal/service/deposit_funds.go:L86](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/deposit_funds.go#L86): Explicitly permits inbound credits into frozen wallets.

---

### Technique 27: Exact Decimal Arithmetic & Scientific Notation Rejection
- **Problem**: `float64` causes floating-point drift (e.g. `0.1 + 0.2 = 0.30000000000000004`), and scientific notation inputs like `"5e4"` introduce ambiguity.
- **Where in Code**:
  - [internal/service/deposit_funds.go:L50-L75](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/deposit_funds.go#L50-L75):
    - Uses `github.com/shopspring/decimal`.
    - Rejects scientific notation: `if depositAmount.Exponent() > 0 { return ErrInvalidDeposit }`.
    - Checks scale against asset limits (USDT $\le 10$ decimals).

---

### Technique 28: Defensive Asset Configuration & Enablement Flags
- **Problem**: Operations could be executed against paused, deprecated, or non-existent cryptocurrency assets.
- **Where in Code**:
  - [internal/service/deposit_funds.go:L61-L70](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/deposit_funds.go#L61-L70):
    Queries `supported_assets` to verify `is_enabled = TRUE`. Disabled assets reject deposits immediately.

---

### Technique 29: Top-Up Integration Extension (`reference_type = "TOPUP"`)
- **Problem**: Financial ledger entries must clearly delineate fiat checkout deposits from crypto bridge deposits.
- **Where in Code**:
  - [migration/00009_allow_topup_reference_type.sql](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00009_allow_topup_reference_type.sql):
    ```sql
    ALTER TABLE wallet_transactions DROP CONSTRAINT IF EXISTS wallet_transactions_reference_type_check;
    ALTER TABLE wallet_transactions ADD CONSTRAINT wallet_transactions_reference_type_check
        CHECK (reference_type IN (
            'INITIAL_ALLOCATION', 'RESERVATION', 'RELEASE',
            'SETTLEMENT', 'DEPOSIT', 'TOPUP', 'WITHDRAWAL'
        ));
    ```
  - [internal/repository/constants.go:L26](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/repository/constants.go#L26): Added `RefTopUp = "TOPUP"`.

---

## 🎯 Top 10 Architectural Highlights for System Design & Interviews

1. **Deterministic Lock Ordering**: Lexicographical sorting of wallet IDs before `SELECT ... FOR UPDATE` eliminates `40P01` deadlocks during multi-party settlement.
2. **Dual-Layer Financial Idempotency**: Scoped composite key `(wallet_id, reference_id, reference_type)` protecting against lost gRPC responses.
3. **Transactional Outbox Pattern**: Staging events in PostgreSQL within the same transaction as balances eliminates Kafka/DB dual-write hazards.
4. **Non-Blocking Work Queues (`SKIP LOCKED`)**: Multi-worker background consumers process batches without lock contention.
5. **Tokenized Lease Fencing**: `claim_token UUID` conditional updates prevent slow/paused workers from overwriting newer state.
6. **Storage-Layer Check Constraints**: `chk_wallet_total_balance` guaranteeing $\text{total} = \text{available} + \text{reserved}$ at the engine level.
7. **Two-Bucket Balance Architecture**: Allows order locking without modifying total user equity.
8. **Double-Entry Immutable Ledger**: Append-only `wallet_transactions` capturing historical before/after state.
9. **Defense in Depth**: Expressive domain errors in Go backed by un-bypassable PostgreSQL constraints.
10. **Asymmetric Security Gate (Policy B)**: Frozen wallets block outgoing debits while safely accepting inbound deposits and refunds.
