package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"tradedrift/services/portfolio/internal/metrics"
	"tradedrift/services/portfolio/internal/repository"
)

type OutboxPublisher struct {
	writer       *kafkago.Writer
	repo         repository.Repository
	logger       *zap.Logger
	pollInterval time.Duration
	batchSize    int
}

func NewOutboxPublisher(
	brokers []string,
	topic string,
	repo repository.Repository,
	logger *zap.Logger,
) *OutboxPublisher {
	writer := &kafkago.Writer{
		Addr:         kafkago.TCP(brokers...),
		Topic:        topic,
		Balancer:     &kafkago.Hash{},
		WriteTimeout: 5 * time.Second,
		RequiredAcks: kafkago.RequireAll,
	}

	return &OutboxPublisher{
		writer:       writer,
		repo:         repo,
		logger:       logger,
		pollInterval: 100 * time.Millisecond,
		batchSize:    50,
	}
}

func (p *OutboxPublisher) Start(ctx context.Context) error {
	p.logger.Info("starting portfolio outbox publisher loop")
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("outbox publisher context cancelled, exiting")
			return nil
		case <-ticker.C:
			if err := p.publishPending(ctx); err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				p.logger.Error("failed to process pending outbox messages", zap.Error(err))
			}
		}
	}
}

func (p *OutboxPublisher) publishPending(ctx context.Context) error {
	messages, err := p.repo.FetchPendingOutbox(ctx, p.batchSize)
	if err != nil {
		return fmt.Errorf("fetch pending outbox: %w", err)
	}

	metrics.OutboxPending.Set(float64(len(messages)))
	if len(messages) == 0 {
		return nil
	}

	kafkaMessages := make([]kafkago.Message, 0, len(messages))
	ids := make([]string, 0, len(messages))

	for _, msg := range messages {
		kafkaMessages = append(kafkaMessages, kafkago.Message{
			Key:   []byte(msg.PartitionKey), // user_id ensures sequential processing per user
			Value: msg.Payload,
			Time:  msg.CreatedAt,
			Headers: []kafkago.Header{
				{Key: "event-type", Value: []byte(msg.EventType)},
				{Key: "event-id", Value: []byte(msg.ID)},
			},
		})
		ids = append(ids, msg.ID)
	}

	// 1. Write batch to Kafka
	if err := p.writer.WriteMessages(ctx, kafkaMessages...); err != nil {
		metrics.OutboxPublishTotal.WithLabelValues("error").Add(float64(len(kafkaMessages)))
		return fmt.Errorf("write outbox messages to kafka: %w", err)
	}

	// 2. Mark records as PUBLISHED
	if err := p.repo.MarkOutboxPublished(ctx, ids); err != nil {
		metrics.OutboxPublishTotal.WithLabelValues("error").Add(float64(len(ids)))
		return fmt.Errorf("mark outbox published: %w", err)
	}

	metrics.OutboxPublishTotal.WithLabelValues("success").Add(float64(len(ids)))
	p.logger.Debug("published portfolio outbox batch", zap.Int("count", len(ids)))
	return nil
}

func (p *OutboxPublisher) Close() error {
	return p.writer.Close()
}
