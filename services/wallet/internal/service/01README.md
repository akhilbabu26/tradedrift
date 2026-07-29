# Service Layer — `initialize_wallet.go` Walkthrough

> **What is the service layer?**
> The service layer contains all business logic. It receives a request (e.g. "initialize wallet for user X"), decides *what* to do and *in what order*, and delegates the actual database work to the repository layer. It knows the rules — the repositories just execute SQL.

---

## The Function: `InitializeWallet`

```go
func (s *Service) InitializeWallet(ctx context.Context, userID string) error
```

**Called by:** Auth Service — immediately after a user verifies their email.
**Purpose:** Create one wallet row per supported asset for this user, and seed USDT with a starting balance.
**Guarantee:** Idempotent — calling it twice for the same user never duplicates wallets or credits balance twice.

---

## Step-by-Step Function Breakdown

### Step 1 — `s.assetRepo.GetEnabled(ctx)`

```go
assets, err := s.assetRepo.GetEnabled(ctx)
```

**What it does:** Fetches the live list of enabled assets from the `supported_assets` table.

**Why dynamic, not hardcoded?**
If the code hardcoded `["USDT", "BTC", "ETH", "SOL"]`, then adding a new asset (e.g. AVAX) would require a code change and redeploy. Instead, `GetEnabled` reads the current list from the database. Adding AVAX is just an `INSERT` into `supported_assets` — no code touched.

**What it returns for TradeDrift:**
```
[USDT, BTC, ETH, SOL]  (in display_order: 1, 2, 3, 4)
```

---

### Step 2 — `s.walletRepo.GetByUserAndAsset(ctx, userID, asset.AssetCode)`

```go
existing, err := s.walletRepo.GetByUserAndAsset(ctx, userID, asset.AssetCode)
if existing != nil {
    continue  // already exists — skip
}
```

**What it does:** Checks if a wallet row already exists for this specific `(user_id, asset)` pair.

