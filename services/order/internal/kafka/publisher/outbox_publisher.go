package publisher

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/order/internal/repository"
)

type OutboxPublisher struct {
	repo     repository.OutboxRepository
	producer Producer
	logger   *zap.Logger
	interval time.Duration
	topicMap map[string]string
}

func NewOutboxPublisher(repo repository.OutboxRepository, producer Producer, logger *zap.Logger, interval time.Duration) *OutboxPublisher {
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	return &OutboxPublisher{
		repo:     repo,
		producer: producer,
		logger:   logger,
		interval: interval,
		topicMap: map[string]string{
			"OrderCreated":         "orders.commands",
			"OrderCancelRequested": "orders.commands",
		},
	}
}

func (p *OutboxPublisher) Start(ctx context.Context) {
	p.logger.Info("Starting Outbox Publisher background loop", zap.Duration("interval", p.interval))
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("Outbox Publisher worker loop stopped")
			return
		case <-ticker.C:
			p.processPendingEvents(ctx)
		}
	}
}

func (p *OutboxPublisher) processPendingEvents(ctx context.Context) {
	events, err := p.repo.GetUnpublishedOutboxEvents(ctx, 50)
	if err != nil {
		p.logger.Error("Failed to claim unpublished outbox events", zap.Error(err))
		return
	}

	for _, event := range events {
		topic, err := p.resolveTopic(event.EventType)
		if err != nil {
			p.logger.Error("Outbox event topic resolution failed",
				zap.String("event_id", event.ID),
				zap.String("event_type", event.EventType),
				zap.Error(err),
			)
			if recErr := p.repo.RecordOutboxPublishError(ctx, event.ID, err.Error()); recErr != nil {
				p.logger.Error("Failed to record topic resolution error", zap.Error(recErr))
			}
			continue
		}

		// 1. Deliver event to Kafka broker
		err = p.producer.Publish(ctx, topic, event.PartitionKey, event.Payload)
		if err != nil {
			p.logger.Error("Kafka Producer failed to deliver event",
				zap.String("event_id", event.ID),
				zap.String("topic", topic),
				zap.Error(err),
			)
			// Record failure error with progressive linear backoff delay
			if recErr := p.repo.RecordOutboxPublishError(ctx, event.ID, err.Error()); recErr != nil {
				p.logger.Error("Failed to record outbox publish error", zap.Error(recErr))
			}
			continue
		}

		// 2. Only mark row as PUBLISHED after receiving Kafka delivery ACK!
		if err := p.repo.MarkOutboxEventAsPublished(ctx, event.ID); err != nil {
			p.logger.Error("Failed to mark outbox event as published post-ACK",
				zap.String("event_id", event.ID),
				zap.Error(err),
			)
			continue
		}
	}
}

func (p *OutboxPublisher) resolveTopic(eventType string) (string, error) {
	if topic, ok := p.topicMap[eventType]; ok {
		return topic, nil
	}
	return "", fmt.Errorf("unknown outbox event type: %s", eventType)
}
