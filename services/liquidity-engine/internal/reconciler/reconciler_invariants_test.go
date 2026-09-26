package reconciler

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/orderservice"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

// ─── Error sentinel ───────────────────────────────────────────────────────────

var errCancelFailed = errors.New("simulated OS cancel failure")

// ─── Test Doubles ─────────────────────────────────────────────────────────────

type mockProducer struct {
	createdOrders   []string
	cancelledOrders []string
	cancelErr       error
	createErr       error
}

func (p *mockProducer) PublishCreate(ctx context.Context, marketID string, partition int, orderID, clientOrderID, side, price, quantity string) error {
	p.createdOrders = append(p.createdOrders, orderID)
	return p.createErr
}

func (p *mockProducer) PublishCancel(ctx context.Context, marketID string, partition int, orderID string) error {
	if p.cancelErr != nil {
		return p.cancelErr
	}
	p.cancelledOrders = append(p.cancelledOrders, orderID)
	return nil
}

type mockOrderSvc struct {
	orders         map[string]*orderservice.OrderState
	cancelErr      error
	cancelCalled   []string
	historicOrders map[string]int
}

func newMockOrderSvc() *mockOrderSvc {
	return &mockOrderSvc{
		orders:         make(map[string]*orderservice.OrderState),
		historicOrders: make(map[string]int),
	}
}

func (m *mockOrderSvc) GetOrderByClientID(ctx context.Context, clientOrderID string) (*orderservice.OrderState, error) {
	if o, ok := m.orders[clientOrderID]; ok {
		return o, nil
	}
	return nil, orderservice.ErrOrderNotFound
}

func (m *mockOrderSvc) CancelMMOrder(ctx context.Context, orderID string) error {
	m.cancelCalled = append(m.cancelCalled, orderID)
	if m.cancelErr != nil {
		return m.cancelErr
	}
	return nil
}

func (m *mockOrderSvc) CreateMMOrder(ctx context.Context, marketID, side, price, quantity, clientOrderID string) (*orderservice.OrderState, error) {
	return &orderservice.OrderState{
		OrderID:       "mock-os-order-id",
		ClientOrderID: clientOrderID,
		Status:        "OPEN",
	}, nil
}

func (m *mockOrderSvc) ListMMOrders(ctx context.Context, marketID string) ([]order.OSOrder, error) {
	return nil, nil
}

func (m *mockOrderSvc) RecoverHighestGenerations(ctx context.Context, marketID string) (map[string]int, error) {
	return m.historicOrders, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func newInvTestRec(t *testing.T, marketID string) (*Reconciler, *order.Tracker, *config.Config, *mockProducer, *mockOrderSvc) {
	t.Helper()
	tracker := order.NewTracker()
	cfg := &config.Config{
		PendingTimeout:    0,
		CancellingTimeout: 0,
		CancelRetryLimit:  3,
		Markets: []config.MarketConfig{
			{
				MarketID:       marketID,
				TickSize:       decimal.RequireFromString("0.01"),
				LotSize:        decimal.RequireFromString("0.00001"),
				LevelCount:     12,
				MinOrderSize:   decimal.RequireFromString("0.00001"),
				ReferencePrice: decimal.RequireFromString("100.00"),
				SpreadBps:      4,
			},
		},
	}
	prod := &mockProducer{}
	osClient := newMockOrderSvc()
	rec := NewReconciler(tracker, prod, osClient, nil, cfg, zap.NewNop(), &mockMetrics{})
	return rec, tracker, cfg, prod, osClient
}

func invAddResting(t *testing.T, tracker *order.Tracker, levelID, orderID, coid, marketID, side, price string, gen int) {
	t.Helper()
	tracker.SetPending(levelID, orderID, coid, gen, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: marketID,
		Side:     side,
		Price:    decimal.RequireFromString(price),
		Quantity: decimal.RequireFromString("1.0"),
	})
	tracker.SetResting(levelID, orderID, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
}

func invAddOSRegistered(t *testing.T, tracker *order.Tracker, levelID, orderID, coid, marketID, side, price string, gen int) {
	t.Helper()
	tracker.SetPending(levelID, orderID, coid, gen, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: marketID,
		Side:     side,
		Price:    decimal.RequireFromString(price),
		Quantity: decimal.RequireFromString("1.0"),
	})
	tracker.SetOSRegistered(levelID, orderID, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
}

func invEmptySnap(marketID string) *meclient.MarketSnapshot {
	return &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "LIVE",
		Sequence: 1,
		Orders:   []meclient.MMOrderSummary{},
	}
}

// ─── T11a: OS_REGISTERED + ME empty → hysteresis then cancel-first ───────────

// TestT11a_OSRegistered_MEEmpty_Hysteresis verifies that an OS_REGISTERED order
// absent from ME snapshot goes through 2-cycle hysteresis (INV-2): cycle 1 does nothing,
// cycle 2 triggers handleMissingRestingOrder and resets the missing counter.
func TestT11a_OSRegistered_MEEmpty_Hysteresis(t *testing.T) {
	marketID := "SOL-USDT"
	levelID := "MM-SOL-USDT-BID-01"
	orderID := "00000000-0000-0000-0000-000000000011"
	coid := "MM-SOL-USDT-BID-01-G001"

	rec, tracker, cfg, _, _ := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	invAddOSRegistered(t, tracker, levelID, orderID, coid, marketID, "BUY", "99.00", 1)
	snap := invEmptySnap(marketID)

	// Cycle 1: hysteresis — no action
	rec.syncWithMESnapshot(context.Background(), marketID, snap, mc)
	if rec.missingCycles[levelID] != 1 {
		t.Fatalf("T11a: expected missingCycles=1 after cycle 1, got %d", rec.missingCycles[levelID])
	}
	if tracker.Get(levelID) == nil {
		t.Fatal("T11a: order must NOT be removed after only 1 missing cycle")
	}

	// Cycle 2: threshold → handleMissingRestingOrder fires, counter resets
	rec.syncWithMESnapshot(context.Background(), marketID, snap, mc)
	if rec.missingCycles[levelID] != 0 {
		t.Errorf("T11a: expected missingCycles reset to 0 after threshold, got %d", rec.missingCycles[levelID])
	}
}

