# Liquidity Engine — Identity, Wallet & Treasury

> **Service:** `services/liquidity-engine`
> **Version:** V1.1

---

## MM Account Model

```sql
-- accounts table entry
INSERT INTO accounts (id, type, role, status) VALUES
  ('MM-001', 'SYSTEM', 'MARKET_MAKER', 'ACTIVE');
```

### Permissions

```
MARKET_MAKER
    ├── create orders
    ├── cancel own orders
    ├── view own orders
    ├── view market data
    └── view own balances

    ❌ manage users
    ❌ withdraw funds
    ❌ modify exchange configuration
    ❌ access admin APIs
```

The MM account has **no admin privileges**. It is a first-class trading account with a special role.

---

## MM Wallet

```
MM-001 Wallet

BTC   = 100
ETH   = 500
SOL   = 5,000
USDT  = 10,000,000
```

### The Wallet Immutability Rule

> **The Liquidity Engine MUST NEVER directly modify MM wallet balances.**

This is the single most important constraint on the entire service.

The complete, unidirectional asset flow is:

```
                 System Treasury
                       |
                       | replenish (via Wallet/Settlement)
                       v
                    MM-001
                       |
                 owns balances
                       |
                       v
              Liquidity Engine
                       |
                 creates orders only
                 (Kafka -> orders.commands)
                       v
               Matching Engine
                       |
                 TradeExecuted
                 (Kafka -> trades.executed)
                       v
                 Settlement
                       |
                       v
              User Wallet <-> MM Wallet
```

**What the Liquidity Engine may do:**
- ✅ Read MM balances from the Wallet Service gRPC API (read-only)
- ✅ Use balance information to determine inventory levels
- ✅ Request replenishment by sending a signal to the Treasury module

**What the Liquidity Engine must never do:**
- ❌ Execute `UPDATE wallet_balances SET amount = ...`
- ❌ Call any settlement or ledger write API directly
- ❌ Bypass the order → match → settle pipeline for any asset movement

---

## System Treasury

A dedicated system treasury for the development environment:

```
SYSTEM TREASURY

BTC   =   1,000
ETH   =   5,000
SOL   =  50,000
USDT  = $100,000,000
```

**Hierarchy:**

```
Treasury
    │
    │ funds / replenishes
    ▼
MM-001 Wallet
    │
    │ provides two-sided liquidity
    ▼
Users
```

The Treasury is **not** the MM. They are distinct entities with distinct roles.

> ⚠️ The Liquidity Engine never directly edits wallet balances. Replenishment requests are
> routed through the Settlement service, which applies the actual balance change.
