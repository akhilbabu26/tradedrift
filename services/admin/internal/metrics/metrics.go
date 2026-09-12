package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	Namespace = "tradedrift"
	Subsystem = "admin"
)

// Canonical service and probe status values ensuring 1-hot invariant across all subsystems
const (
	StatusUP       = "UP"
	StatusDegraded = "DEGRADED"
	StatusDown     = "DOWN"
	StatusTimeout  = "TIMEOUT"
	StatusUnknown  = "UNKNOWN"
)

// Canonical service names ensuring bounded label cardinality
const (
	ServiceAuth            = "auth"
	ServiceWallet          = "wallet"
	ServiceTrade           = "trade"
	ServicePortfolio       = "portfolio"
	ServiceLiquidityEngine = "liquidity_engine"
	ServiceNotification    = "notification"
	ServicePostgres        = "postgres"
	ServiceKafka           = "kafka"
)

var allowedStatuses = []string{
	StatusUP,
	StatusDegraded,
	StatusDown,
	StatusTimeout,
	StatusUnknown,
}

var (
	// HTTP Metrics
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "http_requests_total",
			Help:      "Total number of HTTP requests processed by the admin service.",
		},
		[]string{"method", "route", "status"},
	)

	HTTPRequestDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "http_request_duration_seconds",
			Help:      "Histogram of HTTP request latencies for admin endpoints.",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
		},
		[]string{"method", "route"},
	)

	// Admin Operations Metrics
	OperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "operations_total",
			Help:      "Total number of admin mutation operations completed or failed.",
		},
		[]string{"operation_type", "result"},
	)

	OperationsInFlight = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "operations_in_flight",
			Help:      "Current number of admin mutation operations actively processing.",
		},
		[]string{"operation_type"},
	)

	OperationDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "operation_duration_seconds",
			Help:      "Duration in seconds of admin mutation operations.",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
		},
		[]string{"operation_type"},
	)

	// Outbox Metrics
	OutboxEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "outbox_events_total",
			Help:      "Total number of outbox events published to Kafka or marked terminally failed.",
		},
		[]string{"topic", "result"},
	)

	OutboxPublishDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "outbox_publish_duration_seconds",
			Help:      "Latency of Kafka publish ack per event.",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5},
		},
		[]string{"topic"},
	)

	OutboxRetriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "outbox_retries_total",
			Help:      "Total count of outbox publish retries scheduled.",
		},
		[]string{"topic"},
	)

	OutboxBacklogDepth = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "outbox_backlog_depth",
			Help:      "Number of pending or processing outbox events waiting in database.",
		},
	)

	OutboxOldestUnpublishedSeconds = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "outbox_oldest_unpublished_seconds",
			Help:      "Age in seconds of the oldest pending or processing outbox event.",
		},
	)

	OutboxWorkerErrorsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "outbox_worker_errors_total",
			Help:      "Total number of transient errors encountered by outbox publisher loop.",
		},
	)

	// Saga Metrics
	SagaTasksTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "saga_tasks_total",
			Help:      "Total count of saga tasks resolved (COMPLETED or EXHAUSTED).",
		},
		[]string{"task_type", "result"},
	)

	SagaTaskDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "saga_task_duration_seconds",
			Help:      "Duration of downstream saga task execution.",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0},
		},
		[]string{"task_type"},
	)

	SagaPendingQueueDepth = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "saga_pending_queue_depth",
			Help:      "Number of saga tasks currently pending or processing.",
		},
	)

	SagaQueueCount = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "saga_queue_count",
			Help:      "Current count of saga tasks partitioned by state (PENDING, RETRYING, EXHAUSTED).",
		},
		[]string{"status"},
	)

	SagaWorkerErrorsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "saga_worker_errors_total",
			Help:      "Total number of worker loop errors encountered in saga background worker.",
		},
	)

	// Health Metrics
	SystemOverallStatus = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "system_overall_status",
			Help:      "Aggregated platform health status: 2 = HEALTHY, 1 = DEGRADED, 0 = UNHEALTHY.",
		},
	)

	SystemHealthStatus = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "system_health_status",
			Help:      "1-hot boolean gauge indicating current dependency status (UP, DEGRADED, DOWN, TIMEOUT, UNKNOWN).",
		},
		[]string{"service", "status"},
	)

	HealthProbeLatencySeconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "health_probe_latency_seconds",
			Help:      "Most recent probe latency in seconds for dependency.",
		},
		[]string{"service"},
	)

	HealthProbeDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "health_probe_duration_seconds",
			Help:      "Histogram of probe latencies for dependencies.",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.0},
		},
		[]string{"service"},
	)

	HealthProbeLastRunTimestampSeconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "health_probe_last_run_timestamp_seconds",
			Help:      "Unix timestamp in seconds of the last executed probe for dependency.",
		},
		[]string{"service"},
	)

	HealthProbeFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Subsystem: Subsystem,
			Name:      "health_probe_failures_total",
			Help:      "Total count of health probe failures by service and failure reason (timeout, connection_error, grpc_error, http_5xx, http_503, unknown).",
		},
		[]string{"service", "reason"},
	)
)

