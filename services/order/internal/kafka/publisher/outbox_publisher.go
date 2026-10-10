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
	producer      Producer
	logger        *zap.Logger
	interval      time.Duration
	retention     time.Duration
	pruneInterval time.Duration
	topicMap      map[string]string
}

func NewOutboxPublisher(repo repository.OutboxRepository, producer Producer, logger *zap.Logger, interval time.Duration) *OutboxPublisher {
	return NewOutboxPublisherWithRetention(repo, producer, logger, interval, 24*time.Hour, 10*time.Minute)
}

func NewOutboxPublisherWithRetention(repo repository.OutboxRepository, producer Producer, logger *zap.Logger, interval time.Duration, retention time.Duration, pruneInterval time.Duration) *OutboxPublisher {
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	if pruneInterval <= 0 {
		pruneInterval = 10 * time.Minute
	}
	return &OutboxPublisher{
		repo:          repo,
		producer:      producer,
		logger:        logger,
		interval:      interval,
		retention:     retention,
		pruneInterval: pruneInterval,
		topicMap: map[string]string{
			"OrderCreated":         "orders.commands",
			"OrderCancelRequested": "orders.commands",
		},
	}
}

func (p *OutboxPublisher) Start(ctx context.Context) {
	p.logger.Info("Starting Outbox Publisher background loop",
		zap.Duration("interval", p.interval),
		zap.Duration("retention", p.retention),
		zap.Duration("prune_interval", p.pruneInterval),
	)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	pruneTicker := time.NewTicker(p.pruneInterval)
	defer pruneTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("Outbox Publisher worker loop stopped")
			return
		case <-ticker.C:
			p.processPendingEvents(ctx)
		case <-pruneTicker.C:
			p.prunePublishedEvents(ctx)
		}
	}
}

func (p *OutboxPublisher) prunePublishedEvents(ctx context.Context) {
	deleted, err := p.repo.PrunePublishedOutboxEvents(ctx, p.retention, 1000)
	if err != nil {
		p.logger.Error("Failed to prune old published outbox events", zap.Error(err))
		return
	}
	if deleted > 0 {
		p.logger.Info("Reaped published outbox events",
			zap.Int64("deleted_rows", deleted),
			zap.Duration("retention", p.retention),
		)
	}
}

// PrunePublishedEvents exposes on-demand outbox reaping for testing and operational maintenance.
func (p *OutboxPublisher) PrunePublishedEvents(ctx context.Context) (int64, error) {
	return p.repo.PrunePublishedOutboxEvents(ctx, p.retention, 1000)
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
