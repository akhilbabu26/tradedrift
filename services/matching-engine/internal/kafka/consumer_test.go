package kafka_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
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
