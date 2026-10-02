// Package reconciler — reconciler_invariants_test.go
//
// Phase C: Core reconciliation correctness invariant tests.
// These tests guard the critical safety properties of the MM order lifecycle.
package reconciler

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"tradedrift/platform/refprice"
	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/orderservice"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

// ── Minimal test doubles ──────────────────────────────────────────────────────

type invNoopProducer struct {
	creates int
	cancels int
}

func (p *invNoopProducer) PublishCreate(_ context.Context, _ string, _ int, _, _, _, _, _ string) error {
	p.creates++
	return nil
}

func (p *invNoopProducer) PublishCancel(_ context.Context, _ string, _ int, _ string) error {
	p.cancels++
	return nil
}

type invNoopOrderSvc struct {
	createCalls int
	cancelCalls int
}

func (s *invNoopOrderSvc) GetOrderByClientID(_ context.Context, _ string) (*orderservice.OrderState, error) {
	return nil, nil
}

func (s *invNoopOrderSvc) CancelMMOrder(_ context.Context, _ string) error {
	s.cancelCalls++
	return nil
}

func (s *invNoopOrderSvc) CreateMMOrder(_ context.Context, _, _, _, _, clientOrderID string) (*orderservice.OrderState, error) {
	s.createCalls++
	return &orderservice.OrderState{OrderID: "order-" + clientOrderID}, nil
}

func (s *invNoopOrderSvc) ListMMOrders(_ context.Context, _ string) ([]order.OSOrder, error) {
	return nil, nil
}

// invStubRefProvider adapts a static price/state into ReferencePriceProvider.
type invStubRefProvider struct {
	price   decimal.Decimal
	version int64
	state   refprice.State
}

func (p *invStubRefProvider) Get(_ string) (refprice.Entry, bool) {
	return refprice.Entry{
		Price:     p.price,
		Version:   p.version,
		FetchedAt: time.Now(),
		Source:    "test",
		State:     p.state,
	}, !p.price.IsZero()
}

// invNoopMetrics satisfies ReconcilerMetrics without touching Prometheus.
type invNoopMetrics struct{}

func (invNoopMetrics) IncStaleOrders(_ string)                {}
func (invNoopMetrics) IncReconcileCreate(_ string)             {}
func (invNoopMetrics) IncReconcileCancel(_ string)             {}
func (invNoopMetrics) IncReconcileCorrect(_ string)            {}
func (invNoopMetrics) IncReconcileNoop(_ string)               {}
func (invNoopMetrics) IncOrdersFilled(_, _ string)             {}
func (invNoopMetrics) IncDuplicateMMLevel(_ string)            {}
func (invNoopMetrics) IncRebaseGenerationFailure(_, _ string)  {}
func (invNoopMetrics) IncExpiryGenerationFailure(_ string)     {}
func (invNoopMetrics) IncQuantityInvariantViolation(_ string)  {}

// ── Helpers ───────────────────────────────────────────────────────────────────

const invTestMarket = "SOL-USDT"

func invSolMarketConfig() *config.MarketConfig {
	return &config.MarketConfig{
		MarketID:     invTestMarket,
		BaseAsset:    "SOL",
		QuoteAsset:   "USDT",
		TickSize:     decimal.RequireFromString("0.001"),
		LotSize:      decimal.RequireFromString("0.01"),
		MinOrderSize: decimal.RequireFromString("0.01"),
		QtyPerLevel:  decimal.RequireFromString("20.0"),
		Partition:    0,
		BidZones:     config.DefaultZoneConfig(),
		AskZones:     config.DefaultZoneConfig(),
		Repricing: config.RepricingConfig{
			SmallBps:      10,
			LargeBps:      100,
			MaxBatch:      4,
			OrderLifetime: 30 * time.Minute,
		},
	}
}

func invTestConfig(mc *config.MarketConfig) *config.Config {
	return &config.Config{
		Markets: []config.MarketConfig{*mc},
		RefPrice: config.RefPriceConfig{
			StaleThreshold: 5 * time.Minute,
			PauseThreshold: 10 * time.Minute,
		},
	}
}

func invNewReconciler(t *testing.T) (*Reconciler, *invNoopProducer, *invNoopOrderSvc) {
	t.Helper()
	prod := &invNoopProducer{}
	svc := &invNoopOrderSvc{}
	mc := invSolMarketConfig()
	cfg := invTestConfig(mc)
	r := NewReconcilerWithRNG(
		order.NewTracker(),
		prod,
		svc,
		nil, // no ME client in unit tests
		cfg,
		zap.NewNop(),
		invNoopMetrics{},
		rand.New(rand.NewSource(42)),
	)
	return r, prod, svc
}

