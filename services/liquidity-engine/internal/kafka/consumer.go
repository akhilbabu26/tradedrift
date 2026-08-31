// Package kafka consumer reads trade execution events from trades.executed.
// The LE uses these to update inventory state and trigger targeted reconciliation
// when an MM order is involved in a fill.
//
// IMPORTANT: trades.executed is used ONLY for fast inventory updates.
// It is NOT used to detect ME liveness.
package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/account"
)

// TradeEvent is the LE's internal representation of a trade execution.
type TradeEvent struct {
	TradeID      string
	MarketID     string
	MakerOrderID string
	TakerOrderID string
	BuyerUserID  string
	SellerUserID string
	Price        decimal.Decimal
	Quantity     decimal.Decimal
	ExecutedAt   time.Time

	// Derived fields: set by consumer after parsing
	MMIsMaker bool   // MM-001 was the maker (resting) side
	MMIsTaker bool   // MM-001 was the taker side
	MMSide    string // "BUY" | "SELL" | "" (empty if MM not involved)
}

// tradeExecutedMessage matches the ME publisher's JSON schema exactly.
type tradeExecutedMessage struct {
	TradeID      string `json:"trade_id"`
	MarketID     string `json:"market_id"`
	Sequence     uint64 `json:"sequence"`
	MakerOrderID string `json:"maker_order_id"`
	TakerOrderID string `json:"taker_order_id"`
	BuyOrderID   string `json:"buy_order_id"`
	SellOrderID  string `json:"sell_order_id"`
	BuyerUserID  string `json:"buyer_user_id"`
	SellerUserID string `json:"seller_user_id"`
	Price        string `json:"price"`
	Quantity     string `json:"quantity"`
	ExecutedAt   string `json:"executed_at"`
}

// Consumer reads from trades.executed and sends TradeEvents to the engine event channel.
// It runs in its own goroutine — it ONLY sends to the channel, never modifies state.
type Consumer struct {
	reader *kafkago.Reader
	events chan<- TradeEvent
	logger *zap.Logger
}

// NewConsumer creates a new trades.executed consumer.
func NewConsumer(brokers []string, groupID string, events chan<- TradeEvent, logger *zap.Logger) *Consumer {
	return &Consumer{
		reader: kafkago.NewReader(kafkago.ReaderConfig{
			Brokers:        brokers,
			Topic:          TopicTradesExecuted,
			GroupID:        groupID,
			MinBytes:       1,
			MaxBytes:       10e6,
			MaxWait:        500 * time.Millisecond,
			CommitInterval: 1 * time.Second, // auto-commit — LE is idempotent on replay
		}),
		events: events,
		logger: logger,
	}
}

// Run starts the consumer read loop. Blocks until ctx is cancelled.
func (c *Consumer) Run(ctx context.Context) {
	c.logger.Info("trades.executed consumer started")
	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				c.logger.Info("trades.executed consumer stopped (context cancelled)")
				return
			}
			c.logger.Warn("trades.executed fetch error — retrying in 500ms", zap.Error(err))
			time.Sleep(500 * time.Millisecond)
			continue
		}

		event, err := parseTradeMessage(msg.Value)
		if err != nil {
			c.logger.Error("failed to parse trade message — skipping",
				zap.ByteString("raw", msg.Value),
				zap.Error(err))
			if err := c.reader.CommitMessages(ctx, msg); err != nil {
				c.logger.Warn("failed to commit skipped message", zap.Error(err))
			}
			continue
		}

		// Block until the event loop accepts the trade event.
		// Do NOT drop events — a dropped trade permanently corrupts inventory
		// until the next authoritative wallet refresh.
		// Backpressure here is intentional: if the event loop is slow, the
		// Kafka consumer waits. Commit only after event is accepted.
		select {
		case c.events <- *event:
		case <-ctx.Done():
			return
		}

		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			c.logger.Warn("failed to commit trade message", zap.Error(err))
		}
	}
}

// Close shuts down the Kafka reader.
func (c *Consumer) Close() error {
	return c.reader.Close()
}

// parseTradeMessage deserializes a raw Kafka message into a TradeEvent.
func parseTradeMessage(raw []byte) (*TradeEvent, error) {
	var msg tradeExecutedMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal tradeExecutedMessage: %w", err)
	}

	price, err := decimal.NewFromString(msg.Price)
	if err != nil {
		return nil, fmt.Errorf("invalid price %q: %w", msg.Price, err)
	}
	quantity, err := decimal.NewFromString(msg.Quantity)
	if err != nil {
		return nil, fmt.Errorf("invalid quantity %q: %w", msg.Quantity, err)
	}

	var executedAt time.Time
	if msg.ExecutedAt != "" {
		executedAt, err = time.Parse(time.RFC3339, msg.ExecutedAt)
		if err != nil {
			executedAt = time.Time{}
		}
	}

	// Use the canonical MM UUID — trades.executed contains UUID user_ids,
	// not the human-readable "MM-001" string.
	mmUserID := account.WalletUUIDStr
	event := &TradeEvent{
		TradeID:      msg.TradeID,
		MarketID:     msg.MarketID,
		MakerOrderID: msg.MakerOrderID,
		TakerOrderID: msg.TakerOrderID,
		BuyerUserID:  msg.BuyerUserID,
		SellerUserID: msg.SellerUserID,
		Price:        price,
		Quantity:     quantity,
		ExecutedAt:   executedAt,
	}

	// Determine MM involvement and side
	if msg.BuyerUserID == mmUserID {
		event.MMIsMaker = (msg.MakerOrderID == msg.BuyOrderID)
		event.MMIsTaker = !event.MMIsMaker
		event.MMSide = "BUY"
	} else if msg.SellerUserID == mmUserID {
		event.MMIsMaker = (msg.MakerOrderID == msg.SellOrderID)
		event.MMIsTaker = !event.MMIsMaker
		event.MMSide = "SELL"
	}

	return event, nil
}
