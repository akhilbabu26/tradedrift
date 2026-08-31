# `internal/walletservice` — Wallet Service gRPC Client

**Package:** `walletservice`  
**Service:** Liquidity Engine  
**Last Updated:** August 2026

---

## 1. What This Package Does

This package provides a **read-only gRPC client** for the Wallet Service (`tradedrift/platform/api/gen/wallet/v1`).

The Liquidity Engine uses it to:
1. **Fetch Authoritative Balances**: Query `GetBalances` for the `MM-001` wallet UUID (`00000000-0000-0000-0000-000000000001`) at engine startup and periodically (`WalletRefreshInterval`, default 5m).
2. **Health Verification**: Check if the Wallet Service gRPC endpoint is reachable.

---

## 2. Invariant: Read-Only Balance Discovery

> [!IMPORTANT]
> The Liquidity Engine **never modifies wallet balances or reserves funds directly** via gRPC.
> - The LE does **not** call `ReserveFunds` or `ReleaseFunds`.
> - The LE computes its own *effective available inventory* by subtracting resting MM orders from the wallet's `available_balance`.
> - All wallet balance adjustments for MM trades happen asynchronously via Matching Engine trade fills and Settlement Service.
