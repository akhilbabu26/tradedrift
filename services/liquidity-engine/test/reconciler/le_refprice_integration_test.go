package reconciler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/platform/refprice"
	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/orderservice"
	"tradedrift/services/liquidity-engine/internal/pricing"
	"tradedrift/services/liquidity-engine/internal/reconciler"
)

// controllableIntegrationFetcher allows tests to provide exact price versions on demand
// or trigger outages thread-safely.
type controllableIntegrationFetcher struct {
	mu      sync.Mutex
	pending map[string]decimal.Decimal
	outage  bool
}

func newControllableFetcher() *controllableIntegrationFetcher {
	return &controllableIntegrationFetcher{
		pending: make(map[string]decimal.Decimal),
	}
}

func (f *controllableIntegrationFetcher) Fetch(ctx context.Context, marketIDs []string) (map[string]decimal.Decimal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.outage {
		return nil, errors.New("outage simulation")
	}
	if len(f.pending) == 0 {
		return nil, errors.New("no new update")
	}
	res := make(map[string]decimal.Decimal)
	for _, m := range marketIDs {
		if p, ok := f.pending[m]; ok {
			res[m] = p
		}
	}
	f.pending = make(map[string]decimal.Decimal)
	return res, nil
}

func (f *controllableIntegrationFetcher) SetPrice(marketID string, price decimal.Decimal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending[marketID] = price
	f.outage = false
}

func (f *controllableIntegrationFetcher) SetOutage(outage bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outage = outage
	if outage {
		f.pending = make(map[string]decimal.Decimal)
	}
}

// ── Test Harness for LE + RefPrice Integration ───────────────────────────────

type leRefPriceHarness struct {
	t        *testing.T
	marketID string
	fetcher  *controllableIntegrationFetcher
	prov     *refprice.Provider
	rec      *reconciler.Reconciler
	tracker  *order.Tracker
	producer *mockProducer
	osSvc    *mockOrderSvc
	cfg      *config.Config
	rpCfg    refprice.Config
	meSrv    *httptest.Server
	cancel   context.CancelFunc
}

func newLERefPriceHarness(t *testing.T, initialPrice string, staleThresh, pauseThresh time.Duration) *leRefPriceHarness {
	t.Helper()
	marketID := "BTC-USDT"
	fetcher := newControllableFetcher()
	fetcher.SetPrice(marketID, decimal.RequireFromString(initialPrice))

	rpCfg := refprice.Config{
		RefreshInterval: 20 * time.Millisecond,
		StaleThreshold:  staleThresh,
		PauseThreshold:  pauseThresh,
		FetchTimeout:    50 * time.Millisecond,
	}

	prov, err := refprice.NewProvider(rpCfg, fetcher, []string{marketID}, zap.NewNop())
	if err != nil {
		t.Fatalf("failed to create refprice provider: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go prov.Run(ctx)

	// Wait for initial fetch
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if entry, ok := prov.Get(marketID); ok && entry.State == refprice.StateFresh && entry.Version >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	tracker := order.NewTracker()
	producer := &mockProducer{}
	osSvc := newMockOrderSvc()
	cfg := testConfig()
	cfg.RefPrice = config.RefPriceConfig{
		StaleThreshold: rpCfg.StaleThreshold,
		PauseThreshold: rpCfg.PauseThreshold,
	}
	metrics := &mockMetrics{}

	meSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var meOrders []meclient.MMOrderSummary
		for _, o := range tracker.All(marketID) {
			if o.Status == order.StatusResting {
				meOrders = append(meOrders, meclient.MMOrderSummary{
					OrderID:           o.OrderID,
					ClientOrderID:     o.ClientOrderID,
					Side:              o.Side,
					Price:             o.Price.String(),
					RemainingQuantity: o.RemainingQty.String(),
				})
			}
		}
		_ = json.NewEncoder(w).Encode(meclient.MarketSnapshot{
			MarketID: marketID,
			State:    "LIVE",
			Orders:   meOrders,
		})
	}))

	rec := reconciler.NewReconciler(tracker, producer, osSvc, meclient.New(meSrv.URL, zap.NewNop()), cfg, zap.NewNop(), metrics)
	rec.SetRefProvider(prov)

	h := &leRefPriceHarness{
		t:        t,
		marketID: marketID,
		fetcher:  fetcher,
		prov:     prov,
		rec:      rec,
		tracker:  tracker,
		producer: producer,
		osSvc:    osSvc,
		cfg:      cfg,
		rpCfg:    rpCfg,
		meSrv:    meSrv,
		cancel:   cancel,
	}

	t.Cleanup(func() {
		cancel()
		meSrv.Close()
	})

	return h
}

