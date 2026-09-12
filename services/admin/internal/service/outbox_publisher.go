package service

import (
	"context"
	"math/rand"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/repository"
)

// OutboxPublisher polls admin_outbox for unpublished events, publishes them to Kafka,
// and marks them published ONLY upon receiving a broker ACK.
type OutboxPublisher struct {
	outboxRepo repository.OutboxRepository
	writer     *kafka.Writer
	log        *zap.Logger
	interval   time.Duration
	done       chan struct{}
}

// NewOutboxPublisher constructs the outbox publisher with a real Kafka writer.
func NewOutboxPublisher(
	outboxRepo repository.OutboxRepository,
	kafkaBrokers string,
	log *zap.Logger,
	interval time.Duration,
) *OutboxPublisher {
	brokers := strings.Split(kafkaBrokers, ",")
	for i := range brokers {
		brokers[i] = strings.TrimSpace(brokers[i])
	}

	writer := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Balancer:     &kafka.Hash{},
		RequiredAcks: kafka.RequireAll, // Ensure all in-sync replicas acknowledge before proceeding
		Async:        false,            // Synchronous delivery so we wait for ACK
		BatchTimeout: 10 * time.Millisecond,
	}

	return &OutboxPublisher{
		outboxRepo: outboxRepo,
		writer:     writer,
		log:        log,
		interval:   interval,
		done:       make(chan struct{}),
	}
}

// Start launches the OutboxPublisher polling loop in the background.
func (p *OutboxPublisher) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.publishBatch(ctx)
			case <-ctx.Done():
				return
			case <-p.done:
				return
			}
		}
	}()
}

// Stop closes the publisher loop and flushes the Kafka writer.
func (p *OutboxPublisher) Stop() {
	close(p.done)
	if p.writer != nil {
		if err := p.writer.Close(); err != nil {
			p.log.Error("OutboxPublisher: failed to close kafka writer", zap.Error(err))
		}
	}
}

func (p *OutboxPublisher) publishBatch(ctx context.Context) {
	workerToken := domain.MustNewV7()
	events, err := p.outboxRepo.FetchDue(ctx, workerToken, 50)
	if err != nil {
		p.log.Error("OutboxPublisher: fetch due failed", zap.Error(err))
		return
	}

	for _, event := range events {
		p.publishEvent(ctx, event)
	}
}

func (p *OutboxPublisher) publishEvent(ctx context.Context, event *domain.OutboxEvent) {
	msgCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	msg := kafka.Message{
		Topic: event.Topic,
		Key:   []byte(event.OperationID),
		Value: event.Payload,
		Time:  time.Now().UTC(),
	}

	// 1. Publish to Kafka and wait for ACK
	err := p.writer.WriteMessages(msgCtx, msg)
	if err != nil {
		p.log.Error("OutboxPublisher: Kafka publish failed",
			zap.String("event_id", event.ID),
			zap.String("topic", event.Topic),
			zap.Error(err),
		)

		// 2. Failure: schedule exponential retry with jitter
		nextAttempt := event.AttemptCount + 1
		baseDelay := domain.NextDelay(nextAttempt - 1)
		jitter := time.Duration(float64(baseDelay) * (0.1 * (2*rand.Float64() - 1)))
		nextAt := time.Now().UTC().Add(baseDelay + jitter)

		if updateErr := p.outboxRepo.UpdateRetry(ctx, event.ID, nextAt, nextAttempt, err.Error()); updateErr != nil {
			p.log.Error("OutboxPublisher: UpdateRetry failed",
				zap.String("event_id", event.ID),
				zap.Error(updateErr),
			)
		}
		return
	}

	// 3. Broker ACK confirmed -> mark event published in PostgreSQL
	if markErr := p.outboxRepo.MarkPublished(ctx, event.ID); markErr != nil {
		p.log.Error("OutboxPublisher: MarkPublished failed",
			zap.String("event_id", event.ID),
			zap.Error(markErr),
		)
		return
	}

	p.log.Info("OutboxPublisher: event published and ACK confirmed",
		zap.String("event_id", event.ID),
		zap.String("topic", event.Topic),
		zap.String("operation_id", event.OperationID),
	)
}
