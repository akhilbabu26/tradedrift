package publisher

import (
	"context"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"tradedrift/services/wallet/internal/repository"
)

const (
	pollInterval = 500 * time.Millisecond // polling cadence when outbox has events
	idleInterval = 2 * time.Second        // cadence when outbox is empty
	batchSize    = 50                     // max events per poll cycle
	maxRetries   = 3                      // Kafka write retries before MarkFailed
)

// OutboxPublisher polls the outbox table and publishes pending events to Kafka.
//
// Design:
//   - Polls and claims outbox events via atomic CTE transition to 'PROCESSING' with claimed_at = NOW()
//   - For each event: write to Kafka → MarkPublished (PROCESSED)
//   - On transient Kafka failure: retry locally up to maxRetries, then leave row in 'PROCESSING'
//     where the 1-minute lease expiration recovers it on a subsequent poll cycle
//   - Uses FOR UPDATE SKIP LOCKED for atomic row claiming and crash recovery.
//   - Operational Invariant (V1): Run a single active outbox publisher instance for the Wallet Service.
//     A single publisher polling chronologically (ORDER BY created_at ASC) and publishing sequentially
//     preserves strict chronological ordering per user partition in Kafka.
//   - Multi-Instance Scaling (Future): If multiple publisher instances are deployed for higher throughput,
//     claiming must be partitioned by partition_key (e.g. MOD(HASHTEXT(partition_key), N) or consistent hashing).
//     Running unpartitioned concurrent publishers with SKIP LOCKED can lead to network interleaving on Kafka,
//     where Event 2 for User A reaches Kafka before Event 1 despite correct database insertion order.
//   - EventType "TradeSettled" → trades.settled.v1
//   - EventType "PortfolioUserTrade" → portfolio.user.trades.v1
//
// Partition key: outbox.partition_key (user_id) — routes all accounting events for a given user to the same Kafka partition.

type OutboxPublisher struct {
	outbox                   repository.OutboxRepository
	writer                   *kafkago.Writer
	topicTradeSettled        string
	topicPortfolioUserTrades string
	log                      *zap.Logger
}

// NewOutboxPublisher creates an OutboxPublisher that routes events to their appropriate Kafka topics.
func NewOutboxPublisher(
	outbox repository.OutboxRepository,
	brokers []string,
	topicTradeSettled string,
	topicPortfolioUserTrades string,
	log *zap.Logger,
) *OutboxPublisher {
	writer := &kafkago.Writer{
		Addr:         kafkago.TCP(brokers...),
		Balancer:     &kafkago.Hash{}, // partition by message key (user_id)
		RequiredAcks: kafkago.RequireOne,
		MaxAttempts:  1, // we manage retries manually per-event
	}
	log.Info("Outbox publisher initialised",
		zap.Strings("brokers", brokers),
		zap.String("topic_trade_settled", topicTradeSettled),
		zap.String("topic_portfolio_user_trades", topicPortfolioUserTrades),
	)
	return &OutboxPublisher{
		outbox:                   outbox,
		writer:                   writer,
		topicTradeSettled:        topicTradeSettled,
		topicPortfolioUserTrades: topicPortfolioUserTrades,
		log:                      log,
	}
}

// Run starts the poll loop. Blocks until ctx is cancelled.
func (p *OutboxPublisher) Run(ctx context.Context) {
	p.log.Info("Outbox publisher started")
	for {
		published, err := p.publishBatch(ctx)
		if err != nil {
			if ctx.Err() != nil {
				p.log.Info("Outbox publisher shutting down")
				return
			}
			p.log.Error("outbox poll error", zap.Error(err))
		}

		// Adaptive sleep: shorter when there's work, longer when idle.
		wait := idleInterval
		if published > 0 {
			wait = pollInterval
		}

		select {
		case <-ctx.Done():
			p.log.Info("Outbox publisher stopped")
			return
		case <-time.After(wait):
		}
	}
}

