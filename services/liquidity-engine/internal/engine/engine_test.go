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
	"tradedrift/services/liquidity-engine/internal/kafka"
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
func (m *mockEngineMetrics) IncRebaseGenerationFailure(marketID, levelID string)  {}
func (m *mockEngineMetrics) IncExpiryGenerationFailure(marketID string)           {}
func (m *mockEngineMetrics) IncQuantityInvariantViolation(marketID string)        {}
func (m *mockEngineMetrics) IncCreateBlocked(marketID, reason string)              {}
func (m *mockEngineMetrics) IncRefresh(marketID, reason string)                    {}
func (m *mockEngineMetrics) SetExposure(marketID string, bidUSDT, askBase, bidCap, askCap decimal.Decimal) {}
func (m *mockEngineMetrics) IncRepriceEvent(marketID, action string)               {}
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
func (m *mockEngineMetrics) ObserveRebaseDuration(marketID, action string, seconds float64) {}
func (m *mockEngineMetrics) IncRebaseTargetChanges(marketID string)                         {}
func (m *mockEngineMetrics) SetRebaseLevelsRemaining(marketID string, count int)            {}
func (m *mockEngineMetrics) IncRebaseLevelsRepriced(marketID, zone string)                  {}
func (m *mockEngineMetrics) IncCancelOSFailure(marketID string)                              {}
func (m *mockEngineMetrics) IncCancelMEPublish(marketID string)                              {}
func (m *mockEngineMetrics) IncSpreadBreach(marketID string)                                {}
func (m *mockEngineMetrics) ObserveSpreadRecoveryDuration(marketID string, seconds float64) {}
func (m *mockEngineMetrics) SetInsideSpreadBps(marketID string, bps float64)                {}

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

	rec := reconciler.NewReconciler(tracker, nil, nil, meCl, cfg, logger, metrics)
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

	rec := reconciler.NewReconciler(tracker, nil, nil, meCl, cfg, logger, metrics)
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

	rec := reconciler.NewReconciler(tracker, nil, nil, meCl, cfg, logger, metrics)
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

func TestEngine_HandleTrade_FullFill_RemovesFromTracker(t *testing.T) {
	cfg := &config.Config{
		Markets: []config.MarketConfig{
			{MarketID: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT"},
		},
	}
	logger := zap.NewNop()
	tracker := order.NewTracker()
	inv := inventory.NewManager(tracker, logger)
	metrics := &mockEngineMetrics{}

	eng := NewEngine(cfg, tracker, inv, nil, nil, nil, nil, nil, nil, metrics, logger)

	levelID := "MM-BTC-USDT-ASK-01"
	orderID := "order-full-1"
	qty := decimal.NewFromFloat(1.5)
	tracker.SetPending(levelID, orderID, "MM-BTC-USDT-ASK-01-G001", 1, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: "BTC-USDT",
		Side:     "SELL",
		Price:    decimal.NewFromInt(95000),
		Quantity: qty,
	})
	tracker.SetResting(levelID, orderID, qty, qty)

	if tracker.Get(levelID) == nil {
		t.Fatal("expected order to be present in tracker before trade")
	}

	eng.handleTrade(kafka.TradeEnvelope{
		Event: kafka.TradeEvent{
			TradeID:      "trade-full-1",
			MarketID:     "BTC-USDT",
			MakerOrderID: orderID,
			MMSide:       "SELL",
			Quantity:     qty,
		},
	})

	if ord := tracker.Get(levelID); ord != nil {
		t.Errorf("expected level %s to be removed from tracker on full fill, but found: %+v", levelID, ord)
	}
}

