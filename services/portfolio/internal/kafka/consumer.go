package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/portfolio/internal/metrics"
	"tradedrift/services/portfolio/internal/repository"
)

type PoisonError struct {
	Err error
}

func (e *PoisonError) Error() string { return e.Err.Error() }
func (e *PoisonError) Unwrap() error { return e.Err }

func poisonf(format string, args ...any) *PoisonError {
	return &PoisonError{Err: fmt.Errorf(format, args...)}
}

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
	Sequence     uint64 `json:"sequence"`
	ExecutedAt   string `json:"executed_at"`
	SettledAt    string `json:"settled_at"`
}

type Consumer struct {
	reader    *kafkago.Reader
	dlqWriter *kafkago.Writer
	repo      repository.Repository
	logger    *zap.Logger
	topicDLQ  string
}

func NewConsumer(
	brokers []string,
	groupID string,
	topicTradeSettled string,
	topicDLQ string,
	repo repository.Repository,
	logger *zap.Logger,
) *Consumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:        brokers,
		GroupID:        groupID,
		Topic:          topicTradeSettled,
		MinBytes:       1,
		MaxBytes:       10e6,
		MaxWait:        200 * time.Millisecond,
		CommitInterval: 0, // Manual commit mode
		StartOffset:    kafkago.FirstOffset,
	})

	dlqWriter := &kafkago.Writer{
		Addr:         kafkago.TCP(brokers...),
		Topic:        topicDLQ,
		Balancer:     &kafkago.LeastBytes{},
		WriteTimeout: 5 * time.Second,
		RequiredAcks: kafkago.RequireAll,
	}

	return &Consumer{
		reader:    reader,
		dlqWriter: dlqWriter,
		repo:      repo,
		logger:    logger,
		topicDLQ:  topicDLQ,
	}
}

func (c *Consumer) Start(ctx context.Context) error {
	c.logger.Info("starting portfolio settled trade consumer loop")

	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				c.logger.Info("consumer loop context cancelled, exiting")
				return nil
			}
			c.logger.Error("failed to fetch message from kafka", zap.Error(err))
			continue
		}

		if err := c.processMessage(ctx, msg); err != nil {
			var poison *PoisonError
			if errors.As(err, &poison) {
				c.logger.Warn("poison message encountered; dispatching to DLQ",
					zap.String("partition", strconv.Itoa(msg.Partition)),
					zap.Int64("offset", msg.Offset),
					zap.Error(poison),
				)
				if dlqErr := c.sendToDLQ(ctx, msg, poison.Error()); dlqErr != nil {
					c.logger.Error("failed to publish to DLQ; will not commit offset to prevent data loss", zap.Error(dlqErr))
					time.Sleep(500 * time.Millisecond)
					continue
				}
				c.commitMsg(ctx, msg)
				continue
			}

			// Transient DB error
			c.logger.Error("transient error processing message; will retry", zap.Error(err))
			time.Sleep(250 * time.Millisecond)
			continue
		}

		c.commitMsg(ctx, msg)
	}
}

