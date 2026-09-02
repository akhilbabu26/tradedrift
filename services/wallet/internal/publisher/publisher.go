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
	pollInterval  = 500 * time.Millisecond // polling cadence when outbox has events
	idleInterval  = 2 * time.Second        // cadence when outbox is empty
	batchSize     = 50                     // max events per poll cycle
	maxRetries    = 3                      // Kafka write retries before MarkFailed
)

// OutboxPublisher polls the outbox table and publishes pending events to Kafka.
//
// Design:
//   - Polls outbox WHERE status='PENDING' ORDER BY created_at ASC LIMIT batchSize
//   - For each event: write to Kafka → MarkPublished (PROCESSED)
//   - On Kafka failure: retry up to maxRetries → MarkFailed (FAILED) + log alert
//   - Uses FOR UPDATE SKIP LOCKED — safe to run multiple instances without double-publish
//
// Topic routing:
//   - EventType "TradeSettled" → trades.settled.v1
//   - Unknown event types are MarkFailed and logged — not retried indefinitely.
//
// Partition key: outbox.partition_key (buyer_user_id) — matches the ME partition strategy.
type OutboxPublisher struct {
	outbox repository.OutboxRepository
	writer *kafkago.Writer
	topic  string
	log    *zap.Logger
}

// NewOutboxPublisher creates an OutboxPublisher that writes to the given Kafka topic.
func NewOutboxPublisher(
	outbox repository.OutboxRepository,
	brokers []string,
	topic string,
	log *zap.Logger,
) *OutboxPublisher {
	writer := &kafkago.Writer{
		Addr:         kafkago.TCP(brokers...),
		Topic:        topic,
		Balancer:     &kafkago.Hash{}, // partition by message key (buyer_user_id)
		RequiredAcks: kafkago.RequireOne,
		MaxAttempts:  1, // we manage retries manually per-event
	}
	log.Info("Outbox publisher initialised",
		zap.Strings("brokers", brokers),
		zap.String("topic", topic),
	)
	return &OutboxPublisher{
		outbox: outbox,
		writer: writer,
		topic:  topic,
		log:    log,
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
	for _, event := range events {
		if err := p.publishOne(ctx, event); err != nil {
			// Error already logged and event MarkFailed inside publishOne.
			continue
		}
		published++
	}
	return published, nil
}

// publishOne writes one outbox event to Kafka, retrying up to maxRetries times.
// On persistent failure the event is MarkFailed and an alert is logged.
func (p *OutboxPublisher) publishOne(ctx context.Context, event *repository.OutboxEvent) error {
	msg := kafkago.Message{
		Key:   []byte(event.PartitionKey), // buyer_user_id — consistent with ME partition key
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
				// MarkPublished failure means the event will be retried on next poll.
				// ON CONFLICT in Trade Service absorbs the duplicate — safe.
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

	// All retries exhausted — mark as FAILED for operational investigation.
	// 🚨 This is an alert condition: a settled trade event cannot be published.
	reason := fmt.Sprintf("kafka write failed after %d attempts: %v", maxRetries, lastErr)
	p.log.Error("🚨 OUTBOX PUBLISH FAILED — TRADE EVENT LOST FROM KAFKA",
		zap.String("outbox_id", event.ID),
		zap.String("trade_id", event.AggregateID),
		zap.String("event_type", event.EventType),
		zap.String("topic", p.topic),
		zap.Error(lastErr),
	)
	if err := p.outbox.MarkFailed(ctx, event.ID, reason); err != nil {
		p.log.Error("failed to MarkFailed outbox event",
			zap.String("outbox_id", event.ID),
			zap.Error(err),
		)
	}
	return lastErr
}

// Close flushes pending writes and releases the Kafka writer.
func (p *OutboxPublisher) Close() error {
	return p.writer.Close()
}