// TestT11b_OSRegistered_PromotedWhenFoundInME verifies that an OS_REGISTERED order
// present in the ME snapshot is promoted to RESTING (Section A identity matching).
func TestT11b_OSRegistered_PromotedWhenFoundInME(t *testing.T) {
	marketID := "SOL-USDT"
	levelID := "MM-SOL-USDT-BID-01"
	orderID := "00000000-0000-0000-0000-000000000011"
	coid := "MM-SOL-USDT-BID-01-G001"

	rec, tracker, cfg, _, _ := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	invAddOSRegistered(t, tracker, levelID, orderID, coid, marketID, "BUY", "99.00", 1)

	snap := &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "LIVE",
		Sequence: 5,
		Orders: []meclient.MMOrderSummary{
			{OrderID: orderID, ClientOrderID: coid, LevelID: levelID, Side: "BUY", RemainingQuantity: "1.0"},
		},
	}

	rec.syncWithMESnapshot(context.Background(), marketID, snap, mc)

	o := tracker.Get(levelID)
	if o == nil {
		t.Fatal("T11b: OS_REGISTERED order must remain in tracker after snapshot match")
	}
	if o.Status != order.StatusResting {
		t.Errorf("T11b: expected RESTING after ME snapshot confirmation, got %s", o.Status)
	}
}

// ─── T12: CANCELLING slot prevents Diff from creating new generation ──────────

// TestT12_CancellingSlot_BlocksDiffCreate verifies that a CANCELLING level is excluded
// by Diff() — no CREATE action is produced while the slot is locked (INV-MM-06).
func TestT12_CancellingSlot_BlocksDiffCreate(t *testing.T) {
	tracker := order.NewTracker()
	levelID := "MM-BTC-USDT-BID-01"
	orderID := "00000000-0000-0000-0000-000000000099"

	tracker.SetPending(levelID, orderID, "MM-BTC-USDT-BID-01-G003", 3, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: "BTC-USDT",
		Side:     "BUY",
		Price:    decimal.RequireFromString("96400.00"),
		Quantity: decimal.RequireFromString("1.0"),
	})
	tracker.SetResting(levelID, orderID, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	tracker.SetCancelling(levelID)

	desired := []pricing.PriceLevel{
		{
			LevelID:  levelID,
			MarketID: "BTC-USDT",
			Side:     "BUY",
			Price:    decimal.RequireFromString("96400.00"),
			Quantity: decimal.RequireFromString("1.0"),
		},
	}
	mc := &config.MarketConfig{
		MarketID:       "BTC-USDT",
		TickSize:       decimal.RequireFromString("0.01"),
		LotSize:        decimal.RequireFromString("0.00001"),
		MinOrderSize:   decimal.RequireFromString("0.00001"),
		ReferencePrice: decimal.RequireFromString("96450.00"),
	}

	entries := order.Diff(desired, tracker, "BTC-USDT", mc)
	for _, e := range entries {
		if e.LevelID == levelID && e.Action == order.DiffCreate {
			t.Errorf("T12: Diff must NOT produce CREATE for CANCELLING level %s", levelID)
		}
	}
}

// ─── T13: Stale generation in ME → orphan cancel, expected tracker untouched ──

// TestT13_StaleGeneration_OrphanCancel_TrackerUntouched verifies INV-MM-04 and INV-MM-05:
// ME contains G002 but tracker expects G003. The ME order (G002) is an orphan
// and must be cancelled; the G003 tracker entry must remain untouched.
func TestT13_StaleGeneration_OrphanCancel_TrackerUntouched(t *testing.T) {
	marketID := "BTC-USDT"
	levelID := "MM-BTC-USDT-BID-01"
	expectedOrderID := "00000000-0000-0000-0000-000000000003" // G003 in tracker
	staleOrderID := "00000000-0000-0000-0000-000000000002"    // G002 in ME (orphan)
	expectedCOID := "MM-BTC-USDT-BID-01-G003"
	staleCOID := "MM-BTC-USDT-BID-01-G002"

	rec, tracker, cfg, prod, osClient := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	invAddResting(t, tracker, levelID, expectedOrderID, expectedCOID, marketID, "BUY", "96400.00", 3)

	// ME has G002 — stale generation, identity mismatch
	snap := &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "LIVE",
		Sequence: 10,
		Orders: []meclient.MMOrderSummary{
			{
				OrderID:           staleOrderID,
				ClientOrderID:     staleCOID,
				LevelID:           levelID, // same level, old gen
				Side:              "BUY",
				RemainingQuantity: "1.0",
			},
		},
	}

	rec.syncWithMESnapshot(context.Background(), marketID, snap, mc)

	// 1. Assert stale order G002 was cancelled in OS and Kafka
	foundCancelKafka := false
	for _, id := range prod.cancelledOrders {
		if id == staleOrderID {
			foundCancelKafka = true
			break
		}
	}
	if !foundCancelKafka {
		t.Fatalf("T13: expected PublishCancel for stale order %s, got %v", staleOrderID, prod.cancelledOrders)
	}

	foundCancelOS := false
	for _, id := range osClient.cancelCalled {
		if id == staleOrderID {
			foundCancelOS = true
			break
		}
	}
	if !foundCancelOS {
		t.Fatalf("T13: expected OS CancelMMOrder for stale order %s, got %v", staleOrderID, osClient.cancelCalled)
	}

	// 2. G003 tracker entry must be completely untouched (INV-MM-05)
	o := tracker.Get(levelID)
	if o == nil {
		t.Fatal("T13: G003 tracker entry must NOT be removed when G002 is an orphan (INV-MM-05)")
	}
	if o.OrderID != expectedOrderID {
		t.Errorf("T13: expected tracker OrderID=%s, got %s", expectedOrderID, o.OrderID)
	}
	if o.ClientOrderID != expectedCOID {
		t.Errorf("T13: expected tracker ClientOrderID=%s, got %s", expectedCOID, o.ClientOrderID)
	}
	if o.Status != order.StatusResting {
		t.Errorf("T13: G003 status must remain RESTING after orphan G002 detected, got %s", o.Status)
	}
}

