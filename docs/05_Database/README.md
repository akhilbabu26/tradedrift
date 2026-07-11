# TradeDrift — Database Architecture Specs

> **Status:** ✅ Designed (V1.0)
> **Document:** README.md
> **Directory:** docs/07_Database/
> **Last Updated:** July 2026

---

## 1. Purpose

This directory contains the database design specifications, schemas, index configurations, query patterns, and migration topologies for the TradeDrift platform.

---

## 2. Directory Index

This directory is organized into the following modular design documents:

* **[`01_Database_Standards.md`](01_Database_Standards.md):** Holds all common rules (UUIDv7, naming conventions, decimals, timestamps, transaction scopes, and the prohibition of cross-service foreign keys).
* **[`02_Auth_Database.md`](02_Auth_Database.md):** Schemas for users (with brute force tracking), refresh tokens, and blacklisted access tokens.
* **[`03_Wallet_Database.md`](03_Wallet_Database.md):** Schemas for balances (with freeze states), reservations, ledger entries, and deposits/withdrawals.
* **[`04_Order_Database.md`](04_Order_Database.md):** Schemas for order records (filled/remaining bounds) and standardized transactional outbox queues.
* **[`05_Settlement_Database.md`](05_Settlement_Database.md):** Schemas for settled trade transaction states.
* **[`06_Portfolio_Database.md`](06_Portfolio_Database.md):** Schemas for user holdings and PnL read projections.
* **[`07_Trade_Database.md`](07_Trade_Database.md):** Schemas for trade history logs.
* **[`08_Notification_Database.md`](08_Notification_Database.md):** Schemas for user alerts, notification templates, and WebSocket routing tables.
* **[`09_Market_Database.md`](09_Market_Database.md):** Schemas for active trading pairs and OHLCV statistics.
* **[`10_Index_Strategy.md`](10_Index_Strategy.md):** Master index configurations designed directly around expected read query patterns.
* **[`11_Migration_Order.md`](11_Migration_Order.md):** Specifies the chronologically sequenced schema migration order and dependencies.

---

## 3. Database Diagrams Catalog

The following vector SVG diagrams are saved inside the `diagrams/` folder and render natively in any standard markdown viewer or browser:

1. **[`Database_Ownership.svg`](diagrams/Database_Ownership.svg):** Maps service ownership boundaries over each database instance (loose physical coupling).
2. **[`Migration_Order.svg`](diagrams/Migration_Order.svg):** Shows the sequential dependency order for database schema provisioning.
3. **[`Transaction_Flow.svg`](diagrams/Transaction_Flow.svg):** Illustrates transaction boundaries (Tx 1, Tx 2, Tx 3) and Kafka propagation steps for the spot order fill lifecycle.
4. **[`Query_Flow.svg`](diagrams/Query_Flow.svg):** Displays read-path query boundaries and cross-service/cache database retrieval paths.
5. **ER Diagrams (Entity Relationships per database):**
   - **[`Auth_ER.svg`](diagrams/Auth_ER.svg):** Schemas and associations for users, refresh tokens, and blacklisted sessions.
   - **[`Wallet_ER.svg`](diagrams/Wallet_ER.svg):** Schemas and associations for asset limits, wallets, transactions, and holdings reservations.
   - **[`Order_ER.svg`](diagrams/Order_ER.svg):** Schemas and associations for limit/market orders and outbox event tables.
   - **[`Settlement_ER.svg`](diagrams/Settlement_ER.svg):** Schema for idempotent trade settlement transactions.
   - **[`Portfolio_ER.svg`](diagrams/Portfolio_ER.svg):** Schema for holdings metrics and processed trade offsets.
   - **[`Trade_ER.svg`](diagrams/Trade_ER.svg):** Schema for match execution logs.
   - **[`Notification_ER.svg`](diagrams/Notification_ER.svg):** Schema for user notification inboxes and event offsets.
   - **[`Market_ER.svg`](diagrams/Market_ER.svg):** Schema for trading pairs and OHLC daily statistics.