func TestEngine_HandleTrade_PartialFill_RetainsInTracker(t *testing.T) {
	cfg := &config.Config{
		Markets: []config.MarketConfig{
			{MarketID: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT"},
		},
	}
	logger := zap.NewNop()
	tracker := order.NewTracker()
	inv := inventory.NewManager(tracker, logger)
	metrics := &mockEngineMetrics{}

	eng := NewEngine(cfg, tracker, inv, nil, nil, nil, nil, nil, nil, metrics, logger)

	levelID := "MM-BTC-USDT-ASK-01"
	orderID := "order-part-1"
	origQty := decimal.NewFromFloat(1.5)
	tracker.SetPending(levelID, orderID, "MM-BTC-USDT-ASK-01-G001", 1, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: "BTC-USDT",
		Side:     "SELL",
		Price:    decimal.NewFromInt(95000),
		Quantity: origQty,
	})
	tracker.SetResting(levelID, orderID, origQty, origQty)

	fillQty := decimal.NewFromFloat(0.5)
	eng.handleTrade(kafka.TradeEnvelope{
		Event: kafka.TradeEvent{
			TradeID:      "trade-part-1",
			MarketID:     "BTC-USDT",
			MakerOrderID: orderID,
			MMSide:       "SELL",
			Quantity:     fillQty,
		},
	})

	ord := tracker.Get(levelID)
	if ord == nil {
		t.Fatal("expected order to remain in tracker on partial fill")
	}
	expectedRemaining := origQty.Sub(fillQty)
	if !ord.RemainingQty.Equal(expectedRemaining) {
		t.Errorf("expected remaining qty %s, got %s", expectedRemaining, ord.RemainingQty)
	}
	if !ord.FilledQty.Equal(fillQty) {
		t.Errorf("expected filled qty %s, got %s", fillQty, ord.FilledQty)
	}
}

func TestEngine_HandleTrade_Overfill_ClampsToZeroAndRemoves(t *testing.T) {
	cfg := &config.Config{
		Markets: []config.MarketConfig{
			{MarketID: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT"},
		},
	}
	logger := zap.NewNop()
	tracker := order.NewTracker()
	inv := inventory.NewManager(tracker, logger)
	metrics := &mockEngineMetrics{}

	eng := NewEngine(cfg, tracker, inv, nil, nil, nil, nil, nil, nil, metrics, logger)

	levelID := "MM-BTC-USDT-BID-01"
	orderID := "order-over-1"
	origQty := decimal.NewFromFloat(1.0)
	tracker.SetPending(levelID, orderID, "MM-BTC-USDT-BID-01-G001", 1, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: "BTC-USDT",
		Side:     "BUY",
		Price:    decimal.NewFromInt(95000),
		Quantity: origQty,
	})
	tracker.SetResting(levelID, orderID, origQty, decimal.NewFromFloat(0.5))

	eng.handleTrade(kafka.TradeEnvelope{
		Event: kafka.TradeEvent{
			TradeID:      "trade-over-1",
			MarketID:     "BTC-USDT",
			MakerOrderID: orderID,
			MMSide:       "BUY",
			Quantity:     decimal.NewFromFloat(0.8),
		},
	})

	if ord := tracker.Get(levelID); ord != nil {
		t.Errorf("expected level %s to be removed from tracker on overfill, but found: %+v", levelID, ord)
	}
}

func TestEngine_CheckSpreadBreach_ConfiguredThreshold(t *testing.T) {
	cfg := &config.Config{
		Markets: []config.MarketConfig{
			{
				MarketID: "BTC-USDT",
				Repricing: config.RepricingConfig{
					SpreadBreachBps: 30, // Configured explicitly to 30 bps
				},
				BidZones: config.DefaultZoneConfig(),
			},
		},
	}
	tracker := order.NewTracker()
	eng := NewEngine(cfg, tracker, inventory.NewManager(tracker, zap.NewNop()), nil, nil, nil, nil, nil, nil, &mockEngineMetrics{}, zap.NewNop())

	// 1. Spread = 20 bps (100.00 to 100.20) -> should NOT breach (< 30 bps)
	tracker.SetPending("BID-1", "b1", "cb1", 1, pricing.PriceLevel{MarketID: "BTC-USDT", Side: "BUY", Price: decimal.RequireFromString("100.00"), Quantity: decimal.NewFromInt(1)})
	tracker.SetResting("BID-1", "b1", decimal.NewFromInt(1), decimal.NewFromInt(1))

	tracker.SetPending("ASK-1", "a1", "ca1", 1, pricing.PriceLevel{MarketID: "BTC-USDT", Side: "SELL", Price: decimal.RequireFromString("100.20"), Quantity: decimal.NewFromInt(1)})
	tracker.SetResting("ASK-1", "a1", decimal.NewFromInt(1), decimal.NewFromInt(1))

	if eng.CheckSpreadBreach("BTC-USDT") {
		t.Fatalf("expected CheckSpreadBreach to return false for 20 bps spread when threshold is 30 bps")
	}

	// 2. Spread = 40 bps (100.00 to 100.40) -> SHOULD breach (> 30 bps)
	tracker.Remove("ASK-1")
	tracker.SetPending("ASK-2", "a2", "ca2", 1, pricing.PriceLevel{MarketID: "BTC-USDT", Side: "SELL", Price: decimal.RequireFromString("100.40"), Quantity: decimal.NewFromInt(1)})
	tracker.SetResting("ASK-2", "a2", decimal.NewFromInt(1), decimal.NewFromInt(1))

	if !eng.CheckSpreadBreach("BTC-USDT") {
		t.Fatalf("expected CheckSpreadBreach to return true for 40 bps spread when threshold is 30 bps")
	}
}

