package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/trade/internal/repository"
)

// ── Poison error ──────────────────────────────────────────────────────────────

// PoisonError wraps validation or integrity errors that indicate a permanently
// invalid message. These are ACKed and routed to the DLQ — retrying will never
// fix them.
//
// Examples: malformed UUID, negative price, sequence == 0, self-trade,
// ME sequence uniqueness violation.
type PoisonError struct{ Err error }

func (e *PoisonError) Error() string { return "poison message: " + e.Err.Error() }
func (e *PoisonError) Unwrap() error { return e.Err }

func poisonf(format string, args ...any) *PoisonError {
	return &PoisonError{Err: fmt.Errorf(format, args...)}
}

// ── Kafka event contract ──────────────────────────────────────────────────────

// TradeSettledEvent is the JSON payload of a trades.settled.v1 Kafka message,
// published by the Wallet Service outbox after a successful SettleTrade commit.
//
// Format: JSON — consistent with all existing TradeDrift Kafka events.
// The platform uses JSON-over-Kafka throughout (ME, Settlement, Market).
type TradeSettledEvent struct {
	TradeID      string `json:"trade_id"`
	BuyerID      string `json:"buyer_id"`
	SellerID     string `json:"seller_id"`
	BuyOrderID   string `json:"buy_order_id"`
	SellOrderID  string `json:"sell_order_id"`
	MarketID     string `json:"market_id"`
	BaseAsset    string `json:"base_asset"`
	QuoteAsset   string `json:"quote_asset"`
	Price        string `json:"price"`
	Quantity     string `json:"quantity"`
	// Sequence is the Matching Engine's per-market monotonic counter (> 0).
	// Go's encoding/json leaves uint64 at 0 when the field is absent from JSON.
	// process() validates Sequence > 0 to close this gap before the INSERT.
	Sequence   uint64 `json:"sequence"`
	ExecutedAt string `json:"executed_at"` // RFC3339Nano — ME clock
	SettledAt  string `json:"settled_at"`  // RFC3339Nano — Wallet clock
}

// ── Consumer ─────────────────────────────────────────────────────────────────

// Consumer reads TradeSettled events from Kafka and persists them to the
// trades table.
//
// Commit rules:
//   - Success:          ACK normally.
//   - *PoisonError:     write to DLQ → ACK original offset. Retrying cannot fix it.
//   - Retryable error:  do NOT ACK — Kafka redelivers. ON CONFLICT absorbs duplicates.
type Consumer struct {
	reader    *kafkago.Reader
	dlqWriter *kafkago.Writer
	repo      repository.Repository
	log       *zap.Logger
}

// NewConsumer creates a Consumer with a manual-commit reader and a DLQ writer.
func NewConsumer(
	brokers []string,
	groupID, topic, dlqTopic string,
	repo repository.Repository,
	log *zap.Logger,
) *Consumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:        brokers,
		GroupID:        groupID,
		Topic:          topic,
		MinBytes:       1,
		MaxBytes:       10e6,
		CommitInterval: 0, // manual commit only
		StartOffset:    kafkago.FirstOffset,
	})

	dlqWriter := &kafkago.Writer{
		Addr:         kafkago.TCP(brokers...),
		Topic:        dlqTopic,
		Balancer:     &kafkago.LeastBytes{},
		RequiredAcks: kafkago.RequireOne,
	}

	log.Info("Trade Kafka consumer initialised",
		zap.Strings("brokers", brokers),
		zap.String("group_id", groupID),
		zap.String("topic", topic),
		zap.String("dlq_topic", dlqTopic),
	)
	return &Consumer{reader: reader, dlqWriter: dlqWriter, repo: repo, log: log}
}

// Start begins the sequential consume loop. Blocks until ctx is cancelled.
func (c *Consumer) Start(ctx context.Context) {
	c.log.Info("Trade consumer started — awaiting TradeSettled events")
	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				c.log.Info("Trade consumer shutting down")
				return
			}
			c.log.Error("kafka fetch error", zap.Error(err))
			continue
		}

		c.log.Debug("received TradeSettled event",
			zap.Int("partition", msg.Partition),
			zap.Int64("offset", msg.Offset),
		)

		// ── Deserialise ───────────────────────────────────────────────────────
		var event TradeSettledEvent
		if err := json.Unmarshal(msg.Value, &event); err != nil {
			// Malformed JSON — not retryable. Log safe fields only (no raw payload).
			c.log.Error("malformed TradeSettled JSON — routing to DLQ",
				zap.Int("partition", msg.Partition),
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
				// NOTE: do NOT log msg.Value — contains buyer_id, seller_id, amounts.
			)
			c.sendToDLQ(ctx, msg, "malformed JSON: "+err.Error())
			c.commitMsg(ctx, msg)
			continue
		}

		// ── Process ───────────────────────────────────────────────────────────
		if err := c.process(ctx, event); err != nil {
			var poison *PoisonError
			if errors.As(err, &poison) {
				// Poison — write to DLQ, then ACK. Never retry.
				c.log.Error("poison TradeSettled event — routing to DLQ",
					zap.String("trade_id", event.TradeID),
					zap.String("market_id", event.MarketID),
					zap.Uint64("sequence", event.Sequence),
					zap.Int("partition", msg.Partition),
					zap.Int64("offset", msg.Offset),
					zap.Error(err),
				)
				c.sendToDLQ(ctx, msg, err.Error())
				c.commitMsg(ctx, msg)
				continue
			}

			// Retryable — do NOT ACK. Kafka redelivers. ON CONFLICT absorbs duplicate.
			c.log.Error("trade insert failed — retryable, offset not committed",
				zap.String("trade_id", event.TradeID),
				zap.String("market_id", event.MarketID),
				zap.Int64("offset", msg.Offset),
				zap.Error(err),
			)
			continue
		}

		// ── ACK ───────────────────────────────────────────────────────────────
		c.commitMsg(ctx, msg)
	}
}

