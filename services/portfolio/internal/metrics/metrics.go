package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	EventsConsumedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tradedrift",
			Subsystem: "portfolio",
			Name:      "events_consumed_total",
			Help:      "Total number of settled trade events consumed from Kafka by status.",
		},
		[]string{"status", "market"},
	)

	DBDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "tradedrift",
			Subsystem: "portfolio",
			Name:      "db_duration_seconds",
			Help:      "Duration of database operations in seconds.",
			Buckets:   []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
		},
		[]string{"query"},
	)

	ValuationDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "tradedrift",
			Subsystem: "portfolio",
			Name:      "valuation_duration_seconds",
			Help:      "Duration of dynamic portfolio valuation computations in seconds.",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
		},
		[]string{"endpoint"},
	)

	GRPCRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tradedrift",
			Subsystem: "portfolio",
			Name:      "grpc_requests_total",
			Help:      "Total inbound gRPC requests handled by method and status code.",
		},
		[]string{"method", "code"},
	)

	GRPCDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "tradedrift",
			Subsystem: "portfolio",
			Name:      "grpc_duration_seconds",
			Help:      "Latency of inbound gRPC requests in seconds.",
			Buckets:   []float64{0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
		},
		[]string{"method"},
	)

	OutboxPending = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "tradedrift",
			Subsystem: "portfolio",
			Name:      "outbox_pending",
			Help:      "Number of unpublished portfolio outbox events waiting in database.",
		},
	)

	OutboxPublishTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tradedrift",
			Subsystem: "portfolio",
			Name:      "outbox_publish_total",
			Help:      "Total number of portfolio outbox events dispatched to Kafka by status.",
		},
		[]string{"status"},
	)

	AccountingViolationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tradedrift",
			Subsystem: "portfolio",
			Name:      "accounting_violations_total",
			Help:      "Total number of critical accounting invariant violations detected.",
		},
		[]string{"violation_type"},
	)
)