func TestEngine_CheckSpreadBreach_DerivedFallback(t *testing.T) {
	cfg := &config.Config{
		Markets: []config.MarketConfig{
			{
				MarketID: "BTC-USDT",
				Repricing: config.RepricingConfig{
					SpreadBreachBps: 0, // Unconfigured: should derive fallback from Mid.MaxBps * 2
				},
				BidZones: config.ZoneConfig{
					Mid: config.ZoneBand{MinBps: 15, MaxBps: 35, Count: 4}, // 35 * 2 = 70 bps fallback
				},
			},
		},
	}
	tracker := order.NewTracker()
	eng := NewEngine(cfg, tracker, inventory.NewManager(tracker, zap.NewNop()), nil, nil, nil, nil, nil, nil, &mockEngineMetrics{}, zap.NewNop())

	tracker.SetPending("BID-1", "b1", "cb1", 1, pricing.PriceLevel{MarketID: "BTC-USDT", Side: "BUY", Price: decimal.RequireFromString("100.00"), Quantity: decimal.NewFromInt(1)})
	tracker.SetResting("BID-1", "b1", decimal.NewFromInt(1), decimal.NewFromInt(1))

	// 1. Spread = 50 bps (100.00 to 100.50) -> should NOT breach (< 70 bps fallback)
	tracker.SetPending("ASK-1", "a1", "ca1", 1, pricing.PriceLevel{MarketID: "BTC-USDT", Side: "SELL", Price: decimal.RequireFromString("100.50"), Quantity: decimal.NewFromInt(1)})
	tracker.SetResting("ASK-1", "a1", decimal.NewFromInt(1), decimal.NewFromInt(1))

	if eng.CheckSpreadBreach("BTC-USDT") {
		t.Fatalf("expected CheckSpreadBreach to return false for 50 bps spread under 70 bps fallback")
	}

	// 2. Spread = 80 bps (100.00 to 100.80) -> SHOULD breach (> 70 bps fallback)
	tracker.Remove("ASK-1")
	tracker.SetPending("ASK-2", "a2", "ca2", 1, pricing.PriceLevel{MarketID: "BTC-USDT", Side: "SELL", Price: decimal.RequireFromString("100.80"), Quantity: decimal.NewFromInt(1)})
	tracker.SetResting("ASK-2", "a2", decimal.NewFromInt(1), decimal.NewFromInt(1))

	if !eng.CheckSpreadBreach("BTC-USDT") {
		t.Fatalf("expected CheckSpreadBreach to return true for 80 bps spread under 70 bps fallback")
	}
}

func TestEngine_CheckSpreadBreach_DepletedSide(t *testing.T) {
	cfg := &config.Config{
		Markets: []config.MarketConfig{
			{
				MarketID:  "BTC-USDT",
				Repricing: config.RepricingConfig{SpreadBreachBps: 50},
			},
		},
	}
	tracker := order.NewTracker()
	eng := NewEngine(cfg, tracker, inventory.NewManager(tracker, zap.NewNop()), nil, nil, nil, nil, nil, nil, &mockEngineMetrics{}, zap.NewNop())

	// Only bids, zero asks -> depleted side must return true (spread breach)
	tracker.SetPending("BID-1", "b1", "cb1", 1, pricing.PriceLevel{MarketID: "BTC-USDT", Side: "BUY", Price: decimal.RequireFromString("100.00"), Quantity: decimal.NewFromInt(1)})
	tracker.SetResting("BID-1", "b1", decimal.NewFromInt(1), decimal.NewFromInt(1))

	if !eng.CheckSpreadBreach("BTC-USDT") {
		t.Fatalf("expected CheckSpreadBreach to return true when ask side is depleted")
	}
}