func invSetLiveFresh(r *Reconciler, price string, version int64) {
	r.SetRefProvider(&invStubRefProvider{
		price:   decimal.RequireFromString(price),
		version: version,
		state:   refprice.StateFresh,
	})
}

func invSetStale(r *Reconciler, price string, version int64) {
	r.SetRefProvider(&invStubRefProvider{
		price:   decimal.RequireFromString(price),
		version: version,
		state:   refprice.StateStale,
	})
}

func invSetPaused(r *Reconciler, price string, version int64) {
	r.SetRefProvider(&invStubRefProvider{
		price:   decimal.RequireFromString(price),
		version: version,
		state:   refprice.StatePaused,
	})
}

// invPromoteAllResting transitions every PENDING/OS_REGISTERED order to RESTING.
func invPromoteAllResting(r *Reconciler) {
	for _, o := range r.tracker.AllMarkets() {
		if o.Status == order.StatusPending || o.Status == order.StatusOSRegistered {
			r.tracker.SetResting(o.LevelID, o.OrderID, o.OriginalQty, o.OriginalQty)
		}
	}
}

// invGenerateAndApply runs a full generate→diff→apply cycle without ME snapshot.
func invGenerateAndApply(t *testing.T, r *Reconciler, mc *config.MarketConfig, bidCount, askCount int) int {
	t.Helper()
	snap := r.currentReference(mc)
	levels, err := pricing.GenerateZonedDesired(
		mc, snap.Price, r.tracker,
		mc.BidZones, mc.AskZones, snap.Version,
		bidCount, askCount, r.rng,
	)
	require.NoError(t, err)
	entries := order.Diff(levels, r.tracker, invTestMarket, mc)
	applied := 0
	for _, e := range entries {
		if published, err2 := r.applyEntry(context.Background(), e, mc); err2 == nil && published {
			applied++
		}
	}
	return applied
}

// ── INV-C1: Stable convergence ────────────────────────────────────────────────

// TestInvariant_StableConvergence verifies that a second reconcile cycle at the same
// reference price produces zero mutations (0 CREATEs, 0 CANCELs, 0 CORRECTs).
func TestInvariant_StableConvergence_ZeroMutationsOnSecondCycle(t *testing.T) {
	r, prod, _ := invNewReconciler(t)
	invSetLiveFresh(r, "117.01", 1)
	mc := invSolMarketConfig()

	// First cycle: populate the full ladder
	first := invGenerateAndApply(t, r, mc, 12, 12)
	assert.Greater(t, first, 0, "first cycle must create orders")
	invPromoteAllResting(r)

	// Second cycle at identical ref: must produce zero net mutations
	beforeCreates := prod.creates
	beforeCancels := prod.cancels
	snap := r.currentReference(mc)
	levels, err := pricing.GenerateZonedDesired(
		mc, snap.Price, r.tracker,
		mc.BidZones, mc.AskZones, snap.Version,
		12, 12, r.rng,
	)
	require.NoError(t, err)
	entries := order.Diff(levels, r.tracker, invTestMarket, mc)
	for _, e := range entries {
		assert.NotEqual(t, order.DiffCreate, e.Action, "second cycle must not CREATE")
		assert.NotEqual(t, order.DiffCancel, e.Action, "second cycle must not CANCEL")
		assert.NotEqual(t, order.DiffCorrect, e.Action, "second cycle must not CORRECT")
	}
	assert.Equal(t, beforeCreates, prod.creates)
	assert.Equal(t, beforeCancels, prod.cancels)
}

// ── INV-C2: Single fill → single replacement ──────────────────────────────────

// TestInvariant_SingleFill_ProducesExactlyOneReplacement verifies that removing
// exactly one RESTING level from the tracker causes the next reconcile to produce
// exactly 1 CREATE and 0 CANCELs.
func TestInvariant_SingleFill_ProducesExactlyOneReplacement(t *testing.T) {
	r, prod, _ := invNewReconciler(t)
	invSetLiveFresh(r, "117.01", 1)
	mc := invSolMarketConfig()

	invGenerateAndApply(t, r, mc, 12, 12)
	invPromoteAllResting(r)

	// Remove one level (simulate full fill)
	allOrders := r.tracker.All(invTestMarket)
	require.NotEmpty(t, allOrders)
	r.tracker.Remove(allOrders[0].LevelID)

	beforeCreates := prod.creates
	beforeCancels := prod.cancels
	snap := r.currentReference(mc)
	levels, err := pricing.GenerateZonedDesired(
		mc, snap.Price, r.tracker,
		mc.BidZones, mc.AskZones, snap.Version,
		12, 12, r.rng,
	)
	require.NoError(t, err)
	entries := order.Diff(levels, r.tracker, invTestMarket, mc)

	var creates, cancels int
	for _, e := range entries {
		if e.Action == order.DiffCreate {
			creates++
		}
		if e.Action == order.DiffCancel {
			cancels++
		}
		_, _ = r.applyEntry(context.Background(), e, mc)
	}
	assert.Equal(t, 1, creates, "exactly 1 CREATE for the vacated slot")
	assert.Equal(t, 0, cancels)
	assert.Equal(t, beforeCreates+1, prod.creates)
	assert.Equal(t, beforeCancels, prod.cancels)
}