// ── Canonical Test 1: Normal Fresh Creates ────────────────────────────────────

func TestLERefPrice_NormalFreshCreates(t *testing.T) {
	h := newLERefPriceHarness(t, "95000.00", 500*time.Millisecond, 1*time.Second)
	ctx := context.Background()

	// Initial reference snapshot must be FRESH with version 1
	mc := h.cfg.ForMarket(h.marketID)
	snap := h.rec.CurrentReferenceSnapshot(mc)
	if snap.Freshness != reconciler.FreshnessFresh {
		t.Fatalf("expected FRESH, got: %v", snap.Freshness)
	}
	if !snap.Price.Equal(decimal.RequireFromString("95000.00")) {
		t.Fatalf("expected price 95000.00, got: %s", snap.Price.String())
	}
	if snap.Version != 1 {
		t.Fatalf("expected RefVersion 1, got: %d", snap.Version)
	}

	// Reconcile generates initial MM orders
	mutations, err := h.rec.ReconcileMarket(ctx, h.marketID, 12, 12)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if mutations == 0 || len(h.producer.publishedCreates) == 0 {
		t.Fatalf("expected initial orders to be created, got %d mutations", mutations)
	}

	// Verify all published creates have valid prices and RefVersion
	allOrders := h.tracker.All(h.marketID)
	if len(allOrders) == 0 {
		t.Fatalf("expected orders in tracker")
	}
	for _, o := range allOrders {
		if o.RefVersion != 1 {
			t.Fatalf("order %s has wrong RefVersion: %d (expected 1)", o.LevelID, o.RefVersion)
		}
		if o.Side == "BUY" && !o.Price.LessThan(decimal.RequireFromString("95000.00")) {
			t.Fatalf("BUY order price %s not below reference 95000.00", o.Price.String())
		}
		if o.Side == "SELL" && !o.Price.GreaterThan(decimal.RequireFromString("95000.00")) {
			t.Fatalf("SELL order price %s not above reference 95000.00", o.Price.String())
		}
	}
}

// ── Canonical Test 2: Stale Blocks Creates (Hold Action) ───────────────────────

func TestLERefPrice_StaleBlocksCreates(t *testing.T) {
	h := newLERefPriceHarness(t, "95000.00", 60*time.Millisecond, 300*time.Millisecond)
	ctx := context.Background()

	// Initial fresh creates
	_, err := h.rec.ReconcileMarket(ctx, h.marketID, 12, 12)
	if err != nil {
		t.Fatalf("initial reconcile failed: %v", err)
	}

	// Confirm orders resting
	for _, p := range h.producer.publishedCreates {
		h.tracker.SetResting(p.OrderID, p.OrderID, decimal.RequireFromString(p.Quantity), decimal.RequireFromString(p.Quantity))
	}
	initialRestingCount := len(h.tracker.All(h.marketID))

	// Simulate external outage
	h.fetcher.SetOutage(true)
	time.Sleep(h.rpCfg.StaleThreshold + 30*time.Millisecond)

	entry, ok := h.prov.Get(h.marketID)
	if !ok || entry.State != refprice.StateStale {
		t.Fatalf("expected provider StateStale, got %+v", entry)
	}

	// Clear published creates/cancels
	h.producer.publishedCreates = nil
	h.producer.cancelledOrders = nil

	// Reconcile during STALE: classified as HOLD action
	mutations, err := h.rec.ReconcileMarket(ctx, h.marketID, 12, 12)
	if err != nil {
		t.Fatalf("reconcile during STALE failed: %v", err)
	}
	if mutations != 0 {
		t.Fatalf("expected 0 mutations during STALE (HOLD), got %d", mutations)
	}
	if len(h.producer.publishedCreates) != 0 {
		t.Fatalf("STALE create blocking violated: %v", h.producer.publishedCreates)
	}
	if len(h.producer.cancelledOrders) != 0 {
		t.Fatalf("unexpected cancel during STALE: %v", h.producer.cancelledOrders)
	}

	// Existing resting orders stay intact
	if len(h.tracker.All(h.marketID)) != initialRestingCount {
		t.Fatalf("resting orders were modified during STALE")
	}
}

// ── Canonical Test 3: Paused Blocks Mutations Globally ────────────────────────

