package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
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

type UserTradeSettledEvent struct {
	TradeID     string `json:"trade_id"`
	UserID      string `json:"user_id"`
	OrderID     string `json:"order_id"`
	Role        string `json:"role"` // "BUY" | "SELL"
	MarketID    string `json:"market_id"`
	BaseAsset   string `json:"base_asset"`
	QuoteAsset  string `json:"quote_asset"`
	Price       string `json:"price"`
	Quantity    string `json:"quantity"`
	Sequence    uint64 `json:"sequence"`
	ExecutedAt  string `json:"executed_at"`
	SettledAt   string `json:"settled_at"`
}

type TradeSettledEvent struct {
	TradeID     string `json:"trade_id"`
	BuyerID     string `json:"buyer_id"`
	SellerID    string `json:"seller_id"`
	BuyOrderID  string `json:"buy_order_id"`
	SellOrderID string `json:"sell_order_id"`
	MarketID    string `json:"market_id"`
	BaseAsset   string `json:"base_asset"`
	QuoteAsset  string `json:"quote_asset"`
	Price       string `json:"price"`
	Quantity    string `json:"quantity"`
	Sequence    uint64 `json:"sequence"`
	ExecutedAt  string `json:"executed_at"`
	SettledAt   string `json:"settled_at"`
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
	var event UserTradeSettledEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		metrics.EventsConsumedTotal.WithLabelValues("poison", "unknown").Inc()
		return poisonf("failed to unmarshal UserTradeSettledEvent: %w", err)
	}

	// 1. UUID Validation
	if _, err := uuid.Parse(event.TradeID); err != nil {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("invalid trade_id UUID %q: %w", event.TradeID, err)
	}
	if _, err := uuid.Parse(event.UserID); err != nil {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("invalid user_id UUID %q: %w", event.UserID, err)
	}
	if _, err := uuid.Parse(event.OrderID); err != nil {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("invalid order_id UUID %q: %w", event.OrderID, err)
	}

	// 2. Role Validation
	role := strings.ToUpper(strings.TrimSpace(event.Role))
	if role != "BUY" && role != "SELL" {
		metrics.EventsConsumedTotal.WithLabelValues("poison", event.MarketID).Inc()
		return poisonf("invalid trade role %q: must be BUY or SELL", event.Role)
	}

	// 3. Market & Asset Consistency Validation
	base := strings.ToUpper(strings.TrimSpace(event.BaseAsset))
	quote := strings.ToUpper(strings.TrimSpace(event.QuoteAsset))
	market := strings.ToUpper(strings.TrimSpace(event.MarketID))

	if base == "" || quote == "" || market == "" {
		metrics.EventsConsumedTotal.WithLabelValues("poison", "unknown").Inc()
		return poisonf("missing base_asset, quote_asset, or market_id in event %s", event.TradeID)
	}

	expectedMarket := fmt.Sprintf("%s-%s", base, quote)
	if market != expectedMarket {
		metrics.EventsConsumedTotal.WithLabelValues("poison", market).Inc()
		return poisonf("market_id %q does not match %s-%s", event.MarketID, base, quote)
	}

	if quote != "USDT" {
		metrics.EventsConsumedTotal.WithLabelValues("poison", market).Inc()
		return poisonf("unsupported quote_asset %q: Portfolio V1 strictly requires USDT denomination", event.QuoteAsset)
	}

	if event.Sequence == 0 {
		metrics.EventsConsumedTotal.WithLabelValues("poison", market).Inc()
		return poisonf("invalid sequence for trade %s: must be > 0", event.TradeID)
	}

	// 4. Positive Decimal & Scale Validation (PostgreSQL DECIMAL(30,10))
	price, err := decimal.NewFromString(event.Price)
	if err != nil || price.IsZero() || price.IsNegative() {
		metrics.EventsConsumedTotal.WithLabelValues("poison", market).Inc()
		return poisonf("invalid price %q: must be positive decimal", event.Price)
	}
	if price.Exponent() < -10 {
		metrics.EventsConsumedTotal.WithLabelValues("poison", market).Inc()
		return poisonf("price %s exceeds maximum supported scale of 10 decimal digits", event.Price)
	}

	qty, err := decimal.NewFromString(event.Quantity)
	if err != nil || qty.IsZero() || qty.IsNegative() {
		metrics.EventsConsumedTotal.WithLabelValues("poison", market).Inc()
		return poisonf("invalid quantity %q: must be positive decimal", event.Quantity)
	}
	if qty.Exponent() < -10 {
		metrics.EventsConsumedTotal.WithLabelValues("poison", market).Inc()
		return poisonf("quantity %s exceeds maximum supported scale of 10 decimal digits", event.Quantity)
	}

	// 5. Strict Chronological Timestamp Validation
	executedAt, err := time.Parse(time.RFC3339Nano, event.ExecutedAt)
	if err != nil {
		metrics.EventsConsumedTotal.WithLabelValues("poison", market).Inc()
		return poisonf("invalid executed_at %q: %w", event.ExecutedAt, err)
	}

	settledAt, err := time.Parse(time.RFC3339Nano, event.SettledAt)
	if err != nil {
		metrics.EventsConsumedTotal.WithLabelValues("poison", market).Inc()
		return poisonf("invalid settled_at %q: %w", event.SettledAt, err)
	}

	if settledAt.Before(executedAt) {
		metrics.EventsConsumedTotal.WithLabelValues("poison", market).Inc()
		return poisonf("chronological anomaly: settled_at (%s) is before executed_at (%s)", event.SettledAt, event.ExecutedAt)
	}

	input := repository.UserTradeInput{
		TradeID:    event.TradeID,
		UserID:     event.UserID,
		OrderID:    event.OrderID,
		Role:       role,
		MarketID:   market,
		BaseAsset:  base,
		QuoteAsset: quote,
		Price:      price,
		Quantity:   qty,
		Sequence:   event.Sequence,
		ExecutedAt: executedAt,
		SettledAt:  settledAt,
	}

	_, err = c.repo.ProcessUserTrade(ctx, input)
	if err != nil {
		if errors.Is(err, repository.ErrTradeAlreadyProcessed) {
			metrics.EventsConsumedTotal.WithLabelValues("duplicate", market).Inc()
			c.logger.Debug("trade already processed for user; skipping as harmless duplicate",
				zap.String("trade_id", event.TradeID),
				zap.String("user_id", event.UserID),
			)
			return nil
		}
		if errors.Is(err, repository.ErrInsufficientHoldings) || errors.Is(err, repository.ErrSequenceCollision) {
			metrics.EventsConsumedTotal.WithLabelValues("poison", market).Inc()
			return poisonf("accounting violation: %v", err)
		}
		metrics.EventsConsumedTotal.WithLabelValues("error", market).Inc()
		return err
	}

	metrics.EventsConsumedTotal.WithLabelValues("success", market).Inc()
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