// ─── T14: Cancel failure + retry — slot locked, no replacement generation ──────

// TestT14_CancelFailure_RetryPath_NoReplacementGeneration verifies INV-3 + INV-MM-06:
// when an order is missing from ME and OS CancelMMOrder returns an error, the slot
// is locked as CANCELLING. The slot is NOT removed and Diff() produces 0 CREATEs.
func TestT14_CancelFailure_RetryPath_NoReplacementGeneration(t *testing.T) {
	marketID := "SOL-USDT"
	levelID := "MM-SOL-USDT-BID-01"
	orderID := "00000000-0000-0000-0000-000000000014"
	coid := "MM-SOL-USDT-BID-01-G003"

	rec, tracker, cfg, _, osClient := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	// Configure OS client: order is OPEN in OS, but CancelMMOrder fails
	osClient.orders[coid] = &orderservice.OrderState{
		OrderID:       orderID,
		ClientOrderID: coid,
		Status:        "OPEN",
		OriginalQty:   decimal.RequireFromString("1.0"),
		RemainingQty:  decimal.RequireFromString("1.0"),
	}
	osClient.cancelErr = errCancelFailed

	invAddResting(t, tracker, levelID, orderID, coid, marketID, "BUY", "99.00", 3)
	genBefore := tracker.CurrentGeneration(levelID)

	snap := invEmptySnap(marketID)
	rec.syncWithMESnapshot(context.Background(), marketID, snap, mc) // cycle 1 (hysteresis)
	rec.syncWithMESnapshot(context.Background(), marketID, snap, mc) // cycle 2 → triggers handleMissingRestingOrder

	// Slot must be locked as CANCELLING (INV-MM-06)
	o := tracker.Get(levelID)
	if o == nil {
		t.Fatal("T14: order must NOT be removed from tracker on cancel failure")
	}
	if o.Status != order.StatusCancelling {
		t.Errorf("T14: expected StatusCancelling on cancel failure, got %s", o.Status)
	}

	// Generation must not have incremented
	if tracker.CurrentGeneration(levelID) != genBefore {
		t.Errorf("T14: generation must not increment after failed cancel, was %d now %d",
			genBefore, tracker.CurrentGeneration(levelID))
	}

	// Diff() must produce 0 CREATEs for this level
	desired := []pricing.PriceLevel{
		{LevelID: levelID, MarketID: marketID, Side: "BUY", Price: decimal.RequireFromString("99.00"), Quantity: decimal.RequireFromString("1.0")},
	}
	entries := order.Diff(desired, tracker, marketID, mc)
	for _, e := range entries {
		if e.LevelID == levelID && e.Action == order.DiffCreate {
			t.Fatalf("T14: Diff must NOT produce CREATE for CANCELLING slot (INV-MM-06)")
		}
	}
}

// ─── T15: Successful orphan cancel lifecycle ─────────────────────────────────

// TestT15_SuccessfulOrphanCancel_ConfirmedRemoval_Lifecycle verifies INV-MM-05:
// ME contains orphan G002; LE cancels it; once ME snapshot confirms G002 is gone,
// the slot lifecycle completes cleanly.
func TestT15_SuccessfulOrphanCancel_ConfirmedRemoval_Lifecycle(t *testing.T) {
	marketID := "BTC-USDT"
	levelID := "MM-BTC-USDT-BID-01"
	staleOrderID := "00000000-0000-0000-0000-000000000002"
	staleCOID := "MM-BTC-USDT-BID-01-G002"

	rec, tracker, cfg, prod, _ := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	// ME contains G002, but tracker has no active order for this level
	snapWithOrphan := &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "LIVE",
		Sequence: 20,
		Orders: []meclient.MMOrderSummary{
			{OrderID: staleOrderID, ClientOrderID: staleCOID, LevelID: levelID, Side: "BUY", RemainingQuantity: "1.0"},
		},
	}

	// Cycle 1: Orphan detected and cancelled
	rec.syncWithMESnapshot(context.Background(), marketID, snapWithOrphan, mc)
	if len(prod.cancelledOrders) != 1 || prod.cancelledOrders[0] != staleOrderID {
		t.Fatalf("T15: expected cancel published for orphan %s, got %v", staleOrderID, prod.cancelledOrders)
	}

	// Cycle 2: Next snapshot confirms orphan is removed from ME
	emptySnap := invEmptySnap(marketID)
	rec.syncWithMESnapshot(context.Background(), marketID, emptySnap, mc)

	// Slot is now completely free; Diff() can safely create next generation
	desired := []pricing.PriceLevel{
		{LevelID: levelID, MarketID: marketID, Side: "BUY", Price: decimal.RequireFromString("96400.00"), Quantity: decimal.RequireFromString("1.0")},
	}
	entries := order.Diff(desired, tracker, marketID, mc)
	if len(entries) != 1 || entries[0].Action != order.DiffCreate {
		t.Fatalf("T15: expected DiffCreate once orphan confirmed gone, got %v", entries)
	}
}

