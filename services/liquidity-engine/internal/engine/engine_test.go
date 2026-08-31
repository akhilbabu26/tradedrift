package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/inventory"
	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/pricing"
	"tradedrift/services/liquidity-engine/internal/reconciler"
)

type mockEngineMetrics struct {
	reconcileNoopCount int
	duplicateLevels    int
	meLivenessTimeouts int
	staleOrders        int
	reconcileCreate    int
	reconcileCancel    int
	reconcileCorrect   int
	ordersFilled       int
	healthProbeSuccess int
	healthProbeFailure int
	marketPauses       int
	marketResumes      int
}

func (m *mockEngineMetrics) IncStaleOrders(marketID string)                       { m.staleOrders++ }
func (m *mockEngineMetrics) IncReconcileCreate(marketID string)                   { m.reconcileCreate++ }
func (m *mockEngineMetrics) IncReconcileCancel(marketID string)                   { m.reconcileCancel++ }
func (m *mockEngineMetrics) IncReconcileCorrect(marketID string)                  { m.reconcileCorrect++ }
func (m *mockEngineMetrics) IncReconcileNoop(marketID string)                     { m.reconcileNoopCount++ }
func (m *mockEngineMetrics) IncOrdersFilled(marketID, side string)                { m.ordersFilled++ }
func (m *mockEngineMetrics) IncDuplicateMMLevel(marketID string)                  { m.duplicateLevels++ }
func (m *mockEngineMetrics) SetEngineState(state string)                          {}
func (m *mockEngineMetrics) SetLevelCount(marketID, side string, count int)       {}
func (m *mockEngineMetrics) ObserveReconcileDuration(marketID string, ms float64) {}
func (m *mockEngineMetrics) IncMELivenessTimeout(marketID string)                 { m.meLivenessTimeouts++ }
func (m *mockEngineMetrics) IncMEHealthProbe(status string) {
	if status == "success" {
		m.healthProbeSuccess++
	} else {
		m.healthProbeFailure++
	}
}
func (m *mockEngineMetrics) IncMarketPause(marketID, action string) {
	if action == "pause" {
		m.marketPauses++
	} else {
		m.marketResumes++
	}
}

func TestPendingTimeoutDoesNotAffectMELiveness(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(meclient.StatusResponse{
			Ready:   true,
			Markets: []string{"BTC-USDT"},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		MELivenessThreshold: 3,
		PendingTimeout:      100 * time.Millisecond,
		Markets: []config.MarketConfig{
			{MarketID: "BTC-USDT"},
		},
	}

	logger := zap.NewNop()
	tracker := order.NewTracker()
	inv := inventory.NewManager(tracker, logger)
	meCl := meclient.New(ts.URL, logger)
	metrics := &mockEngineMetrics{}

	rec := reconciler.NewReconciler(tracker, nil, nil, cfg, logger, metrics)
	eng := NewEngine(cfg, tracker, inv, rec, nil, nil, nil, nil, meCl, metrics, logger)

	levelID := "MM-BTC-USDT-BID-01"
	tracker.SetPending(levelID, "order-1", "MM-BTC-USDT-BID-01-G001", 1, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: "BTC-USDT",
		Side:     "BUY",
		Price:    decimal.NewFromInt(90000),
		Quantity: decimal.NewFromInt(1),
	})

	ord := tracker.Get(levelID)
	if ord != nil {
		ord.PendingSince = time.Now().Add(-1 * time.Minute)
		ord.KafkaPublished = true
	}

	eng.handlePendingCheck(context.Background())

	if eng.consecutiveMETimeouts["BTC-USDT"] != 0 {
		t.Errorf("expected consecutiveMETimeouts to be 0 when ME is healthy, got %d", eng.consecutiveMETimeouts["BTC-USDT"])
	}
	if eng.marketPaused["BTC-USDT"] {
		t.Error("expected marketPaused to be false when ME is healthy")
	}
	if metrics.meLivenessTimeouts != 0 {
		t.Errorf("expected 0 ME liveness timeout metrics, got %d", metrics.meLivenessTimeouts)
	}
}

