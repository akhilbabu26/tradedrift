package market_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/orderbook"
)

func newTestEngine(marketID string) *market.MarketEngine {
	config := market.MarketConfig{
		MarketID: marketID,
		TickSize: decimal.RequireFromString("0.01"),
		LotSize:  decimal.RequireFromString("0.001"),
	}
	engine := market.NewMarketEngine(config)
	engine.SetLive()
	return engine
}

// TestSequence_ComprehensiveLifecycle covers the 9 authoritative sequence rules:
//
//	Test 1: Resting LIMIT order insertion -> Sequence = 1
//	Test 2: Order cancel of active order -> Sequence = 2
//	Test 3: Validation reject (tick/lot error) -> Sequence UNCHANGED (2)
//	Test 4: Non-existent cancel (unknown ID) -> Sequence UNCHANGED (2)
//	Test 5: Single match (1 buy matches 1 sell) -> Fill.Seq = 3, Depth.Seq = 3
//	Test 6: Multi-level sweep (1 buy sweeps 3 sells + remainder rests) -> Fill1=4, Fill2=5, Fill3=6, Remainder=7, Depth.Seq=7
//	Test 7: Pure read idempotency (GetDepth called multiple times) -> Sequence UNCHANGED (7)
//	Test 8: Non-zero baseline recovery (starts at 10,000 -> 5 mutations = 10,005 -> next live = 10,006)
//	Test 9: Strict monotonicity across all emitted events
func TestSequence_ComprehensiveLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := newTestEngine("BTC-USDT")
	go engine.Run(ctx)

	var emittedSequences []uint64

	// ─── Test 1: Resting LIMIT Order ──────────────────────────────────────────
	order1ID := uuid.New()
	user1ID := uuid.New()
	engine.InputQueue <- market.InputEvent{
		Type: market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   order1ID,
			UserID:    user1ID,
			MarketID:  "BTC-USDT",
			Side:      orderbook.SideSell,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("65000.00"),
			Quantity:  decimal.RequireFromString("1.000"),
		},
		Topic:  "orders.submitted",
		Offset: 1,
	}

	res1 := <-engine.OutputQueue
	if len(res1.Fills) != 0 {
		t.Fatalf("expected 0 fills on resting order, got %d", len(res1.Fills))
	}
	if res1.DepthSnapshot.Sequence != 1 {
		t.Fatalf("Test 1: expected DepthSnapshot.Sequence=1, got %d", res1.DepthSnapshot.Sequence)
	}
	if engine.GetSequence() != 1 {
		t.Fatalf("Test 1: expected engine.GetSequence()=1, got %d", engine.GetSequence())
	}
	emittedSequences = append(emittedSequences, res1.DepthSnapshot.Sequence)

	// ─── Test 2: Order Cancel ────────────────────────────────────────────────
	engine.InputQueue <- market.InputEvent{
		Type: market.EventOrderCancel,
		OrderCancel: &market.OrderCancelPayload{
			OrderID:  order1ID,
			UserID:   user1ID,
			MarketID: "BTC-USDT",
		},
		Topic:  "orders.cancel-requested",
		Offset: 2,
	}

	res2 := <-engine.OutputQueue
	if res2.CancelResult == nil {
		t.Fatal("Test 2: expected non-nil CancelResult")
	}
	if res2.DepthSnapshot.Sequence != 2 {
		t.Fatalf("Test 2: expected DepthSnapshot.Sequence=2, got %d", res2.DepthSnapshot.Sequence)
	}
	if engine.GetSequence() != 2 {
		t.Fatalf("Test 2: expected engine.GetSequence()=2, got %d", engine.GetSequence())
	}
	emittedSequences = append(emittedSequences, res2.DepthSnapshot.Sequence)

	// ─── Test 3: Validation Reject (Tick Size Violation) ─────────────────────
	invalidOrderID := uuid.New()
	engine.InputQueue <- market.InputEvent{
		Type: market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   invalidOrderID,
			UserID:    uuid.New(),
			MarketID:  "BTC-USDT",
			Side:      orderbook.SideBuy,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("65000.0055"), // invalid tick
			Quantity:  decimal.RequireFromString("1.000"),
		},
		Topic:  "orders.submitted",
		Offset: 3,
	}

	res3 := <-engine.OutputQueue
	if res3.CancelResult == nil || res3.CancelResult.Reason != "invalid_order_parameters" {
		t.Fatal("Test 3: expected invalid_order_parameters cancel")
	}
	// Critical rule: validation reject must NOT increment sequence
	if res3.DepthSnapshot.Sequence != 2 {
		t.Fatalf("Test 3: validation reject must not advance sequence: expected 2, got %d", res3.DepthSnapshot.Sequence)
	}
	if engine.GetSequence() != 2 {
		t.Fatalf("Test 3: engine sequence must remain 2, got %d", engine.GetSequence())
	}

	// ─── Test 4: Non-Existent Cancel (No-Op) ──────────────────────────────────
	engine.InputQueue <- market.InputEvent{
		Type: market.EventOrderCancel,
		OrderCancel: &market.OrderCancelPayload{
			OrderID:  uuid.New(), // unknown ID
			UserID:   uuid.New(),
			MarketID: "BTC-USDT",
		},
		Topic:  "orders.cancel-requested",
		Offset: 4,
	}

	res4 := <-engine.OutputQueue
	// Cancel on non-existent order returns ALREADY_ABSENT (never nil)
	if res4.CancelResult == nil {
		t.Fatal("Test 4: expected non-nil CancelResult with ALREADY_ABSENT for non-existent order")
	}
	if res4.CancelResult.CancelStatus != orderbook.CancelStatusAlreadyAbsent {
		t.Fatalf("Test 4: expected ALREADY_ABSENT, got %q", res4.CancelResult.CancelStatus)
	}
	// Critical rule: ALREADY_ABSENT cancel must NOT increment sequence
	if res4.DepthSnapshot.Sequence != 2 {
		t.Fatalf("Test 4: non-existent cancel must not advance sequence: expected 2, got %d", res4.DepthSnapshot.Sequence)
	}
	if engine.GetSequence() != 2 {
		t.Fatalf("Test 4: engine sequence must remain 2, got %d", engine.GetSequence())
	}

	// ─── Test 5: Single Match (1 Maker, 1 Taker) ──────────────────────────────
	// Place resting ask at 64000
	sellOrder1 := uuid.New()
	engine.InputQueue <- market.InputEvent{
		Type: market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   sellOrder1,
			UserID:    uuid.New(),
			MarketID:  "BTC-USDT",
			Side:      orderbook.SideSell,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("64000.00"),
			Quantity:  decimal.RequireFromString("1.000"),
		},
		Topic:  "orders.submitted",
		Offset: 5,
	}
	res5Maker := <-engine.OutputQueue
	if res5Maker.DepthSnapshot.Sequence != 3 {
		t.Fatalf("Test 5 maker: expected Sequence=3, got %d", res5Maker.DepthSnapshot.Sequence)
	}

	// Incoming matching buy at 64000
	buyOrder1 := uuid.New()
	engine.InputQueue <- market.InputEvent{
		Type: market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   buyOrder1,
			UserID:    uuid.New(),
			MarketID:  "BTC-USDT",
			Side:      orderbook.SideBuy,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("64000.00"),
			Quantity:  decimal.RequireFromString("1.000"),
		},
		Topic:  "orders.submitted",
		Offset: 6,
	}
	res5Taker := <-engine.OutputQueue
	if len(res5Taker.Fills) != 1 {
		t.Fatalf("Test 5: expected 1 fill, got %d", len(res5Taker.Fills))
	}
	if res5Taker.Fills[0].Sequence != 4 {
		t.Fatalf("Test 5: expected Fill.Sequence=4, got %d", res5Taker.Fills[0].Sequence)
	}
	if res5Taker.DepthSnapshot.Sequence != 4 {
		t.Fatalf("Test 5: expected DepthSnapshot.Sequence=4, got %d", res5Taker.DepthSnapshot.Sequence)
	}
	emittedSequences = append(emittedSequences, res5Taker.Fills[0].Sequence)

	// ─── Test 6: Multi-Level Sweep + Remainder Insertion ─────────────────────
	// Insert 3 resting sell levels:
	// Sell A @ 65000 (0.500) -> Seq 5
	// Sell B @ 65100 (0.500) -> Seq 6
	// Sell C @ 65200 (0.500) -> Seq 7
	for i, priceStr := range []string{"65000.00", "65100.00", "65200.00"} {
		engine.InputQueue <- market.InputEvent{
			Type: market.EventOrderCreated,
			OrderCreated: &market.OrderCreatedPayload{
				OrderID:   uuid.New(),
				UserID:    uuid.New(),
				MarketID:  "BTC-USDT",
				Side:      orderbook.SideSell,
				OrderType: orderbook.OrderTypeLimit,
				Price:     decimal.RequireFromString(priceStr),
				Quantity:  decimal.RequireFromString("0.500"),
			},
			Topic:  "orders.submitted",
			Offset: int64(7 + i),
		}
		res := <-engine.OutputQueue
		expectedSeq := uint64(5 + i)
		if res.DepthSnapshot.Sequence != expectedSeq {
			t.Fatalf("Test 6 setup level %d: expected Sequence=%d, got %d", i, expectedSeq, res.DepthSnapshot.Sequence)
		}
	}

	// Incoming BUY order sweeps all 3 levels (1.500 BTC total) + 0.500 remainder rests at 65300
	sweepOrder := uuid.New()
	engine.InputQueue <- market.InputEvent{
		Type: market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   sweepOrder,
			UserID:    uuid.New(),
			MarketID:  "BTC-USDT",
			Side:      orderbook.SideBuy,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("65300.00"),
			Quantity:  decimal.RequireFromString("2.000"), // 1.5 filled across 3 levels + 0.5 rests
		},
		Topic:  "orders.submitted",
		Offset: 10,
	}

	resSweep := <-engine.OutputQueue
	if len(resSweep.Fills) != 3 {
		t.Fatalf("Test 6: expected 3 fills, got %d", len(resSweep.Fills))
	}
	// Verify per-fill sequence progression:
	// Fill 1 -> Seq 8
	// Fill 2 -> Seq 9
	// Fill 3 -> Seq 10
	// Resting LIMIT insertion -> Seq 11
	// Final DepthSnapshot -> Seq 11
	if resSweep.Fills[0].Sequence != 8 {
		t.Fatalf("Test 6: Fill 0 expected Sequence=8, got %d", resSweep.Fills[0].Sequence)
	}
	if resSweep.Fills[1].Sequence != 9 {
		t.Fatalf("Test 6: Fill 1 expected Sequence=9, got %d", resSweep.Fills[1].Sequence)
	}
	if resSweep.Fills[2].Sequence != 10 {
		t.Fatalf("Test 6: Fill 2 expected Sequence=10, got %d", resSweep.Fills[2].Sequence)
	}
	if resSweep.DepthSnapshot.Sequence != 11 {
		t.Fatalf("Test 6: DepthSnapshot expected Sequence=11, got %d", resSweep.DepthSnapshot.Sequence)
	}
	if engine.GetSequence() != 11 {
		t.Fatalf("Test 6: engine sequence expected 11, got %d", engine.GetSequence())
	}

	// ─── Test 7: Pure Read Idempotency ───────────────────────────────────────
	for i := 0; i < 5; i++ {
		snap := engine.GetDepth(20)
		if snap.Sequence != 11 {
			t.Fatalf("Test 7: GetDepth() must not mutate sequence: expected 11, got %d", snap.Sequence)
		}
		if engine.GetSequence() != 11 {
			t.Fatalf("Test 7: engine sequence must remain 11, got %d", engine.GetSequence())
		}
	}
}

