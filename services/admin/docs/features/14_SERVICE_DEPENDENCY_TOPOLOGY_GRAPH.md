# Feature 14: Service Dependency Topology Graph Engine

## 1. What This Feature Does
The **Service Dependency Topology Graph Engine** provides an interactive architectural graph of the TradeDrift cluster via `GET /api/v1/admin/topology`:
* **9 Microservice Nodes**: Categorized into 4 functional tiers (`control_plane`, `core_accounts`, `trading_engine`, `edge_notification`) and 1 infrastructure tier (`infrastructure`).
* **18 Directional Communication Edges**: Documents exact inter-service protocols (`gRPC`, `Kafka Pub/Sub`, `TCP (pgx)`, `HTTP REST`).
* **Live Operational Overlay**: Injects real-time status (`UP`, `DEGRADED`, `DOWN`), round-trip latencies, and circuit-breaker states directly into graph nodes.

---

## 2. Why We Need It
During production incidents, operational chaos often stems from a lack of architectural visibility:
1. **Understanding Upstream/Downstream Blast Radius**: If the Wallet service is failing, operators need an immediate visual representation of which downstream systems (e.g. Order Service, Settlement) will experience cascading faults.
2. **Protocol Clarity**: Different dependencies fail differently (e.g., gRPC channel transient failure vs. Kafka topic partition rebalance). Graph edges explicitly identify the transport protocol.
3. **Frontend Dashboard Integration**: Enables rich, interactive WebGL/SVG architectural topology maps in modern React/Next.js administrative dashboards.

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **Topology Engine** | [`services/admin/internal/service/topology_engine.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/topology_engine.go#L20-L190) | Generates nodes, edges, merges live `HealthWorker` state |
| **HTTP Handler** | [`services/admin/internal/handler/topology_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/topology_handler.go) | `HandleGetTopology` returns JSON graph |
| **Route Registration**| [`services/admin/internal/handler/router.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/router.go#L103-L106) | `GET /api/v1/admin/topology` protected by `RequireAdmin` |

---

## 4. How We Achieve This Feature

1. **Static Graph Foundation**:
   Defines the platform's architectural blueprint:
   - Tiers: `control_plane` (Admin), `core_accounts` (Auth, Wallet), `trading_engine` (Trade, Liquidity Engine), `edge_notification` (Portfolio, Notification), `infrastructure` (PostgreSQL, Kafka).
   - Edges: e.g. `admin` &rarr; `auth` (protocol: `gRPC`), `order` &rarr; `wallet` (protocol: `gRPC`), `trade` &rarr; `kafka` (protocol: `Kafka Pub/Sub`).
2. **Dynamic Live State Injection**:
   The engine queries `healthWorker.GetLatestHealth()`:
   ```go
   snapshot := e.healthWorker.GetLatestHealth()
   for _, node := range nodes {
       if dep, exists := snapshot.Services[node.ID]; exists {
           node.Status = dep.Status
           node.LatencyMS = dep.LatencyMS
       }
   }
   ```
3. **High-Speed Delivery**:
   Since the live health data is retrieved from in-memory snapshots under `RLock()`, the full topological graph generates and returns in **under 5 milliseconds**.

---

## 5. Execution Flow

```
                                OPERATOR (Postman) / FRONTEND
                                              │
                                              │ 1. GET /api/v1/admin/topology
                                              │    (Bearer JWT with role="admin")
                                              ▼
                             ┌─────────────────────────────────┐
                             │        TOPOLOGY HANDLER         │
                             └────────────────┬────────────────┘
                                              │
                                              │ 2. topologyEng.GetTopology(ctx)
                                              ▼
                             ┌─────────────────────────────────┐
                             │         TOPOLOGY ENGINE         │
                             └────────────────┬────────────────┘
                                              │
                                              │ 3. healthWorker.GetLatestHealth()
                                              ▼
                             ┌─────────────────────────────────┐
                             │      HEALTH WORKER SNAPSHOT     │
                             │  (Live latencies & node status) │
                             └────────────────┬────────────────┘
                                              │
                                              ▼
                             ┌─────────────────────────────────┐
                             │ 4. Merge Live State with Model  │
                             │    - 9 Microservices (4 Tiers)  │
                             │    - 18 Directional Comm Edges  │
                             │    - Dynamic Degraded Overlays  │
                             └────────────────┬────────────────┘
                                              │
                                              │ 5. Return Complete Graph JSON (<5ms)
                                              ▼
                             ┌─────────────────────────────────┐
                             │           HTTP 200 OK           │
                             │ Interactive Architectural Graph │
                             └─────────────────────────────────┘
```