func TestEngine_SpreadBreach_TriggersTargetedReconcile(t *testing.T) {
	cfg := &config.Config{
		TargetedReconcileDebounce: 50 * time.Millisecond,
		Markets: []config.MarketConfig{
			{
				MarketID:  "BTC-USDT",
				Repricing: config.RepricingConfig{SpreadBreachBps: 30},
			},
		},
	}
	tracker := order.NewTracker()
	eng := NewEngine(cfg, tracker, inventory.NewManager(tracker, zap.NewNop()), nil, nil, nil, nil, nil, nil, &mockEngineMetrics{}, zap.NewNop())

	// Set resting bid and ask with a wide spread (50 bps > 30 bps)
	tracker.SetPending("BID-1", "b1", "cb1", 1, pricing.PriceLevel{MarketID: "BTC-USDT", Side: "BUY", Price: decimal.RequireFromString("100.00"), Quantity: decimal.NewFromInt(1)})
	tracker.SetResting("BID-1", "b1", decimal.NewFromInt(1), decimal.NewFromInt(1))
	tracker.SetPending("ASK-1", "a1", "ca1", 1, pricing.PriceLevel{MarketID: "BTC-USDT", Side: "SELL", Price: decimal.RequireFromString("100.50"), Quantity: decimal.NewFromInt(1)})
	tracker.SetResting("ASK-1", "a1", decimal.NewFromInt(1), decimal.NewFromInt(1))

	// Send a trade event (even non-MM trade)
	eng.handleTrade(kafka.TradeEnvelope{
		Event: kafka.TradeEvent{
			TradeID:      "trade-ext-1",
			MarketID:     "BTC-USDT",
			MakerOrderID: "external-order-1",
			MMSide:       "BUY",
			Quantity:     decimal.NewFromFloat(0.1),
		},
	})

	// INVARIANT: Spread breach must flag dirtyMarkets and schedule targeted reconcile
	if !eng.dirtyMarkets["BTC-USDT"] {
		t.Fatalf("expected dirtyMarkets[BTC-USDT] to be set to true on spread breach")
	}
	if eng.targetedDebounceTimer == nil {
		t.Fatalf("expected targetedDebounceTimer to be scheduled on spread breach")
	}
}

func TestEngine_SpreadBelowThreshold_NoTargetedReconcile(t *testing.T) {
	cfg := &config.Config{
		TargetedReconcileDebounce: 50 * time.Millisecond,
		Markets: []config.MarketConfig{
			{
				MarketID:  "BTC-USDT",
				Repricing: config.RepricingConfig{SpreadBreachBps: 30},
			},
		},
	}
	tracker := order.NewTracker()
	eng := NewEngine(cfg, tracker, inventory.NewManager(tracker, zap.NewNop()), nil, nil, nil, nil, nil, nil, &mockEngineMetrics{}, zap.NewNop())

	// Set resting bid and ask with tight spread (20 bps < 30 bps threshold)
	tracker.SetPending("BID-1", "b1", "cb1", 1, pricing.PriceLevel{MarketID: "BTC-USDT", Side: "BUY", Price: decimal.RequireFromString("100.00"), Quantity: decimal.NewFromInt(1)})
	tracker.SetResting("BID-1", "b1", decimal.NewFromInt(1), decimal.NewFromInt(1))
	tracker.SetPending("ASK-1", "a1", "ca1", 1, pricing.PriceLevel{MarketID: "BTC-USDT", Side: "SELL", Price: decimal.RequireFromString("100.20"), Quantity: decimal.NewFromInt(1)})
	tracker.SetResting("ASK-1", "a1", decimal.NewFromInt(1), decimal.NewFromInt(1))

	// Send an external (non-MM) trade event
	eng.handleTrade(kafka.TradeEnvelope{
		Event: kafka.TradeEvent{
			TradeID:      "trade-ext-2",
			MarketID:     "BTC-USDT",
			MakerOrderID: "external-order-2",
			MMSide:       "BUY",
			Quantity:     decimal.NewFromFloat(0.1),
		},
	})

	// INVARIANT: Spread < threshold must NOT flag dirtyMarkets or schedule targeted reconcile
	if eng.dirtyMarkets["BTC-USDT"] {
		t.Fatalf("expected dirtyMarkets[BTC-USDT] to remain false when spread (20 bps) is below threshold (30 bps)")
	}
	if eng.targetedDebounceTimer != nil {
		t.Fatalf("expected targetedDebounceTimer to remain nil when spread is below threshold")
	}
}


