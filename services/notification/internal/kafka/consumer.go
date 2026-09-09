package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"tradedrift/services/notification/internal/metrics"
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
// The goroutines run until ctx is cancelled. Use StartWithWaitGroup when you
// need the caller to wait for all goroutines to exit (e.g. in main).
func (c *Consumer) Start(ctx context.Context) {
	c.log.Info("Starting Kafka Consumer routines for notification topics")

	go c.consumeLoop(ctx, c.tradeReader, TopicTradesSettled, c.processTradeSettled)
	go c.consumeLoop(ctx, c.cancelReader, TopicOrdersCancelled, c.processOrderCancelled)
	go c.consumeLoop(ctx, c.portfolioReader, TopicPortfoliosUpdated, c.processPortfolioUpdated)
}

// StartWithWaitGroup is like Start but registers each goroutine with wg so the
// caller can call wg.Wait() to block until all consumer loops have exited after
// ctx cancellation. This makes graceful shutdown deterministic.
func (c *Consumer) StartWithWaitGroup(ctx context.Context, wg *sync.WaitGroup) {
	c.log.Info("Starting Kafka Consumer routines for notification topics (with WaitGroup)")

	topics := []struct {
		reader  MessageReader
		topic   string
		handler messageHandler
	}{
		{c.tradeReader, TopicTradesSettled, c.processTradeSettled},
		{c.cancelReader, TopicOrdersCancelled, c.processOrderCancelled},
		{c.portfolioReader, TopicPortfoliosUpdated, c.processPortfolioUpdated},
	}

	for _, t := range topics {
		wg.Add(1)
		t := t // capture loop variable
		go func() {
			defer wg.Done()
			c.consumeLoop(ctx, t.reader, t.topic, t.handler)
		}()
	}
}


type messageHandler func(ctx context.Context, msg kafka.Message) error

func (c *Consumer) consumeLoop(ctx context.Context, reader MessageReader, topic string, handler messageHandler) {
	c.log.Info("Consumer loop started", zap.String("topic", topic))

	for {
		// --- Fetch phase ---
		// FetchMessage advances the reader's local cursor. We must fully process
		// (or DLQ) this message before calling FetchMessage again. An inner retry
		// loop below guarantees that: the outer loop only iterates after a
		// successful commit or a fatal context cancellation.
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

		// --- Process phase (inner retry loop) ---
		// Retry the handler on the SAME fetched message until it succeeds or the
		// context is cancelled. Transient failures (DB down, network blip) must
		// not advance past this message — that would risk losing it on restart.
		backoff := 1 * time.Second
		for {
			if ctx.Err() != nil {
				c.log.Info("Consumer loop stopping during retry (context cancelled)", zap.String("topic", topic))
				return
			}

			processErr := handler(ctx, msg)
			if processErr == nil {
				// Handler succeeded (or poisoned to DLQ successfully): break inner loop.
				break
			}

			// Transient failure — do NOT advance to the next message.
			c.log.Error("Transient error processing Kafka event; will retry same message",
				zap.String("topic", topic),
				zap.Int("partition", msg.Partition),
				zap.Int64("offset", msg.Offset),
				zap.Duration("backoff", backoff),
				zap.Error(processErr),
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				// Exponential backoff capped at 30 s.
				if backoff < 30*time.Second {
					backoff *= 2
				}
			}
		}

		// --- Commit phase (retry loop) ---
		// At-least-once guarantee: commit the Kafka offset strictly AFTER the
		// handler has written to PostgreSQL. We do NOT fetch the next message
		// until the commit succeeds — a failed commit followed by a successful
		// commit of a later message can advance the group offset past an
		// uncommitted message, causing silent message loss on restart.
		for {
			if err := reader.CommitMessages(ctx, msg); err != nil {
				if ctx.Err() != nil {
					return
				}
				c.log.Error("Failed to commit Kafka offset; retrying commit",
					zap.String("topic", topic),
					zap.Int("partition", msg.Partition),
					zap.Int64("offset", msg.Offset),
					zap.Error(err),
				)
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
				continue
			}
			break
		}
		metrics.KafkaEventsConsumedTotal.WithLabelValues(topic, "success").Inc()
	}
}

