package service

import (
	"context"
	"time"

	"tradedrift/services/admin/internal/metrics"
)

// ServiceTier defines the architectural classification of a platform service.
type ServiceTier string

const (
	TierControlPlane     ServiceTier = "control_plane"
	TierCoreAccounts     ServiceTier = "core_accounts"
	TierTradingEngine    ServiceTier = "trading_engine"
	TierEdgeNotification ServiceTier = "edge_notification"
	TierInfrastructure   ServiceTier = "infrastructure"
)

// TopologyNode represents a single microservice, datastore, or broker in the platform graph.
type TopologyNode struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Tier         ServiceTier `json:"tier"`
	Status       string      `json:"status"` // "UP", "DEGRADED", "DOWN", "UNKNOWN"
	LatencyMs    int64       `json:"latency_ms,omitempty"`
	Dependencies []string    `json:"dependencies"`
}

// TopologyEdge represents a directional communication link (RPC, REST, TCP, or Pub/Sub).
type TopologyEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Protocol string `json:"protocol"` // "gRPC", "HTTP", "TCP", "Kafka Pub/Sub"
	Status   string `json:"status"`   // "HEALTHY", "DEGRADED", "DOWN"
}

// PlatformTopology is the full graph representation with runtime health overlay.
type PlatformTopology struct {
	GeneratedAt time.Time      `json:"generated_at"`
	TotalNodes  int            `json:"total_nodes"`
	Nodes       []TopologyNode `json:"nodes"`
	Edges       []TopologyEdge `json:"edges"`
	Summary     map[string]int `json:"summary"` // count by status
}

// StaticDependencyDeclaration defines the compile-time platform topology graph.
type staticServiceDef struct {
	Name         string
	Tier         ServiceTier
	Dependencies []string
	Protocols    map[string]string // target -> protocol
}

var platformStaticDependencies = []staticServiceDef{
	{
		Name:         metrics.ServiceAdmin,
		Tier:         TierControlPlane,
		Dependencies: []string{metrics.ServicePostgres, metrics.ServiceKafka, metrics.ServiceAuth, metrics.ServiceWallet},
		Protocols: map[string]string{
			metrics.ServicePostgres: "TCP (pgxpool)",
			metrics.ServiceKafka:    "TCP / Kafka-Go",
			metrics.ServiceAuth:     "gRPC",
			metrics.ServiceWallet:   "gRPC",
		},
	},
	{
		Name:         metrics.ServiceTrade,
		Tier:         TierTradingEngine,
		Dependencies: []string{metrics.ServiceAuth, metrics.ServiceWallet, metrics.ServiceLiquidityEngine, metrics.ServicePostgres, metrics.ServiceKafka},
		Protocols: map[string]string{
			metrics.ServiceAuth:            "gRPC",
			metrics.ServiceWallet:          "gRPC",
			metrics.ServiceLiquidityEngine: "HTTP REST",
			metrics.ServicePostgres:        "TCP (pgx)",
			metrics.ServiceKafka:           "Kafka Pub/Sub",
		},
	},
	{
		Name:         metrics.ServiceLiquidityEngine,
		Tier:         TierTradingEngine,
		Dependencies: []string{metrics.ServiceWallet, metrics.ServiceKafka},
		Protocols: map[string]string{
			metrics.ServiceWallet: "gRPC",
			metrics.ServiceKafka:  "Kafka Pub/Sub",
		},
	},
	{
		Name:         metrics.ServiceAuth,
		Tier:         TierCoreAccounts,
		Dependencies: []string{metrics.ServicePostgres, metrics.ServiceWallet},
		Protocols: map[string]string{
			metrics.ServicePostgres: "TCP (SQL)",
			metrics.ServiceWallet:   "gRPC",
		},
	},
	{
		Name:         metrics.ServiceWallet,
		Tier:         TierCoreAccounts,
		Dependencies: []string{metrics.ServicePostgres, metrics.ServiceKafka},
		Protocols: map[string]string{
			metrics.ServicePostgres: "TCP (SQL)",
			metrics.ServiceKafka:    "Kafka Pub/Sub",
		},
	},
	{
		Name:         metrics.ServicePortfolio,
		Tier:         TierEdgeNotification,
		Dependencies: []string{metrics.ServicePostgres, metrics.ServiceKafka},
		Protocols: map[string]string{
			metrics.ServicePostgres: "TCP (SQL)",
			metrics.ServiceKafka:    "Kafka Pub/Sub",
		},
	},
	{
		Name:         metrics.ServiceNotification,
		Tier:         TierEdgeNotification,
		Dependencies: []string{metrics.ServiceKafka},
		Protocols: map[string]string{
			metrics.ServiceKafka: "Kafka Pub/Sub",
		},
	},
	{
		Name:         metrics.ServicePostgres,
		Tier:         TierInfrastructure,
		Dependencies: []string{},
	},
	{
		Name:         metrics.ServiceKafka,
		Tier:         TierInfrastructure,
		Dependencies: []string{},
	},
}

