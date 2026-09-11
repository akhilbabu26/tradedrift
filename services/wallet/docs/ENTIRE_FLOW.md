# TradeDrift Wallet Service — Complete Operational & Architectural Master Guide (`ENTIRE_FLOW.md`)

This document provides a comprehensive, production-grade breakdown of every flow, decision boundary, transaction lifecycle, concurrency guard, and failure recovery path across the **Core Wallet Service** (`services/wallet`).

---

## Master Architecture Flowchart

```text
                    CLIENT / INTERNAL SERVICE
                              │
                              ▼
                    ┌──────────────────┐
                    │  Wallet Service  │
                    └────────┬─────────┘
                             │
              ┌──────────────┼───────────────┬────────────────┐
              │              │               │                │
              ▼              ▼               ▼                ▼
        Initialize       Reserve/Release   Deposit          Settle
          Wallet             Funds          Funds           Trade
              │              │               │                │
              └──────────────┼───────────────┴────────────────┘
                             │
                             ▼
                        PostgreSQL
                             │
                ┌────────────┼────────────┐
                │            │            │
                ▼            ▼            ▼
             Wallet       Ledger       Outbox
             Balance     Transaction      Event
                │            │            │
                └────────────┼────────────┘
                             │
                           COMMIT
                             │
                             ▼
                       Outbox Worker
                             │
                       claim + lease
                        claim_token
                             │
                             ▼
                           Kafka
```

---

## Core Financial Axioms & Invariants

1. **Balance Consistency Equation**:
   $$\text{total\_balance} = \text{available\_balance} + \text{reserved\_balance}$$
   $$\text{available\_balance} \ge 0 \quad \text{and} \quad \text{reserved\_balance} \ge 0$$
   Enforced at the storage layer via PostgreSQL check constraints:
   - `chk_wallet_total_balance`: `CHECK (total_balance = available_balance + reserved_balance)` ([migration 00008](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00008_wallet_total_balance_check.sql))
   - `chk_wallets_available_nonneg`: `CHECK (available_balance >= 0)`
   - `chk_wallets_reserved_nonneg`: `CHECK (reserved_balance >= 0)`

2. **Strict Double-Entry Bookkeeping**:
   Balances are never updated in isolation. Every single balance modification produces an immutable audit record in the `wallet_transactions` ledger table.

3. **Single Database Transaction Boundaries**:
   Financial state changes (wallet balance updates, reservation states, ledger insertions, and transactional outbox event staging) are executed within **one atomic PostgreSQL transaction** (`BEGIN ... COMMIT`).

4. **Zero Dual-Write Anomaly (Transactional Outbox)**:
   The Wallet Service never performs dual-writes to Apache Kafka and PostgreSQL. Outbox records are staged atomically with the balance change, ensuring zero event loss.

5. **Deterministic Lock Ordering**:
   During multi-party settlement, up to 4 wallet rows are locked simultaneously. All wallet IDs are sorted lexicographically before acquiring row locks (`ORDER BY id FOR UPDATE`), mathematically eliminating database deadlocks (`40P01`).

---

## 1. Wallet Initialization Flow

Triggered when a new user registers and passes identity verification in the Auth Service. Auth Service synchronously calls `WalletService.InitializeWallet()`.

```text
                        USER REGISTRATION
                               │
                               ▼
                         User Verified
                               │
                               ▼
                    InitializeWallet(userID)
                               │
                               ▼
                     PostgreSQL Transaction
                               │
                ┌──────────────┴──────────────┐
                │ 1. Lock/Verify User Wallets │
                │ 2. Create Wallet Rows:      │
                │    - USDT (Seed: 10,000.00) │
                │    - BTC  (Seed: 0.00)      │
                │    - ETH  (Seed: 0.00)      │
                │    - SOL  (Seed: 0.00)      │
                │ 3. Record Ledger:           │
                │    ref_type:                │
                │      INITIAL_ALLOCATION     │
                │ 4. Stage Outbox Event       │
                └──────────────┬──────────────┘
                               │
                               ▼
                             COMMIT
                               │
                               ▼
                          Wallet Ready
                   available: 10,000.00 USDT
                   reserved:       0.00 USDT
                   total:     10,000.00 USDT
```