func TestLERefPrice_PausedBlocksMutations(t *testing.T) {
	h := newLERefPriceHarness(t, "95000.00", 40*time.Millisecond, 80*time.Millisecond)
	ctx := context.Background()

	// Initial fresh creates
	_, _ = h.rec.ReconcileMarket(ctx, h.marketID, 12, 12)
	for _, p := range h.producer.publishedCreates {
		h.tracker.SetResting(p.OrderID, p.OrderID, decimal.RequireFromString(p.Quantity), decimal.RequireFromString(p.Quantity))
	}

	// Trigger outage past PauseThreshold
	h.fetcher.SetOutage(true)
	time.Sleep(h.rpCfg.PauseThreshold + 30*time.Millisecond)

	entry, ok := h.prov.Get(h.marketID)
	if !ok || entry.State != refprice.StatePaused {
		t.Fatalf("expected provider StatePaused, got %+v", entry)
	}

	mc := h.cfg.ForMarket(h.marketID)
	snap := h.rec.CurrentReferenceSnapshot(mc)
	if snap.Freshness != reconciler.FreshnessPaused {
		t.Fatalf("expected LE snapshot FreshnessPaused, got %v", snap.Freshness)
	}
	if !h.rec.IsMarketPaused(h.marketID) {
		t.Fatalf("expected IsMarketPaused = true")
	}

	h.producer.publishedCreates = nil
	h.producer.cancelledOrders = nil

	// 1. ReconcileMarket during PAUSED must produce 0 mutations
	mutations, err := h.rec.ReconcileMarket(ctx, h.marketID, 12, 12)
	if err != nil {
		t.Fatalf("reconcile during PAUSED failed: %v", err)
	}
	if mutations != 0 || len(h.producer.publishedCreates) != 0 || len(h.producer.cancelledOrders) != 0 {
		t.Fatalf("mutation occurred during PAUSED reconcile")
	}

	// 2. CheckPendingTimeouts must NOT publish creates
	h.rec.CheckPendingTimeouts(ctx, h.marketID)
	if len(h.producer.publishedCreates) != 0 {
		t.Fatalf("creates published during PAUSED CheckPendingTimeouts")
	}

	// 3. CheckCancellingTimeouts must NOT publish replacement creates
	h.rec.CheckCancellingTimeouts(ctx, h.marketID)
	if len(h.producer.publishedCreates) != 0 {
		t.Fatalf("creates published during PAUSED CheckCancellingTimeouts")
	}

	// 4. CheckExpiredOrders must NOT cancel orders
	h.rec.CheckExpiredOrders(ctx, h.marketID)
	if len(h.producer.cancelledOrders) != 0 {
		t.Fatalf("cancels published during PAUSED CheckExpiredOrders")
	}

	// 5. SyncFromOrderService must NOT publish replacement creations while PAUSED
	h.osSvc.createdOrders = nil
	err = h.rec.SyncFromOrderService(ctx, h.marketID)
	if err != nil {
		t.Fatalf("SyncFromOrderService unexpected error: %v", err)
	}
	if len(h.producer.publishedCreates) != 0 || len(h.osSvc.createdOrders) != 0 {
		t.Fatalf("replacement creations occurred during PAUSED SyncFromOrderService")
	}
}

// ── Canonical Test 4: Recovery Resumes Order Management ───────────────────────

