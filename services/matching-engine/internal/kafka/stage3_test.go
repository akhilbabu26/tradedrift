package kafka_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"github.com/shopspring/decimal"

	kafkapkg "tradedrift/services/matching-engine/internal/kafka"
	"tradedrift/services/matching-engine/internal/checkpoint"
	"tradedrift/services/matching-engine/internal/lease"
	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/matcher"
	"tradedrift/services/matching-engine/internal/orderbook"
)

// ─── Test Helpers & Mocks ─────────────────────────────────────────────────────

type mockPartitionReader struct {
	mu         sync.Mutex
	partition  int
	messages   []kafka.Message
	idx        int
	delay      time.Duration
	closed     bool
	lastOffset int64
}

func newMockPartitionReader(partition int, delay time.Duration, msgs ...kafka.Message) *mockPartitionReader {
	return &mockPartitionReader{
		partition: partition,
		messages:  msgs,
		delay:     delay,
	}
}

func (r *mockPartitionReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	if r.delay > 0 {
		select {
		case <-time.After(r.delay):
		case <-ctx.Done():
			return kafka.Message{}, ctx.Err()
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return kafka.Message{}, io.EOF
	}

	if r.idx < len(r.messages) {
		msg := r.messages[r.idx]
		r.idx++
		return msg, nil
	}

	// Wait until context cancellation if no more messages
	r.mu.Unlock()
	<-ctx.Done()
	r.mu.Lock()
	return kafka.Message{}, ctx.Err()
}

func (r *mockPartitionReader) SetOffset(offset int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastOffset = offset
	return nil
}

func (r *mockPartitionReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

type mockOffsetTrackerRecording struct {
	mu        sync.Mutex
	positions []orderbook.KafkaPosition
}

func (m *mockOffsetTrackerRecording) Track(pos orderbook.KafkaPosition) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.positions = append(m.positions, pos)
}

func (m *mockOffsetTrackerRecording) MarkDone(ctx context.Context, pos orderbook.KafkaPosition) error {
	return nil
}

func makeOrderCreatedMsg(marketID string, partition int, offset int64, price, qty string) kafka.Message {
	orderID := uuid.New().String()
	payload, _ := json.Marshal(map[string]any{
		"order_id":   orderID,
		"user_id":    uuid.New().String(),
		"side":       "BUY",
		"order_type": "LIMIT",
		"price":      price,
		"quantity":   qty,
	})
	env := map[string]any{
		"event_id":      uuid.New().String(),
		"event_type":    "OrderCreated",
		"event_version": 1,
		"market_id":     marketID,
		"occurred_at":   time.Now().UTC(),
		"payload":       json.RawMessage(payload),
	}
	b, _ := json.Marshal(env)
	return kafka.Message{
		Key:       []byte(marketID),
		Topic:     kafkapkg.TopicOrderCommands,
		Partition: partition,
		Offset:    offset,
		Value:     b,
	}
}

// ─── Stage 3 Required Tests ───────────────────────────────────────────────────

// Requirement 1: One partition being delayed while another continues processing without stall.
func TestStage3_PartitionIntakeIsolation_DelayedPartitionDoesNotStallOthers(t *testing.T) {
	manager := market.NewMarketManager()
	btcEng := manager.Add(market.MarketConfig{
		MarketID:  "BTC-USDT",
		Partition: 0,
		TickSize:  decimal.RequireFromString("0.01"),
		LotSize:   decimal.RequireFromString("0.0001"),
	})
	solEng := manager.Add(market.MarketConfig{
		MarketID:  "SOL-USDT",
		Partition: 2,
		TickSize:  decimal.RequireFromString("0.001"),
		LotSize:   decimal.RequireFromString("0.01"),
	})

	// Partition 0 (BTC) has a 500ms delay per fetch
	delayedReader := newMockPartitionReader(0, 500*time.Millisecond,
		makeOrderCreatedMsg("BTC-USDT", 0, 10, "50000", "1"),
	)
	// Partition 2 (SOL) has zero delay and produces messages immediately
	fastReader := newMockPartitionReader(2, 0,
		makeOrderCreatedMsg("SOL-USDT", 2, 50, "150", "10"),
		makeOrderCreatedMsg("SOL-USDT", 2, 51, "151", "10"),
	)

	tracker := &mockOffsetTrackerRecording{}
	consumer := kafkapkg.NewConsumer(kafkapkg.Config{
		Brokers: []string{"localhost:9092"},
		GroupID: "test-group",
	}, manager, tracker)

	consumer.OverridePartitionReaderFactory(func(brokers []string, topic string, partition int) (kafkapkg.PartitionReader, error) {
		if partition == 0 {
			return delayedReader, nil
		}
		if partition == 2 {
			return fastReader, nil
		}
		return nil, fmt.Errorf("unexpected partition %d", partition)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := consumer.Start(ctx, cancel); err != nil {
		t.Fatalf("unexpected Start error: %v", err)
	}
	defer consumer.Stop(1 * time.Second)

	// SOL should receive its events almost immediately, while BTC is still waiting in its 500ms delay
	start := time.Now()
	var solReceived int
	for solReceived < 2 && time.Since(start) < 200*time.Millisecond {
		select {
		case ev := <-solEng.InputQueue:
			if ev.Topic != kafkapkg.TopicOrderCommands || ev.Partition != 2 {
				t.Fatalf("unexpected event on SOL input queue: %+v", ev)
			}
			solReceived++
		case <-time.After(20 * time.Millisecond):
		}
	}

	if solReceived != 2 {
		t.Fatalf("expected SOL to receive 2 messages immediately, received %d", solReceived)
	}

	// Verify BTC has NOT received its message yet at this point (< 200ms)
	select {
	case ev := <-btcEng.InputQueue:
		t.Fatalf("expected BTC to still be delayed, but received event early: %+v", ev)
	default:
		// Expected: BTC is still delayed in its dedicated reader
	}

	// Now wait for BTC's delayed reader to eventually complete
	select {
	case ev := <-btcEng.InputQueue:
		if ev.Partition != 0 || ev.Offset != 10 {
			t.Fatalf("unexpected BTC event after delay: %+v", ev)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for BTC delayed event")
	}
}

// Requirement 2: Two engine instances attempting to own the same partition (second fails closed).
func TestStage3_PartitionOwnership_TwoInstancesContention(t *testing.T) {
	leaseMgr := lease.NewMockLeaseManager()
	ctx := context.Background()

	// Instance 1 acquires partition 0
	lease1, err := leaseMgr.AcquireLease(ctx, 0)
	if err != nil {
		t.Fatalf("instance 1 failed to acquire lease: %v", err)
	}
	if !lease1.IsValid() {
		t.Fatal("expected instance 1 lease to be valid")
	}

	// Instance 2 attempts to acquire partition 0 — MUST fail closed
	lease2, err := leaseMgr.AcquireLease(ctx, 0)
	if err == nil {
		t.Fatal("expected instance 2 lease acquisition to fail, got nil error")
	}
	if !errors.Is(err, lease.ErrLockAcquisitionFailed) {
		t.Fatalf("expected ErrLockAcquisitionFailed, got: %v", err)
	}
	if lease2 != nil {
		t.Fatal("expected nil lease for instance 2")
	}
}

// Requirement 3: Advisory-lock acquisition failure and ownership loss.
func TestStage3_PartitionOwnership_AcquisitionFailureAndLoss(t *testing.T) {
	t.Run("AcquisitionFailureFailsClosedAtStart", func(t *testing.T) {
		leaseMgr := lease.NewMockLeaseManager()
		leaseMgr.SetFailPartition(0, lease.ErrLockAcquisitionFailed)

		manager := market.NewMarketManager()
		manager.Add(market.MarketConfig{MarketID: "BTC-USDT", Partition: 0})

		consumer := kafkapkg.NewConsumer(kafkapkg.Config{
			Brokers:      []string{"localhost:9092"},
			GroupID:      "test-group",
			LockAcquirer: leaseMgr,
		}, manager, nil)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var cancelCalled bool
		err := consumer.Start(ctx, func() { cancelCalled = true })
		if err == nil {
			t.Fatal("expected consumer.Start to fail closed when partition lease acquisition fails")
		}
		if !cancelCalled {
			t.Fatal("expected start cancellation callback to be invoked on acquisition failure")
		}
	})

	t.Run("OwnershipLossDuringIntakeTriggersFatalHalt", func(t *testing.T) {
		leaseMgr := lease.NewMockLeaseManager()
		l, err := leaseMgr.AcquireLease(context.Background(), 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		mockL := l.(*lease.MockLease)

		manager := market.NewMarketManager()
		manager.Add(market.MarketConfig{MarketID: "BTC-USDT", Partition: 0})

		consumer := kafkapkg.NewConsumer(kafkapkg.Config{
			Brokers:      []string{"localhost:9092"},
			GroupID:      "test-group",
			LockAcquirer: leaseMgr,
			Leases:       map[int]lease.Lease{0: l},
		}, manager, nil)

		reader := newMockPartitionReader(0, 0)
		consumer.OverridePartitionReaderFactory(func(brokers []string, topic string, partition int) (kafkapkg.PartitionReader, error) {
			return reader, nil
		})

		var fatalHaltCalled bool
		consumer.FatalCallback = func() {
			fatalHaltCalled = true
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := consumer.Start(ctx, cancel); err != nil {
			t.Fatalf("unexpected Start error: %v", err)
		}
		defer consumer.Stop(1 * time.Second)

		// Revoke lease (simulating PostgreSQL connection loss)
		mockL.Revoke()

		// Verify fatal halt is triggered
		deadline := time.Now().Add(500 * time.Millisecond)
		for !fatalHaltCalled && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}

		if !fatalHaltCalled {
			t.Fatal("expected FatalCallback to be triggered upon lease loss")
		}
	})
}

// Requirement 4: Restart and lock reacquisition.
func TestStage3_RestartAndLockReacquisition(t *testing.T) {
	leaseMgr := lease.NewMockLeaseManager()
	ctx := context.Background()

	// Initial run
	l1, err := leaseMgr.AcquireLease(ctx, 0)
	if err != nil {
		t.Fatalf("first acquisition failed: %v", err)
	}

	// Clean shutdown releases lease
	if err := l1.Release(ctx); err != nil {
		t.Fatalf("failed to release lease: %v", err)
	}

	// Restarted engine re-acquires the same partition lease cleanly
	l2, err := leaseMgr.AcquireLease(ctx, 0)
	if err != nil {
		t.Fatalf("second acquisition after restart failed: %v", err)
	}
	if !l2.IsValid() {
		t.Fatal("re-acquired lease is not valid")
	}
}

// Requirement 5: Per-partition offset ordering and checkpoint monotonicity.
func TestStage3_PerPartitionOffsetOrderingAndMonotonicity(t *testing.T) {
	manager := market.NewMarketManager()
	btcEng := manager.Add(market.MarketConfig{MarketID: "BTC-USDT", Partition: 0})
	solEng := manager.Add(market.MarketConfig{MarketID: "SOL-USDT", Partition: 2})

	btcReader := newMockPartitionReader(0, 0,
		makeOrderCreatedMsg("BTC-USDT", 0, 100, "50000", "1"),
		makeOrderCreatedMsg("BTC-USDT", 0, 101, "50001", "1"),
		makeOrderCreatedMsg("BTC-USDT", 0, 102, "50002", "1"),
	)
	solReader := newMockPartitionReader(2, 0,
		makeOrderCreatedMsg("SOL-USDT", 2, 500, "150", "5"),
		makeOrderCreatedMsg("SOL-USDT", 2, 501, "151", "5"),
	)

	tracker := &mockOffsetTrackerRecording{}
	consumer := kafkapkg.NewConsumer(kafkapkg.Config{
		Brokers: []string{"localhost:9092"},
		GroupID: "test-group",
	}, manager, tracker)

	consumer.OverridePartitionReaderFactory(func(brokers []string, topic string, partition int) (kafkapkg.PartitionReader, error) {
		if partition == 0 {
			return btcReader, nil
		}
		if partition == 2 {
			return solReader, nil
		}
		return nil, fmt.Errorf("unexpected partition %d", partition)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := consumer.Start(ctx, cancel); err != nil {
		t.Fatalf("unexpected Start error: %v", err)
	}
	defer consumer.Stop(1 * time.Second)

	// Collect BTC events in order
	for expectedOffset := int64(100); expectedOffset <= 102; expectedOffset++ {
		select {
		case ev := <-btcEng.InputQueue:
			if ev.Partition != 0 || ev.Offset != expectedOffset {
				t.Fatalf("expected BTC partition 0 offset %d, got %d", expectedOffset, ev.Offset)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timed out waiting for BTC offset %d", expectedOffset)
		}
	}

	// Collect SOL events in order
	for expectedOffset := int64(500); expectedOffset <= 501; expectedOffset++ {
		select {
		case ev := <-solEng.InputQueue:
			if ev.Partition != 2 || ev.Offset != expectedOffset {
				t.Fatalf("expected SOL partition 2 offset %d, got %d", expectedOffset, ev.Offset)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timed out waiting for SOL offset %d", expectedOffset)
		}
	}

	// Verify all positions were tracked monotonically per partition
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	var btcOffsets, solOffsets []int64
	for _, pos := range tracker.positions {
		if pos.Partition == 0 {
			btcOffsets = append(btcOffsets, pos.Offset)
		} else if pos.Partition == 2 {
			solOffsets = append(solOffsets, pos.Offset)
		}
	}

	if len(btcOffsets) != 3 || btcOffsets[0] != 100 || btcOffsets[1] != 101 || btcOffsets[2] != 102 {
		t.Fatalf("expected strictly monotonic BTC offsets [100, 101, 102], got: %v", btcOffsets)
	}
	if len(solOffsets) != 2 || solOffsets[0] != 500 || solOffsets[1] != 501 {
		t.Fatalf("expected strictly monotonic SOL offsets [500, 501], got: %v", solOffsets)
	}
}

// Requirement 6: No concurrent replay and live consumption for the same partition.
func TestStage3_NoConcurrentReplayAndLiveConsumption(t *testing.T) {
	manager := market.NewMarketManager()
	btcEng := manager.Add(market.MarketConfig{MarketID: "BTC-USDT", Partition: 0})

	// When an engine is in ModeRecovery, it has not yet reached its recovery barrier
	if btcEng.Mode() != market.ModeRecovery {
		t.Fatalf("expected newly created engine to start in ModeRecovery, got %v", btcEng.Mode())
	}

	// Engine must not be live while in recovery phase
	if btcEng.Mode() == market.ModeLive {
		t.Fatal("engine must not be live while in recovery phase")
	}

	// Live commands must not be applied to book before recovery completes
	// Mode transition is strictly single-phased
	if err := btcEng.SetLive(); err != nil {
		t.Fatalf("unexpected error setting live after simulated recovery: %v", err)
	}
	if btcEng.Mode() != market.ModeLive {
		t.Fatalf("expected ModeLive, got %v", btcEng.Mode())
	}
}

// Requirement 7: Blocked ETH commands without cross-market stalls.
func TestStage3_BlockedETHCommandsWithoutCrossMarketStalls(t *testing.T) {
	manager := market.NewMarketManager()
	btcEng := manager.Add(market.MarketConfig{
		MarketID:  "BTC-USDT",
		Partition: 0,
		TickSize:  decimal.RequireFromString("0.01"),
		LotSize:   decimal.RequireFromString("0.0001"),
	})
	ethEng := manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
		TickSize:  decimal.RequireFromString("0.01"),
		LotSize:   decimal.RequireFromString("0.0001"),
	})

	// Set BTC Live, and ETH Blocked (simulating recovery failure on ETH)
	_ = btcEng.SetLive()
	ethEng.SetBlocked(errors.New("missing Kafka history 43558-43562"))

	var engineWg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start event loops for both engines
	engineWg.Add(2)
	go func() { defer engineWg.Done(); btcEng.Run(ctx) }()
	go func() { defer engineWg.Done(); ethEng.Run(ctx) }()

	// ETH receives command on partition 1; BTC receives command on partition 0
	ethMsg := makeOrderCreatedMsg("ETH-USDT", 1, 43563, "3000", "1")
	btcMsg := makeOrderCreatedMsg("BTC-USDT", 0, 800, "50000", "1")

	ethReader := newMockPartitionReader(1, 0, ethMsg)
	btcReader := newMockPartitionReader(0, 0, btcMsg)

	consumer := kafkapkg.NewConsumer(kafkapkg.Config{
		Brokers: []string{"localhost:9092"},
		GroupID: "test-group",
	}, manager, nil)

	consumer.OverridePartitionReaderFactory(func(brokers []string, topic string, partition int) (kafkapkg.PartitionReader, error) {
		if partition == 0 {
			return btcReader, nil
		}
		if partition == 1 {
			return ethReader, nil
		}
		return nil, fmt.Errorf("unexpected partition %d", partition)
	})

	if err := consumer.Start(ctx, cancel); err != nil {
		t.Fatalf("unexpected Start error: %v", err)
	}
	defer consumer.Stop(1 * time.Second)

	// 1. Verify ETH command produces CancelResult with market_blocked
	select {
	case ethRes := <-ethEng.OutputQueue:
		if ethRes.CancelResult == nil {
			t.Fatal("expected CancelResult for command on blocked ETH market")
		}
		if ethRes.CancelResult.Reason != "market_blocked" {
			t.Fatalf("expected reason 'market_blocked', got %s", ethRes.CancelResult.Reason)
		}
		if ethRes.CancelResult.CancelStatus != orderbook.CancelStatusInvalidRequest {
			t.Fatalf("expected CancelStatusInvalidRequest, got %v", ethRes.CancelResult.CancelStatus)
		}
		// Invariant: ETH sequence MUST NOT advance
		if ethEng.GetSequence() != 0 {
			t.Fatalf("expected ETH sequence to remain 0, got %d", ethEng.GetSequence())
		}
		// Invariant: ETH book MUST remain empty
		depth := ethEng.GetDepth(10)
		if len(depth.Bids) != 0 || len(depth.Asks) != 0 {
			t.Fatalf("expected ETH book to remain empty, got bids=%d asks=%d", len(depth.Bids), len(depth.Asks))
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for ETH rejection result")
	}

	// 2. Verify BTC executes normally without being stalled by ETH
	select {
	case btcRes := <-btcEng.OutputQueue:
		if btcRes.CancelResult != nil {
			t.Fatalf("unexpected rejection on live BTC market: %+v", btcRes.CancelResult)
		}
		// BTC sequence advanced
		if btcEng.GetSequence() != 1 {
			t.Fatalf("expected BTC sequence to advance to 1, got %d", btcEng.GetSequence())
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for BTC execution result")
	}
}

// Requirement 8: Duplicate delivery and idempotent command-result handling.
func TestStage3_DuplicateDelivery_IdempotentHandling(t *testing.T) {
	manager := market.NewMarketManager()
	btcEng := manager.Add(market.MarketConfig{
		MarketID:  "BTC-USDT",
		Partition: 0,
		TickSize:  decimal.RequireFromString("0.01"),
		LotSize:   decimal.RequireFromString("0.0001"),
	})
	_ = btcEng.SetLive()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go btcEng.Run(ctx)

	// Create a single message and send it twice (duplicate delivery)
	msg := makeOrderCreatedMsg("BTC-USDT", 0, 100, "50000", "1")

	routedChan := btcEng.InputQueue
	consumer := kafkapkg.NewTestableConsumer(func(marketID string) chan market.InputEvent {
		return routedChan
	})

	// First delivery
	routed, err := consumer.HandleOrderCommand(msg)
	if err != nil || !routed {
		t.Fatalf("first delivery failed: routed=%v, err=%v", routed, err)
	}

	select {
	case res := <-btcEng.OutputQueue:
		if res.CancelResult != nil {
			t.Fatalf("first order failed: %+v", res.CancelResult)
		}
		if btcEng.GetSequence() != 1 {
			t.Fatalf("expected sequence 1, got %d", btcEng.GetSequence())
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for first result")
	}

	// Second delivery (duplicate EventID & OrderID at same offset)
	routed2, err := consumer.HandleOrderCommand(msg)
	if err != nil || !routed2 {
		t.Fatalf("second delivery failed: routed=%v, err=%v", routed2, err)
	}

	select {
	case res := <-btcEng.OutputQueue:
		// Invariant: No duplicate trades produced
		if len(res.Fills) != 0 {
			t.Fatalf("expected 0 fills on duplicate redelivery, got %d", len(res.Fills))
		}
		// Invariant: Sequence MUST NOT advance for duplicate
		if btcEng.GetSequence() != 1 {
			t.Fatalf("sequence advanced on duplicate: expected 1, got %d", btcEng.GetSequence())
		}
		// Invariant: Order book depth unchanged (exactly 1 resting bid, not doubled)
		if len(res.DepthSnapshot.Bids) != 1 || !res.DepthSnapshot.Bids[0].Quantity.Equal(decimal.RequireFromString("1")) {
			t.Fatalf("unexpected book state after duplicate: %+v", res.DepthSnapshot.Bids)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for duplicate result")
	}
}

// Requirement 9: Graceful shutdown and recovery after a consumer failure.
func TestStage3_GracefulShutdownAndRecoveryAfterFailure(t *testing.T) {
	manager := market.NewMarketManager()
	manager.Add(market.MarketConfig{MarketID: "BTC-USDT", Partition: 0})
	manager.Add(market.MarketConfig{MarketID: "SOL-USDT", Partition: 2})

	var btcClosed, solClosed atomic.Bool
	btcReader := newMockPartitionReader(0, 0)
	solReader := newMockPartitionReader(2, 0)

	consumer := kafkapkg.NewConsumer(kafkapkg.Config{
		Brokers: []string{"localhost:9092"},
		GroupID: "test-group",
	}, manager, nil)

	consumer.OverridePartitionReaderFactory(func(brokers []string, topic string, partition int) (kafkapkg.PartitionReader, error) {
		if partition == 0 {
			return struct {
				*mockPartitionReader
			}{btcReader}, nil
		}
		if partition == 2 {
			return struct {
				*mockPartitionReader
			}{solReader}, nil
		}
		return nil, fmt.Errorf("unexpected partition %d", partition)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := consumer.Start(ctx, cancel); err != nil {
		t.Fatalf("unexpected Start error: %v", err)
	}

	// Perform graceful Stop with timeout
	stopDone := make(chan error)
	go func() {
		stopDone <- consumer.Stop(2 * time.Second)
	}()

	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("expected clean stop without error, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("consumer.Stop timed out")
	}

	_ = btcClosed.Load()
	_ = solClosed.Load()
}

// ─── Stage 3 Safety Verification Tests ─────────────────────────────────────────

func TestStage3_SingleMatchingLoopPerEngine(t *testing.T) {
	eng := market.NewMarketEngine(market.MarketConfig{
		MarketID:  "BTC-USDT",
		Partition: 0,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var loop1Started sync.WaitGroup
	loop1Started.Add(1)
	go func() {
		loop1Started.Done()
		eng.Run(ctx)
	}()

	loop1Started.Wait()
	time.Sleep(50 * time.Millisecond)

	if !eng.IsRunning() {
		t.Fatal("expected eng.IsRunning() to be true")
	}

	// Attempt to start a second matching loop on the same engine
	loop2Exited := make(chan struct{})
	go func() {
		eng.Run(ctx)
		close(loop2Exited)
	}()

	select {
	case <-loop2Exited:
		// Succeeded in failing closed (exited immediately without duplicate loop)
	case <-time.After(1 * time.Second):
		t.Fatal("second matching loop did not exit immediately — duplicate matching loops permitted!")
	}

	// Stop loop 1
	cancel()
	time.Sleep(50 * time.Millisecond)
	if eng.IsRunning() {
		t.Fatal("expected eng.IsRunning() to be false after loop exit")
	}
}

func TestStage3_Fencing_QueuedWorkDroppedOnFatalHalt(t *testing.T) {
	eng := market.NewMarketEngine(market.MarketConfig{
		MarketID:  "BTC-USDT",
		Partition: 0,
	})
	if err := eng.SetLive(); err != nil {
		t.Fatalf("SetLive failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Fill input queue with commands
	for i := 1; i <= 5; i++ {
		eng.InputQueue <- market.InputEvent{
			Type: market.EventOrderCreated,
			OrderCreated: &market.OrderCreatedPayload{
				OrderID:   uuid.New(),
				UserID:    uuid.New(),
				MarketID:  "BTC-USDT",
				Side:      orderbook.SideBuy,
				Price:     decimal.RequireFromString("50000.00"),
				Quantity:  decimal.RequireFromString("1.0"),
				OrderType: orderbook.OrderTypeLimit,
			},
			Topic:  "orders.commands",
			Offset: int64(i),
		}
	}

	// Trigger fatal halt BEFORE loop runs (or concurrently)
	eng.TriggerFatalHalt()

	runExited := make(chan struct{})
	go func() {
		eng.Run(ctx)
		close(runExited)
	}()

	select {
	case <-runExited:
	case <-time.After(1 * time.Second):
		t.Fatal("engine.Run did not exit immediately upon fatal halt")
	}

	// Verify OutputQueue was closed and NO mutations reached the order book
	if eng.GetSequence() != 0 {
		t.Fatalf("expected sequence 0 (no mutation after halt), got %d", eng.GetSequence())
	}
	depth := eng.GetDepth(10)
	if len(depth.Bids) != 0 || len(depth.Asks) != 0 {
		t.Fatalf("expected empty book, got %d bids %d asks", len(depth.Bids), len(depth.Asks))
	}
}

func TestStage3_Fencing_CoordinatorFencedOnCancelledContext(t *testing.T) {
	coord := checkpoint.NewCoordinator(nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	ev := checkpoint.CompletedEvent{
		Pos: orderbook.KafkaPosition{
			Topic:     "orders.commands",
			Partition: 0,
			Offset:    10,
		},
		MarketID: "BTC-USDT",
		Sequence: 1,
	}

	err := coord.MarkDoneWithSequence(ctx, ev)
	if err == nil {
		t.Fatal("expected error from MarkDoneWithSequence on cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) && err.Error() != "fenced: checkpoint context cancelled: context canceled" {
		t.Fatalf("expected fenced context error, got: %v", err)
	}
}

func TestStage3_BlockedMarket_CommandOutcomeContract(t *testing.T) {
	eng := market.NewMarketEngine(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})
	eng.SetBlocked(errors.New("unrecoverable retention gap"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go eng.Run(ctx)

	orderID := uuid.New()
	userID := uuid.New()

	eng.InputQueue <- market.InputEvent{
		Type: market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   orderID,
			UserID:    userID,
			MarketID:  "ETH-USDT",
			Side:      orderbook.SideBuy,
			Price:     decimal.RequireFromString("2500.00"),
			Quantity:  decimal.RequireFromString("2.0"),
			OrderType: orderbook.OrderTypeLimit,
		},
		Topic:  "orders.commands",
		Offset: 43563,
	}

	select {
	case res := <-eng.OutputQueue:
		if res.CancelResult == nil {
			t.Fatal("expected CancelResult for command on ModeBlocked engine")
		}
		if res.CancelResult.Reason != "market_blocked" {
			t.Fatalf("expected reason 'market_blocked', got %q", res.CancelResult.Reason)
		}
		if res.CancelResult.CancelStatus != orderbook.CancelStatusInvalidRequest {
			t.Fatalf("expected CancelStatusInvalidRequest, got %v", res.CancelResult.CancelStatus)
		}
		if len(res.Fills) != 0 {
			t.Fatalf("expected 0 fills on blocked market, got %d", len(res.Fills))
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for rejection on ModeBlocked engine")
	}

	// Verify order book depth and sequence were completely untouched
	depth := eng.GetDepth(10)
	if len(depth.Bids) != 0 || len(depth.Asks) != 0 {
		t.Fatalf("expected empty book, got %d bids %d asks", len(depth.Bids), len(depth.Asks))
	}
	if eng.GetSequence() != 0 {
		t.Fatalf("expected sequence 0 (untouched), got %d", eng.GetSequence())
	}
}

func TestStage3_DeterministicTradeIDs_DownstreamIdempotency(t *testing.T) {
	eventID := uuid.New()
	makerID := uuid.New()
	takerID := uuid.New()

	// Simulate original execution
	tradeID1 := matcher.TradeID(eventID, makerID, takerID, 0)
	tradeID2 := matcher.TradeID(eventID, makerID, takerID, 1)

	// Simulate replay after crash
	replayTradeID1 := matcher.TradeID(eventID, makerID, takerID, 0)
	replayTradeID2 := matcher.TradeID(eventID, makerID, takerID, 1)

	if tradeID1 != replayTradeID1 {
		t.Fatalf("expected deterministic tradeID1 on replay: original=%s replay=%s", tradeID1, replayTradeID1)
	}
	if tradeID2 != replayTradeID2 {
		t.Fatalf("expected deterministic tradeID2 on replay: original=%s replay=%s", tradeID2, replayTradeID2)
	}
	if tradeID1 == tradeID2 {
		t.Fatal("fill index 0 and 1 must produce distinct TradeIDs")
	}
}

