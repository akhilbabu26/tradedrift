package reconciler_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/orderservice"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

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

	rec.SyncWithMESnapshot(context.Background(), marketID, snap, mc)

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
	rec.SyncWithMESnapshot(context.Background(), marketID, snap, mc) // cycle 1 (hysteresis)
	rec.SyncWithMESnapshot(context.Background(), marketID, snap, mc) // cycle 2 → triggers handleMissingRestingOrder

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
	rec.SyncWithMESnapshot(context.Background(), marketID, snapWithOrphan, mc)
	if len(prod.cancelledOrders) != 1 || prod.cancelledOrders[0] != staleOrderID {
		t.Fatalf("T15: expected cancel published for orphan %s, got %v", staleOrderID, prod.cancelledOrders)
	}

	// Cycle 2: Next snapshot confirms orphan is removed from ME
	emptySnap := invEmptySnap(marketID)
	rec.SyncWithMESnapshot(context.Background(), marketID, emptySnap, mc)

	// Slot is now completely free; Diff() can safely create next generation
	desired := []pricing.PriceLevel{
		{LevelID: levelID, MarketID: marketID, Side: "BUY", Price: decimal.RequireFromString("96400.00"), Quantity: decimal.RequireFromString("1.0")},
	}
	entries := order.Diff(desired, tracker, marketID, mc)
	if len(entries) != 1 || entries[0].Action != order.DiffCreate {
		t.Fatalf("T15: expected DiffCreate once orphan confirmed gone, got %v", entries)
	}
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

	rec.SyncWithMESnapshot(context.Background(), marketID, snap, mc)

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
	rec.SyncWithMESnapshot(context.Background(), marketID, emptySnap, mc)
	if tracker.Get(levelID) == nil {
		t.Fatal("T20: order must not be removed on cycle 1")
	}

	// Cycle 2: missingCycles reaches 2 -> handleMissingRestingOrder executes -> check 1: notFoundCount = 1
	rec.SyncWithMESnapshot(context.Background(), marketID, emptySnap, mc)
	if tracker.Get(levelID) == nil {
		t.Fatal("T20: order must not be removed after only 1 OS NOT_FOUND check")
	}
	if rec.GetNotFoundCount(levelID) != 1 {
		t.Fatalf("T20: expected notFoundCount=1, got %d", rec.GetNotFoundCount(levelID))
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
	rec.SyncWithMESnapshot(context.Background(), marketID, emptySnap, mc)
	if tracker.Get(levelID) == nil {
		t.Fatal("T20: order must not be removed on cycle 3")
	}

	// Cycle 4: missingCycles = 2 -> handleMissingRestingOrder executes -> check 2: notFoundCount = 2 -> removed!
	rec.SyncWithMESnapshot(context.Background(), marketID, emptySnap, mc)
	if tracker.Get(levelID) != nil {
		t.Fatal("T20: order should be removed after 2 consecutive missing cycles and 2 OS not found checks")
	}
	if rec.GetNotFoundCount(levelID) != 0 {
		t.Errorf("T20: notFoundCount must be deleted on removal, got %d", rec.GetNotFoundCount(levelID))
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
	rec.SyncWithMESnapshot(context.Background(), marketID, snapWithG3, mc)

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
	rec.HandleCancellingTimeout(context.Background(), tracker.Get(levelID), mc)

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
	rec.SyncWithMESnapshot(context.Background(), marketID, snapWithOrphan, mc)
	if len(prod.cancelledOrders) != 1 {
		t.Fatalf("expected 1 cancel published, got %d", len(prod.cancelledOrders))
	}

	// Cycle 2: immediate next cycle -> cooldown active, does NOT publish duplicate
	rec.SyncWithMESnapshot(context.Background(), marketID, snapWithOrphan, mc)
	if len(prod.cancelledOrders) != 1 {
		t.Fatalf("expected cancel count to remain 1 due to cooldown, got %d", len(prod.cancelledOrders))
	}

	// Cycle 3: orphan disappears from ME snapshot -> pruned from in-flight map
	emptySnap := invEmptySnap(marketID)
	rec.SyncWithMESnapshot(context.Background(), marketID, emptySnap, mc)

	if rec.HasOrphanCancelInFlight(orphanOID) {
		t.Errorf("expected orphan to be pruned after disappearing from snapshot")
	}

	_ = tracker
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
	rec.SyncWithMESnapshot(context.Background(), marketID, snapWithOrphan, mc)

	// Cooldown MUST NOT be recorded because publish failed
	if rec.HasOrphanCancelInFlight(orphanOID) {
		t.Fatalf("orphan cancel cooldown must NOT be recorded when Kafka publish fails")
	}

	// Cycle 2: Kafka recovered -> publish succeeds
	prod.cancelErr = nil
	rec.SyncWithMESnapshot(context.Background(), marketID, snapWithOrphan, mc)

	if len(prod.cancelledOrders) != 1 {
		t.Fatalf("expected 1 cancel published after Kafka recovery, got %d", len(prod.cancelledOrders))
	}

	// Now cooldown should be recorded
	if !rec.HasOrphanCancelInFlight(orphanOID) {
		t.Fatalf("expected cooldown to be recorded after successful publish")
	}
}

// ─── Nil Producer Orphan Cancel Does Not Cooldown Test ───────────────────────

func TestOrphanCancel_NilProducer_DoesNotCooldown(t *testing.T) {
	marketID := "SOL-USDT"
	orphanOID := "00000000-0000-0000-0000-000000000077"

	rec, _, cfg, _, _ := newInvTestRec(t, marketID)
	// Explicitly set producer to nil
	rec.SetProducer(nil)
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

	rec.SyncWithMESnapshot(context.Background(), marketID, snapWithOrphan, mc)

	// Since producer is nil, cancel was not dispatched -> zero cooldown recorded
	if rec.HasOrphanCancelInFlight(orphanOID) {
		t.Fatalf("orphan cancel cooldown must NOT be recorded when producer is nil")
	}
}