// TestSequence_NonZeroBaselineRecovery verifies that restarting an engine
// with a non-zero historical sequence (e.g. 10,000) does not reset to 0
// and subsequent mutations advance strictly from 10,001.
func TestSequence_NonZeroBaselineRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := newTestEngine("ETH-USDT")

	// Set baseline historical sequence (simulating restored event replay)
	engine.SetSequence(10000)
	if engine.GetSequence() != 10000 {
		t.Fatalf("expected baseline sequence=10000, got %d", engine.GetSequence())
	}

	go engine.Run(ctx)

	// New mutation: Place resting LIMIT order
	engine.InputQueue <- market.InputEvent{
		Type: market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   uuid.New(),
			UserID:    uuid.New(),
			MarketID:  "ETH-USDT",
			Side:      orderbook.SideBuy,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("3500.00"),
			Quantity:  decimal.RequireFromString("1.000"),
		},
		Topic:  "orders.submitted",
		Offset: 501,
	}

	res := <-engine.OutputQueue
	if res.DepthSnapshot.Sequence != 10001 {
		t.Fatalf("expected Sequence=10001 after mutation, got %d", res.DepthSnapshot.Sequence)
	}
	if engine.GetSequence() != 10001 {
		t.Fatalf("expected engine.GetSequence()=10001, got %d", engine.GetSequence())
	}

	// Verify depth snapshot matches
	snap := engine.GetDepth(20)
	if snap.Sequence != 10001 {
		t.Fatalf("expected GetDepth() sequence=10001, got %d", snap.Sequence)
	}
}

