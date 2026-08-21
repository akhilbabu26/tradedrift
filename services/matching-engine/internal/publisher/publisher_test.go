package publisher_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/shopspring/decimal"

	"tradedrift/services/matching-engine/internal/orderbook"
	"tradedrift/services/matching-engine/internal/publisher"
)

// ─── Fakes ────────────────────────────────────────────────────────────────────

type fakeKafka struct {
	published []kafkago.Message
	failErr   error
}

func (f *fakeKafka) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	if f.failErr != nil {
		return f.failErr
	}
	f.published = append(f.published, msgs...)
	return nil
}

type fakeRedis struct {
	stored  map[string][]byte
	failErr error
}

func (f *fakeRedis) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	if f.failErr != nil {
		return f.failErr
	}
	if f.stored == nil {
		f.stored = make(map[string][]byte)
	}
	f.stored[key] = value
	return nil
}

type fakeDB struct {
	written []orderbook.KafkaPosition
	failErr error
}

func (f *fakeDB) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	if f.failErr != nil {
		return pgconn.CommandTag{}, f.failErr
	}
	if len(args) >= 3 {
		f.written = append(f.written, orderbook.KafkaPosition{
			Topic:     args[0].(string),
			Partition: args[1].(int),
			Offset:    args[2].(int64),
		})
	}
	return pgconn.CommandTag{}, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func makeFill(marketID string) orderbook.Fill {
	return orderbook.Fill{
		TradeID:      uuid.New(),
		MarketID:     marketID,
		MakerOrderID: uuid.New(),
		TakerOrderID: uuid.New(),
		BuyOrderID:   uuid.New(),
		SellOrderID:  uuid.New(),
		BuyerUserID:  uuid.New(),
		SellerUserID: uuid.New(),
		Price:        decimal.RequireFromString("100.50"),
		Quantity:     decimal.RequireFromString("1.5"),
	}
}

func makeDepth(marketID string) orderbook.DepthSnapshot {
	return orderbook.DepthSnapshot{
		MarketID:   marketID,
		Bids:       []orderbook.DepthLevel{{Price: decimal.RequireFromString("99"), Quantity: decimal.RequireFromString("2")}},
		Asks:       []orderbook.DepthLevel{{Price: decimal.RequireFromString("101"), Quantity: decimal.RequireFromString("1")}},
		SnapshotAt: time.Now(),
	}
}

func makeResult(fills []orderbook.Fill, marketID, topic string, partition int, offset int64) orderbook.MatchResult {
	return orderbook.MatchResult{
		Fills:         fills,
		DepthSnapshot: makeDepth(marketID),
		SourcePosition: orderbook.KafkaPosition{
			Topic:     topic,
			Partition: partition,
			Offset:    offset,
		},
	}
}

