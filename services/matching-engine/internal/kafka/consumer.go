package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/shopspring/decimal"

	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/orderbook"
)

// Topics the ME consumes from — published by Order Service outbox.
const (
	TopicOrderCreated = "orders.submitted"
	TopicOrderCancel  = "orders.cancel-requested"
)

// orderCreatedMessage matches the JSON payload published by Order Service
// for the "OrderCreated" outbox event.
// Fields match exactly: services/order/internal/service/service.go lines 161–171
type orderCreatedMessage struct {
	OrderID   string `json:"order_id"`
	UserID    string `json:"user_id"`
	MarketID  string `json:"market_id"`
	Side      string `json:"side"`
	OrderType string `json:"order_type"`
	Price     string `json:"price"`    // decimal serialised as string
	Quantity  string `json:"quantity"` // decimal serialised as string
}

// orderCancelMessage matches the JSON payload published by Order Service
// for the "OrderCancelRequested" outbox event.
// Fields match exactly: services/order/internal/service/service.go lines 227–232
type orderCancelMessage struct {
	OrderID  string `json:"order_id"`
	UserID   string `json:"user_id"`
	MarketID string `json:"market_id"`
}

// routeFunc is a function that returns the InputQueue channel for a given
// market ID, or nil if the market is unknown. Used to decouple routing from
// MarketManager so handlers can be tested without a real manager.
type routeFunc func(marketID string) chan market.InputEvent

// offsetTracker tracks in-flight Kafka offsets for contiguous checkpointing.
type offsetTracker interface {
	Track(pos orderbook.KafkaPosition)
	MarkDone(ctx context.Context, pos orderbook.KafkaPosition) error
}

type kafkaCommitter interface {
	CommitMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// ─── Consumer ─────────────────────────────────────────────────────────────────

// Consumer reads from both Kafka topics and routes InputEvents to the correct
// MarketEngine via MarketManager.
type Consumer struct {
	createdReader *kafkago.Reader
	cancelReader  *kafkago.Reader
	manager       *market.MarketManager
	tracker       offsetTracker
}

// Config holds Kafka connection settings for the consumer.
type Config struct {
	Brokers []string
	GroupID string
}

// NewConsumer creates a Consumer connected to both Kafka topics.
func NewConsumer(cfg Config, manager *market.MarketManager, tracker offsetTracker) *Consumer {
	c := &Consumer{
		manager: manager,
		tracker: tracker,
		createdReader: kafkago.NewReader(kafkago.ReaderConfig{
			Brokers:        cfg.Brokers,
			Topic:          TopicOrderCreated,
			GroupID:        cfg.GroupID,
			MinBytes:       1,
			MaxBytes:       10e6, // 10 MB
			MaxWait:        1 * time.Second,
			CommitInterval: 0,
		}),
		cancelReader: kafkago.NewReader(kafkago.ReaderConfig{
			Brokers:        cfg.Brokers,
			Topic:          TopicOrderCancel,
			GroupID:        cfg.GroupID,
			MinBytes:       1,
			MaxBytes:       10e6,
			MaxWait:        1 * time.Second,
			CommitInterval: 0,
		}),
	}

	if committerReg, ok := tracker.(interface {
		RegisterCommitter(topic string, committer kafkaCommitter)
	}); ok {
		committerReg.RegisterCommitter(TopicOrderCreated, c.createdReader)
		committerReg.RegisterCommitter(TopicOrderCancel, c.cancelReader)
	}

	return c
}

// Start launches two goroutines — one per topic — that block until ctx is cancelled.
// MUST be called only AFTER all MarketEngines have completed recovery and are in ModeLive.
func (c *Consumer) Start(ctx context.Context) {
	go c.consume(ctx, c.createdReader, c.handleOrderCreated)
	go c.consume(ctx, c.cancelReader, c.handleOrderCancel)
}

// Close shuts down both Kafka readers cleanly.
func (c *Consumer) Close() error {
	err1 := c.createdReader.Close()
	err2 := c.cancelReader.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// consume is the generic read loop. Calls handler for each message.
// Exits cleanly when ctx is cancelled.
func (c *Consumer) consume(
	ctx context.Context,
	reader *kafkago.Reader,
	handler func(msg kafkago.Message) (bool, error),
) {
	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return // context cancelled — graceful shutdown
			}
			log.Printf("[kafka] fetch error: %v — retrying in 500ms", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		pos := orderbook.KafkaPosition{
			Topic:     msg.Topic,
			Partition: msg.Partition,
			Offset:    msg.Offset,
		}

		// Register in-flight offset in coordinator before routing
		if c.tracker != nil {
			c.tracker.Track(pos)
		}

		routed, err := handler(msg)
		if err != nil {
			log.Printf("[kafka] handle message error (topic=%s partition=%d offset=%d): %v",
				msg.Topic, msg.Partition, msg.Offset, err)
		}

		// If the message was NOT dispatched to a MarketEngine (e.g. unknown market or malformed/poison event),
		// immediately acknowledge it so the contiguous checkpoint coordinator does NOT stall forever!
		if !routed && c.tracker != nil {
			if err := c.tracker.MarkDone(ctx, pos); err != nil {
				log.Printf("[kafka] checkpoint auto-acknowledgment error (topic=%s partition=%d offset=%d): %v",
					pos.Topic, pos.Partition, pos.Offset, err)
			}
		}
	}
}

// handleOrderCreated is the Consumer's method wrapper — delegates to the
// package-level function with a route derived from MarketManager.
func (c *Consumer) handleOrderCreated(msg kafkago.Message) (bool, error) {
	return HandleOrderCreated(msg, func(marketID string) chan market.InputEvent {
		engine := c.manager.Get(marketID)
		if engine == nil {
			return nil
		}
		return engine.InputQueue
	})
}

// handleOrderCancel is the Consumer's method wrapper — delegates to the
// package-level function with a route derived from MarketManager.
func (c *Consumer) handleOrderCancel(msg kafkago.Message) (bool, error) {
	return HandleOrderCancel(msg, func(marketID string) chan market.InputEvent {
		engine := c.manager.Get(marketID)
		if engine == nil {
			return nil
		}
		return engine.InputQueue
	})
}

// ─── Package-level handlers (testable without a real Kafka connection) ────────

// HandleOrderCreated deserialises an OrderCreated Kafka message, validates all
// fields, and sends an InputEvent to the correct engine's InputQueue.
// Returns (routed bool, err error).
// Exported so the recovery Replayer can reuse the same logic during event replay.
func HandleOrderCreated(msg kafkago.Message, route routeFunc) (bool, error) {
	var payload orderCreatedMessage
	if err := json.Unmarshal(msg.Value, &payload); err != nil {
		return false, fmt.Errorf("unmarshal OrderCreated: %w", err)
	}

	orderID, err := uuid.Parse(payload.OrderID)
	if err != nil {
		return false, fmt.Errorf("invalid order_id %q: %w", payload.OrderID, err)
	}
	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return false, fmt.Errorf("invalid user_id %q: %w", payload.UserID, err)
	}
	price, err := decimal.NewFromString(payload.Price)
	if err != nil {
		return false, fmt.Errorf("invalid price %q: %w", payload.Price, err)
	}
	quantity, err := decimal.NewFromString(payload.Quantity)
	if err != nil {
		return false, fmt.Errorf("invalid quantity %q: %w", payload.Quantity, err)
	}
	side, err := parseSide(payload.Side)
	if err != nil {
		return false, err
	}
	orderType, err := parseOrderType(payload.OrderType)
	if err != nil {
		return false, err
	}

	queue := route(payload.MarketID)
	if queue == nil {
		// Unknown market — skip silently.
		log.Printf("[kafka] unknown market_id %q — skipping order %s", payload.MarketID, payload.OrderID)
		return false, nil
	}

	queue <- market.InputEvent{
		Type: market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   orderID,
			UserID:    userID,
			MarketID:  payload.MarketID,
			Side:      side,
			OrderType: orderType,
			Price:     price,
			Quantity:  quantity,
		},
		Topic:     msg.Topic,
		Partition: msg.Partition,
		Offset:    msg.Offset,
	}

	return true, nil
}