// ─── Unified orders.commands Tests ────────────────────────────────────────────

func TestOrdersCommands_CreateThenCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := newTestEngine("BTC-USDT")
	go engine.Run(ctx)

	orderID := uuid.New()
	userID := uuid.New()

	// 1. Create resting limit order
	engine.InputQueue <- market.InputEvent{
		EventID: uuid.New(),
		Type:    market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   orderID,
			UserID:    userID,
			MarketID:  "BTC-USDT",
			Side:      orderbook.SideBuy,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("50000.00"),
			Quantity:  decimal.RequireFromString("1.000"),
		},
		Topic:  "orders.commands",
		Offset: 100,
	}

	res1 := <-engine.OutputQueue
	if res1.DepthSnapshot.Sequence != 1 {
		t.Fatalf("expected Sequence=1, got %d", res1.DepthSnapshot.Sequence)
	}
	if len(res1.DepthSnapshot.Bids) != 1 {
		t.Fatalf("expected 1 bid level on book, got %d", len(res1.DepthSnapshot.Bids))
	}

	// 2. Cancel the order
	engine.InputQueue <- market.InputEvent{
		EventID: uuid.New(),
		Type:    market.EventOrderCancel,
		OrderCancel: &market.OrderCancelPayload{
			OrderID:  orderID,
			UserID:   userID,
			MarketID: "BTC-USDT",
		},
		Topic:  "orders.commands",
		Offset: 101,
	}

	res2 := <-engine.OutputQueue
	if res2.CancelResult == nil {
		t.Fatal("expected non-nil CancelResult")
	}
	if res2.DepthSnapshot.Sequence != 2 {
		t.Fatalf("expected Sequence=2, got %d", res2.DepthSnapshot.Sequence)
	}
	if len(res2.DepthSnapshot.Bids) != 0 {
		t.Fatalf("expected 0 bid levels on book after cancel, got %d", len(res2.DepthSnapshot.Bids))
	}
}

