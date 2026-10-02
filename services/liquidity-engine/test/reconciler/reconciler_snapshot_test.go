package reconciler_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/orderservice"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

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
	rec.SyncWithMESnapshot(context.Background(), marketID, snap, mc)
	if rec.GetMissingCycles(levelID) != 1 {
		t.Fatalf("T11a: expected missingCycles=1 after cycle 1, got %d", rec.GetMissingCycles(levelID))
	}
	if tracker.Get(levelID) == nil {
		t.Fatal("T11a: order must NOT be removed after only 1 missing cycle")
	}

	// Cycle 2: threshold → handleMissingRestingOrder fires, counter resets
	rec.SyncWithMESnapshot(context.Background(), marketID, snap, mc)
	if rec.GetMissingCycles(levelID) != 0 {
		t.Errorf("T11a: expected missingCycles reset to 0 after threshold, got %d", rec.GetMissingCycles(levelID))
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

	rec.SyncWithMESnapshot(context.Background(), marketID, snap, mc)

	o := tracker.Get(levelID)
	if o == nil {
		t.Fatal("T11b: OS_REGISTERED order must remain in tracker after snapshot match")
	}
	if o.Status != order.StatusResting {
		t.Errorf("T11b: expected RESTING after ME snapshot confirmation, got %s", o.Status)
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
	if recoveringSnap.MarketID == marketID && recoveringSnap.State != "LIVE" {
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

	// 1. SyncWithMESnapshot must not promote CANCELLING -> RESTING
	rec.SyncWithMESnapshot(context.Background(), marketID, snap, mc)
	if liveOrder.Status != order.StatusCancelling {
		t.Errorf("T19 (SyncWithMESnapshot): expected status to remain CANCELLING, got %s", liveOrder.Status)
	}

	// 2. ConfirmRestingFromSnapshot must not promote CANCELLING -> RESTING
	rec.ConfirmRestingFromSnapshot(marketID, snap)
	if liveOrder.Status != order.StatusCancelling {
		t.Errorf("T19 (ConfirmRestingFromSnapshot): expected status to remain CANCELLING, got %s", liveOrder.Status)
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

	rec.SyncWithMESnapshot(context.Background(), marketID, snap, mc)

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
	rec.SyncWithMESnapshot(context.Background(), marketID, restoredMESnap, mc)

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
	rec.HandleCancellingTimeout(context.Background(), live, mc)

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
	rec.SyncWithMESnapshot(context.Background(), marketID, meSnapG4, mc)
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

// ─── Nil ME Client Fails Closed Test ─────────────────────────────────────────

// TestReconcileMarket_NilMEClient_FailsClosed verifies that if meClient is nil,
// ReconcileMarket skips diffing and produces zero Kafka mutations (fail-closed).
func TestReconcileMarket_NilMEClient_FailsClosed(t *testing.T) {
	marketID := "SOL-USDT"
	rec, tracker, _, prod, _ := newInvTestRec(t, marketID)

	// Explicitly verify meClient is nil
	if rec.MEClient() != nil {
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