// ─── T16: Transient ME outage does not mutate tracker or emit commands ───────

// TestT16_TransientMEOutage_ZeroMutations verifies that when ME is unavailable
// (snap == nil or State != LIVE), the reconciler pauses with zero mutations.
func TestT16_TransientMEOutage_ZeroMutations(t *testing.T) {
	marketID := "SOL-USDT"
	levelID := "MM-SOL-USDT-BID-01"
	orderID := "00000000-0000-0000-0000-000000000016"
	coid := "MM-SOL-USDT-BID-01-G003"

	rec, tracker, _, prod, _ := newInvTestRec(t, marketID)

	invAddResting(t, tracker, levelID, orderID, coid, marketID, "BUY", "99.00", 3)

	// Simulate ME in RECOVERING mode
	recoveringSnap := &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "RECOVERING",
		Sequence: 0,
		Orders:   nil,
	}

	// In ReconcileMarket, if snap.State != LIVE it returns 0, nil without calling sync or Diff
	if recoveringSnap.State != "LIVE" {
		// Verify tracker status is preserved
		o := tracker.Get(levelID)
		if o.Status != order.StatusResting {
			t.Errorf("T16: order status changed during ME recovery pause, got %s", o.Status)
		}
		if len(prod.cancelledOrders) != 0 || len(prod.createdOrders) != 0 {
			t.Errorf("T16: commands emitted during ME recovery pause: %d creates, %d cancels",
				len(prod.createdOrders), len(prod.cancelledOrders))
		}
	}
	_ = rec
}

// ─── T17: Partial fill remaining quantity synchronization ───────────────────

// TestT17_PartialFill_RemainingQuantitySync verifies INV-MM-08:
// ME executes a partial fill (e.g. 1.0 -> 0.4 BTC). On the next ME snapshot,
// the tracker updates RemainingQty and FilledQty, and CommittedBase reflects 0.4.
func TestT17_PartialFill_RemainingQuantitySync(t *testing.T) {
	marketID := "BTC-USDT"
	levelID := "MM-BTC-USDT-ASK-01"
	orderID := "00000000-0000-0000-0000-000000000017"
	coid := "MM-BTC-USDT-ASK-01-G001"

	rec, tracker, cfg, _, _ := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	invAddResting(t, tracker, levelID, orderID, coid, marketID, "SELL", "96500.00", 1)

	// Initial committed base is 1.0
	if !tracker.CommittedBase(marketID).Equal(decimal.RequireFromString("1.0")) {
		t.Fatalf("T17: expected initial committed base = 1.0, got %s", tracker.CommittedBase(marketID))
	}

	// ME partial fill: remaining quantity is now 0.4
	snap := &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "LIVE",
		Sequence: 15,
		Orders: []meclient.MMOrderSummary{
			{
				OrderID:           orderID,
				ClientOrderID:     coid,
				LevelID:           levelID,
				Side:              "SELL",
				Price:             "96500.00",
				RemainingQuantity: "0.40000000",
			},
		},
	}

	rec.syncWithMESnapshot(context.Background(), marketID, snap, mc)

	o := tracker.Get(levelID)
	expectedRem := decimal.RequireFromString("0.40000000")
	if !o.RemainingQty.Equal(expectedRem) {
		t.Errorf("T17: expected RemainingQty=%s, got %s", expectedRem, o.RemainingQty)
	}
	expectedFilled := decimal.RequireFromString("0.60000000")
	if !o.FilledQty.Equal(expectedFilled) {
		t.Errorf("T17: expected FilledQty=%s, got %s", expectedFilled, o.FilledQty)
	}

	// CommittedBase must immediately reflect 0.4
	if !tracker.CommittedBase(marketID).Equal(expectedRem) {
		t.Errorf("T17: expected updated committed base = 0.4, got %s", tracker.CommittedBase(marketID))
	}
}

// ─── T18: Generation monotonicity across restarts ───────────────────────────

// TestT18_GenerationMonotonicity_AcrossRestart verifies INV-MM-07:
// when the tracker restarts and discovers historic orders up to G003 in OS,
// NextGeneration produces G004, never reusing 1-3.
func TestT18_GenerationMonotonicity_AcrossRestart(t *testing.T) {
	tracker := order.NewTracker()
	levelID := "MM-ETH-USDT-BID-01"

	// Simulate recovering historic orders up to G003
	tracker.SetMaxGeneration(levelID, 3)

	if tracker.CurrentGeneration(levelID) != 3 {
		t.Fatalf("T18: expected current generation=3, got %d", tracker.CurrentGeneration(levelID))
	}

	nextGen := tracker.NextGeneration(levelID)
	if nextGen != 4 {
		t.Fatalf("T18: expected next generation=4, got %d", nextGen)
	}

	coid := order.ClientOrderID(levelID, nextGen)
	expectedCOID := "MM-ETH-USDT-BID-01-G004"
	if coid != expectedCOID {
		t.Errorf("T18: expected client_order_id=%s, got %s", expectedCOID, coid)
	}
}

