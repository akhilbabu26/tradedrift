# TradeDrift Audit — 04. Operational Readiness

> **Status:** ✅ Validated (V1.0)
> **Document:** 04_Operational_Readiness_Audit.md
> **Domain:** Infrastructure, Observability, and Operations

---

## 1. Scope

This audit reviews operational topology constraints: self-hosted vs managed infrastructure options, distributed tracing sampling models, log structured schemas, and Prometheus alerting catalogs.

---

## 2. Scenario Validations

### 2.1 Managed Infrastructure Topology
* **Standard:** To minimize operational risk in production deployments, TradeDrift enforces a clear separation between environment topologies:
  - **Production:** Database engines (PostgreSQL, Redis) and message brokers (Kafka) must be deployed using managed cloud services (e.g. Amazon RDS/Aurora, ElastiCache, and MSK). Self-hosting stateful engines in production is a compliance failure.
  - **Development/Testing:** Kubernetes StatefulSets and local configurations are permitted to keep cost low and enable offline iteration.

### 2.2 Trace Sampling Policy
To balance distributed tracing storage costs against SRE diagnostics requirements, we enforce a hybrid trace sampling strategy:
* **API Ingress Traffic:** 1% probabilistic sampling for successful Gateway/REST requests.
* **Matching Engine Loops:** 0.1% probabilistic sampling for high-frequency internal matcher execution traces.
* **Fail-Override (Tail-based):** 100% override capture for failing HTTP/gRPC requests (status $\ge 400$ or gRPC code $\neq$ OK).

### 2.3 Prometheus & Alertmanager SLA Thresholds
Alertmanager rules map specific metric invariants to high-priority pager alerts:
* **Ledger Balance Invariant Alerts:** Trigger immediately if total wallet balances diverge from seeded allocations ($available + reserved \neq total$).
* **API Availability:** Trigger if Gateway route availability drops below $99.95\%$.
* **Matching Loop Latency:** Trigger if the Matching Engine matching loop latency exceeds 5ms.
* **Settlement Lag Alert:** Trigger if Settlement Service's consumer offset lag exceeds 1,000 messages or if a trade remains in `PENDING` state for $\ge 60$ seconds.

---

## 3. Discovered Inconsistencies & Resolutions

* **Self-Hosted Production Fallacy:** Early infrastructure documents implied hosting Kafka inside Kubernetes StatefulSets was recommended for production. This was resolved by documenting Amazon MSK / managed equivalents as a mandatory prerequisite for production readiness.
* **WebSocket Control Frames Heartbeat:** The WebSocket Gateway previously used a JSON payload `{ "action": "ping" }` for heartbeats. The audit updated this behavior to utilize standard RFC 6455 Ping/Pong control frames, reducing bandwidth consumption and offloading heartbeat handling to standard client/browser libraries.
