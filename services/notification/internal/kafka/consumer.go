package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"tradedrift/services/notification/internal/service"
)

const (
	TopicTradesSettled    = "trades.settled.v1"
	TopicOrdersCancelled  = "orders.cancelled.v1"
	TopicPortfoliosUpdated = "portfolios.updated.v1"
	TopicDLQ              = "notifications.dlq"
)

// NotificationService abstracts the domain handlers for Kafka consumption.
type NotificationService interface {
	HandleTradeSettled(ctx context.Context, ev *service.TradeSettledEvent) error
	HandleOrderCancelled(ctx context.Context, ev *service.OrderCancelledEvent) error
	HandlePortfolioUpdated(ctx context.Context, ev *service.PortfolioUpdatedEvent) error
}

// MessageReader abstracts kafka.Reader for unit testing.
type MessageReader interface {
	FetchMessage(ctx context.Context) (kafka.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafka.Message) error
	Close() error
}

// DLQWriter abstracts kafka.Writer for writing poison messages.
type DLQWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
	Close() error
}

// ConsumerConfig holds connection and tuning parameters.
type ConsumerConfig struct {
	Brokers  []string
	GroupID  string
	DLQTopic string
}

// Consumer consumes domain events from Kafka, dispatches to NotificationService,
// and enforces at-least-once processing by committing offsets strictly after DB persistence.
type Consumer struct {
	tradeReader     MessageReader
	cancelReader    MessageReader
	portfolioReader MessageReader
	dlqWriter       DLQWriter
	svc             NotificationService
	log             *zap.Logger
}

// NewConsumer creates a new Kafka Consumer with readers for each domain topic.
func NewConsumer(cfg ConsumerConfig, svc NotificationService, log *zap.Logger) *Consumer {
	dlqTopic := cfg.DLQTopic
	if dlqTopic == "" {
		dlqTopic = TopicDLQ
	}

	tradeReader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  cfg.Brokers,
		GroupID:  cfg.GroupID,
		Topic:    TopicTradesSettled,
		MinBytes: 10e3, // 10KB
		MaxBytes: 10e6, // 10MB
	})

	cancelReader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  cfg.Brokers,
		GroupID:  cfg.GroupID,
		Topic:    TopicOrdersCancelled,
		MinBytes: 10e3,
		MaxBytes: 10e6,
	})

	portfolioReader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  cfg.Brokers,
		GroupID:  cfg.GroupID,
		Topic:    TopicPortfoliosUpdated,
		MinBytes: 10e3,
		MaxBytes: 10e6,
	})

	dlqWriter := &kafka.Writer{
		Addr:         kafka.TCP(cfg.Brokers...),
		Topic:        dlqTopic,
		Balancer:     &kafka.LeastBytes{},
		BatchTimeout: 10 * time.Millisecond,
	}

	return &Consumer{
		tradeReader:     tradeReader,
		cancelReader:    cancelReader,
		portfolioReader: portfolioReader,
		dlqWriter:       dlqWriter,
		svc:             svc,
		log:             log,
	}
}

// NewConsumerWithMocks enables testing with injected mock readers and writers.
func NewConsumerWithMocks(
	tradeReader, cancelReader, portfolioReader MessageReader,
	dlqWriter DLQWriter,
	svc NotificationService,
	log *zap.Logger,
) *Consumer {
	return &Consumer{
		tradeReader:     tradeReader,
		cancelReader:    cancelReader,
		portfolioReader: portfolioReader,
		dlqWriter:       dlqWriter,
		svc:             svc,
		log:             log,
	}
}

// Start spawns background consumption goroutines for each topic.
func (c *Consumer) Start(ctx context.Context) {
	c.log.Info("Starting Kafka Consumer routines for notification topics")

	go c.consumeLoop(ctx, c.tradeReader, TopicTradesSettled, c.processTradeSettled)
	go c.consumeLoop(ctx, c.cancelReader, TopicOrdersCancelled, c.processOrderCancelled)
	go c.consumeLoop(ctx, c.portfolioReader, TopicPortfoliosUpdated, c.processPortfolioUpdated)
}

type messageHandler func(ctx context.Context, msg kafka.Message) error

