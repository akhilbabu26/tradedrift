package kafka_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	kafkapkg "tradedrift/services/matching-engine/internal/kafka"
	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/orderbook"
)

// ─── Mock MarketManager ───────────────────────────────────────────────────────

type mockEngine struct {
	received []market.InputEvent
}

func (e *mockEngine) send(event market.InputEvent) {
	e.received = append(e.received, event)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func makeMsg(topic string, partition int, offset int64, body any) kafka.Message {
	b, _ := json.Marshal(body)
	return kafka.Message{
		Topic:     topic,
		Partition: partition,
		Offset:    offset,
		Value:     b,
	}
}

func validOrderCreatedBody(marketID string) map[string]any {
	return map[string]any{
		"order_id":   uuid.New().String(),
		"user_id":    uuid.New().String(),
		"market_id":  marketID,
		"side":       "BUY",
		"order_type": "LIMIT",
		"price":      "100.50",
		"quantity":   "1.5",
	}
}

func validOrderCancelBody(marketID string) map[string]any {
	return map[string]any{
		"order_id":  uuid.New().String(),
		"user_id":   uuid.New().String(),
		"market_id": marketID,
	}
}

// ─── OrderCreated Tests ───────────────────────────────────────────────────────

func TestHandleOrderCreated_Valid(t *testing.T) {
	routedChan := make(chan market.InputEvent, 1)
	consumer := kafkapkg.NewTestableConsumer(func(marketID string) chan market.InputEvent {
		if marketID == "BTC-USDT" {
			return routedChan
		}
		return nil
	})

	msg := makeMsg("orders.submitted", 0, 42, validOrderCreatedBody("BTC-USDT"))
	routed, err := consumer.HandleOrderCreated(msg)
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
	if event.Topic != "orders.submitted" {
		t.Fatalf("expected topic=orders.submitted, got %s", event.Topic)
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

func TestHandleOrderCreated_MalformedJSON(t *testing.T) {
	consumer := kafkapkg.NewTestableConsumer(nil)
	msg := kafka.Message{Topic: "orders.submitted", Value: []byte(`{bad json`)}
	routed, err := consumer.HandleOrderCreated(msg)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if routed {
		t.Fatal("expected routed=false for malformed JSON")
	}
}

func TestHandleOrderCreated_InvalidOrderID(t *testing.T) {
	consumer := kafkapkg.NewTestableConsumer(nil)
	body := validOrderCreatedBody("BTC-USDT")
	body["order_id"] = "not-a-uuid"
	msg := makeMsg("orders.submitted", 0, 1, body)
	routed, err := consumer.HandleOrderCreated(msg)
	if err == nil {
		t.Fatal("expected error for invalid order_id UUID")
	}
	if routed {
		t.Fatal("expected routed=false for invalid order_id")
	}
}

func TestHandleOrderCreated_InvalidUserID(t *testing.T) {
	consumer := kafkapkg.NewTestableConsumer(nil)
	body := validOrderCreatedBody("BTC-USDT")
	body["user_id"] = "not-a-uuid"
	msg := makeMsg("orders.submitted", 0, 1, body)
	routed, err := consumer.HandleOrderCreated(msg)
	if err == nil {
		t.Fatal("expected error for invalid user_id UUID")
	}
	if routed {
		t.Fatal("expected routed=false for invalid user_id")
	}
}

func TestHandleOrderCreated_InvalidDecimalPrice(t *testing.T) {
	consumer := kafkapkg.NewTestableConsumer(nil)
	body := validOrderCreatedBody("BTC-USDT")
	body["price"] = "not-a-number"
	msg := makeMsg("orders.submitted", 0, 1, body)
	routed, err := consumer.HandleOrderCreated(msg)
	if err == nil {
		t.Fatal("expected error for invalid price decimal")
	}
	if routed {
		t.Fatal("expected routed=false for invalid price")
	}
}

func TestHandleOrderCreated_InvalidDecimalQuantity(t *testing.T) {
	consumer := kafkapkg.NewTestableConsumer(nil)
	body := validOrderCreatedBody("BTC-USDT")
	body["quantity"] = "abc"
	msg := makeMsg("orders.submitted", 0, 1, body)
	routed, err := consumer.HandleOrderCreated(msg)
	if err == nil {
		t.Fatal("expected error for invalid quantity decimal")
	}
	if routed {
		t.Fatal("expected routed=false for invalid quantity")
	}
}

func TestHandleOrderCreated_InvalidSide(t *testing.T) {
	consumer := kafkapkg.NewTestableConsumer(nil)
	body := validOrderCreatedBody("BTC-USDT")
	body["side"] = "LONG"
	msg := makeMsg("orders.submitted", 0, 1, body)
	routed, err := consumer.HandleOrderCreated(msg)
	if err == nil {
		t.Fatal("expected error for unknown side")
	}
	if routed {
		t.Fatal("expected routed=false for invalid side")
	}
}

func TestHandleOrderCreated_InvalidOrderType(t *testing.T) {
	consumer := kafkapkg.NewTestableConsumer(nil)
	body := validOrderCreatedBody("BTC-USDT")
	body["order_type"] = "STOP"
	msg := makeMsg("orders.submitted", 0, 1, body)
	routed, err := consumer.HandleOrderCreated(msg)
	if err == nil {
		t.Fatal("expected error for unknown order_type")
	}
	if routed {
		t.Fatal("expected routed=false for invalid order_type")
	}
}

func TestHandleOrderCreated_UnknownMarket_Skipped(t *testing.T) {
	consumer := kafkapkg.NewTestableConsumer(func(marketID string) chan market.InputEvent {
		return nil // no engine for any market
	})
	body := validOrderCreatedBody("XYZ-USDT")
	msg := makeMsg("orders.submitted", 0, 1, body)
	routed, err := consumer.HandleOrderCreated(msg)
	if err != nil {
		t.Fatalf("unknown market must not error, got: %v", err)
	}
	if routed {
		t.Fatal("unknown market must have routed=false so consumer auto-acknowledges checkpoint")
	}
}

// ─── OrderCancel Tests ────────────────────────────────────────────────────────

func TestHandleOrderCancel_Valid(t *testing.T) {
	routedChan := make(chan market.InputEvent, 1)
	consumer := kafkapkg.NewTestableConsumer(func(marketID string) chan market.InputEvent {
		if marketID == "BTC-USDT" {
			return routedChan
		}
		return nil
	})

	msg := makeMsg("orders.cancel-requested", 0, 99, validOrderCancelBody("BTC-USDT"))
	routed, err := consumer.HandleOrderCancel(msg)
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
	if event.Offset != 99 {
		t.Fatalf("expected offset=99, got %d", event.Offset)
	}
	if event.Topic != "orders.cancel-requested" {
		t.Fatalf("expected topic=orders.cancel-requested")
	}
}

func TestHandleOrderCancel_MalformedJSON(t *testing.T) {
	consumer := kafkapkg.NewTestableConsumer(nil)
	msg := kafka.Message{Topic: "orders.cancel-requested", Value: []byte(`{`)}
	routed, err := consumer.HandleOrderCancel(msg)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if routed {
		t.Fatal("expected routed=false for malformed JSON")
	}
}

func TestHandleOrderCancel_UnknownMarket_Skipped(t *testing.T) {
	consumer := kafkapkg.NewTestableConsumer(func(marketID string) chan market.InputEvent {
		return nil
	})
	msg := makeMsg("orders.cancel-requested", 0, 1, validOrderCancelBody("XYZ-USDT"))
	routed, err := consumer.HandleOrderCancel(msg)
	if err != nil {
		t.Fatalf("unknown market cancel must not error, got: %v", err)
	}
	if routed {
		t.Fatal("unknown market cancel must have routed=false so consumer auto-acknowledges checkpoint")
	}
}