// RecordHTTPRequest records status code and duration with parameterized route
func RecordHTTPRequest(method, route string, statusCode int, duration time.Duration) {
	statusStr := strconv.Itoa(statusCode)
	HTTPRequestsTotal.WithLabelValues(method, route, statusStr).Inc()
	HTTPRequestDurationSeconds.WithLabelValues(method, route).Observe(duration.Seconds())
}

// RecordOperationStart increments the in-flight gauge for an operation
func RecordOperationStart(opType string) {
	OperationsInFlight.WithLabelValues(opType).Inc()
}

// RecordOperationComplete decrements in-flight, records operation result counter and duration histogram
func RecordOperationComplete(opType string, success bool, duration time.Duration) {
	OperationsInFlight.WithLabelValues(opType).Dec()
	result := "COMPLETED"
	if !success {
		result = "FAILED"
	}
	OperationsTotal.WithLabelValues(opType, result).Inc()
	OperationDurationSeconds.WithLabelValues(opType).Observe(duration.Seconds())
}

// RecordOutboxPublish records a successful outbox publish
func RecordOutboxPublish(topic string, duration time.Duration) {
	OutboxEventsTotal.WithLabelValues(topic, "PUBLISHED").Inc()
	OutboxPublishDurationSeconds.WithLabelValues(topic).Observe(duration.Seconds())
}

// RecordOutboxRetry records a transient outbox publish retry scheduled
func RecordOutboxRetry(topic string) {
	OutboxRetriesTotal.WithLabelValues(topic).Inc()
}

// RecordOutboxFailed records a terminal outbox failure
func RecordOutboxFailed(topic string) {
	OutboxEventsTotal.WithLabelValues(topic, "FAILED").Inc()
}

// RecordSagaComplete records a successfully resolved saga task
func RecordSagaComplete(taskType string, duration time.Duration) {
	SagaTasksTotal.WithLabelValues(taskType, "COMPLETED").Inc()
	SagaTaskDurationSeconds.WithLabelValues(taskType).Observe(duration.Seconds())
}

// RecordSagaExhausted records a dead-letter / max retry exceeded saga task
func RecordSagaExhausted(taskType string, duration time.Duration) {
	SagaTasksTotal.WithLabelValues(taskType, "EXHAUSTED").Inc()
	SagaTaskDurationSeconds.WithLabelValues(taskType).Observe(duration.Seconds())
}

// RecordSagaQueueStats updates both the aggregate pending queue depth and partitioned status gauges
func RecordSagaQueueStats(pending, retrying, exhausted int) {
	SagaPendingQueueDepth.Set(float64(pending + retrying))
	SagaQueueCount.WithLabelValues("PENDING").Set(float64(pending))
	SagaQueueCount.WithLabelValues("RETRYING").Set(float64(retrying))
	SagaQueueCount.WithLabelValues("EXHAUSTED").Set(float64(exhausted))
}

// RecordHealthProbe updates all metrics for a given service probe run maintaining the 1-hot status invariant.
func RecordHealthProbe(service string, currentStatus string, latency time.Duration, runTime time.Time) {
	// Set 1-hot boolean gauges across UP, DEGRADED, DOWN, TIMEOUT, UNKNOWN
	for _, st := range allowedStatuses {
		val := 0.0
		if st == currentStatus {
			val = 1.0
		}
		SystemHealthStatus.WithLabelValues(service, st).Set(val)
	}

	HealthProbeLatencySeconds.WithLabelValues(service).Set(latency.Seconds())
	HealthProbeDurationSeconds.WithLabelValues(service).Observe(latency.Seconds())
	HealthProbeLastRunTimestampSeconds.WithLabelValues(service).Set(float64(runTime.Unix()))
}

// RecordHealthFailure records a probe failure with bounded reason
func RecordHealthFailure(service, reason string) {
	HealthProbeFailuresTotal.WithLabelValues(service, reason).Inc()
}

// SetSystemOverallStatus sets the numerical overall status gauge:
// 2 = HEALTHY, 1 = DEGRADED, 0 = UNHEALTHY
func SetSystemOverallStatus(statusStr string) {
	val := 0.0
	switch statusStr {
	case "HEALTHY":
		val = 2.0
	case "DEGRADED":
		val = 1.0
	case "UNHEALTHY":
		val = 0.0
	}
	SystemOverallStatus.Set(val)
}
