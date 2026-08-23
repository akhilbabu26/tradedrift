package kafka

import (
	"context"
	"encoding/json"
	"fmt"

	kafkago "github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"tradedrift/services/settlement/internal/service"
)

// Consumer reads TradeExecuted events from Kafka and drives the 3-phase
// settlement pipeline for each message.
//
// Commit strategy:
//   - CommitMessages is called ONLY after Phase 3 (MarkSettled) succeeds.
//   - On any Phase 2 or Phase 3 failure, the message is NOT committed.
//     Kafka redelivers it on the next poll, which triggers idempotency check
//     and retries from the correct phase.
//   - Malformed payloads (poison pills) are committed immediately to unblock
//     the partition — the error is logged for investigation.
type Consumer struct {
	reader  *kafkago.Reader
	service *service.Service
	logger  *zap.Logger
}

// NewConsumer creates a Kafka consumer using a consumer group reader.
// The reader is configured to NOT auto-commit offsets — we commit manually
// after Phase 3 only.
func NewConsumer(brokers []string, groupID, topic string, svc *service.Service, log *zap.Logger) *Consumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:        brokers,
		GroupID:        groupID,
		Topic:          topic,
		MinBytes:       1,
		MaxBytes:       10e6,         // 10 MB
		CommitInterval: 0,            // disable auto-commit — we commit manually
		StartOffset:    kafkago.FirstOffset,
	})

	log.Info("Settlement Kafka consumer initialised",
		zap.Strings("brokers", brokers),
		zap.String("group_id", groupID),
		zap.String("topic", topic),
	)

	return &Consumer{
		reader:  reader,
		service: svc,
		logger:  log,
	}
}

// Start begins the sequential consume loop. Blocks until ctx is cancelled.
// Runs in its own goroutine — the main goroutine waits on the shutdown signal.
func (c *Consumer) Start(ctx context.Context) {
	c.logger.Info("Settlement consumer started — awaiting TradeExecuted events")

	for {
		// FetchMessage blocks until a message is available or ctx is cancelled.
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				// Clean shutdown — context cancelled by SIGINT/SIGTERM
				c.logger.Info("Settlement consumer shutting down")
				return
			}
			c.logger.Error("kafka fetch error", zap.Error(err))
			continue
		}

		c.logger.Debug("received TradeExecuted event",
			zap.Int64("offset", msg.Offset),
			zap.Int("partition", msg.Partition),
		)

		// Deserialise the event payload
		var event service.TradeExecutedEvent
		if err := json.Unmarshal(msg.Value, &event); err != nil {
			// Poison pill: malformed JSON — commit and skip to unblock the partition.
			// Log full payload for investigation. Do not crash the consumer.
			c.logger.Error("malformed TradeExecuted payload — skipping (poison pill)",
				zap.Int64("offset", msg.Offset),
				zap.Int("partition", msg.Partition),
				zap.String("raw", string(msg.Value)),
				zap.Error(err),
			)
			_ = c.commitMsg(ctx, msg)
			continue
		}

		// Run the 3-phase settlement pipeline
		if err := c.service.Settle(ctx, event); err != nil {
			// Settlement failed — do NOT commit offset.
			// Kafka will redeliver this message. The idempotency check in
			// service.Settle will determine which phase to resume from.
			c.logger.Error("settlement failed — offset not committed, will retry on redeliver",
				zap.String("trade_id", event.TradeID),
				zap.String("market", event.MarketID),
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
			continue
		}

		// ACK: commit the offset only after Phase 3 succeeds.
		if err := c.commitMsg(ctx, msg); err != nil {
			c.logger.Error("kafka commit failed",
				zap.String("trade_id", event.TradeID),
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
			// Not fatal — Kafka will redeliver. Wallet idempotency absorbs duplicate.
		}
	}
}

// commitMsg commits the given message's offset to Kafka.
func (c *Consumer) commitMsg(ctx context.Context, msg kafkago.Message) error {
	if err := c.reader.CommitMessages(ctx, msg); err != nil {
		return fmt.Errorf("commit kafka offset %d: %w", msg.Offset, err)
	}
	return nil
}

// Close gracefully shuts down the Kafka reader.
// Call this after the consumer's goroutine has exited.
func (c *Consumer) Close() error {
	return c.reader.Close()
}