func TestLERefPrice_RecoveryResumes(t *testing.T) {
	h := newLERefPriceHarness(t, "95000.00", 40*time.Millisecond, 80*time.Millisecond)
	ctx := context.Background()

	// Cause outage -> PAUSED
	h.fetcher.SetOutage(true)
	time.Sleep(h.rpCfg.PauseThreshold + 30*time.Millisecond)

	mc := h.cfg.ForMarket(h.marketID)
	snap := h.rec.CurrentReferenceSnapshot(mc)
	if snap.Freshness != reconciler.FreshnessPaused {
		t.Fatalf("expected PAUSED before recovery, got %v", snap.Freshness)
	}

	// Recovery: fetcher receives updated price
	h.fetcher.SetPrice(h.marketID, decimal.RequireFromString("96500.00"))

	// Wait for Provider to reach StateFresh with Version > 1
	deadline := time.Now().Add(2 * time.Second)
	var entry refprice.Entry
	var ok bool
	for time.Now().Before(deadline) {
		entry, ok = h.prov.Get(h.marketID)
		if ok && entry.State == refprice.StateFresh && entry.Version > 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ok || entry.State != refprice.StateFresh {
		t.Fatalf("expected recovered FRESH entry, got %+v", entry)
	}

	// Verify LE snapshot reflects recovered price and freshness
	snap = h.rec.CurrentReferenceSnapshot(mc)
	if snap.Freshness != reconciler.FreshnessFresh {
		t.Fatalf("expected FRESH freshness after recovery, got %v", snap.Freshness)
	}
	if !snap.Price.Equal(decimal.RequireFromString("96500.00")) {
		t.Fatalf("expected recovered price 96500.00, got %s", snap.Price.String())
	}
	if snap.Version != entry.Version {
		t.Fatalf("expected recovered version %d, got %d", entry.Version, snap.Version)
	}

	// Reconcile resumes order creation anchored to recovered price
	h.producer.publishedCreates = nil
	mutations, err := h.rec.ReconcileMarket(ctx, h.marketID, 12, 12)
	if err != nil {
		t.Fatalf("reconcile after recovery failed: %v", err)
	}
	if mutations == 0 || len(h.producer.publishedCreates) == 0 {
		t.Fatalf("expected order creates to resume after recovery")
	}
}

// ── Canonical Test 5: Partial Fill Replacement Uses RemainingQty ──────────────

func TestLERefPrice_PartialFillReplacement(t *testing.T) {
	h := newLERefPriceHarness(t, "95000.00", 500*time.Millisecond, 1*time.Second)
	ctx := context.Background()

	levelID := "BUY-01"
	orderID := "ord-part-01"
	clientOrderID := "BTC-USDT-BUY-01-1"

	// Order originally 1.0 BTC, partially filled to 0.35 BTC remaining
	origQty := decimal.RequireFromString("1.00000")
	remQty := decimal.RequireFromString("0.35000")
	price := decimal.RequireFromString("94000.00")

	h.tracker.SetPending(levelID, orderID, clientOrderID, 1, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: h.marketID,
		Side:     "BUY",
		Price:    price,
		Quantity: origQty,
	})
	h.tracker.SetResting(levelID, orderID, origQty, remQty)

	// Trigger cancellation with queued correction
	queuedCorrection := pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   h.marketID,
		Side:       "BUY",
		Price:      price,
		Quantity:   origQty, // Queued correction originally targeted full quantity
		RefVersion: 1,
	}
	h.tracker.SetCancelling(levelID)
	h.tracker.QueueCorrection(levelID, queuedCorrection)
	h.tracker.Get(levelID).CancellingSince = time.Now().Add(-100 * time.Millisecond)

	// Order Service confirms CANCELLED with authoritative RemainingQty = 0.35
	h.osSvc.orders[clientOrderID] = &orderservice.OrderState{
		OrderID:       orderID,
		ClientOrderID: clientOrderID,
		Status:        "CANCELLED",
		OriginalQty:   origQty,
		RemainingQty:  remQty,
	}

	h.rec.CheckCancellingTimeouts(ctx, h.marketID)

	// Replacement order published to Order Service must use 0.35 (RemainingQty), NOT 1.0 (OriginalQty)
	if len(h.osSvc.createdOrders) != 1 {
		t.Fatalf("expected 1 replacement order in Order Service, got %d", len(h.osSvc.createdOrders))
	}
	created := h.osSvc.createdOrders[0]
	if created.Quantity != remQty.String() {
		t.Fatalf("CRITICAL CONTRACT VIOLATION: replacement quantity was %s (expected RemainingQty %s)",
			created.Quantity, remQty.String())
	}
}

// ── Canonical Test 6: Full Fill Prevents Resurrection ─────────────────────────

func TestLERefPrice_FullFillNoResurrection(t *testing.T) {
	h := newLERefPriceHarness(t, "95000.00", 500*time.Millisecond, 1*time.Second)
	ctx := context.Background()

	levelID := "BUY-02"
	orderID := "ord-filled-02"
	clientOrderID := "BTC-USDT-BUY-02-1"

	origQty := decimal.RequireFromString("1.00000")
	zeroQty := decimal.Zero
	price := decimal.RequireFromString("94000.00")

	// Order fully filled while cancelling
	h.tracker.SetPending(levelID, orderID, clientOrderID, 1, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: h.marketID,
		Side:     "BUY",
		Price:    price,
		Quantity: origQty,
	})
	h.tracker.SetResting(levelID, orderID, origQty, origQty)
	h.tracker.SetCancelling(levelID)
	h.tracker.QueueCorrection(levelID, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   h.marketID,
		Side:       "BUY",
		Price:      price,
		Quantity:   origQty,
		RefVersion: 1,
	})
	h.tracker.Get(levelID).CancellingSince = time.Now().Add(-100 * time.Millisecond)

	// Order Service reports FILLED with RemainingQty = 0
	h.osSvc.orders[clientOrderID] = &orderservice.OrderState{
		OrderID:       orderID,
		ClientOrderID: clientOrderID,
		Status:        "CANCELLED",
		OriginalQty:   origQty,
		RemainingQty:  zeroQty,
	}

	h.rec.CheckCancellingTimeouts(ctx, h.marketID)

	// MUST NOT create replacement order
	if len(h.osSvc.createdOrders) != 0 {
		t.Fatalf("RESURRECTION VIOLATION: fully filled order was resurrected: %v", h.osSvc.createdOrders)
	}

	// Verify order was cleanly removed from tracker
	if o := h.tracker.Get(levelID); o != nil {
		t.Fatalf("expected level to be removed from tracker, got status %v", o.Status)
	}
}


