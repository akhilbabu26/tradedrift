# TradeDrift Audit — 05. Disaster Recovery

> **Status:** ✅ Validated (V1.0)
> **Document:** 05_Disaster_Recovery_Audit.md
> **Domain:** Disaster Recovery, Replications, and Replays

---

## 1. Scope

This audit validates disaster recovery parameters: RPO tolerances, DNS TTL failover limits, matching engine state checkpoints, and regional failover compliance.

---

## 2. Scenario Validations

### 2.1 Near-Zero RPO replication
* **Constraint:** For regional failover, cross-region asynchronous database replication (PostgreSQL WAL shipping) cannot guarantee a hard $RPO = 0$, as some WAL records may crash before replication commits.
* **Audit Resolution:** Target RPO is defined as "Target RPO = 0 (Near-Zero Objective)". True $RPO = 0$ requires synchronous replication across failure domains, which incurs high network latency costs.
* **Validation Drill:** SREs perform a restoration drill quarterly on an isolated staging database instance. Continuous WAL archiving limits are controlled dynamically by the cloud provider storage host.

### 2.2 DNS Switchover Failover Bounds
* **Metric:** Route 53 health monitoring detects a primary region outage and updates client traffic mapping within **60 seconds**.
* **Design Control:** Internal and public client DNS records set low Time-To-Live (TTL) parameters of 60 seconds to ensure client routing updates are applied.

### 2.3 Matching Engine Recovery and Offset Replay
* **Workflow:** Upon Matching Engine instance crash or regional promotion:
  - The newly active Matching Engine instance loads the last saved orderbook state checkpoint from Postgres.
  - The engine reads its committed consumer offset from Kafka (`__consumer_offsets`).
  - It replays the incoming message stream from that checkpoint offset to reconstruct the active order book in memory.
  - Sockets and API routing endpoints return `503 Service Unavailable` until the engine catch-up phase finishes.

### 2.4 SRE Failover Gate PASS Checklists
Before opening public API routing during regional promotion, SREs execute a mandatory Gate PASS Checklist:
1. **Wallet Balance Invariant Validation:** Run SQL query validating available/reserved balances match seeded amounts.
2. **Kafka Partition Synchronization:** Confirm consumer offset lag is zero across all core partitions.
3. **Matching Engine Reconciliation:** Confirm the in-memory order book matches database reservation figures.
4. **End-to-End order path validation:** Execute automated synthetic trade placements.

---

## 3. Discovered Inconsistencies & Resolutions

* **Hard RPO = 0 Claims:** The disaster recovery document previously claimed $RPO = 0$ for cross-region async replication. This was corrected to "Target RPO = 0" (Near-Zero Objective) to match physical networking constraints.
* **Redis Recovery Strategy:** Added a clear cache recovery policy: Redis is treated as cache-only. tickers are recalculated from PostgreSQL, blacklists rebuilt from token tables, and orderbooks reconstructed from ME checkpoint databases.