func TestMEHealthIndependentOfTrades(t *testing.T) {
	var isHealthy atomic.Bool
	isHealthy.Store(false)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isHealthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "recovering"})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(meclient.StatusResponse{
			Ready:   true,
			Markets: []string{"BTC-USDT"},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		MELivenessThreshold: 3,
		PendingTimeout:      100 * time.Millisecond,
		Markets: []config.MarketConfig{
			{MarketID: "BTC-USDT"},
		},
	}

	logger := zap.NewNop()
	tracker := order.NewTracker()
	inv := inventory.NewManager(tracker, logger)
	meCl := meclient.New(ts.URL, logger)
	metrics := &mockEngineMetrics{}

	rec := reconciler.NewReconciler(tracker, nil, nil, cfg, logger, metrics)
	eng := NewEngine(cfg, tracker, inv, rec, nil, nil, nil, nil, meCl, metrics, logger)

	// Probe #1 fails: consecutiveMETimeouts = 1, marketPaused = FALSE (sub-threshold)
	eng.handlePendingCheck(context.Background())
	if eng.consecutiveMETimeouts["BTC-USDT"] != 1 {
		t.Errorf("expected consecutiveMETimeouts=1, got %d", eng.consecutiveMETimeouts["BTC-USDT"])
	}
	if eng.marketPaused["BTC-USDT"] {
		t.Error("expected marketPaused=false on 1st probe failure (threshold=3)")
	}

	// Probe #2 fails: consecutiveMETimeouts = 2, marketPaused = FALSE (sub-threshold)
	eng.handlePendingCheck(context.Background())
	if eng.consecutiveMETimeouts["BTC-USDT"] != 2 {
		t.Errorf("expected consecutiveMETimeouts=2, got %d", eng.consecutiveMETimeouts["BTC-USDT"])
	}
	if eng.marketPaused["BTC-USDT"] {
		t.Error("expected marketPaused=false on 2nd probe failure (threshold=3)")
	}

	// Probe #3 fails: consecutiveMETimeouts = 3, marketPaused = TRUE (threshold reached)
	eng.handlePendingCheck(context.Background())
	if eng.consecutiveMETimeouts["BTC-USDT"] != 3 {
		t.Errorf("expected consecutiveMETimeouts=3, got %d", eng.consecutiveMETimeouts["BTC-USDT"])
	}
	if !eng.marketPaused["BTC-USDT"] {
		t.Error("expected marketPaused=true on 3rd probe failure (threshold=3)")
	}
	if metrics.meLivenessTimeouts != 1 {
		t.Errorf("expected 1 IncMELivenessTimeout metric call, got %d", metrics.meLivenessTimeouts)
	}
	if metrics.marketPauses != 1 {
		t.Errorf("expected 1 market pause metric call, got %d", metrics.marketPauses)
	}

	// ME Recovers -> Next probe succeeds
	isHealthy.Store(true)
	eng.handlePendingCheck(context.Background())
	if eng.consecutiveMETimeouts["BTC-USDT"] != 0 {
		t.Errorf("expected consecutiveMETimeouts=0 after recovery, got %d", eng.consecutiveMETimeouts["BTC-USDT"])
	}
	if eng.marketPaused["BTC-USDT"] {
		t.Error("expected marketPaused=false after recovery")
	}
	if metrics.marketResumes != 1 {
		t.Errorf("expected 1 market resume metric call, got %d", metrics.marketResumes)
	}
}