// ── Canonical Test 7: Controlled Rebase In-Flight Tracking ────────────────────

func TestLERefPrice_ControlledRebase(t *testing.T) {
	h := newLERefPriceHarness(t, "90000.00", 500*time.Millisecond, 1*time.Second)
	ctx := context.Background()

	// Initial reconcile with Version 1
	_, err := h.rec.ReconcileMarket(ctx, h.marketID, 12, 12)
	if err != nil {
		t.Fatalf("initial reconcile failed: %v", err)
	}

	// Confirm orders resting with RefVersion 1
	for _, o := range h.tracker.All(h.marketID) {
		h.tracker.SetResting(o.LevelID, o.OrderID, o.OriginalQty, o.OriginalQty)
		h.osSvc.orders[o.ClientOrderID] = &orderservice.OrderState{
			OrderID:       o.OrderID,
			ClientOrderID: o.ClientOrderID,
			Status:        "OPEN",
			RemainingQty:  o.OriginalQty,
		}
	}

	// Reference price moves significantly: 90,000 -> 95,000 (> LargeMovementBps)
	h.fetcher.SetPrice(h.marketID, decimal.RequireFromString("95000.00"))

	// Wait for Provider Version 2
	deadline := time.Now().Add(2 * time.Second)
	var entry refprice.Entry
	var ok bool
	for time.Now().Before(deadline) {
		entry, ok = h.prov.Get(h.marketID)
		if ok && entry.State == refprice.StateFresh && entry.Version == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ok || entry.Version != 2 {
		t.Fatalf("expected provider Version 2, got %+v", entry)
	}

	// Reconcile executes controlled rebase
	h.producer.publishedCreates = nil
	h.producer.cancelledOrders = nil

	mutations, err := h.rec.ReconcileMarket(ctx, h.marketID, 12, 12)
	if err != nil {
		t.Fatalf("reconcile during rebase failed: %v", err)
	}
	if mutations == 0 || len(h.producer.cancelledOrders) == 0 {
		t.Fatalf("expected rebase cancellations to be published")
	}

	// Queued corrections must hold RefVersion = 2
	for _, o := range h.tracker.All(h.marketID) {
		if o.QueuedCorrection != nil && o.QueuedCorrection.RefVersion != 2 {
			t.Fatalf("rebase queued correction has RefVersion %d (expected 2)", o.QueuedCorrection.RefVersion)
		}
	}

	// Confirm cancellations in Order Service
	for _, cxlID := range h.producer.cancelledOrders {
		for _, o := range h.tracker.All(h.marketID) {
			if o.OrderID == cxlID {
				h.osSvc.orders[o.ClientOrderID] = &orderservice.OrderState{
					OrderID:       o.OrderID,
					ClientOrderID: o.ClientOrderID,
					Status:        "CANCELLED",
					RemainingQty:  o.OriginalQty,
				}
				h.tracker.Get(o.LevelID).CancellingSince = time.Now().Add(-100 * time.Millisecond)
			}
		}
	}

	// Execute replacement creation
	h.rec.CheckCancellingTimeouts(ctx, h.marketID)

	// Verify replacement orders carry RefVersion = 2
	var foundVersion2 bool
	for _, o := range h.tracker.All(h.marketID) {
		if o.RefVersion == 2 {
			foundVersion2 = true
			break
		}
	}
	if !foundVersion2 {
		t.Fatalf("contract violation: no replacement order with RefVersion 2 found after rebase")
	}
}

// ── Backward-Compatible Full State-Machine Scenarios ──────────────────────────

func TestX03_RefPriceOutageAndRecoveryIntegration(t *testing.T) {
	TestLERefPrice_NormalFreshCreates(t)
	TestLERefPrice_StaleBlocksCreates(t)
	TestLERefPrice_PausedBlocksMutations(t)
	TestLERefPrice_RecoveryResumes(t)
}

func TestLE_RefVersionPropagation(t *testing.T) {
	TestLERefPrice_ControlledRebase(t)
}
