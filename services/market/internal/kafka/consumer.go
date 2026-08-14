package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/market/internal/service"
)

// TradeExecutedEvent represents the canonical payload published by the Matching Engine.
type TradeExecutedEvent struct {
	TradeID      string `json:"trade_id"`
	MarketID     string `json:"market_id"`
	Price        string `json:"price"`
	Quantity     string `json:"quantity"`
	ExecutedAtMs int64  `json:"executed_at_ms"`
}

type Consumer struct {
	reader *kafka.Reader
	svc    service.MarketService
	log    *zap.Logger
}

func NewConsumer(brokers []string, groupID, topic string, svc service.MarketService, log *zap.Logger) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3, // 10KB
		MaxBytes: 10e6, // 10MB
	})

	return &Consumer{
		reader: reader,
		svc:    svc,
		log:    log,
	}
}

// Start continuously consumes TradeExecuted events and rolls them into candles & 24h ticker.
func (c *Consumer) Start(ctx context.Context) {
	c.log.Info("Market Kafka Consumer started listening...")

	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				c.log.Info("Market Kafka Consumer stopping (context cancelled)")
				return
			}
			c.log.Error("Failed to fetch Kafka message", zap.Error(err))
			continue
		}

		// 1. Unmarshal JSON event
		var event TradeExecutedEvent
		if err := json.Unmarshal(msg.Value, &event); err != nil {
			c.log.Error("Poison message: JSON unmarshal failed, skipping offset",
				zap.ByteString("value", msg.Value),
				zap.Error(err),
			)
			c.commitPoisonMessage(ctx, msg)
			continue
		}

		// 2. Explicit UUID parsing
		tradeID, err := uuid.Parse(event.TradeID)
		if err != nil {
			c.log.Error("Poison message: Invalid trade_id UUID, skipping offset",
				zap.String("trade_id", event.TradeID),
				zap.Error(err),
			)
			c.commitPoisonMessage(ctx, msg)
			continue
		}

		// 3. Explicit Decimal Price parsing
		price, err := decimal.NewFromString(event.Price)
		if err != nil || price.LessThanOrEqual(decimal.Zero) {
			c.log.Error("Poison message: Invalid trade price, skipping offset",
				zap.String("trade_id", event.TradeID),
				zap.String("price", event.Price),
				zap.Error(err),
			)
			c.commitPoisonMessage(ctx, msg)
			continue
		}

		// 4. Explicit Decimal Quantity parsing
		quantity, err := decimal.NewFromString(event.Quantity)
		if err != nil || quantity.LessThanOrEqual(decimal.Zero) {
			c.log.Error("Poison message: Invalid trade quantity, skipping offset",
				zap.String("trade_id", event.TradeID),
				zap.String("quantity", event.Quantity),
				zap.Error(err),
			)
			c.commitPoisonMessage(ctx, msg)
			continue
		}

		// 5. Millisecond timestamp validation
		if event.ExecutedAtMs <= 0 {
			c.log.Error("Poison message: Invalid executed_at_ms timestamp, skipping offset",
				zap.String("trade_id", event.TradeID),
				zap.Int64("executed_at_ms", event.ExecutedAtMs),
			)
			c.commitPoisonMessage(ctx, msg)
			continue
		}

		payload := &service.TradeEventPayload{
			TradeID:    tradeID,
			MarketID:   event.MarketID,
			Price:      price,
			Quantity:   quantity,
			ExecutedAt: time.UnixMilli(event.ExecutedAtMs),
		}

		// 6. Process trade in atomic PostgreSQL transaction (Idempotent)
		processed, err := c.svc.ProcessTradeEvent(ctx, payload)
		if err != nil {
			// Check for non-retryable poison errors (e.g. unknown market, invalid payload)
			if errors.Is(err, service.ErrMarketNotFound) || errors.Is(err, service.ErrInvalidMarketID) || errors.Is(err, service.ErrInvalidTradeEvent) {
				c.log.Error("Poison message: Business validation failed, skipping offset",
					zap.String("trade_id", event.TradeID),
					zap.String("market_id", event.MarketID),
					zap.Error(err),
				)
				c.commitPoisonMessage(ctx, msg)
				continue
			}

			// Transient / Database infrastructure error: do NOT commit offset; retry
			c.log.Error("Retryable error: ProcessTradeEvent failed, offset will be retried",
				zap.String("trade_id", event.TradeID),
				zap.Error(err),
			)
			continue
		}

		if processed {
			c.log.Info("Trade successfully aggregated into candles & ticker",
				zap.String("trade_id", event.TradeID),
				zap.String("market_id", event.MarketID),
			)
		} else {
			c.log.Debug("Trade event already processed (idempotently skipped)",
				zap.String("trade_id", event.TradeID),
			)
		}

		// 7. Commit Kafka offset ONLY after DB success
		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			c.log.Error("Failed to commit Kafka offset", zap.Error(err))
		}
	}
}

func (c *Consumer) commitPoisonMessage(ctx context.Context, msg kafka.Message) {
	if err := c.reader.CommitMessages(ctx, msg); err != nil {
		c.log.Error("Failed to commit poison message offset", zap.Error(err))
	}
}

func (c *Consumer) Close() error {
	return c.reader.Close()
}