func (c *Consumer) processTradeSettled(ctx context.Context, msg kafka.Message) error {
	var ev service.TradeSettledEvent
	if err := json.Unmarshal(msg.Value, &ev); err != nil {
		return c.handlePoison(ctx, msg, TopicTradesSettled, fmt.Sprintf("unmarshal error: %v", err))
	}
	if err := ev.Validate(); err != nil {
		return c.handlePoison(ctx, msg, TopicTradesSettled, err.Error())
	}
	return c.svc.HandleTradeSettled(ctx, &ev)
}

func (c *Consumer) processOrderCancelled(ctx context.Context, msg kafka.Message) error {
	var ev service.OrderCancelledEvent
	if err := json.Unmarshal(msg.Value, &ev); err != nil {
		return c.handlePoison(ctx, msg, TopicOrdersCancelled, fmt.Sprintf("unmarshal error: %v", err))
	}
	if err := ev.Validate(); err != nil {
		return c.handlePoison(ctx, msg, TopicOrdersCancelled, err.Error())
	}
	return c.svc.HandleOrderCancelled(ctx, &ev)
}

func (c *Consumer) processPortfolioUpdated(ctx context.Context, msg kafka.Message) error {
	var ev service.PortfolioUpdatedEvent
	if err := json.Unmarshal(msg.Value, &ev); err != nil {
		return c.handlePoison(ctx, msg, TopicPortfoliosUpdated, fmt.Sprintf("unmarshal error: %v", err))
	}
	if err := ev.Validate(); err != nil {
		return c.handlePoison(ctx, msg, TopicPortfoliosUpdated, err.Error())
	}
	return c.svc.HandlePortfolioUpdated(ctx, &ev)
}

// handlePoison routes a permanently-invalid message to the DLQ.
//
// Return value semantics:
//   - nil  → DLQ write succeeded; caller should commit the Kafka offset and move on.
//   - err  → DLQ write failed; caller must NOT commit the offset so the message is
//     retried by the inner retry loop (and the DLQ write is attempted again).
func (c *Consumer) handlePoison(ctx context.Context, msg kafka.Message, topic, reason string) error {
	c.log.Error("Routing poison message to DLQ",
		zap.String("topic", topic),
		zap.Int("partition", msg.Partition),
		zap.Int64("offset", msg.Offset),
		zap.String("reason", reason),
		zap.Int("payload_bytes", len(msg.Value)), // size only — raw payload omitted to avoid leaking user data
	)
	metrics.KafkaEventsConsumedTotal.WithLabelValues(topic, "poison").Inc()

	if c.dlqWriter == nil {
		// DLQ writer is mandatory. Returning an error here keeps the Kafka offset
		// uncommitted so the message is retried indefinitely. This forces the
		// operator to either configure a DLQ topic or restart with DLQ disabled
		// intentionally — there is no silent data loss path.
		return fmt.Errorf("DLQ writer is not configured: cannot route poison message (topic=%s offset=%d reason=%s)",
			topic, msg.Offset, reason)
	}

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
		// Propagate the error: the inner retry loop will back off and retry
		// this message (including the DLQ write) without committing the offset.
		return fmt.Errorf("write poison message to DLQ (topic=%s offset=%d): %w", topic, msg.Offset, err)
	}
	return nil
}

// Close gracefully closes all Kafka readers and writers.
// All resources are closed regardless of individual errors; all errors are returned joined.
func (c *Consumer) Close() error {
	var errs []error

	if c.tradeReader != nil {
		if err := c.tradeReader.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close trade reader: %w", err))
		}
	}
	if c.cancelReader != nil {
		if err := c.cancelReader.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close cancel reader: %w", err))
		}
	}
	if c.portfolioReader != nil {
		if err := c.portfolioReader.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close portfolio reader: %w", err))
		}
	}
	if c.dlqWriter != nil {
		if err := c.dlqWriter.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close dlq writer: %w", err))
		}
	}

	return errors.Join(errs...)
}

