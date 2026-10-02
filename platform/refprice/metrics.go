package refprice

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds Prometheus collectors for the Reference Price Provider.
type Metrics struct {
	fetchSuccess   *prometheus.CounterVec
	fetchFailure   *prometheus.CounterVec
	fetchDuration  *prometheus.HistogramVec
	marketStale    *prometheus.CounterVec
	marketPaused   *prometheus.CounterVec
	marketRecovery *prometheus.CounterVec
	unknownMarket  *prometheus.CounterVec
}

var (
	defaultMetricsOnce sync.Once
	defaultMetrics     *Metrics
)

// DefaultMetrics returns the singleton Metrics registered with prometheus.DefaultRegisterer.
func DefaultMetrics() *Metrics {
	defaultMetricsOnce.Do(func() {
		defaultMetrics = NewMetrics(prometheus.DefaultRegisterer)
	})
	return defaultMetrics
}

// NewMetrics creates and registers refprice metrics with the given registerer.
// If an already-registered collector is encountered, it is reused to avoid panics.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	return &Metrics{
		fetchSuccess: registerOrGetCounterVec(reg, prometheus.CounterOpts{
			Name: "refprice_fetch_success_total",
			Help: "Total successful external reference price fetch attempts",
		}, []string{"source"}),

		fetchFailure: registerOrGetCounterVec(reg, prometheus.CounterOpts{
			Name: "refprice_fetch_failure_total",
			Help: "Total failed external reference price fetch attempts",
		}, []string{"source"}),

		fetchDuration: registerOrGetHistogramVec(reg, prometheus.HistogramOpts{
			Name:    "refprice_fetch_duration_seconds",
			Help:    "Duration of external reference price fetch requests in seconds",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"source"}),

		marketStale: registerOrGetCounterVec(reg, prometheus.CounterOpts{
			Name: "refprice_market_stale_total",
			Help: "Total state transitions from FRESH to STALE per market",
		}, []string{"market_id"}),

		marketPaused: registerOrGetCounterVec(reg, prometheus.CounterOpts{
			Name: "refprice_market_paused_total",
			Help: "Total state transitions from STALE/FRESH to PAUSED per market",
		}, []string{"market_id"}),

		marketRecovery: registerOrGetCounterVec(reg, prometheus.CounterOpts{
			Name: "refprice_recovery_total",
			Help: "Total state transitions from STALE/PAUSED back to FRESH per market",
		}, []string{"market_id"}),

		// NOTE on Cardinality (RP-08):
		// refprice_unknown_market_total label market_id is bounded by the static external API
		// response keys (e.g. from CoinGecko or configured market symbols). It is never
		// populated with unbounded end-user input or arbitrary URL query strings.
		unknownMarket: registerOrGetCounterVec(reg, prometheus.CounterOpts{
			Name: "refprice_unknown_market_total",
			Help: "Total requests or fetch updates rejected due to unconfigured market ID",
		}, []string{"market_id"}),
	}
}

func registerOrGetCounterVec(reg prometheus.Registerer, opts prometheus.CounterOpts, labels []string) *prometheus.CounterVec {
	cv := prometheus.NewCounterVec(opts, labels)
	if err := reg.Register(cv); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := are.ExistingCollector.(*prometheus.CounterVec); ok {
				return existing
			}
		}
		panic(err)
	}
	return cv
}

func registerOrGetHistogramVec(reg prometheus.Registerer, opts prometheus.HistogramOpts, labels []string) *prometheus.HistogramVec {
	hv := prometheus.NewHistogramVec(opts, labels)
	if err := reg.Register(hv); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := are.ExistingCollector.(*prometheus.HistogramVec); ok {
				return existing
			}
		}
		panic(err)
	}
	return hv
}
