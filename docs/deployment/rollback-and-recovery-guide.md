# TradeDrift — Production Rollback & Outage Recovery Guide

This document defines disaster recovery, deployment rollback, and failure containment procedures for TradeDrift on AWS EC2.

---

## 1. Zero-Data-Loss Rollback Principles

> [!WARNING]
> **NEVER run `migrate -action=down` or Goose rollback in production.**  
> Down migrations drop tables (`DROP TABLE wallets CASCADE;`, `DROP TABLE settled_trades;`), permanently destroying user balances, cryptographic audit trails, and trade history.

### Safe Application Rollback Procedure
All TradeDrift database migrations are strictly designed to be **forward-compatible** (additive columns, nullable fields, non-blocking indexes). If a newly deployed container fails in production:

1. **Roll back the container stack to the previous Git commit or Docker tag:**
   ```bash
   git checkout <PREVIOUS_STABLE_COMMIT_OR_TAG>
   docker compose -f docker-compose.prod.yml up -d --build
   ```
2. **Leave the database schema intact.** The previous version of the Go microservice will ignore new columns/tables without failing.
3. If a bug was caused by bad schema, apply a new forward migration (`0000X_fix_issue.sql`) using `deploy-migrations.sh` instead of rolling backward.

---

## 2. Infrastructure Outage Recovery

### Scenario A: PostgreSQL Database Becomes Unavailable or Corrupted
1. **Check Postgres container status:**
   ```bash
   docker compose -f docker-compose.prod.yml logs postgres --tail=100
   ```
2. **Verify data directory volume:**
   The database files persist in the Docker volume `postgres_data` mapped to `/var/lib/postgresql/data`.
3. **Restart the database container:**
   ```bash
   docker compose -f docker-compose.prod.yml restart postgres
   ```
4. **Automated Reconnection:**
   All 13 Go services utilize `jackc/pgx/v5/pgxpool` with exponential backoff retry loops. Once Postgres passes its health check, services will automatically resume processing without requiring container restarts.

---

### Scenario B: Apache Kafka Broker Crash or Lag Accumulation
1. **Inspect Kafka logs & partition health:**
   ```bash
   docker compose -f docker-compose.prod.yml logs kafka --tail=100
   ```
2. **Restart Kafka & re-verify topic readiness:**
   ```bash
   docker compose -f docker-compose.prod.yml restart kafka
   docker compose -f docker-compose.prod.yml up -d kafka-init
   ```
3. **Consumer Recovery & Checkpointing:**
   * **Matching Engine:** Reads partition sequences from `market_sequences` in PostgreSQL (`tradedrift_matching`). On restart, it replays uncommitted order events from its last committed High Watermark (HWM).
   * **Settlement Service:** Utilizes consumer group `settlement-service-group` with auto-offset commit on settled transactions. Any unacknowledged trades will be redelivered.
   * **Wallet Outbox:** The Wallet service uses an Outbox publisher pattern (`services/wallet/internal/service/outbox_publisher.go`). If Kafka was down, events queued safely in the `outbox` table and are dispatched automatically once Kafka recovers.

---

### Scenario C: Redis Crash or Cache Disconnect
1. **Restart Redis:**
   ```bash
   docker compose -f docker-compose.prod.yml restart redis
   ```
2. **Data Durability:**
   Redis runs with Append-Only File (`--appendonly yes`) on persistent volume `redis_data`. Orderbook depth cache, ticker snapshots, and active WebSocket pub/sub states are restored.
3. The API Gateway will automatically reconnect its Redis pub/sub poller within 5 seconds.

---

### Scenario D: Matching Engine / Liquidity State Desynchronization
If an unexpected shutdown causes in-memory matching engine books to diverge from order records:
1. Trigger the Matching Engine reconciliation audit endpoint:
   ```bash
   curl -X POST http://127.0.0.1:8082/admin/reconcile \
     -H "Authorization: Bearer ${RECONCILIATION_ADMIN_SECRET}"
   ```
2. The matching engine compares the live book against active orders in `tradedrift_order` and reconciles sequence numbers without dropping trade history.