func TestOrdersCommands_CreateMatchThenCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := newTestEngine("BTC-USDT")
	go engine.Run(ctx)

	makerID := uuid.New()
	takerID := uuid.New()

	// 1. Resting Maker Sell
	engine.InputQueue <- market.InputEvent{
		EventID: uuid.New(),
		Type:    market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   makerID,
			UserID:    uuid.New(),
			MarketID:  "BTC-USDT",
			Side:      orderbook.SideSell,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("50000.00"),
			Quantity:  decimal.RequireFromString("1.000"),
		},
		Topic:  "orders.commands",
		Offset: 100,
	}
	<-engine.OutputQueue // Seq 1

	// 2. Taker Buy Full Match
	engine.InputQueue <- market.InputEvent{
		EventID: uuid.New(),
		Type:    market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   takerID,
			UserID:    uuid.New(),
			MarketID:  "BTC-USDT",
			Side:      orderbook.SideBuy,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("50000.00"),
			Quantity:  decimal.RequireFromString("1.000"),
		},
		Topic:  "orders.commands",
		Offset: 101,
	}
	resMatch := <-engine.OutputQueue
	if len(resMatch.Fills) != 1 {
		t.Fatalf("expected 1 fill, got %d", len(resMatch.Fills))
	}
	if resMatch.DepthSnapshot.Sequence != 2 {
		t.Fatalf("expected Sequence=2, got %d", resMatch.DepthSnapshot.Sequence)
	}

	// 3. Cancel Taker (Already filled -> No-op)
	engine.InputQueue <- market.InputEvent{
		EventID: uuid.New(),
		Type:    market.EventOrderCancel,
		OrderCancel: &market.OrderCancelPayload{
			OrderID:  takerID,
			UserID:   uuid.New(),
			MarketID: "BTC-USDT",
		},
		Topic:  "orders.commands",
		Offset: 102,
	}
	resCancel := <-engine.OutputQueue
	// Cancel on already-filled order returns ALREADY_ABSENT (never nil)
	if resCancel.CancelResult == nil {
		t.Fatal("expected non-nil CancelResult with ALREADY_ABSENT for already filled order")
	}
	if resCancel.CancelResult.CancelStatus != orderbook.CancelStatusAlreadyAbsent {
		t.Fatalf("expected ALREADY_ABSENT for filled order cancel, got %q", resCancel.CancelResult.CancelStatus)
	}
	// Sequence must remain 2
	if resCancel.DepthSnapshot.Sequence != 2 {
		t.Fatalf("expected Sequence=2 for no-op cancel, got %d", resCancel.DepthSnapshot.Sequence)
	}
}

