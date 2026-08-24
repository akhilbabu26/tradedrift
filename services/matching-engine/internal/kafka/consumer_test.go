package kafka_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/segmentio/kafka-go"
	kafkapkg "tradedrift/services/matching-engine/internal/kafka"
	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/orderbook"
)

// ─── Helpers ──────────────────────────────────────────────────────────────────

func makeCommandMsg(key string, partition int, offset int64, env any) kafka.Message {
	b, _ := json.Marshal(env)
	return kafka.Message{
		Key:       []byte(key),
		Topic:     kafkapkg.TopicOrderCommands,
		Partition: partition,
		Offset:    offset,
		Value:     b,
	}
}

func validOrderCreatedEnvelope(marketID string) map[string]any {
	payload, _ := json.Marshal(map[string]any{
		"order_id":   uuid.New().String(),
		"user_id":    uuid.New().String(),
		"side":       "BUY",
		"order_type": "LIMIT",
		"price":      "100.50",
		"quantity":   "1.5",
	})
	return map[string]any{
		"event_id":      uuid.New().String(),
		"event_type":    "OrderCreated",
		"event_version": 1,
		"market_id":     marketID,
		"occurred_at":   time.Now().UTC(),
		"payload":       json.RawMessage(payload),
	}
}

func validOrderCancelEnvelope(marketID string, orderID string) map[string]any {
	payload, _ := json.Marshal(map[string]any{
		"order_id": orderID,
		"user_id":  uuid.New().String(),
	})
	return map[string]any{
		"event_id":      uuid.New().String(),
		"event_type":    "OrderCancelRequested",
		"event_version": 1,
		"market_id":     marketID,
		"occurred_at":   time.Now().UTC(),
		"payload":       json.RawMessage(payload),
	}
}

// ─── Command Tests ────────────────────────────────────────────────────────────

func TestHandleOrderCommand_OrderCreated_Valid(t *testing.T) {
	routedChan := make(chan market.InputEvent, 1)
	consumer := kafkapkg.NewTestableConsumer(func(marketID string) chan market.InputEvent {
		if marketID == "BTC-USDT" {
			return routedChan
		}
		return nil
	})

	msg := makeCommandMsg("BTC-USDT", 0, 42, validOrderCreatedEnvelope("BTC-USDT"))
	routed, err := consumer.HandleOrderCommand(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !routed {
		t.Fatal("expected message to be routed")
	}

	event := <-routedChan
	if event.Type != market.EventOrderCreated {
		t.Fatalf("expected EventOrderCreated")
	}
	if event.Topic != kafkapkg.TopicOrderCommands {
		t.Fatalf("expected topic=%s, got %s", kafkapkg.TopicOrderCommands, event.Topic)
	}
	if event.Partition != 0 {
		t.Fatalf("expected partition=0, got %d", event.Partition)
	}
	if event.Offset != 42 {
		t.Fatalf("expected offset=42, got %d", event.Offset)
	}
	if event.OrderCreated.Side != orderbook.SideBuy {
		t.Fatalf("expected side=BUY")
	}
}

func TestHandleOrderCommand_OrderCancel_Valid(t *testing.T) {
	routedChan := make(chan market.InputEvent, 1)
	consumer := kafkapkg.NewTestableConsumer(func(marketID string) chan market.InputEvent {
		if marketID == "BTC-USDT" {
			return routedChan
		}
		return nil
	})

	targetOrderID := uuid.New().String()
	msg := makeCommandMsg("BTC-USDT", 0, 43, validOrderCancelEnvelope("BTC-USDT", targetOrderID))
	routed, err := consumer.HandleOrderCommand(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !routed {
		t.Fatal("expected cancel message to be routed")
	}

	event := <-routedChan
	if event.Type != market.EventOrderCancel {
		t.Fatalf("expected EventOrderCancel")
	}
	if event.OrderCancel.OrderID.String() != targetOrderID {
		t.Fatalf("expected order_id=%s, got %s", targetOrderID, event.OrderCancel.OrderID.String())
	}
}

func TestHandleOrderCommand_PartitionKeyMismatch(t *testing.T) {
	consumer := kafkapkg.NewTestableConsumer(nil)
	msg := makeCommandMsg("BTC-USDT", 0, 1, validOrderCreatedEnvelope("ETH-USDT"))
	routed, err := consumer.HandleOrderCommand(msg)
	if err == nil {
		t.Fatal("expected error on partition key mismatch")
	}
	if routed {
		t.Fatal("expected routed=false on error")
	}
}

func TestHandleOrderCommand_InvalidEventID(t *testing.T) {
	consumer := kafkapkg.NewTestableConsumer(nil)
	env := validOrderCreatedEnvelope("BTC-USDT")
	env["event_id"] = "not-a-valid-uuid"
	msg := makeCommandMsg("BTC-USDT", 0, 1, env)
	routed, err := consumer.HandleOrderCommand(msg)
	if err == nil {
		t.Fatal("expected error on invalid event_id")
	}
	if routed {
		t.Fatal("expected routed=false on error")
	}
}

func TestHandleOrderCommand_UnsupportedVersion(t *testing.T) {
	consumer := kafkapkg.NewTestableConsumer(nil)
	env := validOrderCreatedEnvelope("BTC-USDT")
	env["event_version"] = 99
	msg := makeCommandMsg("BTC-USDT", 0, 1, env)
	routed, err := consumer.HandleOrderCommand(msg)
	if err == nil {
		t.Fatal("expected error on unsupported event_version")
	}
	if routed {
		t.Fatal("expected routed=false on error")
	}
}

func TestHandleOrderCommand_UnknownMarket_Fails(t *testing.T) {
	consumer := kafkapkg.NewTestableConsumer(func(marketID string) chan market.InputEvent {
		return nil // unknown market
	})

	msg := makeCommandMsg("XYZ-USDT", 0, 1, validOrderCreatedEnvelope("XYZ-USDT"))
	routed, err := consumer.HandleOrderCommand(msg)
	if err == nil {
		t.Fatal("expected error for unknown market")
	}
	if routed {
		t.Fatal("expected routed=false on error")
	}
}

func TestHandleOrderCommand_MalformedJSON(t *testing.T) {
	consumer := kafkapkg.NewTestableConsumer(nil)
	msg := kafka.Message{
		Topic: kafkapkg.TopicOrderCommands,
		Key:   []byte("BTC-USDT"),
		Value: []byte(`{corrupt json`),
	}
	routed, err := consumer.HandleOrderCommand(msg)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if routed {
		t.Fatal("expected routed=false on error")
	}
}

type mockpgxRow struct {
	val int64
	err error
}

func (r *mockpgxRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*int64)) = r.val
	return nil
}

