# Feature 03: Wallet Controls (Freeze & Unfreeze)

## 1. What This Feature Does
The **Wallet Controls** subsystem provides granular, asset-specific risk mitigation for user wallets:
* **Freeze Wallet (`POST /api/v1/admin/users/{user_id}/wallets/{asset}/freeze`)**: Immediately locks financial debits and reservations for a specific asset (e.g. `BTC`) on a user's account while leaving their other currency wallets (e.g. `USDT`, `ETH`) fully operational.
* **Unfreeze Wallet (`POST /api/v1/admin/users/{user_id}/wallets/{asset}/unfreeze`)**: Restores normal deposit, withdrawal, and order reservation capabilities for the asset.

---

## 2. Why We Need It
Freezing an entire user account (User Suspension) is often too blunt when addressing asset-specific disputes:
1. **Granular Risk Mitigation**: If a user is suspected of a double-spend or unauthorized deposit in `BTC`, locking only `BTC` prevents them from withdrawing or trading the disputed asset while still allowing them to trade their legitimate `USDT` holdings.
2. **Double-Entry Ledger Integrity**: A naive wallet freeze that blocks all database operations would break in-flight trade clearing. If an order crossed on the matching engine right before the freeze, blocking `SettleTrade` would corrupt the exchange's balanced balance-sheet invariant.
3. **Safe Order Cancellations**: When a wallet is frozen, users must still be allowed to cancel existing open orders so locked funds return safely to `available_balance`.

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **HTTP Transport** | [`services/admin/internal/handler/admin_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/admin_handler.go#L103-L199) | `HandleFreezeWallet`, `HandleUnfreezeWallet` |
| **Asset Validation** | [`services/admin/internal/handler/validation.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/validation.go#L38-L45) | `ValidateAsset` (`^[A-Z0-9]{2,10}$`) |
| **Service Logic** | [`services/admin/internal/service/wallet_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/wallet_service.go#L50-L230) | `FreezeWallet`, `UnfreezeWallet`, in-flight reconciliation |
| **Wallet gRPC Client**| [`services/admin/internal/client/wallet_client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/wallet_client.go#L44-L64) | `FreezeWallet` gRPC transport call |
| **Wallet Service** | [`services/wallet/internal/service/freeze.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/freeze.go#L12-L44) | Updates `wallets` table in Wallet DB |
| **Reservation Guard**| [`services/wallet/internal/service/reserve_funds.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/reserve_funds.go#L93-L96) | Checks `wallet.IsFrozen` and returns `ErrWalletFrozen` |
| **Deposit Guard** | [`services/wallet/internal/service/deposit_funds.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/service/deposit_funds.go#L86-L88) | Rejects incoming deposits on frozen wallets |

---

## 4. How We Achieve This Feature

### 4.1 Strict Policy Matrix: Blocked vs. Allowed
When a wallet is frozen, the user's funds **stay in `available_balance`** (they are not deducted or moved away), but the Wallet Service enforces the following rules:

| Operation | Frozen Wallet Status | Technical Enforcement Point |
| :--- | :---: | :--- |
| **New Order (Sell BTC)** | ❌ **BLOCKED** | `ReserveFunds` Step 8 checks `wallet.IsFrozen` &rarr; returns `ErrWalletFrozen`. |
| **New Order (Buy with USDT)** | ❌ **BLOCKED** | If USDT wallet is frozen, `ReserveFunds` rejects quote asset reservation. |
| **Withdrawal / Transfer** | ❌ **BLOCKED** | Debits require reservation &rarr; rejected immediately. |
| **Deposit / Top-up** | ❌ **BLOCKED** | `DepositFunds` checks `wallet.IsFrozen` &rarr; returns `ErrWalletFrozen`. |
| **Order Cancellation** | ✅ **ALLOWED** | `ReleaseFunds` does **not** check freeze state, allowing locked funds to safely return to available balance. |
| **Trade Settlement** | ✅ **ALLOWED** | `SettleTrade` clearing allows crossed trades to settle without corrupting the double-entry ledger. |
| **Other Asset Wallets** | ✅ **ALLOWED** | If `BTC` is frozen, user's `USDT`, `ETH`, and `INR` wallets operate normally. |

### 4.2 In-Flight Reconciliation on Network Timeout
If the Admin Service commits the operation as `PROCESSING` and the gRPC call times out, a client retry with the same idempotency key executes **in-flight reconciliation**:
1. It detects the operation is already in `PROCESSING`.
2. It re-invokes the idempotent Wallet gRPC call using the existing `operation_id`.
3. If Wallet confirms the freeze, Admin marks the operation `COMPLETED` and returns success without duplicate side effects.

---

## 5. Execution Flow

```
                                 OPERATOR / POSTMAN
                                         │
                                         │ 1. POST /api/v1/admin/users/{id}/wallets/{asset}/freeze
                                         │    (Idempotency-Key + Bearer JWT)
                                         ▼
                      ┌──────────────────────────────────────┐
                      │             ADMIN HANDLER            │
                      │  - Validates User UUID               │
                      │  - Validates Asset Code (e.g. "BTC") │
                      │  - Validates Reason & Idempotency Key│
                      └──────────────────┬───────────────────┘
                                         │
                                         │ 2. FreezeWallet(ctx, req)
                                         ▼
                      ┌──────────────────────────────────────┐
                      │            WALLET SERVICE            │
                      │  Idempotency Check in DB             │
                      └──────────────────┬───────────────────┘
                                         │
                       ┌─────────────────┴─────────────────┐
                       │ Cached Operation Exists           │ Fresh Key
                       ▼                                   ▼
         ┌───────────────────────────┐       ┌───────────────────────────────────┐
         │ HTTP 200 OK               │       │ ExecAdminOperationTx (Atomic SQL) │
         │ Return Cached DTO         │       │ ├── admin_operations (PROCESSING) │
         │ (Zero duplicate mutation) │       │ ├── admin_audit_log (FREEZE)      │
         └───────────────────────────┘       │ └── admin_outbox (Outbox Event)   │
                                             └─────────────────┬─────────────────┘
                                                               │
                                                               │ 3. gRPC FreezeWallet()
                                                               ▼
                                             ┌───────────────────────────────────┐
                                             │    WALLET MICROSERVICE (:50052)   │
                                             │  - UPDATE wallets                 │
                                             │    SET is_frozen = true,          │
                                             │        frozen_at = NOW(),         │
                                             │        freeze_reason = ...        │
                                             │    WHERE user_id AND asset        │
                                             └─────────────────┬─────────────────┘
                                                               │
                                              Did Wallet gRPC respond successfully?
                                                               │
                                                ┌──────────────┴──────────────┐
                                                ▼                             ▼
                                               YES                            NO / TIMEOUT
                                 ┌─────────────────────────────┐ ┌─────────────────────────────┐
                                 │ UpdateOperationStatus()     │ │ Leave Status = PROCESSING   │
                                 │ - Operation -> COMPLETED    │ │ - Client retry reconciles   │
                                 │ - Return response struct    │ │   in-flight operation       │
                                 └──────────────┬──────────────┘ └──────────────┬──────────────┘
                                                │                               │
                                                └──────────────┬────────────────┘
                                                               │
                                                               │ 4. Return Operation DTO
                                                               ▼
                                 ┌─────────────────────────────────────────────┐
                                 │         HTTP 200 OK (Wallet Frozen)         │
                                 └─────────────────────────────────────────────┘
                                                               │
                                                               │ User submits trade/withdraw:
                                                               ▼
                                 ┌─────────────────────────────────────────────┐
                                 │          WALLET ENFORCEMENT ENGINE          │
                                 │  - ReserveFunds(BTC):                       │
                                 │    Checks wallet.IsFrozen == true           │
                                 │    -> Rejects: codes.FailedPrecondition     │
                                 │    ("wallet is frozen")                     │
                                 │  - SettleTrade / ReleaseFunds:              │
                                 │    Allowed to complete for ledger safety    │
                                 └─────────────────────────────────────────────┘
```
