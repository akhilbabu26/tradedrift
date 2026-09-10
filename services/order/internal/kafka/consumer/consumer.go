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

type TradeSettledEvent struct {
	TradeID     string `json:"trade_id"`
	BuyOrderID  string `json:"buy_order_id"`
	SellOrderID string `json:"sell_order_id"`
	Quantity    string `json:"quantity"`
}

type Consumer struct {
	reader *kafkago.Reader
	repo   repository.OrderRepository
	logger *zap.Logger
}

func NewConsumer(brokers []string, groupID, topic string, repo repository.OrderRepository, logger *zap.Logger) *Consumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:        brokers,
		GroupID:        groupID,
		Topic:          topic,
		MinBytes:       1,
		MaxBytes:       10e6, // 10MB
		CommitInterval: 0,    // Manual commits only
		StartOffset:    kafkago.FirstOffset,
	})

	return &Consumer{
		reader: reader,
		repo:   repo,
		logger: logger,
	}
}

func (c *Consumer) Start(ctx context.Context) {
	c.logger.Info("Starting Order Service TradeSettled consumer...")

	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				c.logger.Info("TradeSettled consumer stopping: context canceled")
				return
			}
			c.logger.Error("Kafka fetch message error", zap.Error(err))
			time.Sleep(500 * time.Millisecond)
			continue
		}

		var ev TradeSettledEvent
		if err := json.Unmarshal(msg.Value, &ev); err != nil {
			c.logger.Error("Failed to deserialize TradeSettled event",
				zap.Int("partition", msg.Partition),
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
			// ACK malformed message to prevent infinite loop
			if commitErr := c.reader.CommitMessages(ctx, msg); commitErr != nil {
				c.logger.Error("Failed to commit offset for malformed message", zap.Error(commitErr))
			}
			continue
		}

		if ev.TradeID != "" && (ev.BuyOrderID != "" || ev.SellOrderID != "") && ev.Quantity != "" {
			if err := c.repo.ApplyTradeFill(ctx, ev.TradeID, ev.BuyOrderID, ev.SellOrderID, ev.Quantity); err != nil {
				c.logger.Error("Failed to apply trade fill to orders — retrying",
					zap.String("trade_id", ev.TradeID),
					zap.Error(err),
				)
				// Do not commit, retry on next fetch
				continue
			}
		}

		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			c.logger.Error("Failed to commit Kafka offset",
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
		}
	}
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
