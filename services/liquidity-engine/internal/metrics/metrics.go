// Package metrics provides Prometheus instrumentation for the Liquidity Engine.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics implements EngineMetrics and ReconcilerMetrics.
type Metrics struct {
	engineState       *prometheus.GaugeVec
	levelCount        *prometheus.GaugeVec
	reconcileDuration *prometheus.HistogramVec

	reconcileCreate  *prometheus.CounterVec
	reconcileCancel  *prometheus.CounterVec
	reconcileCorrect *prometheus.CounterVec
	reconcileNoop    *prometheus.CounterVec

	ordersFilled       *prometheus.CounterVec
	staleOrders        *prometheus.CounterVec
	meLivenessTimeouts *prometheus.CounterVec
	duplicateLevels    *prometheus.CounterVec
	meHealthProbes     *prometheus.CounterVec
	marketPauseEvents  *prometheus.CounterVec
}

// New creates and registers all LE Prometheus metrics.
func New() *Metrics {
	return &Metrics{
		engineState: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "le",
			Name:      "engine_state",
			Help:      "Current engine state (1 = active state)",
		}, []string{"state"}),

		levelCount: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "le",
			Name:      "active_levels",
			Help:      "Number of currently active MM order levels",
		}, []string{"market_id", "side"}),

		reconcileDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "le",
			Name:      "reconcile_duration_ms",
			Help:      "Reconcile cycle duration in milliseconds",
			Buckets:   []float64{1, 5, 10, 25, 50, 100, 250, 500},
		}, []string{"market_id"}),

		reconcileCreate: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "reconcile_create_total",
			Help:      "Total OrderCreated commands published",
		}, []string{"market_id"}),

		reconcileCancel: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "reconcile_cancel_total",
			Help:      "Total OrderCancelRequested commands published",
		}, []string{"market_id"}),

		reconcileCorrect: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "reconcile_correct_total",
			Help:      "Total CORRECT (cancel + replace) operations",
		}, []string{"market_id"}),

		reconcileNoop: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "reconcile_noop_total",
			Help:      "Total reconcile cycles with zero diff (desired == actual)",
		}, []string{"market_id"}),

		ordersFilled: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "orders_filled_total",
			Help:      "Total MM orders fully filled",
		}, []string{"market_id", "side"}),

		staleOrders: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "stale_orders_total",
			Help:      "Total orders that entered STALE state",
		}, []string{"market_id"}),

		meLivenessTimeouts: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "me_liveness_timeout_total",
			Help:      "Total ME liveness threshold exceeded events",
		}, []string{"market_id"}),

		duplicateLevels: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "duplicate_level_total",
			Help:      "Total duplicate MM LevelIDs detected in Order Service active snapshot",
		}, []string{"market_id"}),

		meHealthProbes: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "me_health_probe_total",
			Help:      "Total Matching Engine health probe attempts",
		}, []string{"status"}),

		marketPauseEvents: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "market_pause_total",
			Help:      "Total market pause and resume events",
		}, []string{"market_id", "action"}),
	}
}

// SetEngineState updates the engine_state gauge.
func (m *Metrics) SetEngineState(state string) {
	for _, s := range []string{"STARTING", "SYNCING", "RUNNING", "DEGRADED", "PAUSED", "STOPPED"} {
		v := 0.0
		if s == state {
			v = 1.0
		}
		m.engineState.WithLabelValues(s).Set(v)
	}
}

func (m *Metrics) SetLevelCount(marketID, side string, count int) {
	m.levelCount.WithLabelValues(marketID, side).Set(float64(count))
}

func (m *Metrics) ObserveReconcileDuration(marketID string, ms float64) {
	m.reconcileDuration.WithLabelValues(marketID).Observe(ms)
}

func (m *Metrics) IncReconcileCreate(marketID string) {
	m.reconcileCreate.WithLabelValues(marketID).Inc()
}

func (m *Metrics) IncReconcileCancel(marketID string) {
	m.reconcileCancel.WithLabelValues(marketID).Inc()
}

func (m *Metrics) IncReconcileCorrect(marketID string) {
	m.reconcileCorrect.WithLabelValues(marketID).Inc()
}

func (m *Metrics) IncReconcileNoop(marketID string) {
	m.reconcileNoop.WithLabelValues(marketID).Inc()
}

func (m *Metrics) IncOrdersFilled(marketID, side string) {
	m.ordersFilled.WithLabelValues(marketID, side).Inc()
}

func (m *Metrics) IncStaleOrders(marketID string) {
	m.staleOrders.WithLabelValues(marketID).Inc()
}

func (m *Metrics) IncMELivenessTimeout(marketID string) {
	m.meLivenessTimeouts.WithLabelValues(marketID).Inc()
}

func (m *Metrics) IncDuplicateMMLevel(marketID string) {
	m.duplicateLevels.WithLabelValues(marketID).Inc()
}

func (m *Metrics) IncMEHealthProbe(status string) {
	m.meHealthProbes.WithLabelValues(status).Inc()
}

func (m *Metrics) IncMarketPause(marketID, action string) {
	m.marketPauseEvents.WithLabelValues(marketID, action).Inc()
}