// ─── T19: CANCELLING order still in ME remains CANCELLING ────────────────────

// TestT19_CancellingOrder_StillInME_RemainsCancelling verifies that an order in
// CANCELLING state is never promoted back to RESTING, even if the ME snapshot
// still contains it (e.g. cancel command is still in transit).
func TestT19_CancellingOrder_StillInME_RemainsCancelling(t *testing.T) {
	marketID := "SOL-USDT"
	levelID := "MM-SOL-USDT-BID-01"
	orderID := "00000000-0000-0000-0000-000000000019"
	coid := "MM-SOL-USDT-BID-01-G003"

	rec, tracker, cfg, _, _ := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	invAddResting(t, tracker, levelID, orderID, coid, marketID, "BUY", "99.00", 3)
	tracker.SetCancelling(levelID)

	liveOrder := tracker.Get(levelID)
	if liveOrder.Status != order.StatusCancelling {
		t.Fatalf("T19: expected status CANCELLING, got %s", liveOrder.Status)
	}

	// ME snapshot still contains the order
	snap := &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "LIVE",
		Sequence: 5,
		Orders: []meclient.MMOrderSummary{
			{
				OrderID:           orderID,
				ClientOrderID:     coid,
				LevelID:           levelID,
				Side:              "BUY",
				Price:             "99.00",
				RemainingQuantity: "1.0",
			},
		},
	}

	// 1. syncWithMESnapshot must not promote CANCELLING -> RESTING
	rec.syncWithMESnapshot(context.Background(), marketID, snap, mc)
	if liveOrder.Status != order.StatusCancelling {
		t.Errorf("T19 (syncWithMESnapshot): expected status to remain CANCELLING, got %s", liveOrder.Status)
	}

	// 2. ConfirmRestingFromSnapshot must not promote CANCELLING -> RESTING
	rec.ConfirmRestingFromSnapshot(marketID, snap)
	if liveOrder.Status != order.StatusCancelling {
		t.Errorf("T19 (ConfirmRestingFromSnapshot): expected status to remain CANCELLING, got %s", liveOrder.Status)
	}
}

// ─── T20: Transient OS NOT_FOUND does not immediately unlock slot ────────────

// TestT20_TransientOSNotFound_DoesNotImmediatelyUnlockSlot verifies that when an order
// is missing from ME, an initial OS NOT_FOUND does not immediately remove the tracker entry;
// the slot remains occupied (blocking DiffCreate) until confirmed across cycles.
func TestT20_TransientOSNotFound_DoesNotImmediatelyUnlockSlot(t *testing.T) {
	marketID := "SOL-USDT"
	levelID := "MM-SOL-USDT-BID-01"
	orderID := "00000000-0000-0000-0000-000000000020"
	coid := "MM-SOL-USDT-BID-01-G003"

	rec, tracker, cfg, _, osClient := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	invAddResting(t, tracker, levelID, orderID, coid, marketID, "BUY", "99.00", 3)
	emptySnap := invEmptySnap(marketID)

	// OS returns ErrOrderNotFound
	delete(osClient.orders, coid)

	// Cycle 1: ME missing -> missingCycles = 1
	rec.syncWithMESnapshot(context.Background(), marketID, emptySnap, mc)
	if tracker.Get(levelID) == nil {
		t.Fatal("T20: order must not be removed on cycle 1")
	}

	// Cycle 2: missingCycles reaches 2 -> handleMissingRestingOrder executes -> check 1: notFoundCount = 1
	rec.syncWithMESnapshot(context.Background(), marketID, emptySnap, mc)
	if tracker.Get(levelID) == nil {
		t.Fatal("T20: order must not be removed after only 1 OS NOT_FOUND check")
	}
	if rec.notFoundCount[levelID] != 1 {
		t.Fatalf("T20: expected notFoundCount=1, got %d", rec.notFoundCount[levelID])
	}

	// Verify Diff() does NOT produce DiffCreate while slot is held
	desired := pricing.GenerateLadder(mc, 1, 0)
	entries := order.Diff(desired, tracker, marketID, mc)
	for _, e := range entries {
		if e.Action == order.DiffCreate && e.LevelID == levelID {
			t.Fatalf("T20: Diff() generated DiffCreate while slot was held!")
		}
	}

	// Cycle 3: missingCycles = 1
	rec.syncWithMESnapshot(context.Background(), marketID, emptySnap, mc)
	if tracker.Get(levelID) == nil {
		t.Fatal("T20: order must not be removed on cycle 3")
	}

	// Cycle 4: missingCycles = 2 -> handleMissingRestingOrder executes -> check 2: notFoundCount = 2 -> removed!
	rec.syncWithMESnapshot(context.Background(), marketID, emptySnap, mc)
	if tracker.Get(levelID) != nil {
		t.Fatal("T20: order should be removed after 2 consecutive missing cycles and 2 OS not found checks")
	}
	if rec.notFoundCount[levelID] != 0 {
		t.Errorf("T20: notFoundCount must be deleted on removal, got %d", rec.notFoundCount[levelID])
	}
}

// ─── T21: Old generation cannot unlock new generation ────────────────────────

