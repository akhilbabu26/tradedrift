package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"tradedrift/services/order/internal/repository"
)

type WalletClient interface {
	ReleaseFunds(ctx context.Context, orderID string) error
}

type OrderCancelledEvent struct {
	EventID   string `json:"event_id"`
	OrderID   string `json:"order_id"`
	UserID    string `json:"user_id"`
	MarketID  string `json:"market_id"`
	Reason    string `json:"reason"`
	Timestamp string `json:"timestamp"`
}

type CancelConsumer struct {
	reader *kafkago.Reader
	repo   repository.OrderRepository
	wallet WalletClient
	logger *zap.Logger
}

func NewCancelConsumer(brokers []string, groupID, topic string, repo repository.OrderRepository, wallet WalletClient, logger *zap.Logger) *CancelConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:        brokers,
		GroupID:        groupID,
		Topic:          topic,
		MinBytes:       1,
		MaxBytes:       10e6,
		CommitInterval: 0,
		StartOffset:    kafkago.FirstOffset,
	})

	return &CancelConsumer{
		reader: reader,
		repo:   repo,
		wallet: wallet,
		logger: logger,
	}
}

// IDEMPOTENCY CONTRACT:
// The cancel consumer must NOT use event_id as its deduplication key.
// The Matching Engine replays cancel outcomes on restart (generating deterministic
// event_ids derived from order_id + source offset). Idempotency is guaranteed by:
//   1. MarkCancellingOrderCancelled: WHERE status='CANCELLING' conditional UPDATE
//   2. Wallet: UNIQUE(reference_id, reference_type, asset) constraint
func (c *CancelConsumer) Start(ctx context.Context) {
	c.logger.Info("Starting Order Service OrderCancelled consumer...")

	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				c.logger.Info("OrderCancelled consumer stopping: context canceled")
				return
			}
			c.logger.Error("Kafka fetch message error in cancel consumer", zap.Error(err))
			time.Sleep(500 * time.Millisecond)
			continue
		}

		var ev OrderCancelledEvent
		if err := json.Unmarshal(msg.Value, &ev); err != nil {
			c.logger.Error("Failed to deserialize OrderCancelled event",
				zap.Int("partition", msg.Partition),
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
			if commitErr := c.reader.CommitMessages(ctx, msg); commitErr != nil {
				c.logger.Error("Failed to commit offset for malformed cancel message", zap.Error(commitErr))
			}
			continue
		}

		if ev.OrderID != "" {
			// 1. Mark order as CANCELLED in database
			if _, err := c.repo.MarkOrderCancelled(ctx, ev.OrderID); err != nil {
				c.logger.Error("Failed to mark order as cancelled — retrying",
					zap.String("order_id", ev.OrderID),
					zap.Error(err),
				)
				continue
			}

			// 2. Release reserved funds in Wallet Service
			if c.wallet != nil {
				if err := c.wallet.ReleaseFunds(ctx, ev.OrderID); err != nil {
					c.logger.Error("Failed to release funds for cancelled order — retrying",
						zap.String("order_id", ev.OrderID),
						zap.Error(err),
					)
					continue
				}
			}

			c.logger.Info("Order cancelled and funds released successfully",
				zap.String("order_id", ev.OrderID),
				zap.String("user_id", ev.UserID),
				zap.String("reason", ev.Reason),
			)
		}

		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			c.logger.Error("Failed to commit Kafka offset in cancel consumer",
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
		}
	}
}

func (c *CancelConsumer) Close() error {
	return c.reader.Close()
}