func (c *Consumer) processMessage(ctx context.Context, msg kafkago.Message) error {
	var event TradeSettledEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		metrics.EventsConsumedTotal.WithLabelValues("poison", "unknown").Inc()
		return poisonf("failed to unmarshal TradeSettledEvent: %w", err)
	}

	// 1. Invariant Validation: Valid UUIDs
	if _, err := uuid.Parse(event.TradeID); err != nil {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("invalid trade_id UUID %q: %w", event.TradeID, err)
	}
	if _, err := uuid.Parse(event.BuyerID); err != nil {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("invalid buyer_id UUID %q: %w", event.BuyerID, err)
	}
	if _, err := uuid.Parse(event.SellerID); err != nil {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("invalid seller_id UUID %q: %w", event.SellerID, err)
	}
	if _, err := uuid.Parse(event.BuyOrderID); err != nil {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("invalid buy_order_id UUID %q: %w", event.BuyOrderID, err)
	}
	if _, err := uuid.Parse(event.SellOrderID); err != nil {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("invalid sell_order_id UUID %q: %w", event.SellOrderID, err)
	}

	// 2. Invariant Validation: Identifiers & Sequence
	if event.MarketID == "" {
		metrics.EventsConsumedTotal.WithLabelValues("poison", "unknown").Inc()
		return poisonf("missing market_id in event %s", event.TradeID)
	}
	if event.BaseAsset == "" {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("missing base_asset in event %s", event.TradeID)
	}
	if event.QuoteAsset == "" {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("missing quote_asset in event %s", event.TradeID)
	}
	if event.Sequence == 0 {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("invalid sequence for trade %s: must be > 0", event.TradeID)
	}

	// 3. Invariant Validation: Self-Trade strictly rejected
	if event.BuyerID == event.SellerID {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("self-trade detected: buyer_id equals seller_id (%s)", event.BuyerID)
	}

	// 4. Invariant Validation: Positive decimal values
	price, err := decimal.NewFromString(event.Price)
	if err != nil || price.IsZero() || price.IsNegative() {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("invalid price %q: must be positive decimal", event.Price)
	}

	qty, err := decimal.NewFromString(event.Quantity)
	if err != nil || qty.IsZero() || qty.IsNegative() {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("invalid quantity %q: must be positive decimal", event.Quantity)
	}

	// 5. Strict Timestamps Parsing (Fatal if invalid -> DLQ)
	executedAt, err := time.Parse(time.RFC3339Nano, event.ExecutedAt)
	if err != nil {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("invalid executed_at %q: %w", event.ExecutedAt, err)
	}

	settledAt, err := time.Parse(time.RFC3339Nano, event.SettledAt)
	if err != nil {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("invalid settled_at %q: %w", event.SettledAt, err)
	}

	input := repository.TradeSettledInput{
		TradeID:    event.TradeID,
		BuyerID:    event.BuyerID,
		SellerID:   event.SellerID,
		MarketID:   event.MarketID,
		BaseAsset:  event.BaseAsset,
		QuoteAsset: event.QuoteAsset,
		Price:      price,
		Quantity:   qty,
		Sequence:   event.Sequence,
		ExecutedAt: executedAt,
		SettledAt:  settledAt,
	}

	// 4. Process in 1 Atomic Database Transaction
	_, err = c.repo.ProcessTradeSettled(ctx, input)
	if err != nil {
		if errors.Is(err, repository.ErrTradeAlreadyProcessed) {
			metrics.EventsConsumedTotal.WithLabelValues("duplicate", event.MarketID).Inc()
			c.logger.Debug("trade already processed; skipping as harmless duplicate", zap.String("trade_id", event.TradeID))
			return nil
		}
		if errors.Is(err, repository.ErrInsufficientHoldings) || errors.Is(err, repository.ErrSelfTrade) {
			metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
			return poisonf("accounting violation: %v", err)
		}
		metrics.EventsConsumedTotal.WithLabelValues("error", event.MarketID).Inc()
		return err
	}

	metrics.EventsConsumedTotal.WithLabelValues("success", event.MarketID).Inc()
	return nil
}

func (c *Consumer) sendToDLQ(ctx context.Context, original kafkago.Message, reason string) error {
	dlqMsg := kafkago.Message{
		Key:   original.Key,
		Value: original.Value,
		Headers: []kafkago.Header{
			{Key: "dlq-reason", Value: []byte(reason)},
			{Key: "dlq-source-topic", Value: []byte(original.Topic)},
			{Key: "dlq-partition", Value: []byte(strconv.Itoa(original.Partition))},
			{Key: "dlq-offset", Value: []byte(strconv.FormatInt(original.Offset, 10))},
			{Key: "dlq-timestamp", Value: []byte(time.Now().UTC().Format(time.RFC3339Nano))},
		},
	}

	return c.dlqWriter.WriteMessages(ctx, dlqMsg)
}

func (c *Consumer) commitMsg(ctx context.Context, msg kafkago.Message) {
	if err := c.reader.CommitMessages(ctx, msg); err != nil {
		c.logger.Error("failed to commit kafka message offset",
			zap.String("topic", msg.Topic),
			zap.Int("partition", msg.Partition),
			zap.Int64("offset", msg.Offset),
			zap.Error(err),
		)
	}
}

func (c *Consumer) Close() error {
	var errs []error
	if err := c.reader.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close reader: %w", err))
	}
	if err := c.dlqWriter.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close dlq writer: %w", err))
	}
	return errors.Join(errs...)
}
