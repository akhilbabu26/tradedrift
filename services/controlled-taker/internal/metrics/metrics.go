// Package metrics provides Prometheus metric collectors for CTS.
package metrics

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

var (
	OrdersSubmitted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cts_orders_submitted_total",
			Help: "Total number of controlled taker orders submitted",
		},
		[]string{"market_id", "side", "profile"},
	)

	OrdersFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cts_orders_failed_total",
			Help: "Total number of failed or rejected controlled taker orders",
		},
		[]string{"market_id", "reason"},
	)

	ConservativeNotionalVolume = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cts_conservative_notional_usdt_total",
			Help: "Cumulative conservative notional volume submitted by CTS in USDT (filled_qty * side-specific conservative price)",
		},
		[]string{"market_id"},
	)

	CircuitBreakerTripped = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cts_circuit_breaker_tripped_total",
			Help: "Total number of circuit breaker trip occurrences",
		},
		[]string{"market_id"},
	)

	OrdersFullyFilled = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cts_orders_fully_filled_total",
			Help: "Total number of controlled taker orders completely filled immediately",
		},
		[]string{"market_id"},
	)

	OrdersPartiallyFilled = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cts_orders_partially_filled_total",
			Help: "Total number of controlled taker orders partially filled and residual cancelled",
		},
		[]string{"market_id"},
	)

	OrdersCancelledUnfilled = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cts_orders_cancelled_unfilled_total",
			Help: "Total number of controlled taker orders cancelled with zero fills",
		},
		[]string{"market_id"},
	)

	UnresolvedResiduals = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cts_unresolved_residuals_total",
			Help: "Critical alert counter for orders where residual status could not be verified cancelled",
		},
		[]string{"market_id"},
	)

	ResidualsCancelled = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cts_residuals_cancelled_total",
			Help: "Total number of residual order quantities cancelled to prevent becoming a maker",
		},
		[]string{"market_id"},
	)

	OrderLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cts_order_latency_seconds",
			Help:    "Latency of order submission and verification loop in seconds",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
		[]string{"market_id", "profile"},
	)

	DepthReadErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cts_depth_read_errors_total",
			Help: "Total number of errors encountered reading or validating Redis depth snapshots",
		},
		[]string{"market_id", "reason"},
	)

	SweepOrders = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cts_sweep_orders_total",
			Help: "Total number of multi-level sweep orders executed by CTS",
		},
		[]string{"market_id", "profile", "side"},
	)

	SweepLevels = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cts_sweep_levels_total",
			Help: "Distribution of reachable depth levels involved in sweeps",
		},
		[]string{"market_id", "profile", "levels"},
	)

	SweepQuantity = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cts_sweep_quantity_total",
			Help: "Cumulative filled base quantity executed via sweeps",
		},
		[]string{"market_id", "profile"},
	)

	RegimeTransitions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cts_regime_transitions_total",
			Help: "Total number of market regime transitions",
		},
		[]string{"market_id", "from_regime", "to_regime"},
	)

	RegimeCurrent = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cts_regime_current",
			Help: "Current active market regime (1 = active state)",
		},
		[]string{"market_id", "regime"},
	)

	ProfileSelected = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cts_profile_selected_total",
			Help: "Total number of taker order profiles selected",
		},
		[]string{"market_id", "regime", "profile"},
	)

	InterarrivalSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cts_interarrival_seconds",
			Help:    "Distribution of inter-arrival wait durations between order cycles",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300},
		},
		[]string{"market_id", "regime"},
	)

	PostHighCooldownSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cts_post_high_cooldown_seconds",
			Help:    "Duration of post-HIGH sweep pacing cooldown floors",
			Buckets: []float64{5, 15, 30, 45, 60, 90, 120, 180, 300},
		},
		[]string{"market_id", "severity"},
	)

	DepthAgeSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cts_depth_age_seconds",
			Help:    "Observed age of orderbook depth snapshot at read or submission",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 3, 5, 10},
		},
		[]string{"market_id", "stage"},
	)

	SubmissionLatencySeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cts_order_submission_latency_seconds",
			Help:    "Latency of gRPC order submission to Order Service in seconds",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
		[]string{"market_id", "profile"},
	)
)

func init() {
	prometheus.MustRegister(OrdersSubmitted)
	prometheus.MustRegister(OrdersFailed)
	prometheus.MustRegister(ConservativeNotionalVolume)
	prometheus.MustRegister(CircuitBreakerTripped)
	prometheus.MustRegister(OrdersFullyFilled)
	prometheus.MustRegister(OrdersPartiallyFilled)
	prometheus.MustRegister(OrdersCancelledUnfilled)
	prometheus.MustRegister(UnresolvedResiduals)
	prometheus.MustRegister(ResidualsCancelled)
	prometheus.MustRegister(OrderLatency)
	prometheus.MustRegister(DepthReadErrors)
	prometheus.MustRegister(SweepOrders)
	prometheus.MustRegister(SweepLevels)
	prometheus.MustRegister(SweepQuantity)
	prometheus.MustRegister(RegimeTransitions)
	prometheus.MustRegister(RegimeCurrent)
	prometheus.MustRegister(ProfileSelected)
	prometheus.MustRegister(InterarrivalSeconds)
	prometheus.MustRegister(PostHighCooldownSeconds)
	prometheus.MustRegister(DepthAgeSeconds)
	prometheus.MustRegister(SubmissionLatencySeconds)
}

// SetCurrentRegime updates the cts_regime_current gauge vector for all regimes.
func SetCurrentRegime(marketID, activeRegime string) {
	for _, reg := range []string{"QUIET", "NORMAL", "ACTIVE", "BURST"} {
		v := 0.0
		if reg == activeRegime {
			v = 1.0
		}
		RegimeCurrent.WithLabelValues(marketID, reg).Set(v)
	}
}

// Server serves Prometheus metrics.
type Server struct {
	httpServer *http.Server
	logger     *zap.Logger
}

// NewServer creates a Prometheus HTTP scrape server.
func NewServer(port string, logger *zap.Logger) *Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	return &Server{
		httpServer: &http.Server{
			Addr:         ":" + port,
			Handler:      mux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
		},
		logger: logger,
	}
}

// Start starts the metrics server.
func (s *Server) Start() {
	go func() {
		s.logger.Info("Metrics server listening", zap.String("addr", s.httpServer.Addr))
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("Metrics server error", zap.Error(err))
		}
	}()
}

// Stop gracefully shuts down the metrics server.
func (s *Server) Stop(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
