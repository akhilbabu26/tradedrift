// Package metrics provides Prometheus instrumentation for the Liquidity Engine.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/shopspring/decimal"
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
	marketPauseEvents           *prometheus.CounterVec
	rebaseGenFailures           *prometheus.CounterVec
	expiryGenFailures           *prometheus.CounterVec
	quantityInvariantViolations *prometheus.CounterVec
	createBlocked               *prometheus.CounterVec
	refreshTotal                *prometheus.CounterVec

	bidExposure       *prometheus.GaugeVec
	askExposure       *prometheus.GaugeVec
	bidCapUtilization *prometheus.GaugeVec
	askCapUtilization *prometheus.GaugeVec
	repriceEvents     *prometheus.CounterVec

	rebaseDuration         *prometheus.HistogramVec
	rebaseTargetChanges    *prometheus.CounterVec
	rebaseLevelsRemaining  *prometheus.GaugeVec
	rebaseLevelsRepriced   *prometheus.CounterVec
	spreadBreachTotal      *prometheus.CounterVec
	spreadRecoveryDuration *prometheus.HistogramVec
	insideSpreadBps        *prometheus.GaugeVec

	cancelOSFailure *prometheus.CounterVec
	cancelMEPublish *prometheus.CounterVec
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

		rebaseGenFailures: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "rebase_generation_failure_total",
			Help:      "Total level price generation failures during rebase",
		}, []string{"market_id", "level_id"}),

		expiryGenFailures: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "expiry_generation_failure_total",
			Help:      "Total level price generation failures during order expiry",
		}, []string{"market_id"}),

		quantityInvariantViolations: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "quantity_invariant_violation_total",
			Help:      "Total quantity invariant violations detected (e.g. remaining exceeds original)",
		}, []string{"market_id"}),

		createBlocked: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "create_blocked_total",
			Help:      "Total new liquidity creations or replacements blocked by safety invariants",
		}, []string{"market_id", "reason"}),

		refreshTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "refresh_total",
			Help:      "Total orders successfully refreshed by smart replenishment or expiration",
		}, []string{"market_id", "reason"}),

		bidExposure: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "le",
			Name:      "bid_exposure_usdt",
			Help:      "Total current MM committed quote exposure in USDT",
		}, []string{"market_id"}),

		askExposure: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "le",
			Name:      "ask_exposure_base",
			Help:      "Total current MM committed base asset exposure",
		}, []string{"market_id"}),

		bidCapUtilization: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "le",
			Name:      "bid_cap_utilization_ratio",
			Help:      "Ratio of committed bid exposure to max configured bid cap (0.0 - 1.0+)",
		}, []string{"market_id"}),

		askCapUtilization: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "le",
			Name:      "ask_cap_utilization_ratio",
			Help:      "Ratio of committed ask exposure to max configured ask cap (0.0 - 1.0+)",
		}, []string{"market_id"}),

		repriceEvents: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "reprice_events_total",
			Help:      "Total reprice operations triggered across zones and reconcile cycles",
		}, []string{"market_id", "action"}),

		rebaseDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "le",
			Name:      "rebase_duration_seconds",
			Help:      "Duration of full or batch ladder rebase in seconds",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		}, []string{"market_id", "action"}),

		rebaseTargetChanges: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "rebase_target_changes_total",
			Help:      "Total times reference price shifted while a rebase was actively in flight",
		}, []string{"market_id"}),

		rebaseLevelsRemaining: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "le",
			Name:      "rebase_levels_remaining",
			Help:      "Number of eligible levels remaining to be repriced in active rebase",
		}, []string{"market_id"}),

		rebaseLevelsRepriced: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "rebase_levels_repriced_total",
			Help:      "Total levels repriced by rebase, partitioned by zone",
		}, []string{"market_id", "zone"}),

		spreadBreachTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "spread_breach_total",
			Help:      "Total occurrences of inside spread exceeding configured or derived breach threshold",
		}, []string{"market_id"}),

		spreadRecoveryDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "le",
			Name:      "spread_recovery_duration_seconds",
			Help:      "Duration in seconds from spread breach occurrence to restored inside spread",
			Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		}, []string{"market_id"}),

		insideSpreadBps: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "le",
			Name:      "inside_spread_bps",
			Help:      "Current inside spread of market maker ladder in basis points",
		}, []string{"market_id"}),

		cancelOSFailure: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "cancel_os_failure_total",
			Help:      "Total Order Service gRPC cancel failures",
		}, []string{"market_id"}),

		cancelMEPublish: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "le",
			Name:      "cancel_me_publish_total",
			Help:      "Total ME cancel commands published to Kafka",
		}, []string{"market_id"}),
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

