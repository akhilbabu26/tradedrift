package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
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
	wallet WalletClient
	logger *zap.Logger
}

func NewConsumer(brokers []string, groupID, topic string, repo repository.OrderRepository, wallet WalletClient, logger *zap.Logger) *Consumer {
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
		wallet: wallet,
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

		if err := c.ProcessMessage(ctx, msg); err != nil {
			c.logger.Error("Failed to process trade message — retrying", zap.Error(err))
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			c.logger.Error("Failed to commit Kafka offset",
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
		}
	}
}

// ProcessMessage handles a single TradeSettled event.
// Exported for unit testing and deterministic message replay.
func (c *Consumer) ProcessMessage(ctx context.Context, msg kafkago.Message) error {
	var ev TradeSettledEvent
	if err := json.Unmarshal(msg.Value, &ev); err != nil {
		c.logger.Error("Failed to deserialize TradeSettled event",
			zap.Int("partition", msg.Partition),
			zap.Int64("offset", msg.Offset),
			zap.Error(err),
		)
		return nil // skip malformed message
	}

	// Strict inbound event validation to prevent poison-pill crashes and DB query errors
	if ev.TradeID == "" {
		c.logger.Warn("Skipping malformed TradeSettled event: missing trade_id",
			zap.Int("partition", msg.Partition),
			zap.Int64("offset", msg.Offset),
		)
		return nil
	}
	if _, err := uuid.Parse(ev.TradeID); err != nil {
		c.logger.Warn("Skipping malformed TradeSettled event: trade_id is not a valid UUID",
			zap.String("trade_id", ev.TradeID),
			zap.Error(err),
		)
		return nil
	}

	if ev.BuyOrderID == "" && ev.SellOrderID == "" {
		c.logger.Warn("Skipping malformed TradeSettled event: both buy_order_id and sell_order_id are empty",
			zap.String("trade_id", ev.TradeID),
		)
		return nil
	}

	if ev.BuyOrderID != "" {
		if _, err := uuid.Parse(ev.BuyOrderID); err != nil {
			c.logger.Warn("Skipping malformed TradeSettled event: buy_order_id is not a valid UUID",
				zap.String("trade_id", ev.TradeID),
				zap.String("buy_order_id", ev.BuyOrderID),
				zap.Error(err),
			)
			return nil
		}
	}

	if ev.SellOrderID != "" {
		if _, err := uuid.Parse(ev.SellOrderID); err != nil {
			c.logger.Warn("Skipping malformed TradeSettled event: sell_order_id is not a valid UUID",
				zap.String("trade_id", ev.TradeID),
				zap.String("sell_order_id", ev.SellOrderID),
				zap.Error(err),
			)
			return nil
		}
	}

	if ev.BuyOrderID != "" && ev.SellOrderID != "" && ev.BuyOrderID == ev.SellOrderID {
		c.logger.Warn("Skipping malformed TradeSettled event: self-match detected (buy_order_id == sell_order_id)",
			zap.String("trade_id", ev.TradeID),
			zap.String("order_id", ev.BuyOrderID),
		)
		return nil
	}

	qty, err := decimal.NewFromString(ev.Quantity)
	if err != nil || !qty.GreaterThan(decimal.Zero) {
		c.logger.Warn("Skipping malformed TradeSettled event: quantity is invalid or non-positive",
			zap.String("trade_id", ev.TradeID),
			zap.String("quantity", ev.Quantity),
		)
		return nil
	}

	filledOrderIDs, err := c.repo.ApplyTradeFill(ctx, ev.TradeID, ev.BuyOrderID, ev.SellOrderID, ev.Quantity)
	if err != nil {
		c.logger.Error("Failed to apply trade fill to orders",
			zap.String("trade_id", ev.TradeID),
			zap.Error(err),
		)
		return err
	}

	// Opportunistic immediate release attempt.
	// Note: Correctness does NOT depend on this call because ApplyTradeFill has already enqueued
	// the durable intent into wallet_releases table inside the same DB transaction.
	if c.wallet != nil && len(filledOrderIDs) > 0 {
		for _, filledID := range filledOrderIDs {
			if releaseErr := c.wallet.ReleaseFunds(ctx, filledID); releaseErr != nil {
				c.logger.Info("Immediate wallet release attempt deferred to background worker",
					zap.String("order_id", filledID),
					zap.Error(releaseErr),
				)
			} else {
				if markErr := c.repo.MarkWalletReleaseCompletedByOrderID(ctx, filledID); markErr != nil {
					c.logger.Debug("Could not mark wallet release completed immediately; background worker will reconcile", zap.Error(markErr))
				}
				c.logger.Info("Leftover reserved funds released for filled order",
					zap.String("order_id", filledID),
				)
			}
		}
	}

	return nil
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
