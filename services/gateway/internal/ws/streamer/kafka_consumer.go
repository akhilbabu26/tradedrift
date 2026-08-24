package streamer

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"tradedrift/services/gateway/internal/ws/protocol"
)

// runKafkaTradeStreamer consumes trades from Kafka with exponential backoff on failure.
//
// # Kafka Consumption Semantics & Frontend Deduplication Contract
//
// Consumption is at-least-once:
//  1. FetchMessage retrieves the event.
//  2. Broadcast fanout delivers to active subscribers.
//  3. CommitMessages records the offset.
//
// If the Gateway crashes between Broadcast and Commit, the event is re-delivered
// upon restart. Clients should deduplicate events by tradeId or (marketId + sequence).
func (s *Streamer) runKafkaTradeStreamer(ctx context.Context) {
	if len(s.kafkaBrokers) == 0 {
		s.logger.Warn("No Kafka brokers configured for WebSocket Trade Streamer")
		return
	}

	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		reader := kafka.NewReader(kafka.ReaderConfig{
			Brokers:        s.kafkaBrokers,
			GroupID:        s.kafkaGroupID,
			Topic:          s.kafkaTopic,
			MinBytes:       1,
			MaxBytes:       10e6, // 10 MB
			CommitInterval: 0,    // synchronous manual commits
			MaxWait:        500 * time.Millisecond,
		})

		s.logger.Info("Kafka trade consumer connected",
			zap.String("topic", s.kafkaTopic),
			zap.String("group_id", s.kafkaGroupID),
			zap.Int("attempt", attempt),
		)

		startTime := time.Now()
		err := s.consumeKafkaTrades(ctx, reader)
		_ = reader.Close()

		if err == nil || errors.Is(err, context.Canceled) {
			return
		}

		// If the connection remained healthy for over 1 minute, reset backoff attempt
		if time.Since(startTime) > 1*time.Minute {
			attempt = 0
		}

		atomic.AddInt64(&s.kafkaErrorsTotal, 1)
		atomic.AddInt64(&s.kafkaReconnectsTotal, 1)

		delay := KafkaBackoff(attempt)
		s.logger.Error("Kafka consumer disconnected",
			zap.Error(err),
			zap.Int("attempt", attempt),
			zap.Duration("retry_in", delay),
		)

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		attempt++
	}
}

func (s *Streamer) consumeKafkaTrades(ctx context.Context, reader *kafka.Reader) error {
	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			return err
		}

		var event RawTradeEvent
		if err := json.Unmarshal(msg.Value, &event); err != nil {
			s.logger.Warn("Malformed trade event from Kafka: skipping", zap.Error(err))
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		if err := ValidateTradeEvent(event); err != nil {
			s.logger.Warn("Invalid trade event from Kafka: skipping",
				zap.Error(err),
				zap.String("trade_id", event.TradeID),
				zap.String("market_id", event.MarketID),
			)
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		channel := "market:trades:" + event.MarketID
		if s.broadcaster != nil && s.broadcaster.HasSubscribers(channel) {
			execMs := event.ExecutedAt.UnixMilli()
			if execMs <= 0 {
				execMs = time.Now().UnixMilli()
			}

			tradePayload := protocol.TradePayload{
				TradeID:    event.TradeID,
				MarketID:   event.MarketID,
				Price:      event.Price,
				Quantity:   event.Quantity,
				Side:       event.Side,
				ExecutedAt: execMs,
				Sequence:   s.NextTradeSeq(event.MarketID),
			}
			env := protocol.OutboundEnvelope{Stream: channel, Data: tradePayload}
			if b, err := json.Marshal(env); err == nil {
				s.broadcaster.Broadcast(channel, b, protocol.StreamTypeTrades)
			}
		}

		if err := reader.CommitMessages(ctx, msg); err != nil {
			s.logger.Debug("Kafka commit error", zap.Error(err))
		}
	}
}

// ValidateTradeEvent performs sanity validation on inbound Kafka trade executions.
// Verifies non-empty IDs and strictly positive numeric price & quantity.
func ValidateTradeEvent(e RawTradeEvent) error {
	if e.TradeID == "" {
		return errors.New("missing trade_id")
	}
	if e.MarketID == "" {
		return errors.New("missing market_id")
	}
	if e.Price == "" {
		return errors.New("missing price")
	}
	if e.Quantity == "" {
		return errors.New("missing quantity")
	}

	p, err := strconv.ParseFloat(e.Price, 64)
	if err != nil || p <= 0 {
		return errors.New("invalid price: must be a positive number")
	}

	q, err := strconv.ParseFloat(e.Quantity, 64)
	if err != nil || q <= 0 {
		return errors.New("invalid quantity: must be a positive number")
	}

	return nil
}

// KafkaBackoff returns the reconnect delay using capped exponential backoff with ±25% jitter.
func KafkaBackoff(attempt int) time.Duration {
	const maxDelay = 30 * time.Second
	base := time.Duration(1<<uint(attempt)) * time.Second
	if base > maxDelay || base <= 0 {
		base = maxDelay
	}
	jitter := time.Duration(rand.Int63n(int64(base/2))) - base/4
	d := base + jitter
	if d < time.Second {
		d = time.Second
	}
	return d
}