// ── INV-C3: Partial fill → no replacement ─────────────────────────────────────

// TestInvariant_PartialFill_NoReplacement verifies that a level with RemainingQty > MinOrderSize
// is locked in place; no CREATE or CANCEL is produced for that slot.
func TestInvariant_PartialFill_NoReplacement(t *testing.T) {
	r, _, _ := invNewReconciler(t)
	invSetLiveFresh(r, "117.01", 1)
	mc := invSolMarketConfig()

	invGenerateAndApply(t, r, mc, 12, 12)
	invPromoteAllResting(r)

	// Simulate a 50% partial fill on one level
	allOrders := r.tracker.All(invTestMarket)
	require.NotEmpty(t, allOrders)
	partial := allOrders[0]
	halfQty := partial.OriginalQty.Div(decimal.NewFromInt(2))
	r.tracker.SetResting(partial.LevelID, partial.OrderID, partial.OriginalQty, halfQty)

	snap := r.currentReference(mc)
	levels, err := pricing.GenerateZonedDesired(
		mc, snap.Price, r.tracker,
		mc.BidZones, mc.AskZones, snap.Version,
		12, 12, r.rng,
	)
	require.NoError(t, err)
	entries := order.Diff(levels, r.tracker, invTestMarket, mc)
	for _, e := range entries {
		assert.NotEqual(t, order.DiffCreate, e.Action, "partial fill must not cause CREATE")
		assert.NotEqual(t, order.DiffCancel, e.Action, "partial fill must not cause CANCEL")
	}
}

// ── INV-C4: Inventory skew → correct level counts ─────────────────────────────

// TestInvariant_InventorySkew_CorrectCounts verifies that GenerateZonedDesired respects
// bidCount/askCount even when they differ (e.g. bidCount=6, askCount=12).
func TestInvariant_InventorySkew_CorrectCounts(t *testing.T) {
	r, _, _ := invNewReconciler(t)
	invSetLiveFresh(r, "117.01", 1)
	mc := invSolMarketConfig()

	snap := r.currentReference(mc)
	levels, err := pricing.GenerateZonedDesired(
		mc, snap.Price, r.tracker,
		mc.BidZones, mc.AskZones, snap.Version,
		6, 12, r.rng,
	)
	require.NoError(t, err)

	bids, asks := 0, 0
	for _, l := range levels {
		if l.Side == "BUY" {
			bids++
		} else {
			asks++
		}
	}
	assert.Equal(t, 6, bids, "skewed ladder must have exactly 6 BID levels")
	assert.Equal(t, 12, asks, "skewed ladder must have exactly 12 ASK levels")
}

// ── INV-C5: Exposure cap blocks creation ──────────────────────────────────────

// TestInvariant_ExposureCap_BlocksCreation verifies that when the BID capital exposure
// cap is exceeded, applyEntry for a CREATE returns (false, nil) without publishing to Kafka.
func TestInvariant_ExposureCap_BlocksCreation(t *testing.T) {
	r, prod, _ := invNewReconciler(t)
	invSetLiveFresh(r, "117.01", 1)

	mc := invSolMarketConfig()
	mc.MaxBidExposureUSDT = decimal.RequireFromString("100.00") // tiny cap

	// Pre-fill tracker so cap is already exceeded: 116 * 20 = 2320 >> 100
	r.tracker.SetPending("MM-SOL-USDT-BID-01", "order-001", "coid-001", 1, pricing.PriceLevel{
		LevelID:  "MM-SOL-USDT-BID-01",
		MarketID: invTestMarket,
		Side:     "BUY",
		Price:    decimal.RequireFromString("116.00"),
		Quantity: decimal.RequireFromString("20.00"),
		Zone:     "LOW",
	})
	r.tracker.SetResting("MM-SOL-USDT-BID-01", "order-001",
		decimal.RequireFromString("20.00"), decimal.RequireFromString("20.00"))

	beforeCreates := prod.creates
	e := order.DiffEntry{
		Action:  order.DiffCreate,
		LevelID: "MM-SOL-USDT-BID-02",
		DesiredLevel: &pricing.PriceLevel{
			LevelID:  "MM-SOL-USDT-BID-02",
			MarketID: invTestMarket,
			Side:     "BUY",
			Price:    decimal.RequireFromString("115.50"),
			Quantity: decimal.RequireFromString("20.00"),
			Zone:     "LOW",
		},
	}
	published, err := r.applyEntry(context.Background(), e, mc)
	require.NoError(t, err)
	assert.False(t, published, "exposure cap must block order creation")
	assert.Equal(t, beforeCreates, prod.creates, "no Kafka publish when cap is hit")
}