func TestEngine_MarketStates_ExplicitTaxonomy(t *testing.T) {
	cfg := &config.Config{
		MaxBalanceStaleness:    60 * time.Second,
		MaxOrderStateStaleness: 90 * time.Second,
		Markets: []config.MarketConfig{
			{MarketID: "BTC-USDT"},
			{MarketID: "ETH-USDT"},
			{MarketID: "SOL-USDT"},
		},
	}

	logger := zap.NewNop()
	tracker := order.NewTracker()
	inv := inventory.NewManager(tracker, logger)
	inv.RefreshFromWallet(map[string]decimal.Decimal{
		"BTC":  decimal.NewFromInt(100),
		"ETH":  decimal.NewFromInt(500),
		"SOL":  decimal.NewFromInt(5000),
		"USDT": decimal.NewFromInt(5000000),
	})
	metrics := &mockEngineMetrics{}

	eng := NewEngine(cfg, tracker, inv, nil, nil, nil, nil, nil, nil, metrics, logger)

	// Market 1: Unsynchronized (LastSuccessfulSync is zero)
	// Market 2: Running (LastSuccessfulSync is recent)
	tracker.RecordSync("ETH-USDT")

	// Market 3: Paused due to ME
	eng.marketPaused["SOL-USDT"] = true
	eng.publishSnapshot()

	states := eng.MarketStates()
	if states["BTC-USDT"] != "UNSYNCHRONIZED" {
		t.Errorf("expected BTC-USDT to be UNSYNCHRONIZED, got %s", states["BTC-USDT"])
	}
	if states["ETH-USDT"] != "RUNNING" {
		t.Errorf("expected ETH-USDT to be RUNNING, got %s", states["ETH-USDT"])
	}
	if states["SOL-USDT"] != "PAUSED_ME" {
		t.Errorf("expected SOL-USDT to be PAUSED_ME, got %s", states["SOL-USDT"])
	}
}

func TestEngine_ConcurrentHTTPReads_NoDataRace(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(meclient.StatusResponse{
			Ready:   true,
			Markets: []string{"BTC-USDT", "ETH-USDT"},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		MELivenessThreshold: 3,
		PendingTimeout:      100 * time.Millisecond,
		Markets: []config.MarketConfig{
			{MarketID: "BTC-USDT"},
			{MarketID: "ETH-USDT"},
		},
	}

	logger := zap.NewNop()
	tracker := order.NewTracker()
	inv := inventory.NewManager(tracker, logger)
	meCl := meclient.New(ts.URL, logger)
	metrics := &mockEngineMetrics{}

	rec := reconciler.NewReconciler(tracker, nil, nil, cfg, logger, metrics)
	eng := NewEngine(cfg, tracker, inv, rec, nil, nil, nil, nil, meCl, metrics, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup

	// 50 concurrent reader goroutines simulating high-frequency HTTP /status and /readyz queries
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				_ = eng.State()
				_ = eng.IsReady()
				_ = eng.MarketStates()
				_ = eng.ReadyBids("BTC-USDT")
				_ = eng.ReadyAsks("BTC-USDT")
				time.Sleep(1 * time.Millisecond)
			}
		}()
	}

	// Engine event loop simulation: rapidly updating states, probes, and snapshots
	for i := 0; i < 100 && ctx.Err() == nil; i++ {
		eng.handlePendingCheck(ctx)
		eng.setState(StateDegraded)
		eng.setState(StateRunning)
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	wg.Wait()
}

func TestEngine_RunReconcileMarket_UnsynchronizedGuard(t *testing.T) {
	cfg := &config.Config{
		MaxBalanceStaleness:    60 * time.Second,
		MaxOrderStateStaleness: 90 * time.Second,
		Markets: []config.MarketConfig{
			{
				MarketID:   "BTC-USDT",
				BaseAsset:  "BTC",
				QuoteAsset: "USDT",
				MinBase:    decimal.NewFromInt(10),
				MinQuote:   decimal.NewFromInt(100000),
			},
		},
	}

	logger := zap.NewNop()
	tracker := order.NewTracker()
	inv := inventory.NewManager(tracker, logger)
	metrics := &mockEngineMetrics{}

	eng := NewEngine(cfg, tracker, inv, nil, nil, nil, nil, nil, nil, metrics, logger)

	inv.RefreshFromWallet(map[string]decimal.Decimal{
		"BTC":  decimal.NewFromInt(100),
		"USDT": decimal.NewFromInt(5000000),
	})

	if !tracker.LastSuccessfulSync("BTC-USDT").IsZero() {
		t.Fatal("expected LastSuccessfulSync to be zero")
	}

	eng.runReconcileMarket(context.Background(), "BTC-USDT")

	if len(tracker.All("BTC-USDT")) != 0 {
		t.Errorf("expected 0 orders in tracker, got %d", len(tracker.All("BTC-USDT")))
	}
}
