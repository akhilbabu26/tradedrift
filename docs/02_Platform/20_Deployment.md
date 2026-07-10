# TradeDrift — Kubernetes Deployment Specification

> **Status:** ✅ Designed (V1.1)
> **Document:** 20_Deployment.md
> **Service:** Platform Architecture
> **Version:** V1.1
> **Last Updated:** July 2026
> Revision notes: V1.1 upgrades deployment definitions: (1) clarifies managed infrastructure standards (MSK, RDS) for Production vs Kubernetes StatefulSets for Dev; (2) implements PodDisruptionBudgets and Availability Zone topologySpreadConstraints; (3) broadens HPA scaling rules using KEDA lag and connections metrics; (4) structures Secret vs ConfigMap divisions and NetworkPolicies; (5) details zero-downtime rolling updates and Single Active Replica upgrade runbooks.

---

## 1. High-Level Cluster Architecture

The TradeDrift platform is containerized and runs inside a managed Kubernetes (EKS / GKE) cluster. Services are partitioned into functional namespaces to enforce network isolation and access controls.

```
                  [ Public Internet ]
                           │
                           ▼
                  [ Ingress Controller ]
                           │
             ┌─────────────┼─────────────┐
      /ws    │             │ HTTP        │
             ▼             ▼             ▼
      [ WS Gateway ] [ API Gateway ] [ Auth Service ]
             │             │             │
        (Pub/Sub)        gRPC          gRPC
             ▼             ▼             ▼
       [ Redis Cluster ]  [ Internal Services ]
```

* `tradedrift-infra`: Houses stateful database and message broker configurations for non-production environments.

### 1.1 Infrastructure Hosting Model (Production vs Dev/Staging)
To ensure durability, service redundancy, and operational ease:
* **Development / Staging:** Stateful datastores (Apache Kafka, PostgreSQL, Redis) run directly within the Kubernetes cluster inside the `tradedrift-infra` namespace using StatefulSets, local PersistentVolumes, or open-source Operators (e.g. Strimzi, CloudNativePG).
* **Production:** Self-hosting databases and brokers inside Kubernetes is prohibited. Production environments must utilize managed cloud services located outside the application cluster:
  - **Message Broker:** Amazon MSK (Managed Streaming for Apache Kafka) or Confluent Cloud.
  - **Relational Databases:** Amazon RDS / Aurora PostgreSQL or GCP Cloud SQL.
  - **In-Memory Cache:** Amazon ElastiCache / Redis Enterprise or GCP Memorystore.
  Applications reference these managed services via standard Kubernetes DNS aliases or ExternalName Services.

---

| Component Name | K8s Workload Type | Replicas | Scaling Profile & Metrics | HA Routing Policies |
|---|---|---|---|---|
| `api-gateway` | `Deployment` | `3+` | HPA: CPU/Memory > 70% or request rate | PodDisruptionBudget, TopologySpread |
| `auth-service` | `Deployment` | `2+` | HPA: CPU/Memory > 80% | PodDisruptionBudget, TopologySpread |
| `user-service` | `Deployment` | `2+` | HPA: CPU/Memory > 80% | PodDisruptionBudget, TopologySpread |
| `wallet-service` | `Deployment` | `3+` | HPA: CPU/Memory > 70% | PodDisruptionBudget, TopologySpread |
| `order-service` | `Deployment` | `3+` | HPA: CPU/Memory > 70% | PodDisruptionBudget, TopologySpread |
| `market-service-api` | `Deployment` | `2+` | HPA: CPU/Memory > 80% | TopologySpread |
| `market-service-cron` | `Deployment` | **`1`** | **Single Active Replica** (No Autoscaling) | N/A (Recreate Strategy) |
| `matching-engine` | `StatefulSet` | **`1`** | **Single Active Replica** (No Autoscaling) | N/A (Volume check pointing) |
| `settlement-service` | `Deployment` | `3+` | KEDA HPA: Kafka lag on `trades.executed.v1` | PodDisruptionBudget, TopologySpread |
| `portfolio-service` | `Deployment` | `2+` | HPA: CPU/Memory > 80% | TopologySpread |
| `notification-gateway` | `Deployment` | `3+` | HPA: Concurrent WebSocket connections | PodDisruptionBudget, TopologySpread |
| `notification-worker` | `Deployment` | `3` | KEDA HPA: Kafka lag on `trades.settled.v1` | TopologySpread |
| `trade-service` | `Deployment` | `2+` | HPA: CPU/Memory > 80% | TopologySpread |