func (m *Metrics) IncRebaseGenerationFailure(marketID, levelID string) {
	m.rebaseGenFailures.WithLabelValues(marketID, levelID).Inc()
}

func (m *Metrics) IncExpiryGenerationFailure(marketID string) {
	m.expiryGenFailures.WithLabelValues(marketID).Inc()
}

func (m *Metrics) IncQuantityInvariantViolation(marketID string) {
	m.quantityInvariantViolations.WithLabelValues(marketID).Inc()
}

func (m *Metrics) IncCreateBlocked(marketID, reason string) {
	if m != nil && m.createBlocked != nil {
		m.createBlocked.WithLabelValues(marketID, reason).Inc()
	}
}

func (m *Metrics) IncRefresh(marketID, reason string) {
	if m != nil && m.refreshTotal != nil {
		m.refreshTotal.WithLabelValues(marketID, reason).Inc()
	}
}

func (m *Metrics) SetExposure(marketID string, bidUSDT, askBase, bidCap, askCap decimal.Decimal) {
	if m == nil {
		return
	}
	if m.bidExposure != nil {
		m.bidExposure.WithLabelValues(marketID).Set(bidUSDT.InexactFloat64())
	}
	if m.askExposure != nil {
		m.askExposure.WithLabelValues(marketID).Set(askBase.InexactFloat64())
	}
	if m.bidCapUtilization != nil {
		if bidCap.GreaterThan(decimal.Zero) {
			ratio := bidUSDT.Div(bidCap).InexactFloat64()
			m.bidCapUtilization.WithLabelValues(marketID).Set(ratio)
		} else {
			m.bidCapUtilization.WithLabelValues(marketID).Set(0)
		}
	}
	if m.askCapUtilization != nil {
		if askCap.GreaterThan(decimal.Zero) {
			ratio := askBase.Div(askCap).InexactFloat64()
			m.askCapUtilization.WithLabelValues(marketID).Set(ratio)
		} else {
			m.askCapUtilization.WithLabelValues(marketID).Set(0)
		}
	}
}

func (m *Metrics) IncRepriceEvent(marketID, action string) {
	if m != nil && m.repriceEvents != nil {
		m.repriceEvents.WithLabelValues(marketID, action).Inc()
	}
}

func (m *Metrics) ObserveRebaseDuration(marketID, action string, seconds float64) {
	if m != nil && m.rebaseDuration != nil {
		m.rebaseDuration.WithLabelValues(marketID, action).Observe(seconds)
	}
}

func (m *Metrics) IncRebaseTargetChanges(marketID string) {
	if m != nil && m.rebaseTargetChanges != nil {
		m.rebaseTargetChanges.WithLabelValues(marketID).Inc()
	}
}

func (m *Metrics) SetRebaseLevelsRemaining(marketID string, count int) {
	if m != nil && m.rebaseLevelsRemaining != nil {
		m.rebaseLevelsRemaining.WithLabelValues(marketID).Set(float64(count))
	}
}

func (m *Metrics) IncRebaseLevelsRepriced(marketID, zone string) {
	if m != nil && m.rebaseLevelsRepriced != nil {
		m.rebaseLevelsRepriced.WithLabelValues(marketID, zone).Inc()
	}
}

func (m *Metrics) IncSpreadBreach(marketID string) {
	if m != nil && m.spreadBreachTotal != nil {
		m.spreadBreachTotal.WithLabelValues(marketID).Inc()
	}
}

func (m *Metrics) ObserveSpreadRecoveryDuration(marketID string, seconds float64) {
	if m != nil && m.spreadRecoveryDuration != nil {
		m.spreadRecoveryDuration.WithLabelValues(marketID).Observe(seconds)
	}
}

func (m *Metrics) SetInsideSpreadBps(marketID string, bps float64) {
	if m != nil && m.insideSpreadBps != nil {
		m.insideSpreadBps.WithLabelValues(marketID).Set(bps)
	}
}

func (m *Metrics) IncCancelOSFailure(marketID string) {
	if m != nil && m.cancelOSFailure != nil {
		m.cancelOSFailure.WithLabelValues(marketID).Inc()
	}
}

func (m *Metrics) IncCancelMEPublish(marketID string) {
	if m != nil && m.cancelMEPublish != nil {
		m.cancelMEPublish.WithLabelValues(marketID).Inc()
	}
}