func TestOrdersCommands_CreatePartialFillThenCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := newTestEngine("BTC-USDT")
	go engine.Run(ctx)

	makerID := uuid.New()
	takerID := uuid.New()

	// 1. Maker Sell 1.0 BTC @ 50,000
	engine.InputQueue <- market.InputEvent{
		EventID: uuid.New(),
		Type:    market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   makerID,
			UserID:    uuid.New(),
			MarketID:  "BTC-USDT",
			Side:      orderbook.SideSell,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("50000.00"),
			Quantity:  decimal.RequireFromString("1.000"),
		},
		Topic:  "orders.commands",
		Offset: 100,
	}
	<-engine.OutputQueue // Seq 1

	// 2. Taker Buy 1.5 BTC @ 50,000 (1.0 fills, 0.5 rests)
	engine.InputQueue <- market.InputEvent{
		EventID: uuid.New(),
		Type:    market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   takerID,
			UserID:    uuid.New(),
			MarketID:  "BTC-USDT",
			Side:      orderbook.SideBuy,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("50000.00"),
			Quantity:  decimal.RequireFromString("1.500"),
		},
		Topic:  "orders.commands",
		Offset: 101,
	}
	resMatch := <-engine.OutputQueue
	if len(resMatch.Fills) != 1 {
		t.Fatalf("expected 1 fill, got %d", len(resMatch.Fills))
	}
	// Fill = Seq 2, Remainder = Seq 3
	if resMatch.DepthSnapshot.Sequence != 3 {
		t.Fatalf("expected Sequence=3 after partial fill + remainder, got %d", resMatch.DepthSnapshot.Sequence)
	}

	// 3. Cancel Taker (Removes remaining 0.5 BTC)
	engine.InputQueue <- market.InputEvent{
		EventID: uuid.New(),
		Type:    market.EventOrderCancel,
		OrderCancel: &market.OrderCancelPayload{
			OrderID:  takerID,
			UserID:   uuid.New(),
			MarketID: "BTC-USDT",
		},
		Topic:  "orders.commands",
		Offset: 102,
	}
	resCancel := <-engine.OutputQueue
	if resCancel.CancelResult == nil {
		t.Fatal("expected non-nil CancelResult for partially filled resting order")
	}
	if resCancel.DepthSnapshot.Sequence != 4 {
		t.Fatalf("expected Sequence=4 after cancel, got %d", resCancel.DepthSnapshot.Sequence)
	}
	if len(resCancel.DepthSnapshot.Bids) != 0 {
		t.Fatalf("expected 0 bids after cancel, got %d", len(resCancel.DepthSnapshot.Bids))
	}
}

func TestOrdersCommands_DuplicateEventID_FailClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := newTestEngine("BTC-USDT")
	haltCalled := false
	engine.HaltCallback = func() {
		haltCalled = true
	}
	go engine.Run(ctx)

	sharedEventID := uuid.New()
	orderID := uuid.New()

	event := market.InputEvent{
		EventID: sharedEventID,
		Type:    market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   orderID,
			UserID:    uuid.New(),
			MarketID:  "BTC-USDT",
			Side:      orderbook.SideBuy,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("50000.00"),
			Quantity:  decimal.RequireFromString("1.000"),
		},
		Topic:  "orders.commands",
		Offset: 100,
	}

	// First execution -> Mutates book, Seq = 1
	engine.InputQueue <- event
	res1 := <-engine.OutputQueue
	if res1.DepthSnapshot.Sequence != 1 {
		t.Fatalf("expected Sequence=1, got %d", res1.DepthSnapshot.Sequence)
	}

	// Redelivery with same event_id but higher offset -> HaltCallback must be triggered!
	event.Offset = 101
	engine.InputQueue <- event

	// Wait a bit or assert haltCalled
	time.Sleep(100 * time.Millisecond)
	if !haltCalled {
		t.Fatal("expected HaltCallback to be called on duplicate event_id")
	}
}