### 2.1 PodDisruptionBudget (PDB)
To protect high-availability workloads from unexpected node eviction during cluster draining or maintenance updates:
* Target services (`api-gateway`, `wallet-service`, `order-service`, `notification-gateway`) must deploy a PDB:
  ```yaml
  apiVersion: policy/v1
  kind: PodDisruptionBudget
  metadata:
    name: wallet-pdb
    namespace: tradedrift-apps
  spec:
    minAvailable: 2
    selector:
      matchLabels:
        app: wallet-service
  ```

### 2.2 Topology Spread Constraints
To enforce geographic redundancy across physical data centers, core transaction services must distribute replicas across different Availability Zones (AZs) using topology spread constraints:
```yaml
topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: topology.kubernetes.io/zone
    whenUnsatisfiable: DoNotSchedule
    labelSelector:
      matchLabels:
        app: wallet-service
```

---

## 3. Single Active Replicas & Probes

Two critical components of the system must run with exactly **one active replica (`replicas: 1`)** to maintain state consistency: the **Matching Engine** (to prevent book divergence) and the **Market Service Cron Role** (to avoid duplicate ticker calculation writes).

```
                      [ Kubernetes Control Plane ]
                                   │
                         (Readiness Probe Fails)
                                   ▼
                    (Pod Pulled from Traffic Routing)
                                   │
                          (Liveness Probe Fails)
                                   ▼
                       (Evict / Terminate Container)
                                   │
                       (Fetch Volume Claim / PV)
                                   ▼
                    [ Spin Up New Pod Instance (1/1) ]
```

### 3.1 Recovery Policies for Single Active Replicas:
To guarantee rapid self-healing without risking double-instantiation (split-brain):
* **Matching Engine Recovery:** Deployed as a `StatefulSet` with `volumeClaimTemplates` to mount a PersistentVolume (PV) for checkpoint records. If the node hosting the Matching Engine crashes:
  - Kubernetes schedules a replacement pod.
  - The pod mounts the same PV and reads the last checkpoint.
  - The Matching Engine process replays the matching Kafka partitions from the checkpoint offset to restore the resting book state in-memory.
* **Market Service Cron Recovery:** Deployed as a `Deployment` with `strategy: type: Recreate` (never use `RollingUpdate` for single-replica nodes to avoid concurrent execution during rolling handovers).
* **Liveness vs Readiness Probe Strategy:**
  - **Readiness Probe (`/healthz/readiness`):** Checks if downstream dependencies (databases, brokers) are reachable. If readiness fails, the pod is pulled from traffic routing immediately so no new requests are sent to it.
  - **Liveness Probe (`/healthz/liveness`):** Checks if the service application process is healthy and active. If liveness fails, Kubernetes forcefully restarts the container.
  - Readiness probe failure will occur first during minor resource locks, preventing traffic blackholes before a liveness restart is triggered.

---

## 4. Resource Allocation & Limits

To prevent Out-Of-Memory (OOM) kills and guarantee CPU cycles under peak load, resources are carefully budgeted:

```yaml
# Example high-performance Matching Engine container configuration
resources:
  requests:
    cpu: "4000m"
    memory: "8Gi"
  limits:
    cpu: "4000m"
    memory: "8Gi"
```

### Resource Allocation Grid:

| Pod Name | CPU Request | CPU Limit | Memory Request | Memory Limit | Performance Notes |
|---|---|---|---|---|---|
| `matching-engine` | `4000m` | `4000m` | `8Gi` | `8Gi` | Guaranteed Quality of Service (QoS). Swap disabled. |
| `api-gateway` | `500m` | `1000m` | `512Mi` | `1Gi` | Burstable limits for routing spikes. |
| `order-service` | `1000m` | `2000m` | `1Gi` | `2Gi` | Thread-pooled transaction validation. |
| `wallet-service` | `1000m` | `2000m` | `1Gi` | `2Gi` | DB pool size aligned with limits. |
| `notification-gateway` | `1000m` | `2000m` | `2Gi` | `4Gi` | Memory sized for 10k connections. |
| Other stateless APIs | `500m` | `1000m` | `512Mi` | `1Gi` | Standard stateless defaults. |

---

## 5. Public Ingress & Router Configuration

Public HTTP/REST and WebSocket traffic routes through the NGINX Ingress Controller.