// process validates and persists one TradeSettledEvent.
// Returns *PoisonError for non-retryable problems, or a plain error for retryable ones.
func (c *Consumer) process(ctx context.Context, event TradeSettledEvent) error {
	// ── Parse UUIDs ───────────────────────────────────────────────────────────
	tradeID, err := uuid.Parse(event.TradeID)
	if err != nil {
		return poisonf("invalid trade_id %q: %v", event.TradeID, err)
	}
	buyerID, err := uuid.Parse(event.BuyerID)
	if err != nil {
		return poisonf("invalid buyer_id for trade %s", event.TradeID)
	}
	sellerID, err := uuid.Parse(event.SellerID)
	if err != nil {
		return poisonf("invalid seller_id for trade %s", event.TradeID)
	}
	buyOrderID, err := uuid.Parse(event.BuyOrderID)
	if err != nil {
		return poisonf("invalid buy_order_id for trade %s", event.TradeID)
	}
	sellOrderID, err := uuid.Parse(event.SellOrderID)
	if err != nil {
		return poisonf("invalid sell_order_id for trade %s", event.TradeID)
	}

	// ── Self-trade guard ──────────────────────────────────────────────────────
	if buyerID == sellerID {
		return poisonf("self-trade: buyer_id == seller_id for trade %s", event.TradeID)
	}

	// ── Sequence > 0 ──────────────────────────────────────────────────────────
	// ME guarantees sequence > 0. A zero value means the JSON field was absent
	// (Go uint64 zero value) or the producer has a bug.
	// PostgreSQL NOT NULL only catches a missing SQL parameter — it does NOT
	// catch a Go uint64 zero value being passed as a valid 0.
	// This check closes that gap before the INSERT.
	if event.Sequence == 0 {
		return poisonf("invalid sequence for trade %s: must be > 0 (got 0 — field absent or producer bug)", event.TradeID)
	}

	// ── Parse financials ──────────────────────────────────────────────────────
	price, err := decimal.NewFromString(event.Price)
	if err != nil || price.LessThanOrEqual(decimal.Zero) {
		return poisonf("invalid price for trade %s", event.TradeID)
	}
	qty, err := decimal.NewFromString(event.Quantity)
	if err != nil || qty.LessThanOrEqual(decimal.Zero) {
		return poisonf("invalid quantity for trade %s", event.TradeID)
	}
	executedAt, err := time.Parse(time.RFC3339Nano, event.ExecutedAt)
	if err != nil {
		return poisonf("invalid executed_at for trade %s", event.TradeID)
	}
	settledAt, err := time.Parse(time.RFC3339Nano, event.SettledAt)
	if err != nil {
		return poisonf("invalid settled_at for trade %s", event.TradeID)
	}

	// ── Persist (idempotent) ──────────────────────────────────────────────────
	// repo.Create returns:
	//   nil                         → inserted, or duplicate trade_id (no-op)
	//   ErrSequenceConflict         → same (market_id, me_sequence) for different trade_id
	//   any other error             → retryable DB failure
	err = c.repo.Create(ctx, &repository.Trade{
		ID:          tradeID,
		BuyerID:     buyerID,
		SellerID:    sellerID,
		BuyOrderID:  buyOrderID,
		SellOrderID: sellOrderID,
		MarketID:    event.MarketID,
		BaseAsset:   event.BaseAsset,
		QuoteAsset:  event.QuoteAsset,
		Price:       price,
		Quantity:    qty,
		Sequence:    event.Sequence,
		ExecutedAt:  executedAt,
		SettledAt:   settledAt,
	})
	if err != nil {
		if errors.Is(err, repository.ErrSequenceConflict) {
			return poisonf("sequence conflict for trade %s: market_id=%s sequence=%d already exists with a different trade_id (producer integrity bug)",
				event.TradeID, event.MarketID, event.Sequence)
		}
		return fmt.Errorf("db insert: %w", err) // retryable
	}
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// sendToDLQ writes the original Kafka message bytes plus error context to the
// DLQ topic. Non-fatal — a DLQ write failure is logged but does NOT change the
// ACK decision (we still ACK the original to unblock the partition).
func (c *Consumer) sendToDLQ(ctx context.Context, original kafkago.Message, reason string) {
	dlqMsg := kafkago.Message{
		Key:   original.Key,
		Value: original.Value,
		Headers: []kafkago.Header{
			{Key: "dlq-reason", Value: []byte(reason)},
			{Key: "dlq-topic", Value: []byte(original.Topic)},
			{Key: "dlq-partition", Value: []byte(fmt.Sprintf("%d", original.Partition))},
			{Key: "dlq-offset", Value: []byte(fmt.Sprintf("%d", original.Offset))},
		},
	}
	if err := c.dlqWriter.WriteMessages(ctx, dlqMsg); err != nil {
		c.log.Error("failed to write to DLQ — event may be lost",
			zap.Int64("offset", original.Offset),
			zap.String("reason", reason),
			zap.Error(err),
		)
	}
}

// commitMsg commits the Kafka offset. Non-fatal — ON CONFLICT absorbs redelivery.
func (c *Consumer) commitMsg(ctx context.Context, msg kafkago.Message) {
	if err := c.reader.CommitMessages(ctx, msg); err != nil {
		c.log.Error("kafka commit failed — will redeliver safely",
			zap.Int64("offset", msg.Offset),
			zap.Error(err),
		)
	}
}

// Close shuts down the Kafka reader and DLQ writer.
func (c *Consumer) Close() error {
	_ = c.dlqWriter.Close()
	return c.reader.Close()
}