// TopologyEngine builds a topological dependency graph with dynamic health overlays from HealthWorker.
type TopologyEngine struct {
	healthWorker *HealthWorker
}

// NewTopologyEngine constructs a TopologyEngine.
func NewTopologyEngine(hw ...*HealthWorker) *TopologyEngine {
	var worker *HealthWorker
	if len(hw) > 0 {
		worker = hw[0]
	}
	return &TopologyEngine{healthWorker: worker}
}

// SetHealthWorker configures or updates the HealthWorker reference.
func (e *TopologyEngine) SetHealthWorker(hw *HealthWorker) {
	e.healthWorker = hw
}

// GetTopology generates the topological graph with real-time status overlays.
func (e *TopologyEngine) GetTopology(ctx context.Context) *PlatformTopology {
	now := time.Now().UTC()

	var healthSnapshot *SystemHealthResponse
	if e.healthWorker != nil {
		healthSnapshot = e.healthWorker.GetLatestHealth()
	}

	// Status lookup table
	statusMap := make(map[string]ServiceStatusReport)
	if healthSnapshot != nil {
		for k, v := range healthSnapshot.Services {
			statusMap[k] = v
		}
		statusMap[metrics.ServicePostgres] = healthSnapshot.Admin.Postgres
		statusMap[metrics.ServiceKafka] = healthSnapshot.Admin.Kafka
		statusMap[metrics.ServiceAdmin] = ServiceStatusReport{
			Name:   metrics.ServiceAdmin,
			Status: healthSnapshot.Admin.Status,
		}
	}

	summary := map[string]int{
		"UP":       0,
		"DEGRADED": 0,
		"DOWN":     0,
		"TIMEOUT":  0,
		"UNKNOWN":  0,
	}

	var nodes []TopologyNode
	var edges []TopologyEdge

	for _, def := range platformStaticDependencies {
		st := "UNKNOWN"
		var latencyMs int64

		if rep, ok := statusMap[def.Name]; ok && rep.Status != "" {
			st = rep.Status
			latencyMs = rep.LatencyMs
		}

		if count, ok := summary[st]; ok {
			summary[st] = count + 1
		} else {
			summary["UNKNOWN"]++
		}

		nodes = append(nodes, TopologyNode{
			ID:           def.Name,
			Name:         def.Name,
			Tier:         def.Tier,
			Status:       st,
			LatencyMs:    latencyMs,
			Dependencies: def.Dependencies,
		})

		for _, dep := range def.Dependencies {
			edgeStatus := "HEALTHY"
			targetStatus := "UNKNOWN"
			if depRep, ok := statusMap[dep]; ok {
				targetStatus = depRep.Status
			}

			// Consistent edge health evaluation:
			// If either source or target is unavailable (DOWN or TIMEOUT), the directional link is DOWN.
			// If either is DEGRADED, the link is DEGRADED.
			// If either is UNKNOWN (unprobed), the link is UNKNOWN.
			if st == "DOWN" || st == "TIMEOUT" || targetStatus == "DOWN" || targetStatus == "TIMEOUT" {
				edgeStatus = "DOWN"
			} else if st == "DEGRADED" || targetStatus == "DEGRADED" {
				edgeStatus = "DEGRADED"
			} else if st == "UNKNOWN" || targetStatus == "UNKNOWN" {
				edgeStatus = "UNKNOWN"
			}

			proto := "TCP"
			if p, ok := def.Protocols[dep]; ok {
				proto = p
			}

			edges = append(edges, TopologyEdge{
				Source:   def.Name,
				Target:   dep,
				Protocol: proto,
				Status:   edgeStatus,
			})
		}
	}

	return &PlatformTopology{
		GeneratedAt: now,
		TotalNodes:  len(nodes),
		Nodes:       nodes,
		Edges:       edges,
		Summary:     summary,
	}
}

// GetImpactedDownstreamServices finds which services depend on a degraded service.
func (e *TopologyEngine) GetImpactedDownstreamServices(serviceName string) []string {
	var impacted []string
	for _, def := range platformStaticDependencies {
		for _, dep := range def.Dependencies {
			if dep == serviceName {
				impacted = append(impacted, def.Name)
				break
			}
		}
	}
	return impacted
}

// GetUpstreamDependencies finds which services the specified service depends on.
func (e *TopologyEngine) GetUpstreamDependencies(serviceName string) []string {
	for _, def := range platformStaticDependencies {
		if def.Name == serviceName {
			result := make([]string, len(def.Dependencies))
			copy(result, def.Dependencies)
			return result
		}
	}
	return nil
}

// GetRelatedDependencies returns all downstream and upstream services associated with the given service.
func (e *TopologyEngine) GetRelatedDependencies(serviceName string) []string {
	visited := make(map[string]bool)
	var result []string

	for _, s := range e.GetImpactedDownstreamServices(serviceName) {
		if !visited[s] {
			visited[s] = true
			result = append(result, s)
		}
	}

	for _, s := range e.GetUpstreamDependencies(serviceName) {
		if !visited[s] {
			visited[s] = true
			result = append(result, s)
		}
	}

	return result
}