### Ingress Routing Rules:
```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: tradedrift-ingress
  namespace: tradedrift-apps
  annotations:
    kubernetes.io/ingress.class: "nginx"
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600" # Keep WebSockets open
    nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
spec:
  rules:
  - host: api.tradedrift.com
    http:
      paths:
      - path: /auth
        pathType: Prefix
        backend:
          service:
            name: auth-service
            port:
              number: 8080
      - path: /users
        pathType: Prefix
        backend:
          service:
            name: user-service
            port:
              number: 8080
      - path: /orders
        pathType: Prefix
        backend:
          service:
            name: order-service
            port:
              number: 8080
      - path: /markets
        pathType: Prefix
        backend:
          service:
            name: market-service-api
            port:
              number: 8080
      - path: /portfolio
        pathType: Prefix
        backend:
          service:
            name: portfolio-service
            port:
              number: 8080
      - path: /ws
        pathType: Exact
        backend:
          service:
            name: notification-gateway
            port:
              number: 8080

---

## 6. Secrets, ConfigMaps, and Network Policies

### 6.1 Configuration vs Secret Separation
* **Kubernetes Secrets:** Sensitive credentials, including JWT signing keys, PostgreSQL passwords, Redis Sentinel auth tokens, and Kafka SASL credentials, must reside exclusively in Kubernetes Secrets. Secrets are mounted as environment variables or read-only volume directories.
* **ConfigMaps:** ConfigMaps are used strictly for non-sensitive values, such as broker URLs, database hostnames, cache expiration configurations, and debug log levels. Business definitions or constants (like market `tick_size`) must not reside in ConfigMaps; they are queried from database tables.

### 6.2 Network Policies (`NetworkPolicy`)
To limit the lateral blast radius of a pod compromise, ingress and egress rules are locked down:
* **API Gateway NetworkPolicy:** Allows ingress from the public Ingress controller namespace. Allows egress to services (`auth-service`, `order-service`, `market-service-api`, `portfolio-service`, `trade-service`).
* **Transactional Services (`wallet-service`, `order-service`):** Deny all direct ingress from the public Ingress namespace. Ingress is permitted only from specific application namespaces (e.g. `api-gateway` -> `order-service` -> `wallet-service`).
* **Stateful Datastores:** Database ports are restricted to accept connections only from their client namespace pods (e.g., PostgreSQL only accepts ports from `wallet-service` or `settlement-service`).

---

## 7. Deployment Upgrade Runbooks

### 7.1 Zero-Downtime Stateless Rolling Updates
For stateless API deployments, update rollouts utilize a rolling strategy to avoid capacity drops:
```yaml
spec:
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 0
      maxSurge: 1
```
* **Procedure:** A new replica pod is started -> wait for its readiness check to pass -> add to load balancer routing -> gracefully terminate the old pod replica.

### 7.2 Single Active Replica (Matching Engine) Upgrade Runbook
Because the Matching Engine is stateful and runs as a singleton process, a rolling update is not possible. Upgrades are coordinated using a Recreate policy:
1. **Decommission Phase:** The active pod stops processing incoming Kafka partition offsets, flushes in-memory transaction logs, commits a database state checkpoint, and gracefully stops the process.
2. **Re-routing:** Settle/API routes pull the ME pod from readiness registers immediately.
3. **Provisioning Phase:** A new container pod starts up, mounts the PersistentVolume, loads the database checkpoint record, and replays any trailing Kafka offsets from the checkpoint marker.
4. **Activation:** Once offsets are caught up, the readiness check is marked OK, and the engine resumes processing incoming order execution streams.
```

---

## 6. Service Invariants

- **DEP-1 (Singleton Guarantee):** The Matching Engine StatefulSet and Market Service Cron Deployment must never run with replicas > 1.
- **DEP-2 (QoS Reservation):** High-performance components (`matching-engine`) must use identical request and limit declarations to enforce Kubernetes Guaranteed QoS scheduling, preventing eviction during node memory constraints.
- **DEP-3 (Recreate Strategy):** Single-replica deployments must enforce the `Recreate` rollout strategy. `RollingUpdate` is prohibited to prevent simultaneous executions during deployments.
- **DEP-4 (Spread Constraint Compliance):** Core transaction engines must use `topologySpreadConstraints` across AZs.
- **DEP-5 (Secrets Protection):** All cluster credentials must be injected via `Secret` mounts. ConfigMaps must not hold plain-text secrets.
- **DEP-6 (Zero-Downtime Rollover):** Stateless services must use `maxUnavailable: 0` and `maxSurge: 1` configurations.
