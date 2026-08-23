# TradeDrift — Settlement Service (`services/settlement`)

> **Service:** Trade Settlement Service  
> **Directory:** `services/settlement/`  
> **Database:** PostgreSQL (`tradedrift_settlement`)  
> **Message Bus:** Apache Kafka (Consumer on topic `trades.executed`)  
> **Wallet gRPC:** `wallet:50052`  
> **Role:** Consumes matched trade events from the Matching Engine and calls the Wallet Service to atomically transfer reserved funds between buyer and seller.

---

## 1. Executive Summary & Core Purpose

The **Settlement Service** is the transactional clearinghouse of the TradeDrift platform. After two orders are matched by the Matching Engine, this service:

1. Consumes the `TradeExecuted` event from the `trades.executed` Kafka topic.
2. Records the trade in a local `settled_trades` ledger with idempotency protection.
3. Calls `WalletService.SettleTrade` over gRPC to atomically shift reserved funds.
4. Marks the trade as `SETTLED` in the ledger and acknowledges the Kafka offset.

**This service publishes no Kafka events.** All downstream notification (`TradeSettled`) is the exclusive responsibility of the Wallet Service's own outbox, which fires after `SettleTrade` commits.

---

## 2. System Architecture & Data Flow

```
┌──────────────────────────────────────┐
│         Matching Engine              │
│    (Executes Buy & Sell Orders)      │
└──────────────────┬───────────────────┘
                   │ Publishes TradeExecuted
                   ▼
┌──────────────────────────────────────┐
│       Kafka: trades.executed         │
└──────────────────┬───────────────────┘
                   │ Consumes
                   ▼
┌──────────────────────────────────────┐
│        Settlement Service            │
│                                      │
│  ① Idempotency Check                 │
│  ② INSERT settled_trades (PENDING)   │   ← Phase 1 (Short DB TX)
│  ③ Wallet.SettleTrade (gRPC)         │   ← Phase 2 (No DB conn held)
│  ④ UPDATE settled_trades (SETTLED)   │   ← Phase 3 (Short DB TX)
│  ⑤ ACK Kafka Offset                  │
│                                      │
│  DB: tradedrift_settlement           │
└──────────────────┬───────────────────┘
                   │ gRPC
                   ▼
┌──────────────────────────────────────────────────────┐
│                 Wallet Service                        │
│  Moves: Buyer reserved USDT → Seller available USDT  │
│  Moves: Seller reserved BTC  → Buyer  available BTC  │
│  Publishes TradeSettled via its own outbox →         │
│  → Portfolio, Notification services                  │
└──────────────────────────────────────────────────────┘
```

---

## 3. The 3-Phase Settlement Pipeline

```
TradeExecuted consumed from Kafka
              │
              ▼
  ┌── IDEMPOTENCY CHECK ─────────────────────────────┐
  │  SELECT status FROM settled_trades               │
  │  WHERE trade_id = ?                              │
  └──────────────────────────────────────────────────┘
              │
    ┌─────────┼──────────────────┐
    │         │                  │
 SETTLED   PENDING           Not Found
    │         │                  │
   ACK        │   ┌── PHASE 1 ───────────────────────┐
  (no-op)     │   │ INSERT settled_trades (PENDING)   │
              │   │ ON CONFLICT (trade_id) DO NOTHING │
              │   │ COMMIT ← DB connection released   │
              │   └──────────────────────────────────┘
              │                  │
              └──────────────────┘
                        │
           ┌── PHASE 2 (No DB conn held) ────────┐
           │ gRPC: Wallet.SettleTrade(trade_id)   │
           └─────────────────────────────────────┘
                        │
             ┌──────────┴──────────┐
           Error               Success
             │                    │
         No ACK        ┌── PHASE 3 ──────────────────────┐
       (redeliver)     │ UPDATE settled_trades (SETTLED)  │
                       │ COMMIT ← DB connection released  │
                       └──────────────────────────────────┘
                                   │
                               ACK Kafka offset
```

