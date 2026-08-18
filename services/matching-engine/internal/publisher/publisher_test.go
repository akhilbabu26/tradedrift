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

func TestProcess_RedisFailure_CheckpointNotWritten(t *testing.T) {
	k, db := &fakeKafka{}, &fakeDB{}
	r := &fakeRedis{failErr: errors.New("redis timeout")}
	p := newTestPublisher(k, r, db)

	result := makeResult(nil, "BTC-USDT", "orders.submitted", 0, 50)

	err := p.Process(context.Background(), result)
	if err == nil {
		t.Fatal("expected error when Redis fails")
	}
	if len(db.written) != 0 {
		t.Fatalf("checkpoint must NOT be written when Redis fails, got %d writes", len(db.written))
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