func TestOrdersCommands_SameOrderDifferentEventID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := newTestEngine("BTC-USDT")
	go engine.Run(ctx)

	orderID := uuid.New()
	userID := uuid.New()

	// 1. Create with event_id A
	engine.InputQueue <- market.InputEvent{
		EventID: uuid.New(),
		Type:    market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   orderID,
			UserID:    userID,
			MarketID:  "BTC-USDT",
			Side:      orderbook.SideBuy,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("50000.00"),
			Quantity:  decimal.RequireFromString("1.000"),
		},
		Topic:  "orders.commands",
		Offset: 100,
	}
	res1 := <-engine.OutputQueue
	if res1.DepthSnapshot.Sequence != 1 {
		t.Fatalf("expected Sequence=1, got %d", res1.DepthSnapshot.Sequence)
	}

	// 2. Cancel same order with distinct event_id B
	engine.InputQueue <- market.InputEvent{
		EventID: uuid.New(),
		Type:    market.EventOrderCancel,
		OrderCancel: &market.OrderCancelPayload{
			OrderID:  orderID,
			UserID:   userID,
			MarketID: "BTC-USDT",
		},
		Topic:  "orders.commands",
		Offset: 101,
	}
	res2 := <-engine.OutputQueue
	if res2.CancelResult == nil {
		t.Fatal("expected non-nil CancelResult")
	}
	if res2.DepthSnapshot.Sequence != 2 {
		t.Fatalf("expected Sequence=2, got %d", res2.DepthSnapshot.Sequence)
	}
}

func TestOrdersCommands_FatalHaltSkipsDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	engine := newTestEngine("BTC-USDT")
	haltCalled := false
	engine.HaltCallback = func() {
		haltCalled = true
	}

	// Push 3 events before starting engine
	for i := 0; i < 3; i++ {
		engine.InputQueue <- market.InputEvent{
			EventID: uuid.New(),
			Type:    market.EventOrderCreated,
			OrderCreated: &market.OrderCreatedPayload{
				OrderID:   uuid.New(),
				UserID:    uuid.New(),
				MarketID:  "BTC-USDT",
				Side:      orderbook.SideBuy,
				OrderType: orderbook.OrderTypeLimit,
				Price:     decimal.RequireFromString("50000.00"),
				Quantity:  decimal.RequireFromString("1.000"),
			},
			Topic:  "orders.commands",
			Offset: int64(100 + i),
		}
	}

	// Trigger fatal halt and cancel context
	engine.TriggerFatalHalt()
	if !haltCalled {
		t.Fatal("expected HaltCallback to be called by TriggerFatalHalt")
	}
	cancel()

	// Run engine in goroutine
	done := make(chan struct{})
	go func() {
		engine.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for engine.Run to exit")
	}

	// Assert OutputQueue receives 0 events and is closed
	count := 0
	for range engine.OutputQueue {
		count++
	}
	if count != 0 {
		t.Fatalf("expected 0 events in OutputQueue after fatal halt, got %d", count)
	}
	if engine.GetSequence() != 0 {
		t.Fatalf("expected engine sequence to remain 0, got %d", engine.GetSequence())
	}
}

