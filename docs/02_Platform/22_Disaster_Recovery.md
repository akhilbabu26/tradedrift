# TradeDrift — Disaster Recovery & Business Continuity Specification

> **Status:** ✅ Designed (V1.1)
> **Document:** 22_Disaster_Recovery.md
> **Service:** Platform Architecture
> **Version:** V1.1
> **Last Updated:** July 2026
> Revision notes: V1.1 updates platform recovery parameters: (1) clarifies Target RPO = 0 limits under cross-region async replication; (2) adds Redis cold start recovery rules; (3) specifies committed consumer offset recovery; (4) clarifies DNS health checks and TTL parameters; (5) introduces SRE Gate PASS checks; (6) mandates backup validation tests; (7) maps governance roles; (8) appends an operational Failure/Recovery reference matrix.

---

## 1. Disaster Recovery (DR) Target Metrics

Disaster Recovery operations are governed by target metrics defined by service tiers:

```
[ Tier 1: Core Financials (Wallet / Settlement) ] ──► Target RPO = 0 (Near-Zero Objective) │ RTO < 1 Hour
[ Tier 2: Order Matching & Book State ]          ──► RPO < 5s                          │ RTO < 1 Hour
[ Tier 3: Query & Reporting (Portfolio / Trades) ] ──► RPO < 1 Hour                     │ RTO < 4 Hours
```

> [!NOTE]
> Cross-region asynchronous replication is subject to minor replication delays. Achieving a strict physical RPO = 0 across regions is an operational target; under severe region failure, the absolute transaction loss is minimized near zero using tail transaction recoveries. Cross-region synchronous replication is avoided due to the prohibitive latency impact on write throughput.

### Metrics Definitions:
* **Recovery Point Objective (RPO):** The maximum age of data that can be lost because of a major system crash or region failure.
* **Recovery Time Objective (RTO):** The maximum duration of system downtime permitted before services must be fully restored in a recovery zone.

---

## 2. Platform Backup & Replication Standards

To meet our strict RPO targets, all stateful datastores utilize continuous geo-replication and automated snapshots:

### 2.1 PostgreSQL Databases (Wallet, User, Order, Settlement)
* **Replication:** Multi-AZ replication is enabled for the primary cluster. For cross-region failover, a passive Read Replica runs asynchronously in the designated DR region (latency target: $< 1\text{s}$).
* **Continuous Archiving (WAL):** Write-Ahead Logs (WAL) are shipped continuously to a secured, geo-replicated cloud object store (AWS S3 with Cross-Region Replication enabled) as determined by the database/storage platform.
* **Point-in-Time Recovery (PITR):** Enables transactional recovery down to the millisecond, permitting SREs to restore databases to the exact second preceding a corruption event.
* **Retention Policy:** Full database backups are executed daily (retained for 30 days). WAL logs are archived and retained for 14 days.
* **Backup Validation Standard:** **Backups are not considered valid or compliant until a scheduled quarterly restore test succeeds** on an isolated staging database replica instance.

### 2.2 Apache Kafka Event Streams
* **Replication:** Topics use a replication factor of $3$ (`min.insync.replicas = 2`) across separate Availability Zones inside the primary region.
* **Archival Replay Log:** Raw transaction logs of core topics (`orders.*`, `trades.*`) utilize Kafka Tiered Storage. Events are offloaded asynchronously to S3 object storage with a **7-year retention lease** to support long-term auditing and complete ledger reconstruction.

### 2.3 Redis State & Cache Recovery
Redis operates strictly as a volatile Cache and Pub/Sub Backplane, never the system of record. If Redis fails completely:
* **Cold Start Recovery:** Redis nodes are booted from a cold state.
* **24h rolling tickers rebuild:** The API nodes dynamically execute postgres aggregate queries to recalculate historical 24h market stats.
* **JWT Blacklist:** Restored on boot by pulling revoked identifiers from the SQL blacklisted tokens database.
* **L2 Order Books:** Reconstructed on demand from active Matching Engine memory snapshots or PostgreSQL checkpoints. No Redis-specific backup restoration is required.

---

## 3. Active-Passive Cross-Region Failover Blueprint

In the event of a catastrophic primary region outage, the platform executes a failover sequence to transition services to the passive disaster recovery region.

```
[ Primary Region Outage ]
           │
           ▼
[ DNS Switchover (Route 53) ] ──► Points traffic to DR Region Gateway
           │
           ▼
[ Promote Postgres Replica ]   ──► Promotes Passive Read Replica to Master
           │
           ▼
[ Boot Application Workloads ] ──► Spins up API Gateway, Core Services & Workers
           │
           ▼
[ Recover Matching Engine ]   ──► ME loads latest checkpoint & replays Kafka offsets
           │
           ▼
[ System Health Checks (PASS) ] ──► Enable public trading paths
```