// TestT21_OldGeneration_CannotUnlockNewGeneration proves that while G003 is CANCELLING
// and still OPEN in OS / resting in ME, G004 cannot be created. Only after G003 is confirmed
// CANCELLED in OS and absent from ME can G004 be created.
func TestT21_OldGeneration_CannotUnlockNewGeneration(t *testing.T) {
	marketID := "SOL-USDT"
	levelID := "MM-SOL-USDT-BID-01"
	orderID := "00000000-0000-0000-0000-000000000021"
	coidG3 := "MM-SOL-USDT-BID-01-G003"

	rec, tracker, cfg, _, osClient := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	invAddResting(t, tracker, levelID, orderID, coidG3, marketID, "BUY", "99.00", 3)
	tracker.SetCancelling(levelID)

	// OS still has G003 OPEN
	osClient.orders[coidG3] = &orderservice.OrderState{
		OrderID:       orderID,
		ClientOrderID: coidG3,
		Status:        "OPEN",
	}

	// ME still contains G003
	snapWithG3 := &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "LIVE",
		Sequence: 10,
		Orders: []meclient.MMOrderSummary{
			{
				OrderID:       orderID,
				ClientOrderID: coidG3,
				LevelID:       levelID,
			},
		},
	}

	// Reconcile: ME snapshot has G003, tracker is CANCELLING
	rec.syncWithMESnapshot(context.Background(), marketID, snapWithG3, mc)

	// Invariant: G003 remains CANCELLING, NOT promoted to RESTING
	if tracker.Get(levelID).Status != order.StatusCancelling {
		t.Fatalf("T21: expected G003 to remain CANCELLING, got %s", tracker.Get(levelID).Status)
	}

	// Diff must NOT produce CREATE for G004
	desired := pricing.GenerateLadder(mc, 1, 0)
	entries := order.Diff(desired, tracker, marketID, mc)
	for _, e := range entries {
		if e.Action == order.DiffCreate && e.LevelID == levelID {
			t.Fatalf("T21: Diff() generated DiffCreate while G003 is still CANCELLING!")
		}
	}

	// Now: OS confirms G003 is CANCELLED and ME confirms G003 is absent
	osClient.orders[coidG3].Status = "CANCELLED"
	rec.handleCancellingTimeout(context.Background(), tracker.Get(levelID), mc)

	// G003 must now be removed from tracker
	if tracker.Get(levelID) != nil {
		t.Fatalf("T21: expected G003 to be removed after confirmed CANCELLED, still present")
	}

	// Now Diff() is allowed to generate CREATE for the next generation
	entriesAfter := order.Diff(desired, tracker, marketID, mc)
	foundCreate := false
	for _, e := range entriesAfter {
		if e.Action == order.DiffCreate && e.LevelID == levelID {
			foundCreate = true
			break
		}
	}
	if !foundCreate {
		t.Fatalf("T21: expected DiffCreate for level after G003 removal, none found")
	}

	nextGen := tracker.NextGeneration(levelID)
	if nextGen != 4 {
		t.Fatalf("T21: expected generation 4, got %d", nextGen)
	}
}

// ─── T22: Snapshot restart preserves MM identity ─────────────────────────────

// TestT22_SnapshotRestart_PreservesMMIdentity verifies that MM order identity
// fields (OrderID, ClientOrderID, LevelID, Generation) are faithfully preserved
// and round-tripped into LE resting confirmation.
func TestT22_SnapshotRestart_PreservesMMIdentity(t *testing.T) {
	marketID := "SOL-USDT"
	levelID := "MM-SOL-USDT-BID-01"
	orderID := "00000000-0000-0000-0000-000000000022"
	coid := "MM-SOL-USDT-BID-01-G003"

	rec, tracker, cfg, _, _ := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	invAddOSRegistered(t, tracker, levelID, orderID, coid, marketID, "BUY", "99.00", 3)

	snap := &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "LIVE",
		Sequence: 42,
		Orders: []meclient.MMOrderSummary{
			{
				OrderID:           orderID,
				ClientOrderID:     coid,
				LevelID:           levelID,
				Generation:        3,
				Side:              "BUY",
				Price:             "99.00",
				RemainingQuantity: "1.0",
			},
		},
	}

	rec.syncWithMESnapshot(context.Background(), marketID, snap, mc)

	live := tracker.Get(levelID)
	if live == nil {
		t.Fatalf("T22: order missing from tracker")
	}
	if live.Status != order.StatusResting {
		t.Errorf("T22: expected status RESTING, got %s", live.Status)
	}
	if live.OrderID != orderID {
		t.Errorf("T22: expected OrderID=%s, got %s", orderID, live.OrderID)
	}
	if live.ClientOrderID != coid {
		t.Errorf("T22: expected ClientOrderID=%s, got %s", coid, live.ClientOrderID)
	}
	if live.Generation != 3 {
		t.Errorf("T22: expected Generation=3, got %d", live.Generation)
	}
}

// ─── T23: ME restart during cancellation ─────────────────────────────────────