type mockConsumerDB struct {
	checkpoints map[string]int64
}

func (db *mockConsumerDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	topic := args[0].(string)
	partition := args[1].(int)
	key := fmt.Sprintf("%s/%d", topic, partition)
	if val, ok := db.checkpoints[key]; ok {
		return &mockpgxRow{val: val}
	}
	return &mockpgxRow{err: fmt.Errorf("no row")}
}

type mockOffsetTracker struct{}

func (m *mockOffsetTracker) Track(pos orderbook.KafkaPosition) {}
func (m *mockOffsetTracker) MarkDone(ctx context.Context, pos orderbook.KafkaPosition) error {
	return nil
}

func TestConsumer_SeekToPostgresCheckpoints(t *testing.T) {
	db := &mockConsumerDB{
		checkpoints: map[string]int64{
			"orders.commands/0": 100,
			"orders.commands/1": 200,
		},
	}

	manager := market.NewMarketManager()
	tracker := &mockOffsetTracker{}

	consumer := kafkapkg.NewConsumer(kafkapkg.Config{
		Brokers: []string{"localhost:9092"},
		GroupID: "test-group",
		DB:      db,
	}, manager, tracker)

	// Mock partition discovery
	var committedOffset []int64
	var committedPartition []int
	consumer.OverrideDiscoveryAndCommit(
		func(topic string) ([]int, error) {
			return []int{0, 1}, nil
		},
		func(ctx context.Context, brokers []string, topic string, groupID string, partition int, offset int64) error {
			committedPartition = append(committedPartition, partition)
			committedOffset = append(committedOffset, offset)
			return nil
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	consumer.Start(ctx, func() {})

	if len(committedOffset) != 2 {
		t.Fatalf("expected 2 offset commits, got %d", len(committedOffset))
	}
	if committedPartition[0] != 0 || committedOffset[0] != 100 {
		t.Errorf("expected partition 0 to seek to 100, got partition %d at %d", committedPartition[0], committedOffset[0])
	}
	if committedPartition[1] != 1 || committedOffset[1] != 200 {
		t.Errorf("expected partition 1 to seek to 200, got partition %d at %d", committedPartition[1], committedOffset[1])
	}
}

func TestConsumer_SeekToPostgresCheckpoints_Failure(t *testing.T) {
	db := &mockConsumerDB{
		checkpoints: map[string]int64{
			"orders.commands/0": 100,
		},
	}

	manager := market.NewMarketManager()
	tracker := &mockOffsetTracker{}

	consumer := kafkapkg.NewConsumer(kafkapkg.Config{
		Brokers: []string{"localhost:9092"},
		GroupID: "test-group",
		DB:      db,
	}, manager, tracker)

	// Mock partition discovery and make commit fail (Issue 2 / Test H)
	consumer.OverrideDiscoveryAndCommit(
		func(topic string) ([]int, error) {
			return []int{0}, nil
		},
		func(ctx context.Context, brokers []string, topic string, groupID string, partition int, offset int64) error {
			return fmt.Errorf("simulated broker offset commit failure")
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var cancelCalled bool
	cancelTracker := func() {
		cancelCalled = true
		cancel()
	}

	consumer.Start(ctx, cancelTracker)

	if !cancelCalled {
		t.Fatal("expected fail-stop cancellation on dynamic offset committed positioning failure")
	}
}