### Failover Operational Checklist:
1. **DNS Redirection:** Route 53 or Cloudflare DNS failover records are modified to redirect ingress gateway traffic to the passive DR ingress load balancers. **DNS propagation is not instantaneous; failover execution assumes automated routing policies, low TTL settings (60 seconds), and active-active load-balancer endpoints to minimize resolution lag.**
2. **Database Promotion:** SREs or automated orchestrators issue the promotion command (`SELECT pg_promote();`) to transition the cross-region passive PostgreSQL replica into the active Primary Master database.
3. **Application Scale-up:** Horizontal Pod Autoscalers in the DR cluster scale up replica counts for API nodes and consumer daemon replicas.
4. **Matching Engine Reconstruction:** The Matching Engine StatefulSet spins up in the DR region, fetches the latest in-memory checkpoint, connects to the promoted Kafka cluster endpoints, recovers committed consumer offsets from Kafka's `__consumer_offsets` topic metadata, and replays events from the checkpoint offset marker to restore the active order book structure.

### 3.1 SRE Gate PASS Verification Checklist
Before opening public traffic pathways to the DR region, SRE teams must verify the system passes all verification gates:
* **Wallet Invariants (`PASS`):** Run the balance check query (Section 4). Zero mismatched records must be returned.
* **Kafka Cluster (`PASS`):** Validate Kafka brokers are online, and in-sync replicas (ISR) match partitions status.
* **Matching Engine (`PASS`):** Confirm the engine is online, has finished offset replay, and is processing current event streams.
* **E2E Smoke Test (`PASS`):** Execute a simulated order placement, execution, and cancellation test using diagnostic accounts.
Once all checks return `PASS`, public trading access is enabled.

---

## 4. Ledger Reconciliation Runbook

To guarantee database integrity and verify that transactional operations match exactly, an automated **Ledger Reconciliation Job** runs **daily at 01:00 UTC as a baseline, with high-frequency reconciliation (e.g. hourly or continuous) planned for implementation as trading volume scales.**

```
               [ Run Ledger Reconciliation Job ]
                               │
               (Check Wallet Balance Invariants)
             available + reserved == total_balance
                               │
                (Cross-Check Transaction Logs)
         initial_bal + credits - debits == current_bal
                               │
                   ┌───────────┴───────────┐
                   ▼ (Success)             ▼ (Mismatch Detected)
             [ Log Audit OK ]       [ 1. Trigger P0 SRE Alarm ]
                                    [ 2. Freeze Wallet Updates ]
                                    [ 3. Lock Suspect Account ]
```

### Audit Procedures:
1. **Wallet Balance Invariant Validation:**
   For every asset wallet inside PostgreSQL, the cron job executes:
   ```sql
   SELECT user_id, asset, available_balance, reserved_balance, total_balance 
   FROM wallets 
   WHERE (available_balance + reserved_balance) != total_balance;
   ```
   *Action:* If any row is returned, the system triggers a P0 alarm.
2. **Double-Entry Balance Verification:**
   Cross-references wallet balances against the sequential ledger transaction log:
   ```sql
   SELECT w.id, w.total_balance, 
          COALESCE(SUM(t.amount), 0) as ledger_delta
   FROM wallets w
   LEFT JOIN wallet_transactions t ON t.wallet_id = w.id
   GROUP BY w.id
   HAVING w.total_balance != (w.initial_balance + ledger_delta);
   ```
   *Action:* Discrepancies lock transaction pathways for the suspect account and escalate to on-call engineers.

---

## 5. Recovery Governance & Roles

Disaster Recovery execution is coordinated by designated roles to ensure clear lines of authority during incidents:
* **Incident Commander (IC):** Declares the incident state, monitors recovery checkpoints, coordinates communication channels, and grants final authorization for DNS switchover.
* **Database SRE:** Promotes database replicas (`pg_promote`), verifies transaction log sequence integrity, and confirms wallet balance invariant compliance.
* **Platform SRE:** Initiates DNS redirection rules, updates Kubernetes ingress paths, and scales deployment replicas in the target region.
* **Application SRE:** Monitors the Matching Engine boot sequence, checks Kafka partition offset progress, and runs the E2E verification gate checks.

---

## 6. Service Invariants

- **DR-1 (Zero Financial Loss Target):** Ledger balance and double-entry transaction databases must enforce a Target RPO of 0. Failures must not cause uncommitted balance mutations.
- **DR-2 (Audit Enforce):** The ledger reconciliation audit job must run daily. SRE dashboard logs must reflect the audit summary.
- **DR-3 (Verification Drill):** Disaster recovery cross-region failover playbooks must undergo mock drill runs twice a year to verify RTO SLA compliance limits.
- **DR-4 (Backup Restore Compliance):** Backup retention validity is contingent on quarterly restore drill success verification.

---

## Appendix A: Disaster Recovery Failure Matrix

| Failure Event | Primary Impact | Recovery Action |
|---|---|---|
| **API Gateway Pod Crash** | Temporary routing failure | Kubernetes automated restart |
| **Wallet/Order Pod Crash** | Minor request latency | Kubernetes automated restart |
| **PostgreSQL Primary Failure** | Write mutations blocked | Promote passive Read Replica (`SELECT pg_promote();`) |
| **Kafka Broker Failure** | Minor partition latency | Automatic In-Sync Replicas (ISR) leader election |
| **Redis Master Failure** | Cache/Pub-Sub temporary drop | Redis Sentinel automated failover |
| **Matching Engine Pod Crash** | Order matching paused | Pod mounts PersistentVolume, loads checkpoint, replays Kafka offsets |
| **Entire Region Failure** | Complete platform blackout | Execute Cross-Region Failover Runbook (Active-Passive) |