**Why this check?** This is the **idempotency guard**. `InitializeWallet` may be called:
- During normal registration (first call — creates wallets)
- As a retry if the first call timed out (second call — skips existing, only creates missing ones)
- In the future when a new asset is added to `supported_assets` (creates only the new asset's wallet for existing users)

The check is **per asset**, not per user. This is critical: if a new asset (AVAX) is added, re-running `InitializeWallet` for an existing user will skip USDT/BTC/ETH/SOL (already exist) and only create the AVAX wallet.

**Return behavior:**
- `nil, nil` → wallet does NOT exist → proceed to create it
- `&Wallet{}, nil` → wallet EXISTS → `continue` to next asset (skip)
- `nil, err` → database error → return error up the call chain

---

### Step 3 — `platformuuid.New()`

```go
walletID, err := platformuuid.New()
```

**What it does:** Generates a new **UUIDv7** — a universally unique identifier for the wallet row.

**Why UUIDv7, not UUID v4?**
UUIDv7 encodes a timestamp prefix, making it sortable by creation time. When you query wallets by `created_at` or by `id` range, UUIDv7 IDs sort naturally in chronological order. UUID v4 is random — no ordering guarantee.

**Why does the service generate the ID, not the database?**
The service generates the ID *before* the insert so it knows the wallet's ID immediately — without doing a round-trip to retrieve it after insertion. This ID is then passed to the ledger transaction row in Step 5.

---

### Step 4 — `s.walletRepo.Create(ctx, wallet)`

```go
wallet := &repository.Wallet{
    ID:               walletID,
    AvailableBalance: asset.SeedAmount,  // "10000.0000000000" for USDT, "0" for others
    ReservedBalance:  "0",
    InitialBalance:   asset.SeedAmount,
    TotalBalance:     asset.SeedAmount,
}
s.walletRepo.Create(ctx, wallet)
```

**What it does:** Inserts one row into the `wallets` table for this `(user, asset)` pair.

**Field details:**

| Field | Value | Why |
|---|---|---|
| `AvailableBalance` | `asset.SeedAmount` | Starts with the seed (10,000 USDT, 0 for others) |
| `ReservedBalance` | `"0"` | No orders placed yet — nothing reserved |
| `InitialBalance` | `asset.SeedAmount` | Snapshot of starting balance — used for PnL calculations later |
| `TotalBalance` | `asset.SeedAmount` | `available + reserved` — always equal to total holdings |
| `IsFrozen` | `false` | New accounts are not frozen |

**Why are balances strings?**
`DECIMAL(30,10)` precision. Go's `float64` cannot represent all decimal values exactly — `0.1 + 0.2 = 0.30000000000000004`. Using decimal strings passed to PostgreSQL avoids all floating-point rounding errors in financial calculations.

---

### Step 5 — The Seed Amount Check + `s.txnRepo.Create(ctx, txn)`

```go
txnID, err := platformuuid.New()

if asset.SeedAmount != "0" && asset.SeedAmount != "0.0000000000" {
    txn := &repository.WalletTransaction{
        ID:              txnID,
        WalletID:        walletID,
        ReferenceID:     userID,
        ReferenceType:   "INITIAL_ALLOCATION",
        TransactionType: "CREDIT",
        Asset:           asset.AssetCode,
        Amount:          asset.SeedAmount,
    }
    s.txnRepo.Create(ctx, txn)
}
```

**What it does:** If the asset has a non-zero seed amount (only USDT = 10,000), writes an **immutable ledger entry** recording where the balance came from.

**Why only USDT gets a transaction row?**
BTC, ETH, SOL start with 0. Writing a `CREDIT +0` ledger entry is meaningless noise. Only non-zero credits become ledger entries.

**Why write a ledger entry at all?**
The accounting invariant: every balance that exists must have a transaction row explaining where it came from. A user's USDT balance of 10,000 doesn't just appear — it starts with `INITIAL_ALLOCATION +10000 USDT`. A user's full balance history is always reconstructable from `wallet_transactions`.

**The `reference_type = "INITIAL_ALLOCATION"`** is the unique tag that identifies this transaction as a seeding event. Combined with the `UNIQUE(reference_id, reference_type, asset)` constraint on `wallet_transactions`, it ensures this entry can only ever be written once per `(user_id, asset)` combination — enforced at the database level.

---

### Step 6 — `s.log.Info(...)`

```go
s.log.Info("wallet initialized",
    zap.String("userID", userID),
    zap.String("asset", asset.AssetCode),
    zap.String("seedAmount", asset.SeedAmount),
)
```

**What it does:** Writes a structured JSON log line for every wallet created.

**Why structured logging?**
In production, logs are aggregated into systems like Datadog or ELK Stack. Structured fields (`userID`, `asset`, `seedAmount`) are searchable as individual attributes — not buried in a string. You can query: *"Show me all wallets initialized in the last hour"* or *"Did user X get their USDT wallet?"*.

**`s.log.Debug` vs `s.log.Info`:**
- `Debug` → wallet already existed (idempotent skip) — low-value, only visible in development
- `Info` → new wallet created — worth recording in production logs

---

## Complete Execution Flow

```
Auth Service calls InitializeWallet("user-123")
            │
            ▼
    GetEnabled()  →  [USDT, BTC, ETH, SOL]
            │
    ┌───────┴──────────────────────────────────────┐
    │  for each asset:                              │
    │                                               │
    │  GetByUserAndAsset(userID, "USDT")            │
    │    → nil (not found)                          │
    │                                               │
    │  platformuuid.New() → "018f-abc1..."          │
    │                                               │
    │  walletRepo.Create(wallet{USDT, 10000})       │
    │    → INSERT INTO wallets ...                  │
    │                                               │
    │  platformuuid.New() → "018f-abc2..."          │
    │  txnRepo.Create(INITIAL_ALLOCATION, +10000)   │
    │    → INSERT INTO wallet_transactions ...      │
    │                                               │
    │  log.Info("wallet initialized", USDT)         │
    │                                               │
    │  [repeat for BTC, ETH, SOL — no txn row      │
    │   because seed = 0]                           │
    └───────────────────────────────────────────────┘
            │
            ▼
    return nil  ← success
```

---

## Idempotency Summary

If `InitializeWallet` is called a second time for the same user:

```
GetByUserAndAsset(userID, "USDT") → &Wallet{} (found)  → continue (skip)
GetByUserAndAsset(userID, "BTC")  → &Wallet{} (found)  → continue (skip)
GetByUserAndAsset(userID, "ETH")  → &Wallet{} (found)  → continue (skip)
GetByUserAndAsset(userID, "SOL")  → &Wallet{} (found)  → continue (skip)
return nil  ← success, no duplicate wallets, no duplicate credits
```

A future new asset (AVAX, if added to `supported_assets`):
```
GetByUserAndAsset(userID, "USDT") → found → skip
GetByUserAndAsset(userID, "BTC")  → found → skip
GetByUserAndAsset(userID, "ETH")  → found → skip
GetByUserAndAsset(userID, "SOL")  → found → skip
GetByUserAndAsset(userID, "AVAX") → nil   → CREATE new wallet ✅
```