func TestOrdersCommands_GracefulShutdownDrains(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	engine := newTestEngine("BTC-USDT")

	// Push 3 events before starting engine
	for i := 0; i < 3; i++ {
		engine.InputQueue <- market.InputEvent{
			EventID: uuid.New(),
			Type:    market.EventOrderCreated,
			OrderCreated: &market.OrderCreatedPayload{
				OrderID:   uuid.New(),
				UserID:    uuid.New(),
				MarketID:  "BTC-USDT",
				Side:      orderbook.SideBuy,
				OrderType: orderbook.OrderTypeLimit,
				Price:     decimal.RequireFromString("50000.00"),
				Quantity:  decimal.RequireFromString("1.000"),
			},
			Topic:  "orders.commands",
			Offset: int64(100 + i),
		}
	}

	// Cancel context directly for graceful shutdown (NO fatal halt)
	cancel()

	// Run engine in goroutine
	done := make(chan struct{})
	go func() {
		engine.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for engine.Run to exit")
	}

	// Assert OutputQueue receives all 3 drained events + 1 final shutdown snapshot
	var results []orderbook.MatchResult
	for res := range engine.OutputQueue {
		results = append(results, res)
	}
	if len(results) != 4 {
		t.Fatalf("expected 4 results in OutputQueue (3 events + 1 shutdown snapshot), got %d", len(results))
	}
	if engine.GetSequence() != 3 {
		t.Fatalf("expected engine sequence to be 3 after drain, got %d", engine.GetSequence())
	}
}

func TestCancel_AlreadyAbsent_PreservesIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := newTestEngine("SOL-USDT")
	go engine.Run(ctx)

	absentOrderID := uuid.New()
	userID := uuid.New()
	marketID := "SOL-USDT"

	engine.InputQueue <- market.InputEvent{
		Type: market.EventOrderCancel,
		OrderCancel: &market.OrderCancelPayload{
			OrderID:  absentOrderID,
			UserID:   userID,
			MarketID: marketID,
		},
		Topic:  "orders.commands",
		Offset: 42,
	}

	res := <-engine.OutputQueue
	if res.CancelResult == nil {
		t.Fatal("expected non-nil CancelResult")
	}
	if res.CancelResult.CancelStatus != orderbook.CancelStatusAlreadyAbsent {
		t.Fatalf("expected CancelStatusAlreadyAbsent, got %v", res.CancelResult.CancelStatus)
	}
	if res.CancelResult.OrderID != absentOrderID {
		t.Fatalf("expected OrderID %s, got %s", absentOrderID, res.CancelResult.OrderID)
	}
	if res.CancelResult.UserID != userID {
		t.Fatalf("expected UserID %s, got %s", userID, res.CancelResult.UserID)
	}
	if res.CancelResult.MarketID != marketID {
		t.Fatalf("expected MarketID %s, got %s", marketID, res.CancelResult.MarketID)
	}
}

func TestSnapshot_DeterministicSerialization(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := newTestEngine("SOL-USDT")
	go engine.Run(ctx)

	// Insert 3 MM orders out of order
	levels := []struct {
		coid  string
		price string
	}{
		{"MM-SOL-USDT-BID-05-G001", "140.00"},
		{"MM-SOL-USDT-ASK-01-G002", "150.00"},
		{"MM-SOL-USDT-BID-01-G003", "145.00"},
	}

	for i, l := range levels {
		engine.InputQueue <- market.InputEvent{
			Type: market.EventOrderCreated,
			OrderCreated: &market.OrderCreatedPayload{
				OrderID:       uuid.New(),
				UserID:        market.MMUserID,
				MarketID:      "SOL-USDT",
				Side:          orderbook.SideBuy,
				OrderType:     orderbook.OrderTypeLimit,
				Price:         decimal.RequireFromString(l.price),
				Quantity:      decimal.RequireFromString("1.000"),
				ClientOrderID: l.coid,
			},
			Topic:  "orders.commands",
			Offset: int64(i + 1),
		}
		<-engine.OutputQueue
	}

	engine.PublishAtomicSnapshot()
	snap1 := engine.GetSnapshot()
	bytes1, err := json.Marshal(snap1.Orders)
	if err != nil {
		t.Fatalf("failed to marshal snap1.Orders: %v", err)
	}
	hash1 := sha256.Sum256(bytes1)

	engine.PublishAtomicSnapshot()
	snap2 := engine.GetSnapshot()
	bytes2, err := json.Marshal(snap2.Orders)
	if err != nil {
		t.Fatalf("failed to marshal snap2.Orders: %v", err)
	}
	hash2 := sha256.Sum256(bytes2)

	if !bytes.Equal(bytes1, bytes2) {
		t.Fatalf("expected identical serialized bytes across snapshots, got:\n%s\nvs\n%s", string(bytes1), string(bytes2))
	}
	if hash1 != hash2 {
		t.Fatalf("expected identical SHA256 checksums across snapshots, got %x vs %x", hash1, hash2)
	}
}