// publishBatch fetches up to batchSize PENDING events and publishes each one.
// Returns the number of successfully published events.
func (p *OutboxPublisher) publishBatch(ctx context.Context) (int, error) {
	events, err := p.outbox.FetchPending(ctx, batchSize)
	if err != nil {
		return 0, fmt.Errorf("fetch pending outbox: %w", err)
	}
	if len(events) == 0 {
		return 0, nil
	}

	published := 0
	for i, event := range events {
		if err := p.publishOne(ctx, event); err != nil {
			// Transient publish failure: release claims on this failed event and all remaining
			// unattempted events in this batch back to PENDING.
			// Halting batch processing immediately preserves strict chronological FIFO ordering:
			// on the next poll cycle, this event is retried first (ORDER BY created_at ASC, id ASC)
			// rather than allowing later events to reach Kafka ahead of it.
			remainingIDs := make([]string, 0, len(events)-i)
			for _, ev := range events[i:] {
				remainingIDs = append(remainingIDs, ev.ID)
			}
			if relErr := p.outbox.ReleaseClaims(ctx, remainingIDs); relErr != nil {
				p.log.Error("failed to release claims for uncompleted outbox events",
					zap.Int("count", len(remainingIDs)),
					zap.Error(relErr),
				)
			}
			return published, err
		}
		published++
	}
	return published, nil
}

// publishOne writes one outbox event to Kafka, retrying up to maxRetries times.
// On persistent failure the event is MarkFailed and an alert is logged.
func (p *OutboxPublisher) publishOne(ctx context.Context, event *repository.OutboxEvent) error {
	var targetTopic string
	switch event.EventType {
	case "TradeSettled":
		targetTopic = p.topicTradeSettled
	case "PortfolioUserTrade":
		targetTopic = p.topicPortfolioUserTrades
	default:
		p.log.Error("unknown outbox event type; marking failed",
			zap.String("outbox_id", event.ID),
			zap.String("event_type", event.EventType),
		)
		p.outbox.MarkFailed(ctx, event.ID, "unknown event type: "+event.EventType)
		return nil
	}

	msg := kafkago.Message{
		Topic: targetTopic,
		Key:   []byte(event.PartitionKey), // user_id — preserves strict per-user FIFO ordering
		Value: event.Payload,
		Headers: []kafkago.Header{
			{Key: "event-type", Value: []byte(event.EventType)},
			{Key: "aggregate-id", Value: []byte(event.AggregateID)},
		},
	}

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		lastErr = p.writer.WriteMessages(writeCtx, msg)
		cancel()

		if lastErr == nil {
			// Successfully published — mark as PROCESSED.
			if err := p.outbox.MarkPublished(ctx, event.ID); err != nil {
				p.log.Error("failed to mark outbox event published — will redeliver safely",
					zap.String("outbox_id", event.ID),
					zap.String("trade_id", event.AggregateID),
					zap.Error(err),
				)
			}
			p.log.Debug("outbox event published",
				zap.String("outbox_id", event.ID),
				zap.String("trade_id", event.AggregateID),
				zap.String("event_type", event.EventType),
				zap.String("topic", targetTopic),
			)
			return nil
		}

		p.log.Warn("kafka write attempt failed",
			zap.String("outbox_id", event.ID),
			zap.Int("attempt", attempt),
			zap.Int("max", maxRetries),
			zap.Error(lastErr),
		)

		// Exponential back-off between retries (100ms, 200ms, 400ms).
		backoff := time.Duration(attempt*attempt) * 100 * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}

	// All local retries exhausted due to transient Kafka/network failure.
	// Returning error causes publishBatch to release this event and remaining batch items back to PENDING,
	// halting the poll loop so the event is retried immediately on the next poll cycle without out-of-order delivery.
	p.log.Error("⚠️ outbox publish temporary failure; halting batch to preserve ordering",
		zap.String("outbox_id", event.ID),
		zap.String("trade_id", event.AggregateID),
		zap.String("event_type", event.EventType),
		zap.String("topic", targetTopic),
		zap.Int("attempts", maxRetries),
		zap.Error(lastErr),
	)
	return lastErr
}


// Close flushes pending writes and releases the Kafka writer.
func (p *OutboxPublisher) Close() error {
	return p.writer.Close()
}
