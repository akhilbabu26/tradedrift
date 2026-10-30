package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"tradedrift/services/order/internal/repository"
	"tradedrift/services/order/internal/wallet"
)

type WalletClient interface {
	ReleaseFunds(ctx context.Context, orderID string) error
}

type OrderCancelledEvent struct {
	EventID   string `json:"event_id"`
	OrderID   string `json:"order_id"`
	UserID    string `json:"user_id"`
	MarketID  string `json:"market_id"`
	Status    string `json:"status"`
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

		if err := c.ProcessMessage(ctx, msg); err != nil {
			c.logger.Error("Failed to process cancel message — retrying", zap.Error(err))
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			c.logger.Error("Failed to commit Kafka offset in cancel consumer",
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
		}
	}
}

// ProcessMessage handles a single cancel message from Kafka.
// Exported for unit testing and deterministic message replay.
func (c *CancelConsumer) ProcessMessage(ctx context.Context, msg kafkago.Message) error {
	var ev OrderCancelledEvent
	if err := json.Unmarshal(msg.Value, &ev); err != nil {
		c.logger.Error("Failed to deserialize OrderCancelled event",
			zap.Int("partition", msg.Partition),
			zap.Int64("offset", msg.Offset),
			zap.Error(err),
		)
		return nil // skip malformed message
	}

	if ev.OrderID != "" {
		if ev.Status == "USER_MISMATCH" {
			c.logger.Warn("Order cancellation rejected by matching engine due to user mismatch — ignoring event without DB or wallet mutation",
				zap.String("order_id", ev.OrderID),
				zap.String("user_id", ev.UserID),
				zap.String("market_id", ev.MarketID),
			)
			return nil
		}

		if ev.Status == "INVALID_REQUEST" {
			if ev.Reason == "market_blocked" || ev.Reason == "market_reconciling" {
				c.logger.Info("Order rejected due to non-live market; reconciling status based on filled quantity",
					zap.String("order_id", ev.OrderID),
					zap.String("market_id", ev.MarketID),
					zap.String("reason", ev.Reason),
				)
				reconciled, err := c.repo.ReconcileAbsentOrder(ctx, ev.OrderID)
				if err != nil {
					return fmt.Errorf("reconcile %s market order %s: %w", ev.Reason, ev.OrderID, err)
				}
				if reconciled != nil && c.wallet != nil {
					if err := c.wallet.ReleaseFunds(ctx, ev.OrderID); err != nil {
						if wallet.IsReservationAlreadyReleased(err) {
							_ = c.repo.MarkWalletReleaseCompletedByOrderID(ctx, ev.OrderID)
						} else {
							c.logger.Warn("Immediate opportunistic wallet release failed for non-live market order; delegated to durable WalletReleaseWorker",
								zap.String("order_id", ev.OrderID),
								zap.String("status", string(reconciled.Status)),
								zap.String("reason", ev.Reason),
								zap.Error(err),
							)
						}
					} else {
						_ = c.repo.MarkWalletReleaseCompletedByOrderID(ctx, ev.OrderID)
					}
				}
				return nil
			}

			c.logger.Warn("Matching engine rejected event due to invalid market routing — ignoring event without DB or wallet mutation",
				zap.String("order_id", ev.OrderID),
				zap.String("user_id", ev.UserID),
				zap.String("market_id", ev.MarketID),
				zap.String("reason", ev.Reason),
			)
			return nil
		}

		if ev.Status == "ALREADY_ABSENT" {
			c.logger.Info("Order was already absent from matching engine; reconciling status based on filled quantity",
				zap.String("order_id", ev.OrderID),
			)
			reconciled, err := c.repo.ReconcileAbsentOrder(ctx, ev.OrderID)
			if err != nil {
				return fmt.Errorf("reconcile absent order %s: %w", ev.OrderID, err)
			}

			// In both terminal outcomes (FILLED or CANCELLED), ReconcileAbsentOrder has already enqueued
			// the durable intent into wallet_releases table inside the same DB transaction.
			// Opportunistic immediate release attempt:
			if reconciled != nil && c.wallet != nil {
				if err := c.wallet.ReleaseFunds(ctx, ev.OrderID); err != nil {
					if wallet.IsReservationAlreadyReleased(err) {
						_ = c.repo.MarkWalletReleaseCompletedByOrderID(ctx, ev.OrderID)
					} else {
						c.logger.Warn("Immediate opportunistic wallet release failed for reconciled order; delegated to durable WalletReleaseWorker",
							zap.String("order_id", ev.OrderID),
							zap.String("status", string(reconciled.Status)),
							zap.Error(err),
						)
					}
				} else {
					_ = c.repo.MarkWalletReleaseCompletedByOrderID(ctx, ev.OrderID)
				}
			}
			return nil
		}

		// 1. Mark order as CANCELLED in database (durably enqueues wallet_releases inside the same tx)
		cancelled, err := c.repo.MarkOrderCancelled(ctx, ev.OrderID)
		if err != nil {
			return fmt.Errorf("mark order cancelled %s: %w", ev.OrderID, err)
		}

		// 2. Opportunistic immediate release attempt in Wallet Service
		if cancelled != nil && c.wallet != nil {
			if err := c.wallet.ReleaseFunds(ctx, ev.OrderID); err != nil {
				if wallet.IsReservationAlreadyReleased(err) {
					_ = c.repo.MarkWalletReleaseCompletedByOrderID(ctx, ev.OrderID)
				} else {
					c.logger.Warn("Immediate opportunistic wallet release failed for cancelled order; delegated to durable WalletReleaseWorker",
						zap.String("order_id", ev.OrderID),
						zap.Error(err),
					)
				}
			} else {
				_ = c.repo.MarkWalletReleaseCompletedByOrderID(ctx, ev.OrderID)
			}
		}

		c.logger.Info("Order cancelled and funds released successfully",
			zap.String("order_id", ev.OrderID),
			zap.String("user_id", ev.UserID),
			zap.String("reason", ev.Reason),
		)
	}

	return nil
}

func (c *CancelConsumer) Close() error {
	return c.reader.Close()
}
