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

	"tradedrift/services/matching-engine/internal/checkpoint"
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

type fakeCoordinator struct {
	done    []orderbook.KafkaPosition
	failErr error
}

func (f *fakeCoordinator) MarkDone(_ context.Context, pos orderbook.KafkaPosition) error {
	if f.failErr != nil {
		return f.failErr
	}
	f.done = append(f.done, pos)
	return nil
}

type fakeDBForCoord struct {
	commits []int64
}

func (f *fakeDBForCoord) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	if len(args) >= 3 {
		if offset, ok := args[2].(int64); ok {
			f.commits = append(f.commits, offset)
		}
	}
	return pgconn.CommandTag{}, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func makeFill(marketID string) orderbook.Fill {
	return orderbook.Fill{
		TradeID:      uuid.New(),
		MarketID:     marketID,
		Sequence:     101,
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
		Sequence:   101,
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

func newTestPublisher(k *fakeKafka, r *fakeRedis, coord *fakeCoordinator) *publisher.TestablePublisher {
	return publisher.NewTestable(k, r, coord)
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestProcess_OneFill_OneKafkaMessage(t *testing.T) {
	k, r, coord := &fakeKafka{}, &fakeRedis{}, &fakeCoordinator{}
	p := newTestPublisher(k, r, coord)

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
	k, r, coord := &fakeKafka{}, &fakeRedis{}, &fakeCoordinator{}
	p := newTestPublisher(k, r, coord)

	fills := []orderbook.Fill{makeFill("ETH-USDT"), makeFill("ETH-USDT"), makeFill("ETH-USDT")}
	result := makeResult(fills, "ETH-USDT", "orders.submitted", 0, 5)

	if err := p.Process(context.Background(), result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(k.published) != 3 {
		t.Fatalf("expected 3 Kafka messages, got %d", len(k.published))
	}
}

func TestProcess_MarketID_UsedAsPartitionKey(t *testing.T) {
	k, r, coord := &fakeKafka{}, &fakeRedis{}, &fakeCoordinator{}
	p := newTestPublisher(k, r, coord)

	fill := makeFill("BTC-USDT")
	result := makeResult([]orderbook.Fill{fill}, "BTC-USDT", "orders.submitted", 0, 2)

	if err := p.Process(context.Background(), result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Invariant: trades.executed must partition by MarketID so all trades for a market are ordered
	expectedKey := "BTC-USDT"
	actualKey := string(k.published[0].Key)
	if actualKey != expectedKey {
		t.Fatalf("expected partition key=%s, got %s", expectedKey, actualKey)
	}
}

func TestProcess_DepthSnapshot_WrittenToRedis(t *testing.T) {
	k, r, coord := &fakeKafka{}, &fakeRedis{}, &fakeCoordinator{}
	p := newTestPublisher(k, r, coord)

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

func TestProcess_CheckpointCoordinator_MarkDoneCalled(t *testing.T) {
	k, r, coord := &fakeKafka{}, &fakeRedis{}, &fakeCoordinator{}
	p := newTestPublisher(k, r, coord)

	result := makeResult(nil, "BTC-USDT", "orders.submitted", 1, 42)

	if err := p.Process(context.Background(), result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(coord.done) != 1 {
		t.Fatalf("expected 1 MarkDone call, got %d", len(coord.done))
	}
	cp := coord.done[0]
	if cp.Topic != "orders.submitted" || cp.Partition != 1 || cp.Offset != 42 {
		t.Fatalf("unexpected checkpoint position: %+v", cp)
	}
}

func TestProcess_KafkaFailure_CoordinatorNotCalled(t *testing.T) {
	k := &fakeKafka{failErr: errors.New("kafka broker down")}
	r, coord := &fakeRedis{}, &fakeCoordinator{}
	p := newTestPublisher(k, r, coord)

	fill := makeFill("BTC-USDT")
	result := makeResult([]orderbook.Fill{fill}, "BTC-USDT", "orders.submitted", 0, 99)

	err := p.Process(context.Background(), result)
	if err == nil {
		t.Fatal("expected error when Kafka fails")
	}
	if len(coord.done) != 0 {
		t.Fatalf("MarkDone must NOT be called when Kafka fails, got %d writes", len(coord.done))
	}
}

func TestProcess_RedisFailure_BufferedForRetry_AndCheckpointStillAdvances(t *testing.T) {
	k, coord := &fakeKafka{}, &fakeCoordinator{}
	r := &fakeRedis{failErr: errors.New("redis timeout")}
	p := newTestPublisher(k, r, coord)

	result := makeResult(nil, "BTC-USDT", "orders.submitted", 0, 50)

	err := p.Process(context.Background(), result)
	if err != nil {
		t.Fatalf("expected no error when Redis fails (non-critical), got: %v", err)
	}
	if len(coord.done) != 1 {
		t.Fatalf("MarkDone MUST still be called even when Redis fails, got %d calls", len(coord.done))
	}
	if coord.done[0].Offset != 50 {
		t.Fatalf("expected checkpoint offset 50, got %d", coord.done[0].Offset)
	}
}

func TestPublisher_EmitsAuthoritativeSequenceToKafkaAndRedis(t *testing.T) {
	k := &fakeKafka{}
	r := &fakeRedis{stored: make(map[string][]byte)}
	coord := &fakeCoordinator{}
	p := newTestPublisher(k, r, coord)

	fill := makeFill("BTC-USDT")
	fill.Sequence = 183421

	depth := makeDepth("BTC-USDT")
	depth.Sequence = 183422

	result := orderbook.MatchResult{
		Fills:         []orderbook.Fill{fill},
		DepthSnapshot: depth,
		SourcePosition: orderbook.KafkaPosition{
			Topic:     "orders.submitted",
			Partition: 0,
			Offset:    50,
		},
	}

	if err := p.Process(context.Background(), result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1. Verify Kafka message carries Sequence 183421
	if len(k.published) != 1 {
		t.Fatalf("expected 1 Kafka message, got %d", len(k.published))
	}
	var tradeMsg map[string]interface{}
	if err := json.Unmarshal(k.published[0].Value, &tradeMsg); err != nil {
		t.Fatalf("failed to unmarshal kafka trade message: %v", err)
	}
	tradeSeq, ok := tradeMsg["sequence"].(float64)
	if !ok || uint64(tradeSeq) != 183421 {
		t.Fatalf("expected Kafka trade sequence 183421, got %v", tradeMsg["sequence"])
	}

	// 2. Verify Redis depth carries Sequence 183422
	depthBytes, ok := r.stored["depth:BTC-USDT"]
	if !ok {
		t.Fatal("expected Redis key depth:BTC-USDT to be set")
	}
	var depthMsg map[string]interface{}
	if err := json.Unmarshal(depthBytes, &depthMsg); err != nil {
		t.Fatalf("failed to unmarshal redis depth message: %v", err)
	}
	depthSeq, ok := depthMsg["sequence"].(float64)
	if !ok || uint64(depthSeq) != 183422 {
		t.Fatalf("expected Redis depth sequence 183422, got %v", depthMsg["sequence"])
	}
}

// TestPublisher_IntegratedWithCoordinator_PreventsGapCheckpoints tests the end-to-end
// race condition where ETH finishes offset 101 before BTC finishes offset 100.
func TestPublisher_IntegratedWithCoordinator_PreventsGapCheckpoints(t *testing.T) {
	db := &fakeDBForCoord{}
	coord := checkpoint.NewCoordinator(db)
	ctx := context.Background()
	topic := "orders.submitted"
	const partition = 0

	coord.InitBaseline(topic, partition, 99)

	k := &fakeKafka{}
	r := &fakeRedis{}
	p := publisher.NewTestable(k, r, coord)

	// Step 1: Consumer tracks incoming offsets 100 (BTC) and 101 (ETH)
	coord.Track(orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 100})
	coord.Track(orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 101})

	// Step 2: ETH publisher finishes offset 101 FIRST
	resETH := makeResult(nil, "ETH-USDT", topic, partition, 101)
	if err := p.Process(ctx, resETH); err != nil {
		t.Fatalf("process ETH: %v", err)
	}

	// CRITICAL INVARIANT: Checkpoint MUST NOT advance to 101 because offset 100 is still in-flight!
	committed, _ := coord.GetCommittedOffset(topic, partition)
	if committed != 99 {
		t.Fatalf("gap invariant violated: checkpoint advanced to %d before offset 100 finished", committed)
	}
	if len(db.commits) != 0 {
		t.Fatalf("expected 0 DB commits during gap, got %d", len(db.commits))
	}

	// Step 3: BTC publisher finishes offset 100
	resBTC := makeResult(nil, "BTC-USDT", topic, partition, 100)
	if err := p.Process(ctx, resBTC); err != nil {
		t.Fatalf("process BTC: %v", err)
	}

	// Now that both 100 and 101 are complete, contiguous checkpoint jumps to 101
	committed, _ = coord.GetCommittedOffset(topic, partition)
	if committed != 101 {
		t.Fatalf("expected contiguous checkpoint 101, got %d", committed)
	}
	if len(db.commits) != 1 || db.commits[0] != 101 {
		t.Fatalf("expected DB commit with offset 101, got %+v", db.commits)
	}
}
