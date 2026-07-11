# TradeDrift — Database Standards & Conventions

> **Status:** ✅ Frozen (V1.0)
> **Document:** 01_Database_Standards.md
> **Directory:** docs/07_Database/
> **Last Updated:** July 2026

---

## 1. Core Principles

To guarantee absolute data integrity, prevent transaction deadlocks, and support high-throughput horizontal scaling across our microservice architecture, all TradeDrift databases must adhere to the following standards.

---

## 2. Naming & Data Type Conventions

* **Case Format:** Lower snake_case for all tables, columns, indexes, and constraints (e.g., `user_id`, `idx_wallets_user_id_asset`).
* **Primary Keys:** Every table must have a primary key named `id` of type `UUID` (generated as a sortable UUIDv7 in application memory before insertion).
* **Timestamps:** All time columns must use the `TIMESTAMPTZ` data type (timestamp with time zone) and default to `NOW()`.
* **Precision Arithmetic:** Under no circumstances are float or double types permitted for prices, quantities, fees, or balances. All monetary and mathematical values must use:
  - PostgreSQL: `DECIMAL(30,10)`
  - Go application code: `string` (serialized as arbitrary-precision decimals via shared serializer libraries)

---

## 3. Physical Partitioning & Service Boundaries

* **Database per Service:** Every write-side microservice (Auth, Wallet, Order, Settlement) owns its own physical database instance. No cross-service database reads or writes are allowed.
* **No Cross-Service Foreign Keys:** To prevent tight physical database coupling and database-level deadlocks across services, **foreign key constraints must not cross service database boundaries.**
  - Example: `wallets.user_id` must NOT have a foreign key pointing to the `users` table in the Auth database.
  - Example: `orders.user_id` must NOT have a foreign key pointing to the Wallet database.
  - *Enforcement:* Referential integrity is validated programmatically at the application/API layer. Within a service's own database, local foreign keys (e.g., `wallet_reservations.wallet_id` pointing to `wallets.id`) are encouraged.

---

## 4. Transaction Isolation & Locking Standards

* **Default Isolation Level:** All databases run under PostgreSQL's default **`READ COMMITTED`** isolation level.
* **Explicit Row Locking:** To prevent lost updates or double-spending during concurrent actions (e.g., balance updates), services must use explicit row-level locks:
```sql
SELECT available_balance, reserved_balance 
FROM wallets 
WHERE id = $1 
FOR UPDATE;
```
* **Lock Sorting:** When acquiring locks on multiple rows in a single transaction, the rows must be sorted lexicographically in application memory before executing the SQL queries to prevent deadlocks.
* **Transactional Outbox leasing:** Pending events must be leased using `SELECT ... FOR UPDATE SKIP LOCKED` and updated after acks are received.

---

## 5. Index & Migration Naming Schema

### 5.1 Index Naming Format
All indexes must use a standard prefix to prevent namespace collisions and ease performance audits:
- Primary Key index: Automatically managed by Postgres as `{table}_pkey`.
- Unique index: `uq_{table}_{column1}_{column2}`.
- Partial / Non-unique index: `idx_{table}_{column1}_{column2}`.

### 5.2 Migration File Sequencing
Migration files must use standard prefix numbering to enforce linear ordering:
- Format: `{three-digit-sequence}_{action_description}.sql`
- Example: `001_create_wallets_table.sql`
- Example: `002_add_is_frozen_to_wallets.sql`

---

## 6. Soft Delete & Immutability Standard

* **Zero Soft Deletes on Core Ledger Tables:** To maintain auditability, financial databases (`wallets`, `wallet_reservations`, `wallet_transactions`, `orders`, `trades`) are designated as **strictly immutable ledger structures**. Rows are never deleted, nor do they support `deleted_at` fields.
* **State Mutations Only:** Balance modifications, cancellations, and status changes are recorded as ledger additions or status updates (e.g. `status` transitions from `OPEN` to `CANCELLED`).
* **Non-financial Tables:** Administrative configurations or temporary tables may support soft-deleting using a nullable `deleted_at TIMESTAMPTZ` column.

---

## 7. State Status & Evolvability Standard

* **Reject Native Postgres ENUM Types:** Postgres `CREATE TYPE ... AS ENUM` objects are difficult to alter and modify without database locks or transaction blocks.
* **Status Fields Format:** All status and type indicators must use standard `VARCHAR(20)` columns.
* **Database-Level CHECK Constraints:** Allowed statuses must be restricted at the database layer using explicit `CHECK (column IN (...))` constraints to protect data integrity against bad API payloads while maintaining easy schema changes.
* *Example:*
  ```sql
  status VARCHAR(20) NOT NULL CHECK (status IN ('OPEN', 'PARTIALLY_FILLED', 'FILLED', 'CANCELLING', 'CANCELLED'))
  ```

---

## 8. Data Retention Standard

To manage database growth and maintain index buffer performance, data retention policies are defined as follows:

| Table Category / Table Name | Retention Window | Storage Action on Expiry |
|---|---|---|
| **Outbox Queue (`outbox`)** | 30 Days | Hard delete (records with `status = 'PUBLISHED'`) |
| **Notification History (`notifications`)** | 90 Days | Hard delete |
| **Active Session Tokens / JWT Logs** | Expired + 24 Hours | Automated purge |
| **Core Financial Records (`wallets`, `trades`, `orders`)** | **Forever** | Retained in primary tables (historic read partitions allowed) |
| **System Event / Debug Logs** | 30 Days | Rotated out to external object stores |

