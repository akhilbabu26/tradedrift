# TradeDrift Admin Service: Feature Architecture Catalog

This directory contains in-depth architectural and technical design documentation for all **20 core subsystems** of the **TradeDrift Admin Control Plane**.

Each feature document contains:
1. **What this feature does**
2. **Why we need it** (Production risks mitigated)
3. **Where we used it in code** (Source files, line references, repositories, and interfaces)
4. **How we achieve this feature** (Invariants, policies, algorithms, data models)
5. **Execution Flow** (Rendered in unified ASCII box-drawing diagram style)

---

## Complete Feature Directory Index

| # | Feature Document | Core Responsibility |
| :---: | :--- | :--- |
| **01** | [**01. User Controls (Suspend & Unsuspend)**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/01_USER_CONTROLS.md) | Multi-service account suspension across Auth DB, Redis blacklist, and API Gateway 403 guard. |
| **02** | [**02. Market Controls (Halt & Resume)**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/02_MARKET_CONTROLS.md) | Emergency circuit breaker to halt/resume trading pairs in sub-milliseconds with Order Service protection. |
| **03** | [**03. Wallet Controls (Freeze & Unfreeze)**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/03_WALLET_CONTROLS.md) | Granular per-asset wallet freezing; blocks debits/deposits while preserving trade settlement and cancellations. |
| **04** | [**04. Distributed Saga Orchestration**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/04_DISTRIBUTED_SAGA_ORCHESTRATION.md) | Persistent Saga task engine with exponential backoff retries for cross-service eventual consistency. |
| **05** | [**05. Caller-Scoped Idempotency Engine**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/05_CALLER_SCOPED_IDEMPOTENCY.md) | Enforces `UNIQUE(admin_id, idempotency_key)` to short-circuit duplicate requests and prevent side-effect duplication. |
| **06** | [**06. Transactional Outbox Pattern**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/06_TRANSACTIONAL_OUTBOX.md) | Atomic PostgreSQL event staging and reliable Kafka publication with `RequireAll` acks to eliminate dual-write bugs. |
| **07** | [**07. Real-Time Redis Enforcement**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/07_REAL_TIME_REDIS_ENFORCEMENT.md) | Sub-millisecond operational circuit breaker keys (`user:suspended:{id}`, `market:halted:{id}`) with retry pipelines. |
| **08** | [**08. Fail-Closed Architecture**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/08_FAIL_CLOSED_ARCHITECTURE.md) | Order Service and Gateway default-deny policies on Redis timeout, connection failure, or sentinel absence. |
| **09** | [**09. Readiness Sentinel Protocol**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/09_READINESS_SENTINEL_PROTOCOL.md) | `market:enforcement:ready` key invariant preventing order leakage during cold starts and Redis reboots. |
| **10** | [**10. Anti-Drift State Reconciler**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/10_ANTI_DRIFT_STATE_RECONCILER.md) | Continuous 15s background self-healing engine comparing PostgreSQL snapshots against Redis cache keys. |
| **11** | [**11. Multi-Tier Health Probing Architecture**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/11_MULTI_TIER_HEALTH_PROBING.md) | Decoupled 3-tier health checks: `/health` (liveness), `/ready` (readiness), and `/system/health` (diagnostic). |
| **12** | [**12. Autonomous HealthWorker Engine**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/12_AUTONOMOUS_HEALTH_WORKER_ENGINE.md) | Decoupled 15s background probe across 9 platform services, pools, and message brokers with zero request-path I/O. |
| **13** | [**13. Prometheus 1-Hot Health Gauges**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/13_PROMETHEUS_1_HOT_GAUGES.md) | Mutually exclusive metric vectors (`UP`, `DEGRADED`, `DOWN`) eliminating Grafana race conditions and stale gauges. |
| **14** | [**14. Service Dependency Topology Graph Engine**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/14_SERVICE_DEPENDENCY_TOPOLOGY_GRAPH.md) | Live interactive cluster map (`/topology`) with 9 nodes across 4 tiers and 18 protocol communication edges. |
| **15** | [**15. Automated Incident Lifecycle & Recovery Engine**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/15_AUTOMATED_INCIDENT_LIFECYCLE.md) | Automated P1 outage / P2 degradation detection in PostgreSQL, autonomous healing, and MTTR calculation. |
| **16** | [**16. Temporal Blast-Radius Incident Correlation**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/16_TEMPORAL_BLAST_RADIUS_CORRELATION.md) | Correlates outages with administrative actions across a ±15-minute rolling window in `admin_audit_log`. |
| **17** | [**17. Operational Analytics & Risk Signals Engine**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/17_OPERATIONAL_ANALYTICS_AND_RISK_SIGNALS.md) | 24-hour volume analytics and automated anomaly detection (mass lockouts, frequent halts, admin IP diversity). |
| **18** | [**18. Immutable Enterprise Audit Trail**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/18_IMMUTABLE_ENTERPRISE_AUDIT_TRAIL.md) | Append-only database ledger recording actor, target, reason, IP address, user-agent, and UUIDv7 request trace. |
| **19** | [**19. Two-Stage Graceful Drainage Protocol**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/19_TWO_STAGE_GRACEFUL_DRAINAGE.md) | Instant 503 unreadiness switch on SIGTERM followed by coordinated worker and HTTP connection drainage. |
| **20** | [**20. Platform SDK & Standardized Error Architecture**](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/docs/features/20_PLATFORM_SDK_AND_STANDARDIZED_ERRORS.md) | Decoupled transport layer mapping company-wide canonical errors to HTTP status codes without info leakage. |