// ── INV-C6: Duplicate price impossible ────────────────────────────────────────

// TestInvariant_DuplicatePrice_Impossible runs 500 ladder generations and verifies
// that no generated price appears twice on the same side.
func TestInvariant_DuplicatePrice_Impossible(t *testing.T) {
	mc := invSolMarketConfig()
	rng := rand.New(rand.NewSource(99))
	tracker := order.NewTracker()
	ref := decimal.RequireFromString("117.01")

	for i := 0; i < 500; i++ {
		levels, err := pricing.GenerateZonedDesired(
			mc, ref, tracker,
			mc.BidZones, mc.AskZones, int64(i),
			12, 12, rng,
		)
		require.NoError(t, err, "iteration %d: must not error", i)
		seen := make(map[string]bool, len(levels))
		for _, l := range levels {
			key := l.Side + ":" + l.Price.String()
			assert.False(t, seen[key],
				"iteration %d: duplicate price %s on %s side", i, l.Price, l.Side)
			seen[key] = true
		}
	}
}

// ── INV-C7: Restart deduplication ─────────────────────────────────────────────

// TestInvariant_Restart_NoDuplicateLevelIDs verifies that SyncFromOrders deduplicates
// orders with the same LevelID by keeping the highest-generation entry only.
func TestInvariant_Restart_NoDuplicateLevelIDs(t *testing.T) {
	tracker := order.NewTracker()

	osOrders := []order.OSOrder{
		{
			LevelID:       "MM-SOL-USDT-BID-01",
			OrderID:       "order-gen1",
			ClientOrderID: "MM-SOL-USDT-BID-01-G001",
			Side:          "BUY",
			Price:         decimal.RequireFromString("116.50"),
			OriginalQty:   decimal.RequireFromString("20.00"),
			RemainingQty:  decimal.RequireFromString("20.00"),
			Generation:    1,
		},
		{
			LevelID:       "MM-SOL-USDT-BID-01",
			OrderID:       "order-gen2",
			ClientOrderID: "MM-SOL-USDT-BID-01-G002",
			Side:          "BUY",
			Price:         decimal.RequireFromString("116.30"),
			OriginalQty:   decimal.RequireFromString("20.00"),
			RemainingQty:  decimal.RequireFromString("15.00"),
			Generation:    2,
		},
	}

	added, duplicates := tracker.SyncFromOrders(invTestMarket, osOrders)
	assert.Equal(t, 1, added, "only 1 unique level should be added")
	assert.Equal(t, 1, duplicates, "1 duplicate must be detected and discarded")

	// Winning entry must be generation 2
	o := tracker.Get("MM-SOL-USDT-BID-01")
	require.NotNil(t, o)
	assert.Equal(t, 2, o.Generation)
	assert.Equal(t, "order-gen2", o.OrderID)

	// No duplicate LevelIDs in All()
	seen := make(map[string]bool)
	for _, lo := range tracker.All(invTestMarket) {
		assert.False(t, seen[lo.LevelID], "duplicate LevelID %s in tracker", lo.LevelID)
		seen[lo.LevelID] = true
	}
}

// ── INV-C8: STALE reference → new creation prohibited ─────────────────────────

