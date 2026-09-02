package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// ── 1. Kafka Ingestion Metrics ────────────────────────────────────────────

	// EventsConsumedTotal counts settled trade events processed by the Kafka consumer.
	// Labels:
	//   status: "success", "duplicate", "poison", "retryable_error"
	EventsConsumedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tradedrift",
			Subsystem: "trade",
			Name:      "events_consumed_total",
			Help:      "Total number of TradeSettled Kafka events processed by status.",
		},
		[]string{"status"},
	)

	// DLQEventsTotal tracks events sent to the Dead-Letter Queue.
	// Labels:
	//   reason: "invalid_uuid", "self_trade", "zero_sequence", "invalid_financials", "sequence_conflict", "unknown"
	DLQEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tradedrift",
			Subsystem: "trade",
			Name:      "dlq_events_total",
			Help:      "Total number of poison TradeSettled events sent to the DLQ.",
		},
		[]string{"reason"},
	)

	// ConsumerEventAgeSeconds measures end-to-end event freshness
	// (time difference between settled_at timestamp and consumption time).
	// Labels:
	//   partition: Kafka partition number (e.g. "0", "1")
	ConsumerEventAgeSeconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "tradedrift",
			Subsystem: "trade",
			Name:      "consumer_event_age_seconds",
			Help:      "Lag in seconds between trade settlement timestamp and consumption time.",
		},
		[]string{"partition"},
	)

	// ── 2. Database Metrics ───────────────────────────────────────────────────

	// DBDurationSeconds measures database query durations.
	// Labels:
	//   operation: "create", "get_by_id", "list_by_user", "list_by_market"
	DBDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "tradedrift",
			Subsystem: "trade",
			Name:      "db_duration_seconds",
			Help:      "PostgreSQL query latency in seconds.",
			Buckets:   []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
		},
		[]string{"operation"},
	)

	// ── 3. gRPC Server Metrics ────────────────────────────────────────────────

	// GRPCRequestsTotal counts inbound RPC calls.
	// Labels:
	//   method: "GetTrade", "ListUserTrades", "ListMarketTrades"
	//   code: gRPC status code ("OK", "NotFound", "PermissionDenied", "InvalidArgument", "Internal")
	GRPCRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tradedrift",
			Subsystem: "trade",
			Name:      "grpc_requests_total",
			Help:      "Total number of gRPC requests by method and status code.",
		},
		[]string{"method", "code"},
	)

	// GRPCDurationSeconds measures gRPC method execution duration.
	// Labels:
	//   method: "GetTrade", "ListUserTrades", "ListMarketTrades"
	GRPCDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "tradedrift",
			Subsystem: "trade",
			Name:      "grpc_duration_seconds",
			Help:      "gRPC request duration in seconds.",
			Buckets:   []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
		},
		[]string{"method"},
	)
)
