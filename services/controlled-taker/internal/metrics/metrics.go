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
