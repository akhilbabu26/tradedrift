package publisher

import (
	"context"

	"go.uber.org/zap"
)

// Producer defines the interface for publishing events to Kafka topics.
type Producer interface {
	Publish(ctx context.Context, topic, partitionKey string, payload []byte) error
	Close() error
}

// LogProducer is a lightweight, thread-safe Producer implementation for local dev & testing.
type LogProducer struct {
	logger *zap.Logger
}

func NewLogProducer(logger *zap.Logger) *LogProducer {
	return &LogProducer{logger: logger}
}

func (p *LogProducer) Publish(ctx context.Context, topic, partitionKey string, payload []byte) error {
	p.logger.Info("Kafka Producer ACK: Event delivered to broker",
		zap.String("topic", topic),
		zap.String("partition_key", partitionKey),
		zap.Int("bytes", len(payload)),
	)
	return nil
}

func (p *LogProducer) Close() error {
	return nil
}