**Core invariant:** No database connection is ever open while the gRPC call is in flight.

---

## 4. Crash Recovery

If the service crashes between Phase 2 (gRPC success) and Phase 3 (mark SETTLED):
- The Kafka offset was not committed → Kafka redelivers the message on restart.
- The `PENDING` row exists → Phase 1 is skipped, Phase 2 is retried.
- `Wallet.SettleTrade` is idempotent on `trade_id` → duplicate gRPC call is silently absorbed.
- Phase 3 commits → status becomes `SETTLED` → Kafka ACK.

A **background recovery goroutine** (60-second ticker) also scans for stale `PENDING` rows older than 60 seconds as a safety net for missed Kafka redeliveries.

---

## 5. Service Invariants

| # | Invariant |
|---|---|
| **SI-1** | No database transaction open while gRPC is in flight |
| **SI-2** | `status = 'SETTLED'` causes all future redeliveries of same trade to ACK as no-op |
| **SI-3** | Each trade is independent — no cross-trade state or ordering constraints |
| **SI-4** | Settlement publishes no Kafka events — Wallet Service's outbox handles all downstream notification |

---

## 6. Directory Structure

```
services/settlement/
├── .env                            ← Local development environment variables
├── Dockerfile                      ← Multi-stage alpine build
├── README.md                       ← This file
├── go.mod                          ← module: tradedrift/services/settlement
├── go.sum
├── cmd/
│   ├── README.md
│   └── server/
│       └── main.go                 ← Bootstrap entrypoint
├── migration/
│   ├── README.md
│   └── 00001_create_settled_trades.sql
└── internal/
    ├── README.md
    ├── client/
    │   ├── README.md
    │   └── wallet_client.go        ← Wallet gRPC client wrapper
    ├── config/
    │   ├── README.md
    │   └── config.go               ← Environment variable loading
    ├── domain/
    │   ├── README.md
    │   └── trade.go                ← SettledTrade struct + status constants
    ├── kafka/
    │   ├── README.md
    │   └── consumer.go             ← kafka-go reader loop (manual commit)
    ├── repository/
    │   ├── README.md
    │   ├── repository.go           ← Interface
    │   └── postgres/
    │       ├── README.md
    │       └── repository.go       ← pgx implementation
    └── service/
        ├── README.md
        └── service.go              ← 3-phase pipeline + recovery logic
```

---

## 7. Environment Variables

| Variable | Default | Description |
|---|---|---|
| `SETTLEMENT_POSTGRES_DSN` | `postgres://...tradedrift_settlement` | PostgreSQL connection string |
| `SETTLEMENT_MIGRATIONS_DIR` | `migration` | Path to goose migration files |
| `KAFKA_BROKERS` | `localhost:9092` | Comma-separated Kafka broker list |
| `KAFKA_GROUP_ID` | `settlement-service-group` | Consumer group ID |
| `KAFKA_TOPIC_TRADE_EXECUTED` | `trades.executed` | Kafka topic to consume |
| `WALLET_GRPC_ADDR` | `localhost:50052` | Wallet Service gRPC address |
| `LOG_LEVEL` | `info` | Uber Zap log level (`debug`, `info`, `warn`, `error`) |

---

## 8. Acceptance Test — Money Movement Verification

After a matched trade, verify the complete fund movement:

```sql
-- 1. Settlement ledger: confirm SETTLED
SELECT trade_id, status, settled_at
FROM settled_trades ORDER BY executed_at DESC LIMIT 5;

-- 2. Buyer received base asset (e.g. BTC)
SELECT asset, available_balance, reserved_balance
FROM wallets WHERE user_id = '<buyer-uuid>';

-- 3. Seller received quote asset (e.g. USDT)
SELECT asset, available_balance, reserved_balance
FROM wallets WHERE user_id = '<seller-uuid>';

-- 4. Wallet ledger entries created
SELECT reference_id, transaction_type, asset, amount
FROM wallet_transactions WHERE reference_id = '<trade-id>';
```
