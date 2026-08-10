# Wallet Service — Handler Package (`internal/handler`)

> **Package:** `tradedrift/services/wallet/internal/handler`  
> **Directory:** `services/wallet/internal/handler/`  
> **Role:** gRPC API Server Handler & Error Mapper

---

## 1. Purpose & Responsibilities

The `handler` package implements the generated `walletv1.WalletServiceServer` interface. It handles balance queries, fund reservations, fund releases, trade settlements, and asset listing calls.

---

## 2. Files in This Directory

| File | Role |
| :--- | :--- |
| [`handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/handler/handler.go) | gRPC server method implementations (`InitializeWallet`, `ReserveFunds`, `ReleaseFunds`, `SettleTrade`, `GetBalance`, etc.) |
| [`mapper.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/wallet/internal/handler/mapper.go) | Error status mapping utility (`mapToGRPCError`) converting domain errors to gRPC codes |

---

## 3. Handled gRPC Endpoints

- `InitializeWallet(ctx, req)` $\rightarrow$ Seeds default initial wallet balances for new traders
- `ReserveFunds(ctx, req)` $\rightarrow$ Locks available funds for open orders
- `ReleaseFunds(ctx, req)` $\rightarrow$ Unlocks reserved funds upon order cancellation
- `SettleTrade(ctx, req)` $\rightarrow$ Atomic multi-asset ledger transfer between buyer and seller
- `GetBalance(ctx, req)` $\rightarrow$ Fetches available & reserved balance for a specific asset
- `GetBalances(ctx, req)` $\rightarrow$ Lists all asset balances for a user
- `GetSupportedAssets(ctx, req)` $\rightarrow$ Lists platform-supported trading assets (BTC, USDT, ETH)
- `Health(ctx, req)` $\rightarrow$ Service health check ping