func (c *Consumer) consumeLoop(ctx context.Context, reader MessageReader, topic string, handler messageHandler) {
	c.log.Info("Consumer loop started", zap.String("topic", topic))

	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				c.log.Info("Consumer loop stopping (context cancelled)", zap.String("topic", topic))
				return
			}
			c.log.Error("Failed to fetch Kafka message", zap.String("topic", topic), zap.Error(err))
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
				continue
			}
		}

		// Process message
		if err := handler(ctx, msg); err != nil {
			// Transient ingestion failure: DB connection drop, transaction conflict, etc.
			// Do NOT commit Kafka offset. Back off and retry fetching the same message.
			c.log.Error("Transient error processing Kafka event; offset will not be committed and will retry",
				zap.String("topic", topic),
				zap.Int("partition", msg.Partition),
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
				continue
			}
		}

		// At-least-once guarantee: commit Kafka offset strictly after PostgreSQL transaction success
		if err := reader.CommitMessages(ctx, msg); err != nil {
			c.log.Error("Failed to commit Kafka offset",
				zap.String("topic", topic),
				zap.Int("partition", msg.Partition),
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
		}
	}
}

func (c *Consumer) processTradeSettled(ctx context.Context, msg kafka.Message) error {
	var ev service.TradeSettledEvent
	if err := json.Unmarshal(msg.Value, &ev); err != nil {
		c.handlePoison(ctx, msg, TopicTradesSettled, fmt.Sprintf("unmarshal error: %v", err))
		return nil
	}

	if ev.TradeID == "" || ev.BuyerUserID == "" || ev.SellerUserID == "" {
		c.handlePoison(ctx, msg, TopicTradesSettled, "missing required fields (trade_id, buyer_user_id, seller_user_id)")
		return nil
	}

	return c.svc.HandleTradeSettled(ctx, &ev)
}

func (c *Consumer) processOrderCancelled(ctx context.Context, msg kafka.Message) error {
	var ev service.OrderCancelledEvent
	if err := json.Unmarshal(msg.Value, &ev); err != nil {
		c.handlePoison(ctx, msg, TopicOrdersCancelled, fmt.Sprintf("unmarshal error: %v", err))
		return nil
	}

	if ev.OrderID == "" || ev.UserID == "" {
		c.handlePoison(ctx, msg, TopicOrdersCancelled, "missing required fields (order_id, user_id)")
		return nil
	}

	return c.svc.HandleOrderCancelled(ctx, &ev)
}

func (c *Consumer) processPortfolioUpdated(ctx context.Context, msg kafka.Message) error {
	var ev service.PortfolioUpdatedEvent
	if err := json.Unmarshal(msg.Value, &ev); err != nil {
		c.handlePoison(ctx, msg, TopicPortfoliosUpdated, fmt.Sprintf("unmarshal error: %v", err))
		return nil
	}

	if ev.UserID == "" {
		c.handlePoison(ctx, msg, TopicPortfoliosUpdated, "missing required fields (user_id)")
		return nil
	}

	return c.svc.HandlePortfolioUpdated(ctx, &ev)
}

// handlePoison routes corrupted or unparseable messages to DLQ and commits their offset to avoid blockage.
func (c *Consumer) handlePoison(ctx context.Context, msg kafka.Message, topic, reason string) {
	c.log.Error("Routing poison message to DLQ",
		zap.String("topic", topic),
		zap.Int("partition", msg.Partition),
		zap.Int64("offset", msg.Offset),
		zap.String("reason", reason),
		zap.ByteString("payload", msg.Value),
	)

	if c.dlqWriter != nil {
		dlqMsg := kafka.Message{
			Key:   msg.Key,
			Value: msg.Value,
			Headers: []kafka.Header{
				{Key: "original-topic", Value: []byte(topic)},
				{Key: "error-reason", Value: []byte(reason)},
				{Key: "dlq-time", Value: []byte(time.Now().UTC().Format(time.RFC3339))},
			},
		}
		if err := c.dlqWriter.WriteMessages(ctx, dlqMsg); err != nil {
			c.log.Error("Failed writing poison message to DLQ", zap.Error(err))
		}
	}
}

// Close gracefully closes all Kafka readers and writers.
func (c *Consumer) Close() error {
	var firstErr error
	if c.tradeReader != nil {
		if err := c.tradeReader.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if c.cancelReader != nil {
		if err := c.cancelReader.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if c.portfolioReader != nil {
		if err := c.portfolioReader.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if c.dlqWriter != nil {
		if err := c.dlqWriter.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