func newTestPublisher(k *fakeKafka, r *fakeRedis, db *fakeDB) *publisher.TestablePublisher {
	return publisher.NewTestable(k, r, db)
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestProcess_OneFill_OneKafkaMessage(t *testing.T) {
	k, r, db := &fakeKafka{}, &fakeRedis{}, &fakeDB{}
	p := newTestPublisher(k, r, db)

	fill := makeFill("BTC-USDT")
	result := makeResult([]orderbook.Fill{fill}, "BTC-USDT", "orders.submitted", 0, 1)

	if err := p.Process(context.Background(), result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(k.published) != 1 {
		t.Fatalf("expected 1 Kafka message, got %d", len(k.published))
	}
}

func TestProcess_MultipleFills_MultipleKafkaMessages(t *testing.T) {
	k, r, db := &fakeKafka{}, &fakeRedis{}, &fakeDB{}
	p := newTestPublisher(k, r, db)

	fills := []orderbook.Fill{makeFill("ETH-USDT"), makeFill("ETH-USDT"), makeFill("ETH-USDT")}
	result := makeResult(fills, "ETH-USDT", "orders.submitted", 0, 5)

	if err := p.Process(context.Background(), result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(k.published) != 3 {
		t.Fatalf("expected 3 Kafka messages, got %d", len(k.published))
	}
}

func TestProcess_MarketID_IncludedInPayload(t *testing.T) {
	k, r, db := &fakeKafka{}, &fakeRedis{}, &fakeDB{}
	p := newTestPublisher(k, r, db)

	fill := makeFill("SOL-USDT")
	result := makeResult([]orderbook.Fill{fill}, "SOL-USDT", "orders.submitted", 0, 10)

	if err := p.Process(context.Background(), result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(k.published[0].Value, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["market_id"] != "SOL-USDT" {
		t.Fatalf("expected market_id=SOL-USDT, got %v", payload["market_id"])
	}
}

func TestProcess_BuyOrderID_UsedAsPartitionKey(t *testing.T) {
	k, r, db := &fakeKafka{}, &fakeRedis{}, &fakeDB{}
	p := newTestPublisher(k, r, db)

	fill := makeFill("BTC-USDT")
	result := makeResult([]orderbook.Fill{fill}, "BTC-USDT", "orders.submitted", 0, 2)

	if err := p.Process(context.Background(), result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedKey := fill.BuyOrderID.String()
	actualKey := string(k.published[0].Key)
	if actualKey != expectedKey {
		t.Fatalf("expected partition key=%s, got %s", expectedKey, actualKey)
	}
}

func TestProcess_DepthSnapshot_WrittenToRedis(t *testing.T) {
	k, r, db := &fakeKafka{}, &fakeRedis{}, &fakeDB{}
	p := newTestPublisher(k, r, db)

	result := makeResult(nil, "BTC-USDT", "orders.submitted", 0, 3)

	if err := p.Process(context.Background(), result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, ok := r.stored["depth:BTC-USDT"]
	if !ok {
		t.Fatal("expected Redis key depth:BTC-USDT to be set")
	}
	var snap map[string]any
	if err := json.Unmarshal(val, &snap); err != nil {
		t.Fatalf("unmarshal Redis value: %v", err)
	}
	if snap["market_id"] != "BTC-USDT" {
		t.Fatalf("expected market_id=BTC-USDT in Redis snapshot")
	}
}

func TestProcess_CheckpointWritten_AfterSuccess(t *testing.T) {
	k, r, db := &fakeKafka{}, &fakeRedis{}, &fakeDB{}
	p := newTestPublisher(k, r, db)

	result := makeResult(nil, "BTC-USDT", "orders.submitted", 1, 42)

	if err := p.Process(context.Background(), result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(db.written) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(db.written))
	}
	cp := db.written[0]
	if cp.Topic != "orders.submitted" || cp.Partition != 1 || cp.Offset != 42 {
		t.Fatalf("unexpected checkpoint: %+v", cp)
	}
}

func TestProcess_KafkaFailure_CheckpointNotWritten(t *testing.T) {
	k := &fakeKafka{failErr: errors.New("kafka broker down")}
	r, db := &fakeRedis{}, &fakeDB{}
	p := newTestPublisher(k, r, db)

	fill := makeFill("BTC-USDT")
	result := makeResult([]orderbook.Fill{fill}, "BTC-USDT", "orders.submitted", 0, 99)

	err := p.Process(context.Background(), result)
	if err == nil {
		t.Fatal("expected error when Kafka fails")
	}
	if len(db.written) != 0 {
		t.Fatalf("checkpoint must NOT be written when Kafka fails, got %d writes", len(db.written))
	}
}

func TestProcess_RedisFailure_CheckpointStillWritten(t *testing.T) {
	// Redis is a non-critical projection cache.
	// A Redis failure must NOT block the Postgres checkpoint from advancing.
	// The checkpoint represents Kafka consumer progress, not Redis state.
	// On restart, the next successful event will overwrite the stale Redis snapshot.
	k, db := &fakeKafka{}, &fakeDB{}
	r := &fakeRedis{failErr: errors.New("redis timeout")}
	p := newTestPublisher(k, r, db)

	result := makeResult(nil, "BTC-USDT", "orders.submitted", 0, 50)

	err := p.Process(context.Background(), result)
	if err != nil {
		t.Fatalf("expected no error when Redis fails (non-critical), got: %v", err)
	}
	if len(db.written) != 1 {
		t.Fatalf("checkpoint MUST still be written even when Redis fails, got %d writes", len(db.written))
	}
	if db.written[0].Offset != 50 {
		t.Fatalf("expected checkpoint offset 50, got %d", db.written[0].Offset)
	}
}

func TestProcess_CheckpointFailure_ReturnsError(t *testing.T) {
	k, r := &fakeKafka{}, &fakeRedis{}
	db := &fakeDB{failErr: errors.New("postgres connection lost")}
	p := newTestPublisher(k, r, db)

	result := makeResult(nil, "BTC-USDT", "orders.submitted", 0, 7)

	if err := p.Process(context.Background(), result); err == nil {
		t.Fatal("expected error when checkpoint write fails")
	}
}

func TestProcess_NoFills_SkipsKafka_StillWritesDepthAndCheckpoint(t *testing.T) {
	k, r, db := &fakeKafka{}, &fakeRedis{}, &fakeDB{}
	p := newTestPublisher(k, r, db)

	// Cancel event — no fills
	result := makeResult(nil, "ETH-USDT", "orders.cancel-requested", 0, 12)

	if err := p.Process(context.Background(), result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(k.published) != 0 {
		t.Fatalf("no Kafka messages expected for no-fill result, got %d", len(k.published))
	}
	if _, ok := r.stored["depth:ETH-USDT"]; !ok {
		t.Fatal("depth snapshot must always be written")
	}
	if len(db.written) != 1 {
		t.Fatal("checkpoint must be written even when no fills")
	}
}

func TestProcess_Sequential_CheckpointAlwaysAdvances(t *testing.T) {
	k, r, db := &fakeKafka{}, &fakeRedis{}, &fakeDB{}
	p := newTestPublisher(k, r, db)
	ctx := context.Background()

	for offset := int64(1); offset <= 5; offset++ {
		result := makeResult(nil, "BTC-USDT", "orders.submitted", 0, offset)
		if err := p.Process(ctx, result); err != nil {
			t.Fatalf("offset %d failed: %v", offset, err)
		}
	}

	if len(db.written) != 5 {
		t.Fatalf("expected 5 checkpoints, got %d", len(db.written))
	}
	// Verify strictly increasing offsets
	for i, cp := range db.written {
		if cp.Offset != int64(i+1) {
			t.Fatalf("expected checkpoint offset %d at position %d, got %d", i+1, i, cp.Offset)
		}
	}
}

// ─── Checkpoint Monotonicity Tests ───────────────────────────────────────────
//
// These tests verify the core correctness invariant:
//   A checkpoint must only advance forward (monotonic).
//   An incoming offset that is ≤ the current checkpoint must be silently ignored.
//
// This mirrors the Postgres UPSERT guard in publisher.go:
//   WHERE kafka_checkpoints.offset < EXCLUDED.offset
//
// The invariant matters because multiple MarketEngine publishers write to the
// same (topic, partition) checkpoint row. Without the guard, a slower publisher
// processing an earlier offset can move the checkpoint backwards, causing
// duplicate event replay on restart.

// fakeMonotonicDB mirrors the Postgres UPSERT monotonic guard in memory.
// It only advances the stored offset — never retreats — matching:
//
//	ON CONFLICT (topic, partition)
//	DO UPDATE SET offset = EXCLUDED.offset, updated_at = NOW()
//	WHERE kafka_checkpoints.offset < EXCLUDED.offset
type fakeMonotonicDB struct {
	// checkpoints maps "topic:partition" → last written offset.
	// -1 means no checkpoint written yet.
	checkpoints map[string]int64
	// calls records every Exec() invocation (including those the WHERE guard ignores).
	calls []orderbook.KafkaPosition
}

func newFakeMonotonicDB() *fakeMonotonicDB {
	return &fakeMonotonicDB{checkpoints: make(map[string]int64)}
}

func (f *fakeMonotonicDB) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	if len(args) < 3 {
		return pgconn.CommandTag{}, nil
	}
	topic := args[0].(string)
	partition := args[1].(int)
	offset := args[2].(int64)

	pos := orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: offset}
	f.calls = append(f.calls, pos)

	// Enforce the monotonic WHERE guard: only advance, never retreat.
	key := topic + ":" + string(rune('0'+partition))
	current, exists := f.checkpoints[key]
	if !exists || offset > current {
		f.checkpoints[key] = offset // advance
	}
	// If offset <= current: silently ignore — matches Postgres WHERE guard behaviour.
	return pgconn.CommandTag{}, nil
}

// currentCheckpoint returns the last persisted offset for a topic+partition,
// or -1 if nothing has been written yet.
func (f *fakeMonotonicDB) currentCheckpoint(topic string, partition int) int64 {
	key := topic + ":" + string(rune('0'+partition))
	v, ok := f.checkpoints[key]
	if !ok {
		return -1
	}
	return v
}

// TestCheckpointMonotonicity_DoesNotRegressWhenLowerOffsetArrives verifies that
// an incoming offset LOWER than the current checkpoint is silently ignored.
//
// Scenario:
//
//	ETH publisher (fast): writes checkpoint = 101
//	BTC publisher (slow): writes checkpoint = 100  ← must be ignored
//	Expected final checkpoint: 101
func TestCheckpointMonotonicity_DoesNotRegressWhenLowerOffsetArrives(t *testing.T) {
	db := newFakeMonotonicDB()
	p := publisher.NewTestable(
		&fakeKafka{},
		&fakeRedis{stored: make(map[string][]byte)},
		db,
	)
	ctx := context.Background()
	topic := "orders.submitted"
	const partition = 0

	// Step 1: ETH publisher commits offset 101 (higher offset — fast path).
	result101 := makeResult(nil, "ETH-USDT", topic, partition, 101)
	if err := p.Process(ctx, result101); err != nil {
		t.Fatalf("process offset 101: %v", err)
	}

	if got := db.currentCheckpoint(topic, partition); got != 101 {
		t.Fatalf("after offset 101: expected checkpoint=101, got=%d", got)
	}

	// Step 2: BTC publisher commits offset 100 (lower offset — slow path).
	// The WHERE guard must silently ignore this — checkpoint stays at 101.
	result100 := makeResult(nil, "BTC-USDT", topic, partition, 100)
	if err := p.Process(ctx, result100); err != nil {
		t.Fatalf("process offset 100: %v", err)
	}

	got := db.currentCheckpoint(topic, partition)
	if got != 101 {
		t.Fatalf("checkpoint regression: expected 101 after lower-offset write, got %d", got)
	}

	// Both calls reached Exec() — the guard is enforced at the DB layer, not by skipping Exec.
	if len(db.calls) != 2 {
		t.Fatalf("expected 2 Exec calls, got %d", len(db.calls))
	}
}

// TestCheckpointMonotonicity_AdvancesWhenHigherOffsetArrives verifies that
// an incoming offset HIGHER than the current checkpoint is correctly applied.
//
// Scenario:
//
//	Current checkpoint: 100
//	Incoming:           102
//	Expected:           102  (advanced)
func TestCheckpointMonotonicity_AdvancesWhenHigherOffsetArrives(t *testing.T) {
	db := newFakeMonotonicDB()
	p := publisher.NewTestable(
		&fakeKafka{},
		&fakeRedis{stored: make(map[string][]byte)},
		db,
	)
	ctx := context.Background()
	topic := "orders.submitted"
	const partition = 0

	// Establish a baseline checkpoint at offset 100.
	result100 := makeResult(nil, "BTC-USDT", topic, partition, 100)
	if err := p.Process(ctx, result100); err != nil {
		t.Fatalf("process offset 100: %v", err)
	}
	if got := db.currentCheckpoint(topic, partition); got != 100 {
		t.Fatalf("baseline: expected checkpoint=100, got=%d", got)
	}

	// Submit offset 102 — must advance the checkpoint.
	result102 := makeResult(nil, "BTC-USDT", topic, partition, 102)
	if err := p.Process(ctx, result102); err != nil {
		t.Fatalf("process offset 102: %v", err)
	}

	got := db.currentCheckpoint(topic, partition)
	if got != 102 {
		t.Fatalf("expected checkpoint to advance to 102, got %d", got)
	}
}