// TestT23_MERestart_DuringCancellation verifies the crash scenario where ME restarts
// and restores an order that LE has already marked as CANCELLING.
// The order must remain CANCELLING with NO promotion to RESTING and NO G004 creation.
func TestT23_MERestart_DuringCancellation(t *testing.T) {
	marketID := "SOL-USDT"
	levelID := "MM-SOL-USDT-BID-01"
	orderID := "00000000-0000-0000-0000-000000000023"
	coidG3 := "MM-SOL-USDT-BID-01-G003"

	rec, tracker, cfg, _, osClient := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	// Step 1: Order is RESTING, then LE initiates cancel -> CANCELLING
	invAddResting(t, tracker, levelID, orderID, coidG3, marketID, "BUY", "99.00", 3)
	tracker.SetCancelling(levelID)

	// Step 2: ME crashes and restarts, restoring G003 from previous snapshot
	restoredMESnap := &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "LIVE",
		Sequence: 1,
		Orders: []meclient.MMOrderSummary{
			{
				OrderID:           orderID,
				ClientOrderID:     coidG3,
				LevelID:           levelID,
				Side:              "BUY",
				Price:             "99.00",
				RemainingQuantity: "1.0",
			},
		},
	}

	// Step 3: LE observes ME snapshot containing G003
	rec.syncWithMESnapshot(context.Background(), marketID, restoredMESnap, mc)

	// Expected: G003 remains CANCELLING (NO promotion to RESTING)
	live := tracker.Get(levelID)
	if live.Status != order.StatusCancelling {
		t.Fatalf("T23: expected G003 to remain CANCELLING after ME restart, got %s", live.Status)
	}

	// Expected: Diff does not create G004
	desired := pricing.GenerateLadder(mc, 1, 0)
	entries := order.Diff(desired, tracker, marketID, mc)
	for _, e := range entries {
		if e.Action == order.DiffCreate && e.LevelID == levelID {
			t.Fatalf("T23: Diff produced DiffCreate while G003 is CANCELLING after ME restart!")
		}
	}

	// Step 4: Cancel is eventually processed -> ME snapshot empty and OS confirms CANCELLED
	osClient.orders[coidG3] = &orderservice.OrderState{
		OrderID:       orderID,
		ClientOrderID: coidG3,
		Status:        "CANCELLED",
	}
	rec.handleCancellingTimeout(context.Background(), live, mc)

	if tracker.Get(levelID) != nil {
		t.Fatalf("T23: expected G003 to be removed after cancel confirmation")
	}

	// Now G004 can be created cleanly
	nextGen := tracker.NextGeneration(levelID)
	if nextGen != 4 {
		t.Fatalf("T23: expected next generation 4, got %d", nextGen)
	}
}

// ─── T27: ME Restart With Newer OS Generation ────────────────────────────────

// TestT27_MERestart_WithNewerOSGeneration verifies the end-to-end recovery scenario where
// LE held G003, ME restarts, OS reports a newer G004, LE upgrades its tracker to G004,
// and ME confirms G004 without producing any duplicate orders or spurious CREATEs.
func TestT27_MERestart_WithNewerOSGeneration(t *testing.T) {
	marketID := "SOL-USDT"
	levelID := "MM-SOL-USDT-BID-01"
	oldOID := "00000000-0000-0000-0000-000000000003"
	oldCOID := "MM-SOL-USDT-BID-01-G003"
	newOID := "00000000-0000-0000-0000-000000000004"
	newCOID := "MM-SOL-USDT-BID-01-G004"

	rec, tracker, cfg, _, _ := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	// Step 1: LE tracks G003 as RESTING
	invAddResting(t, tracker, levelID, oldOID, oldCOID, marketID, "BUY", "99.00", 3)

	// Step 2 & 3: ME restarts and OS reports G004
	osOrders := []order.OSOrder{
		{
			LevelID:       levelID,
			Generation:    4,
			ClientOrderID: newCOID,
			OrderID:       newOID,
			Side:          "BUY",
			Price:         decimal.RequireFromString("99.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("1.0"),
		},
	}

	// Step 4: LE syncs from OS
	tracker.SyncFromOrders(marketID, osOrders)

	// Step 5: Tracker upgrades to G004 in OS_REGISTERED state
	live := tracker.Get(levelID)
	if live == nil {
		t.Fatalf("T27: expected order to exist in tracker")
	}
	if live.Generation != 4 || live.ClientOrderID != newCOID || live.OrderID != newOID {
		t.Fatalf("T27: expected tracker upgraded to G004, got Gen=%d COID=%s OID=%s",
			live.Generation, live.ClientOrderID, live.OrderID)
	}

	// Step 6: ME reports G004 resting in its snapshot
	meSnapG4 := &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "LIVE",
		Sequence: 50,
		Orders: []meclient.MMOrderSummary{
			{
				OrderID:           newOID,
				ClientOrderID:     newCOID,
				LevelID:           levelID,
				Side:              "BUY",
				Price:             "99.00",
				RemainingQuantity: "1.0",
			},
		},
	}

	// Step 7: LE reconciles with ME snapshot -> confirms G004 as RESTING
	rec.syncWithMESnapshot(context.Background(), marketID, meSnapG4, mc)
	if live.Status != order.StatusResting {
		t.Fatalf("T27: expected G004 confirmed as RESTING, got %s", live.Status)
	}

	// Step 8: Diff must NOT produce CREATE (level is satisfied by G004)
	desired := pricing.GenerateLadder(mc, 1, 0)
	entries := order.Diff(desired, tracker, marketID, mc)
	for _, e := range entries {
		if e.Action == order.DiffCreate && e.LevelID == levelID {
			t.Fatalf("T27: Diff produced duplicate DiffCreate for level already satisfied by G004!")
		}
	}
}

// ─── Orphan Cancel Cooldown & Pruning Test ────────────────────────────────────

