package publisher

import (
	"context"
	"fmt"

	kafkago "github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

// KafkaProducer is a real Kafka producer using kafka-go Writer.
// It implements the Publisher interface and actually delivers messages to the broker.
type KafkaProducer struct {
	writer *kafkago.Writer
	logger *zap.Logger
}

// NewKafkaProducer creates a KafkaProducer connected to the given brokers.
// The writer uses manual topic routing — topic is passed per-message.
func NewKafkaProducer(brokers []string, logger *zap.Logger) *KafkaProducer {
	w := &kafkago.Writer{
		Addr:                   kafkago.TCP(brokers...),
		Balancer:               &kafkago.Hash{}, // partition by partition key
		AllowAutoTopicCreation: true,            // auto-create topic if not exists
		RequiredAcks:           kafkago.RequireOne,
	}
	return &KafkaProducer{writer: w, logger: logger}
}

// Publish sends a message to the given Kafka topic with the given partition key and payload.
func (p *KafkaProducer) Publish(ctx context.Context, topic, partitionKey string, payload []byte) error {
	msg := kafkago.Message{
		Topic: topic,
		Key:   []byte(partitionKey),
		Value: payload,
	}

	if err := p.writer.WriteMessages(ctx, msg); err != nil {
		return fmt.Errorf("kafka write to topic %q: %w", topic, err)
	}

	p.logger.Info("Kafka Producer ACK: Event delivered to broker",
		zap.String("topic", topic),
		zap.String("partition_key", partitionKey),
		zap.Int("bytes", len(payload)),
	)
	return nil
}

// Close shuts down the Kafka writer cleanly, flushing pending messages.
func (p *KafkaProducer) Close() error {
	return p.writer.Close()
}