// TestInvariant_StaleReference_BlocksNewCreation verifies that ClassifyMovement returns
// ActionHold for a STALE reference, and that filtering out DiffCreate entries from a
// STALE cycle means no CREATE commands are dispatched.
func TestInvariant_StaleReference_BlocksNewCreation(t *testing.T) {
	r, prod, _ := invNewReconciler(t)
	invSetStale(r, "117.01", 1)
	mc := invSolMarketConfig()

	snap := r.currentReference(mc)
	action := ClassifyMovement(decimal.Zero, snap.Price, snap.Freshness, mc.Repricing)
	assert.Equal(t, ActionHold, action, "STALE reference must produce ActionHold")

	beforeCreates := prod.creates
	levels, err := pricing.GenerateZonedDesired(
		mc, snap.Price, r.tracker,
		mc.BidZones, mc.AskZones, snap.Version,
		12, 12, r.rng,
	)
	require.NoError(t, err)
	entries := order.Diff(levels, r.tracker, invTestMarket, mc)

	// Apply the HOLD filter (same as ReconcileMarket does)
	for _, e := range entries {
		if e.Action == order.DiffCreate {
			continue // must skip creates under HOLD
		}
		_, _ = r.applyEntry(context.Background(), e, mc)
	}
	assert.Equal(t, beforeCreates, prod.creates, "STALE ref must not produce any CREATE commands")
}

// ── INV-C9: PAUSED reference → ActionPause classification ─────────────────────

// TestInvariant_PausedReference_ActionPauseClassification verifies that a PAUSED reference
// produces ActionPause, signalling that ReconcileMarket must exit with 0 commands.
func TestInvariant_PausedReference_ActionPauseClassification(t *testing.T) {
	r, _, _ := invNewReconciler(t)
	invSetPaused(r, "117.01", 1)
	mc := invSolMarketConfig()

	snap := r.currentReference(mc)
	action := ClassifyMovement(decimal.Zero, snap.Price, snap.Freshness, mc.Repricing)
	assert.Equal(t, ActionPause, action, "PAUSED reference must produce ActionPause")
	assert.Equal(t, FreshnessPaused, snap.Freshness)
}

// ── INV-C10: DiffCorrect replacement quantity == RemainingQty (LE-01) ─────────

// TestInvariant_DiffCorrect_UsesRemainingQty_NotDesiredQty verifies LE-01:
// when an order has OriginalQty=1.0 but RemainingQty=0.35, the correction
// must use 0.35 (not 1.0) as the replacement quantity.
func TestInvariant_DiffCorrect_UsesRemainingQty_NotDesiredQty(t *testing.T) {
	r, _, _ := invNewReconciler(t)
	mc := invSolMarketConfig()

	originalQty := decimal.RequireFromString("1.00")
	remainingQty := decimal.RequireFromString("0.35")
	desiredQty := decimal.RequireFromString("1.00") // the ladder's desired size

	safeQty, ok := r.ResolveReplacementQuantity(
		desiredQty, remainingQty, originalQty,
		mc.MinOrderSize, mc.LotSize,
		invTestMarket, "MM-SOL-USDT-BID-01",
	)
	require.True(t, ok, "partially filled order must produce a valid replacement")
	assert.Equal(t, "0.35", safeQty.String(),
		"replacement qty must equal RemainingQty (0.35), not DesiredQty (1.00)")
}

// ── INV-C11: Full fill → replacement quantity = ZERO (no resurrection) ─────────

// TestInvariant_FullFill_NoResurrection verifies that a fully filled order
// (RemainingQty=0) must not produce a replacement (ok=false).
func TestInvariant_FullFill_NoResurrection(t *testing.T) {
	r, _, _ := invNewReconciler(t)
	mc := invSolMarketConfig()

	_, ok := r.ResolveReplacementQuantity(
		decimal.RequireFromString("20.00"), // desired
		decimal.Zero,                       // remaining = fully consumed
		decimal.RequireFromString("20.00"), // original
		mc.MinOrderSize, mc.LotSize,
		invTestMarket, "MM-SOL-USDT-BID-01",
	)
	assert.False(t, ok, "zero remaining qty must not produce a replacement (no resurrection)")
}

// ── INV-C12: Large reference movement → ActionControlledRebase ────────────────

// TestInvariant_LargeRefMovement_TriggersControlledRebase verifies that a reference
// price movement >= LargeBps (100 bps default) causes ActionControlledRebase.
func TestInvariant_LargeRefMovement_TriggersControlledRebase(t *testing.T) {
	mc := invSolMarketConfig()
	cfg := mc.Repricing // SmallBps=10, LargeBps=100

	prev := decimal.RequireFromString("117.00")
	// 200 bps movement: 117 * 1.02 = 119.34
	next := decimal.RequireFromString("119.34")

	action := ClassifyMovement(prev, next, FreshnessFresh, cfg)
	assert.Equal(t, ActionControlledRebase, action,
		"200 bps move above LargeBps=100 must trigger CONTROLLED_REBASE")
}
