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
	TopicOrderCommands = "orders.commands"
)

// CommandEnvelope represents the standard envelope for all messages on orders.commands.
type CommandEnvelope struct {
	EventID      string          `json:"event_id"`
	EventType    string          `json:"event_type"`
	EventVersion int             `json:"event_version"`
	MarketID     string          `json:"market_id"`
	OccurredAt   time.Time       `json:"occurred_at"`
	Payload      json.RawMessage `json:"payload"`
}

type orderCreatedPayload struct {
	OrderID   string `json:"order_id"`
	UserID    string `json:"user_id"`
	Side      string `json:"side"`
	OrderType string `json:"order_type"`
	Price     string `json:"price"`
	Quantity  string `json:"quantity"`
}

type orderCancelPayload struct {
	OrderID string `json:"order_id"`
	UserID  string `json:"user_id"`
}

type routeFunc func(marketID string) chan market.InputEvent

type offsetTracker interface {
	Track(pos orderbook.KafkaPosition)
	MarkDone(ctx context.Context, pos orderbook.KafkaPosition) error
}

type kafkaCommitter interface {
	CommitMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// Consumer reads from the orders.commands Kafka topic and routes InputEvents.
type Consumer struct {
	commandReader *kafkago.Reader
	manager       *market.MarketManager
	tracker       offsetTracker
	cancelCtx     context.CancelFunc // context cancel func to fail closed gracefully (Issue #10)
}

type Config struct {
	Brokers []string
	GroupID string
}

func NewConsumer(cfg Config, manager *market.MarketManager, tracker offsetTracker) *Consumer {
	c := &Consumer{
		manager: manager,
		tracker: tracker,
		commandReader: kafkago.NewReader(kafkago.ReaderConfig{
			Brokers:        cfg.Brokers,
			Topic:          TopicOrderCommands,
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
		committerReg.RegisterCommitter(TopicOrderCommands, c.commandReader)
	}

	return c
}

// Start launches the consumer read loop.
func (c *Consumer) Start(ctx context.Context, cancel context.CancelFunc) {
	c.cancelCtx = cancel
	go c.consume(ctx, c.commandReader, c.handleOrderCommand)
}

func (c *Consumer) Close() error {
	return c.commandReader.Close()
}

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

		if c.tracker != nil {
			c.tracker.Track(pos)
		}

		routed, err := handler(msg)
		if err != nil {
			// INVARIANT (Issue #10): Fail-Closed policy on live malformed command.
			// Trigger graceful shutdown immediately to prevent divergence or CPU spin loops.
			log.Printf("[kafka] FATAL: malformed command (topic=%s partition=%d offset=%d): %v — initiating fail-closed graceful shutdown",
				msg.Topic, msg.Partition, msg.Offset, err)
			if c.cancelCtx != nil {
				c.cancelCtx()
			}
			return
		}

		// INTENTIONAL SKIP: unknown market_id.
		if !routed && c.tracker != nil {
			if err := c.tracker.MarkDone(ctx, pos); err != nil {
				log.Printf("[kafka] checkpoint skip-acknowledgment error (topic=%s partition=%d offset=%d): %v",
					pos.Topic, pos.Partition, pos.Offset, err)
			}
		}
	}
}

func (c *Consumer) handleOrderCommand(msg kafkago.Message) (bool, error) {
	return HandleOrderCommand(msg, func(marketID string) chan market.InputEvent {
		engine := c.manager.Get(marketID)
		if engine == nil {
			return nil
		}
		return engine.InputQueue
	})
}

func HandleOrderCommand(msg kafkago.Message, route routeFunc) (bool, error) {
	var env CommandEnvelope
	if err := json.Unmarshal(msg.Value, &env); err != nil {
		return false, fmt.Errorf("unmarshal CommandEnvelope: %w", err)
	}

	// 1. Partition key invariant:
	if len(msg.Key) == 0 {
		return false, fmt.Errorf("missing partition key: all orders.commands messages must carry key=market_id")
	}
	if string(msg.Key) != env.MarketID {
		return false, fmt.Errorf("partition key mismatch: key=%q envelope.market_id=%q", string(msg.Key), env.MarketID)
	}

	// 2. Event ID validation
	eventUUID, err := uuid.Parse(env.EventID)
	if err != nil || env.EventID == "" {
		return false, fmt.Errorf("invalid event_id %q: %w", env.EventID, err)
	}

	// 3. Schema version check
	if env.EventVersion != 1 {
		return false, fmt.Errorf("unsupported event_version %d (expected 1)", env.EventVersion)
	}

	queue := route(env.MarketID)
	if queue == nil {
		return false, fmt.Errorf("unknown market_id %q — command rejected", env.MarketID)
	}

	switch env.EventType {
	case "OrderCreated":
		var p orderCreatedPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return false, fmt.Errorf("unmarshal OrderCreated payload: %w", err)
		}

		orderID, err := uuid.Parse(p.OrderID)
		if err != nil {
			return false, fmt.Errorf("invalid order_id %q: %w", p.OrderID, err)
		}
		userID, err := uuid.Parse(p.UserID)
		if err != nil {
			return false, fmt.Errorf("invalid user_id %q: %w", p.UserID, err)
		}
		price, err := decimal.NewFromString(p.Price)
		if err != nil {
			return false, fmt.Errorf("invalid price %q: %w", p.Price, err)
		}
		quantity, err := decimal.NewFromString(p.Quantity)
		if err != nil {
			return false, fmt.Errorf("invalid quantity %q: %w", p.Quantity, err)
		}
		side, err := parseSide(p.Side)
		if err != nil {
			return false, err
		}
		orderType, err := parseOrderType(p.OrderType)
		if err != nil {
			return false, err
		}

		queue <- market.InputEvent{
			EventID: eventUUID,
			Type:    market.EventOrderCreated,
			OrderCreated: &market.OrderCreatedPayload{
				OrderID:   orderID,
				UserID:    userID,
				MarketID:  env.MarketID,
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

	case "OrderCancelRequested":
		var p orderCancelPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return false, fmt.Errorf("unmarshal OrderCancelRequested payload: %w", err)
		}

		orderID, err := uuid.Parse(p.OrderID)
		if err != nil {
			return false, fmt.Errorf("invalid order_id %q: %w", p.OrderID, err)
		}
		userID, err := uuid.Parse(p.UserID)
		if err != nil {
			return false, fmt.Errorf("invalid user_id %q: %w", p.UserID, err)
		}

		queue <- market.InputEvent{
			EventID: eventUUID,
			Type:    market.EventOrderCancel,
			OrderCancel: &market.OrderCancelPayload{
				OrderID:  orderID,
				UserID:   userID,
				MarketID: env.MarketID,
			},
			Topic:     msg.Topic,
			Partition: msg.Partition,
			Offset:    msg.Offset,
		}
		return true, nil

	default:
		return false, fmt.Errorf("unknown event_type %q", env.EventType)
	}
}

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

type TestableConsumer struct {
	route routeFunc
}

func NewTestableConsumer(route routeFunc) *TestableConsumer {
	return &TestableConsumer{route: route}
}

func (c *TestableConsumer) HandleOrderCommand(msg kafkago.Message) (bool, error) {
	return HandleOrderCommand(msg, c.route)
}