### Key Guarantees:
- **Seed Amount**: USDT wallet is automatically credited with 10,000 USDT to allow immediate trading.
- **Idempotency**: If `InitializeWallet()` is called again for the same user, it detects existing wallets and returns existing balances without re-seeding funds.

---

## 2. Deposit / Top-Up Flow (`wallet-topup` Integration)

The **Wallet Top-Up Service** (`wallet-topup`) delegates all financial crediting to the Wallet Service via gRPC.

```text
                    Wallet Top-Up Service
                              │
                              │ gRPC DepositFunds()
                              │ • userID: <uuid>
                              │ • asset: "USDT"
                              │ • amount: "5000.0000000000"
                              │ • reference_id: <topup_order_id>
                              │ • reference_type: "TOPUP"
                              ▼
                        Wallet Service
```

The Wallet Service enforces that:
- Top-up funds are credited strictly in whole or exact decimals.
- The reference type is explicitly `'TOPUP'` (enabled via [Migration 00009](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00009_allow_topup_reference_type.sql)).

---

## 3. `DepositFunds` Validation Flow

In [internal/service/deposit_funds.go:L36-L85](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/deposit_funds.go#L36-L85), the Wallet Service runs strict input validation before opening any transaction:

```text
                       DepositFunds(req)
                               │
                               ▼
                ┌─────────────────────────────┐
                │ 1. User ID Present?         │
                │ 2. Asset Supported & Active?│
                │ 3. Amount > 0?              │
                │ 4. Plain Decimal Notation?  │
                │    (Rejects "5e4")          │
                │ 5. Decimal Scale <= Decimals│
                │    (USDT max scale: 10)     │
                │ 6. Reference ID & Type Set? │
                └──────────────┬──────────────┘
                               │
                ┌──────────────┴──────────────┐
                │                             │
             INVALID                        VALID
                │                             │
                ▼                             ▼
       Return InvalidDeposit         Check Wallet Exists
       (HTTP / gRPC InvalidArg)     (Must be Initialized)
```

---

## 4. Deposit Idempotency Flow

Networks are unreliable. If the Top-Up Service reconciler calls `DepositFunds()`, the credit succeeds, but the network connection drops before the HTTP/2 response is received:

```text
                  DEPOSIT IDEMPOTENCY BOUNDARY

           Top-Up Reconciler                    Wallet Service
                   │                                   │
                   │ RPC #1: DepositFunds(topup_123)   │
                   ├──────────────────────────────────>│
                   │                                   │ Credits +5,000 USDT
                   │                                   │ Inserts (wallet, topup_123, TOPUP)
                   │      x Network Drop / Timeout     │
                   │< - - - - - - - - - - - - - - - - -┤
                   │                                   │
                   │ RPC #2: Retry Deposit(topup_123)  │
                   ├──────────────────────────────────>│
                   │                                   │ Sees existing:
                   │                                   │ (wallet, topup_123, TOPUP)
                   │ 200 OK (Existing Txn Returned)    │ Zero second credit!
                   │<──────────────────────────────────┤
                   ▼                                   ▼
        Final Balance: +5,000 USDT           NO DUPLICATE INFLATION
```

### Uniqueness Boundary:
$$\text{UNIQUE}(\text{wallet\_id}, \text{reference\_id}, \text{reference\_type})$$
- `RPC #1` $\longrightarrow$ +5,000 USDT credited (Balance: 15,000 USDT).
- `RPC #2` $\longrightarrow$ Duplicate detected; returns transaction receipt. Balance stays 15,000 USDT.
- `RPC #3` $\longrightarrow$ Duplicate detected; returns transaction receipt. Balance stays 15,000 USDT.

---

## 5. Actual Wallet Credit Transaction

In [internal/service/deposit_funds.go:L114-L185](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/deposit_funds.go#L114-L185):

```text
                      BEGIN TRANSACTION
                              │
                              ▼
                     Lock Wallet Row:
                SELECT FOR UPDATE FROM wallets
                              │
                              ▼
                Layer 2 DB Idempotency Check
                (Guards against concurrent races)
                              │
                              ▼
                available_balance += amount
                total_balance     += amount
                              │
                              ▼
                INSERT INTO wallet_transactions
                (wallet_id, ref_id, "TOPUP", CREDIT)
                              │
                              ▼
                INSERT INTO outbox
                (event_type: "WalletCredited")
                              │
                              ▼
                      COMMIT TRANSACTION
```

Both the balance mutation and financial ledger row commit atomically.

---

## 6. Wallet Balance Model

The Wallet Service uses a strict two-bucket model:

$$\text{total\_balance} = \text{available\_balance} + \text{reserved\_balance}$$

### Example 1: Top-Up Deposit (+5,000 USDT)
| Balance Bucket | Before Top-Up | Mutation | After Top-Up |
| :--- | :--- | :--- | :--- |
| **Available** | 10,000.00 | $+5,000.00$ | **15,000.00** |
| **Reserved** | 0.00 | $0.00$ | **0.00** |
| **Total** | 10,000.00 | $+5,000.00$ | **15,000.00** |

### Example 2: Placing an Order (Reserve 2,000 USDT)
| Balance Bucket | Before Order | Mutation | After Order |
| :--- | :--- | :--- | :--- |
| **Available** | 15,000.00 | $-2,000.00$ | **13,000.00** |
| **Reserved** | 0.00 | $+2,000.00$ | **2,000.00** |
| **Total** | 15,000.00 | $0.00$ | **15,000.00** |

*Note: Total balance does not change when reserving or releasing funds.*

---

## 7. Wallet Transaction Ledger Flow

Every financial mutation creates an immutable audit row in `wallet_transactions`:

| Column | Value in Top-Up | Value in Reservation | Value in Settlement |
| :--- | :--- | :--- | :--- |
| `transaction_type` | `CREDIT` | `DEBIT` (from available) | `CREDIT` / `DEBIT` |
| `reference_type` | `TOPUP` | `RESERVATION` | `SETTLEMENT` |
| `reference_id` | `topup_order_uuid` | `order_uuid` | `trade_uuid` |
| `amount` | 5,000.00 | 2,000.00 | 2,000.00 |
| `balance_before` | 10,000.00 | 15,000.00 | 13,000.00 |
| `balance_after` | 15,000.00 | 13,000.00 | 13,000.00 |

This guarantees complete financial auditability and enables point-in-time balance reconstruction.

---

## 8. Transactional Outbox Pattern

To prevent the **dual-write hazard** (database commits, but Kafka publish fails, or vice versa):

```text
                  TRANSACTIONAL OUTBOX ATOMICITY

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
                        ┌────────────────────┴────────────────────┐
                        ▼                                         ▼
            Database State Guaranteed                  Event Persisted in DB
            (Cannot be lost on crash)                  (Durable background queue)
```

---

## 9. Outbox Publisher Pipeline

In [internal/publisher/publisher.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/publisher/publisher.go):

```text
               OUTBOX PUBLISHER BACKGROUND WORKER

                    Ticker (every 100ms)
                             │
                             ▼
                ┌─────────────────────────┐
                │ 1. Claim Batch          │
                │    SELECT FOR UPDATE    │
                │    SKIP LOCKED          │
                │    WHERE status=PENDING │
                │    OR lease expired     │
                └────────────┬────────────┘
                             │
                             ▼
                ┌─────────────────────────┐
                │ 2. Stamp Worker Lease   │
                │    claim_token = UUID_1 │
                │    claim_until = +30s   │
                │    status = PROCESSING  │
                └────────────┬────────────┘
                             │
                             ▼
                ┌─────────────────────────┐
                │ 3. Publish to Kafka     │
                │    Key: UserID (FIFO)   │
                └────────────┬────────────┘
                             │
              ┌──────────────┴──────────────┐
              │                             │
           SUCCESS                        ERROR
              │                             │
              ▼                             ▼
   ┌──────────────────────┐      ┌──────────────────────────┐
   │ MarkPublished()      │      │ ReleaseClaim()           │
   │ WHERE                │      │ Reset status = PENDING   │
   │ claim_token = UUID_1 │      │ (Retried next cycle)     │
   │ status = PUBLISHED   │      └──────────────────────────┘
   └──────────────────────┘
```

---

## 10. Outbox Claim Leases & Tokenization

When multiple replicas of the Wallet Service run in Kubernetes:
- Worker 1 claims Event 1 and stamps `claim_token = UUID_A` and `claim_until = NOW() + 30s`.
- Worker 2 scans the outbox table concurrently using `FOR UPDATE SKIP LOCKED`.
- Worker 2 skips Event 1 without blocking and claims Event 2 with `claim_token = UUID_B`.

---

## 11. Outbox Fencing (Stale Worker Protection)

Enabled by [Migration 00010 (`00010_outbox_claim_token.sql`)](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00010_outbox_claim_token.sql):

```text
                 OUTBOX STALE WORKER FENCING

       Worker A (Pod 1)                        Worker B (Pod 2)
              │                                       │
              │ Claims Event 1                        │
              │ claim_token = UUID_A                  │
              │ lease = 30s                           │
              ▼                                       │
      Worker A pauses 45s                             │
      (GC pause / CPU freeze)                         │
              │                                       │
              │ ─── 30s lease expires ───             │
              │                                       ▼
              │                               Claims Event 1
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

## 12. Outbox Publish Success & Terminal State

Once Kafka confirms delivery via `ProduceChannel` acknowledgement:
```sql
UPDATE outbox
SET status = 'PUBLISHED',
    published_at = NOW(),
    updated_at = NOW()
WHERE id = $1 AND claim_token = $2;
```
The event transitions permanently to `PUBLISHED`.

---

## 13. Outbox Failure & Exponential Backoff

If Kafka is temporarily down or partition leaders are re-electing:
1. Publisher catches the error.
2. Calls `ReleaseClaim(ctx, eventID, token)`.
3. Sets `status = 'PENDING'` and `claim_until = NOW()`, releasing the lease immediately so that retry happens without delay once Kafka recovers.
4. If an event exceeds maximum retry attempts (e.g. malformed serialization), it is marked `FAILED` and raises an alert.

---

## 14. Reserve Funds Flow (Order Placement)

When a user places an order on the exchange, Order Service calls `WalletService.ReserveFunds()`.

```text
                  RESERVE FUNDS WORKFLOW

                   Order Service Request:
                   Reserve 2,000 USDT for Order #101
                               │
                               ▼
                    ┌─────────────────────┐
                    │ Available >= 2,000? │
                    └──────────┬──────────┘
                               │
                ┌──────────────┴──────────────┐
                │                             │
              FALSE                          TRUE
                │                             │
                ▼                             ▼
       ErrInsufficientFunds          available -= 2,000
       (HTTP 422 / gRPC Failed)      reserved  += 2,000
                                     total     = unchanged
                                              │
                                              ▼
                                     Record RESERVATION
                                     in wallet_reservations
                                              │
                                              ▼
                                     Record RESERVATION
                                     in wallet_transactions
                                              │
                                              ▼
                                     Stage Outbox Event
                                              │
                                              ▼
                                           COMMIT
```

---

## 15. Reserve Transaction Guarantees

In [internal/service/reserve_funds.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/reserve_funds.go):
- **Idempotency**: If the same `order_id` is reserved again with the same parameters, `ReserveFunds` returns the existing reservation.
- **Conflict Rejection**: If the same `order_id` is reused with a different user or amount, it rejects with `ErrReservationConflict`.
- **Database Lock**: Locks the wallet row with `FOR UPDATE` to prevent concurrent orders from double-spending available balance.

---

## 16. Release Funds Flow (Order Cancellation)

When an order is cancelled or expires unfilled, Order Service calls `WalletService.ReleaseFunds(orderID)`.

```text
                  RELEASE FUNDS WORKFLOW

                   Order Service Request:
                   Release Order #101 (2,000 USDT)
                               │
                               ▼
                ┌───────────────────────────────┐
                │ Fetch Reservation by OrderID  │
                │ Status must be ACTIVE or      │
                │ PARTIALLY_CONSUMED            │
                └──────────────┬────────────────┘
                               │
                               ▼
                ┌───────────────────────────────┐
                │ Return Remaining Amount:      │
                │ reserved_balance  -= 2,000    │
                │ available_balance += 2,000    │
                │ total_balance      = unchanged│
                └──────────────┬────────────────┘
                               │
                               ▼
                ┌───────────────────────────────┐
                │ Update Reservation:           │
                │ status = 'RELEASED'           │
                └──────────────┬────────────────┘
                               │
                               ▼
                ┌───────────────────────────────┐
                │ Insert RELEASE Ledger Entry   │
                │ Stage Outbox Event            │
                │ COMMIT TRANSACTION            │
                └───────────────────────────────┘
```

---

## 17. Trade Settlement Flow

When the Matching Engine matches a buyer and a seller, Settlement Service calls `WalletService.SettleTrade()`.

```text
               MATCHING ENGINE / TRADE EXECUTION
                               │
                               │ SettleTrade()
                               │ • trade_id
                               │ • market_id (e.g. "BTC-USDT")
                               │ • sequence (e.g. 1042)
                               │ • buyer_id, seller_id
                               │ • buyer_order_id, seller_order_id
                               │ • base_amount (BTC), quote_amount (USDT)
                               │ • price
                               ▼
                         Wallet Service
```

---

## 18. Settlement Idempotency & Sequence Integrity

In [internal/service/settle_trade.go:L55-L95](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/settle_trade.go#L55-L95):

The service checks `settled_trades` table:
```sql
SELECT trade_id, market_id, sequence FROM settled_trades WHERE trade_id = $1 FOR UPDATE;
```
1. **Identical Retry**: Same `trade_id`, same `market_id`, same `sequence` $\to$ Returns existing success receipt (idempotent no-op).
2. **Metadata Conflict**: Same `trade_id` with different parameters $\to$ Rejects with `ErrSettlementConflict`.
3. **Sequence Collision**: Different `trade_id` attempting to use an already committed sequence $\to$ Rejects via `UNIQUE (market_id, sequence)` constraint ([Migration 00007](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/migration/00007_settled_trades_unique_sequence.sql)).

---

## 19. Deterministic 4-Wallet Lock Ordering

During trade settlement, 4 distinct wallet accounts must be updated:
1. Buyer Quote Wallet (USDT)
2. Buyer Base Wallet (BTC)
3. Seller Quote Wallet (USDT)
4. Seller Base Wallet (BTC)

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

Because both concurrent trades acquire locks in the exact same order ($W1 \to W2 \to W3 \to W4$), **deadlocks are mathematically impossible**!

---

## 20. Settlement Balance Movement Matrix

Inside the single PostgreSQL transaction, funds move across the 4 wallets:

```text
                     SETTLEMENT BALANCE MOVEMENT

                 BUYER                               SELLER
     ┌───────────────────────────┐       ┌───────────────────────────┐
     │ Quote Wallet (USDT):      │       │ Quote Wallet (USDT):      │
     │ reserved -= quote_amount  │       │ available += quote_amount │
     │ total    -= quote_amount  │       │ total     += quote_amount │
     ├───────────────────────────┤       ├───────────────────────────┤
     │ Base Wallet (BTC):        │       │ Base Wallet (BTC):        │
     │ available += base_amount  │       │ reserved  -= base_amount  │
     │ total     += base_amount  │       │ total     -= base_amount  │
     └───────────────────────────┘       └───────────────────────────┘
```

Both buyer and seller portfolios remain balanced; money is neither created nor destroyed.

---

## 21. Settlement Reservation Consumption

The buyer already reserved the quote funds upon order placement, and the seller already reserved the base asset.
Settlement consumes the reservation:
```sql
UPDATE wallet_reservations
SET consumed_amount  = consumed_amount + $1,
    remaining_amount = remaining_amount - $1,
    status = CASE WHEN remaining_amount - $1 = 0 THEN 'CONSUMED' ELSE 'PARTIALLY_CONSUMED' END
WHERE order_id = $2;
```

---

## 22. Settlement Validation Pipeline

In [internal/service/settle_trade_validate.go](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/settle_trade_validate.go), all conditions are checked:
- Buyer and seller are distinct users (self-trading prevented unless MM).
- Base and quote amounts match the price: $|\text{price} \times \text{base} - \text{quote}| \le \text{tolerance}$.
- Precision does not exceed asset limits.
- Buyer has sufficient reserved quote currency.
- Seller has sufficient reserved base currency.

If any check fails, the transaction calls `ROLLBACK`.

---

## 23. Settlement + Outbox Atomicity

```text
            ATOMIC SETTLEMENT TRANSACTION BOUNDARY

                      BEGIN TRANSACTION
                              │
                              ▼
                 Lock 4 Wallets (Sorted Order)
                              │
                              ▼
                 Consume Buyer Quote Reservation
                 Consume Seller Base Reservation
                              │
                              ▼
                 Credit Buyer Base Available
                 Credit Seller Quote Available
                              │
                              ▼
                 Insert 4 Double-Entry Ledger Rows
                              │
                              ▼
                 Insert settled_trades Record
                              │
                              ▼
                 Insert Outbox Event:
                 "TradeSettled" & "PortfolioUserTrade"
                              │
                              ▼
                      COMMIT TRANSACTION
```

---

## 24. Settlement Failure & Rollback

If any step fails (e.g. buyer reservation already released due to race condition, or database constraint violation):
- PostgreSQL executes `ROLLBACK`.
- Zero wallet balances change.
- No ledger rows are written.
- No outbox events are published.
- Entire system returns to the clean, pre-trade state.

---

## 25. Concurrent Settlement Flow

When multiple trades execute against overlapping market makers:
- Transaction 1 and Transaction 2 queue sequentially on the sorted wallet locks.
- Neither transaction fails or deadlocks.
- The outbox publisher streams events to Kafka in sequential order partitioned by `user_id`.

---

## 26. Frozen Wallet Invariant & Policy (Policy B)

In TradeDrift, frozen wallets enforce **Policy B**:
- **Outgoing Debits Blocked**: `ReserveFunds` rejects frozen wallets with `ErrWalletFrozen`. A user under investigation or fraud hold cannot lock funds to trade or withdraw.
- **Incoming Credits Permitted**: `DepositFunds` (Top-Ups) and trade settlement credits succeed. Refunds and deposits are safely received.

---

## 27. Read-Only Query Flows

### `GetBalance(userID, asset)`
- Queries `wallets` table by `(user_id, asset)`.
- Returns available, reserved, and total balance formatted to exact decimal precision.

### `GetBalances(userID)`
- Queries all asset wallets for a user.
- Used by the API Gateway to render the user's unified asset dashboard.

### `GetSupportedAssets()`
- Queries `supported_assets` where `is_enabled = TRUE`.
- Returns tradeable assets, display ordering, and precision metadata.

---

## 28. Top-Up $\to$ Wallet Complete Integration

Combining the **Wallet Top-Up Service** and **Core Wallet Service**:

```text
                    USER
                      │
                      ▼
               Top-Up Service
                      │
              Reserve ₹5 quota
                      │
                      ▼
              Payment Provider
                      │
                 User pays
                      │
                      ▼
                  Webhook
                      │
              Payment confirmed
                      │
                      ▼
               CREDIT_PENDING
                      │
                 Reconciler
                      │
             claim + fencing
                      │
                      ▼
             Wallet Service
                      │
                DepositFunds
                      │
              idempotency check
                      │
                      ▼
              Lock Wallet
                      │
                      ▼
             Credit USDT
                      │
                      ├──────────────┐
                      ▼              ▼
                   Ledger         Outbox
                      │              │
                      └──────┬───────┘
                             ▼
                           COMMIT
                             │
                             ▼
                     Deposit success
                             │
                             ▼
                    Top-Up COMPLETED
```

---

## Summary of Core Architectural Patterns in Wallet Service

| Pattern | Where Used | Problem Solved |
| :--- | :--- | :--- |
| **PostgreSQL ACID Transactions** | Balance + Ledger + Outbox | Eliminates partial financial mutations |
| **Dual-Layer Idempotency** | `DepositFunds` & `SettleTrade` | Replay attacks, network retry safety |
| **Deterministic Lock Ordering** | Multi-wallet settlement | Eliminates `40P01` database deadlocks |
| **Pessimistic Row Locking** | `SELECT ... FOR UPDATE` | Concurrent double-spend prevention |
| **Transactional Outbox** | Staged Kafka events | Zero dual-write discrepancies |
| **Tokenized Worker Leases** | Outbox publisher | Crash recovery & lease expiration |
| **Fencing Tokens** | `claim_token UUID` updates | Stale worker split-brain defense |
| **Database-Level Invariants** | CHECK constraints | Balance corruption prevention at engine level |
| **Double-Entry Ledger** | `wallet_transactions` | Immutable financial audit trail |
| **At-Least-Once Delivery** | Outbox to Kafka | Guaranteed event delivery |
| **FIFO Partition Ordering** | Kafka Key = `user_id` | Preserves chronological event order per user |
