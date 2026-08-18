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

// ─── Consumer ─────────────────────────────────────────────────────────────────

// Consumer reads from both Kafka topics and routes InputEvents to the correct
// MarketEngine via MarketManager.
type Consumer struct {
	createdReader *kafkago.Reader
	cancelReader  *kafkago.Reader
	manager       *market.MarketManager
}

// Config holds Kafka connection settings for the consumer.
type Config struct {
	Brokers []string
	GroupID string
}

// NewConsumer creates a Consumer connected to both Kafka topics.
func NewConsumer(cfg Config, manager *market.MarketManager) *Consumer {
	return &Consumer{
		manager: manager,
		createdReader: kafkago.NewReader(kafkago.ReaderConfig{
			Brokers:        cfg.Brokers,
			Topic:          TopicOrderCreated,
			GroupID:        cfg.GroupID,
			MinBytes:       1,
			MaxBytes:       10e6,            // 10 MB
			MaxWait:        1 * time.Second,
			CommitInterval: 0, // manual — checkpoint is handled by Publisher via Postgres
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
	handler func(msg kafkago.Message) error,
) {
	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return // context cancelled — graceful shutdown
			}
			log.Printf("[kafka] FetchMessage error (topic=%s): %v", reader.Config().Topic, err)
			continue // transient error — retry
		}

		if err := handler(msg); err != nil {
			// Handler error — log and skip.
			// Offset is NOT advanced so the ME re-processes on restart.
			log.Printf("[kafka] handler error (topic=%s partition=%d offset=%d): %v",
				msg.Topic, msg.Partition, msg.Offset, err)
			continue
		}

		// NOTE: We intentionally do NOT call reader.CommitMessages() here.
		// The ME uses its own Postgres checkpoint (topic+partition+offset) as the
		// source of truth for recovery. Committing Kafka offsets separately would
		// create a second source of truth that could drift from the checkpoint.
	}
}

// handleOrderCreated is the Consumer's method wrapper — delegates to the
// package-level function with a route derived from MarketManager.
func (c *Consumer) handleOrderCreated(msg kafkago.Message) error {
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
func (c *Consumer) handleOrderCancel(msg kafkago.Message) error {
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
// Returns an error for any parse/validation failure. Unknown markets are skipped.
// Exported so the recovery Replayer can reuse the same logic during event replay.
func HandleOrderCreated(msg kafkago.Message, route routeFunc) error {
	var payload orderCreatedMessage
	if err := json.Unmarshal(msg.Value, &payload); err != nil {
		return fmt.Errorf("unmarshal OrderCreated: %w", err)
	}

	orderID, err := uuid.Parse(payload.OrderID)
	if err != nil {
		return fmt.Errorf("invalid order_id %q: %w", payload.OrderID, err)
	}
	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return fmt.Errorf("invalid user_id %q: %w", payload.UserID, err)
	}
	price, err := decimal.NewFromString(payload.Price)
	if err != nil {
		return fmt.Errorf("invalid price %q: %w", payload.Price, err)
	}
	quantity, err := decimal.NewFromString(payload.Quantity)
	if err != nil {
		return fmt.Errorf("invalid quantity %q: %w", payload.Quantity, err)
	}
	side, err := parseSide(payload.Side)
	if err != nil {
		return err
	}
	orderType, err := parseOrderType(payload.OrderType)
	if err != nil {
		return err
	}

	queue := route(payload.MarketID)
	if queue == nil {
		// Unknown market — skip silently. Crashing the consumer would halt ALL markets.
		log.Printf("[kafka] unknown market_id %q — skipping order %s", payload.MarketID, payload.OrderID)
		return nil
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
		Topic:     msg.Topic,     // ← full Kafka position
		Partition: msg.Partition, // ← partition is part of the checkpoint key
		Offset:    msg.Offset,
	}

	return nil
}

// HandleOrderCancel deserialises an OrderCancelRequested Kafka message and
// sends an InputEvent to the correct engine's InputQueue.
// Exported so the recovery Replayer can reuse the same logic during event replay.
func HandleOrderCancel(msg kafkago.Message, route routeFunc) error {
	var payload orderCancelMessage
	if err := json.Unmarshal(msg.Value, &payload); err != nil {
		return fmt.Errorf("unmarshal OrderCancelRequested: %w", err)
	}

	orderID, err := uuid.Parse(payload.OrderID)
	if err != nil {
		return fmt.Errorf("invalid order_id %q: %w", payload.OrderID, err)
	}
	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return fmt.Errorf("invalid user_id %q: %w", payload.UserID, err)
	}

	queue := route(payload.MarketID)
	if queue == nil {
		log.Printf("[kafka] unknown market_id %q — skipping cancel %s", payload.MarketID, payload.OrderID)
		return nil
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

	return nil
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
// This allows unit tests to call handlers directly without a real Kafka connection
// or a real MarketManager.
type TestableConsumer struct {
	route routeFunc
}

// NewTestableConsumer creates a TestableConsumer with the given routing function.
// Pass nil for route to test error paths where market lookup is irrelevant.
func NewTestableConsumer(route routeFunc) *TestableConsumer {
	return &TestableConsumer{route: route}
}

// HandleOrderCreated exposes the order-created handler for unit testing.
func (c *TestableConsumer) HandleOrderCreated(msg kafkago.Message) error {
	return HandleOrderCreated(msg, c.route)
}

// HandleOrderCancel exposes the order-cancel handler for unit testing.
func (c *TestableConsumer) HandleOrderCancel(msg kafkago.Message) error {
	return HandleOrderCancel(msg, c.route)
}