// HandleOrderCancel deserialises an OrderCancelRequested Kafka message and
// sends an InputEvent to the correct engine's InputQueue.
// Exported so the recovery Replayer can reuse the same logic during event replay.
func HandleOrderCancel(msg kafkago.Message, route routeFunc) (bool, error) {
	var payload orderCancelMessage
	if err := json.Unmarshal(msg.Value, &payload); err != nil {
		return false, fmt.Errorf("unmarshal OrderCancelRequested: %w", err)
	}

	orderID, err := uuid.Parse(payload.OrderID)
	if err != nil {
		return false, fmt.Errorf("invalid order_id %q: %w", payload.OrderID, err)
	}
	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return false, fmt.Errorf("invalid user_id %q: %w", payload.UserID, err)
	}

	queue := route(payload.MarketID)
	if queue == nil {
		log.Printf("[kafka] unknown market_id %q — skipping cancel %s", payload.MarketID, payload.OrderID)
		return false, nil
	}

	queue <- market.InputEvent{
		Type: market.EventOrderCancel,
		OrderCancel: &market.OrderCancelPayload{
			OrderID:  orderID,
			UserID:   userID,
			MarketID: payload.MarketID,
		},
		Topic:     msg.Topic,
		Partition: msg.Partition,
		Offset:    msg.Offset,
	}

	return true, nil
}

// ─── Parsers ──────────────────────────────────────────────────────────────────

func parseSide(s string) (orderbook.SideType, error) {
	switch s {
	case "BUY":
		return orderbook.SideBuy, nil
	case "SELL":
		return orderbook.SideSell, nil
	default:
		return "", fmt.Errorf("unknown side %q", s)
	}
}

func parseOrderType(s string) (orderbook.OrderType, error) {
	switch s {
	case "LIMIT":
		return orderbook.OrderTypeLimit, nil
	case "MARKET":
		return orderbook.OrderTypeMarket, nil
	default:
		return "", fmt.Errorf("unknown order_type %q", s)
	}
}

// ─── TestableConsumer (unit tests only) ───────────────────────────────────────

// TestableConsumer wraps the package-level handlers with an injectable routeFunc.
type TestableConsumer struct {
	route routeFunc
}

// NewTestableConsumer creates a TestableConsumer with the given routing function.
func NewTestableConsumer(route routeFunc) *TestableConsumer {
	return &TestableConsumer{route: route}
}

// HandleOrderCreated exposes the order-created handler for unit testing.
func (c *TestableConsumer) HandleOrderCreated(msg kafkago.Message) (bool, error) {
	return HandleOrderCreated(msg, c.route)
}

// HandleOrderCancel exposes the order-cancel handler for unit testing.
func (c *TestableConsumer) HandleOrderCancel(msg kafkago.Message) (bool, error) {
	return HandleOrderCancel(msg, c.route)
}