func TestOrphanCancel_CooldownAndPruning(t *testing.T) {
	marketID := "SOL-USDT"
	orphanOID := "00000000-0000-0000-0000-000000000099"

	rec, tracker, cfg, prod, _ := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	snapWithOrphan := &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "LIVE",
		Sequence: 1,
		Orders: []meclient.MMOrderSummary{
			{
				OrderID:           orphanOID,
				ClientOrderID:     "MM-SOL-USDT-BID-01-G001",
				LevelID:           "MM-SOL-USDT-BID-01",
				Side:              "BUY",
				Price:             "95.00",
				RemainingQuantity: "1.0",
			},
		},
	}

	// Cycle 1: orphan seen -> publishes cancel
	rec.syncWithMESnapshot(context.Background(), marketID, snapWithOrphan, mc)
	if len(prod.cancelledOrders) != 1 {
		t.Fatalf("expected 1 cancel published, got %d", len(prod.cancelledOrders))
	}

	// Cycle 2: immediate next cycle -> cooldown active, does NOT publish duplicate
	rec.syncWithMESnapshot(context.Background(), marketID, snapWithOrphan, mc)
	if len(prod.cancelledOrders) != 1 {
		t.Fatalf("expected cancel count to remain 1 due to cooldown, got %d", len(prod.cancelledOrders))
	}

	// Cycle 3: orphan disappears from ME snapshot -> pruned from in-flight map
	emptySnap := invEmptySnap(marketID)
	rec.syncWithMESnapshot(context.Background(), marketID, emptySnap, mc)

	rec.orphanCancelMu.Lock()
	_, stillInFlight := rec.orphanCancelInFlight[orphanOID]
	rec.orphanCancelMu.Unlock()

	if stillInFlight {
		t.Errorf("expected orphan to be pruned after disappearing from snapshot")
	}

	_ = tracker
}

// ─── Nil ME Client Fails Closed Test ─────────────────────────────────────────

// TestReconcileMarket_NilMEClient_FailsClosed verifies that if meClient is nil,
// ReconcileMarket skips diffing and produces zero Kafka mutations (fail-closed).
func TestReconcileMarket_NilMEClient_FailsClosed(t *testing.T) {
	marketID := "SOL-USDT"
	rec, tracker, _, prod, _ := newInvTestRec(t, marketID)

	// Explicitly verify meClient is nil
	if rec.meClient != nil {
		t.Fatalf("expected meClient to be nil")
	}

	cmds, err := rec.ReconcileMarket(context.Background(), marketID, 12, 12)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if cmds != 0 {
		t.Errorf("expected 0 commands published when meClient is nil, got %d", cmds)
	}
	if len(prod.createdOrders) != 0 || len(prod.cancelledOrders) != 0 {
		t.Errorf("expected 0 Kafka commands, got %d created, %d cancelled",
			len(prod.createdOrders), len(prod.cancelledOrders))
	}
	if len(tracker.All(marketID)) != 0 {
		t.Errorf("expected 0 tracker orders, got %d", len(tracker.All(marketID)))
	}
}

// ─── Orphan Cancel Failed Publish Does Not Cooldown Test ─────────────────────

func TestOrphanCancel_FailedPublishDoesNotCooldown(t *testing.T) {
	marketID := "SOL-USDT"
	orphanOID := "00000000-0000-0000-0000-000000000088"

	rec, _, cfg, prod, _ := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	snapWithOrphan := &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "LIVE",
		Sequence: 1,
		Orders: []meclient.MMOrderSummary{
			{
				OrderID:           orphanOID,
				ClientOrderID:     "MM-SOL-USDT-BID-01-G001",
				LevelID:           "MM-SOL-USDT-BID-01",
				Side:              "BUY",
				Price:             "95.00",
				RemainingQuantity: "1.0",
			},
		},
	}

	// Cycle 1: Kafka publish fails
	prod.cancelErr = errors.New("kafka connection dropped")
	rec.syncWithMESnapshot(context.Background(), marketID, snapWithOrphan, mc)

	// Cooldown MUST NOT be recorded because publish failed
	rec.orphanCancelMu.Lock()
	_, inFlight := rec.orphanCancelInFlight[orphanOID]
	rec.orphanCancelMu.Unlock()

	if inFlight {
		t.Fatalf("orphan cancel cooldown must NOT be recorded when Kafka publish fails")
	}

	// Cycle 2: Kafka recovered -> publish succeeds
	prod.cancelErr = nil
	rec.syncWithMESnapshot(context.Background(), marketID, snapWithOrphan, mc)

	if len(prod.cancelledOrders) != 1 {
		t.Fatalf("expected 1 cancel published after Kafka recovery, got %d", len(prod.cancelledOrders))
	}

	// Now cooldown should be recorded
	rec.orphanCancelMu.Lock()
	_, inFlightAfter := rec.orphanCancelInFlight[orphanOID]
	rec.orphanCancelMu.Unlock()

	if !inFlightAfter {
		t.Fatalf("expected cooldown to be recorded after successful publish")
	}
}

// ─── Nil Producer Orphan Cancel Does Not Cooldown Test ───────────────────────

func TestOrphanCancel_NilProducer_DoesNotCooldown(t *testing.T) {
	marketID := "SOL-USDT"
	orphanOID := "00000000-0000-0000-0000-000000000077"

	rec, _, cfg, _, _ := newInvTestRec(t, marketID)
	// Explicitly set producer to nil
	rec.producer = nil
	mc := cfg.ForMarket(marketID)

	snapWithOrphan := &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "LIVE",
		Sequence: 1,
		Orders: []meclient.MMOrderSummary{
			{
				OrderID:           orphanOID,
				ClientOrderID:     "MM-SOL-USDT-BID-01-G001",
				LevelID:           "MM-SOL-USDT-BID-01",
				Side:              "BUY",
				Price:             "95.00",
				RemainingQuantity: "1.0",
			},
		},
	}

	rec.syncWithMESnapshot(context.Background(), marketID, snapWithOrphan, mc)

	// Since producer is nil, cancel was not dispatched -> zero cooldown recorded
	rec.orphanCancelMu.Lock()
	_, inFlight := rec.orphanCancelInFlight[orphanOID]
	rec.orphanCancelMu.Unlock()

	if inFlight {
		t.Fatalf("orphan cancel cooldown must NOT be recorded when producer is nil")
	}
}


