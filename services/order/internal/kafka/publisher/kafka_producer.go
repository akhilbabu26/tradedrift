package publisher

import (
	"context"
	"fmt"

	kafkago "github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"tradedrift/platform/config"
)

// KafkaProducer is a real Kafka producer using kafka-go Writer.
// It implements the Publisher interface and explicitly routes orders by market partition.
type KafkaProducer struct {
	writer             *kafkago.Writer
	logger             *zap.Logger
	partitionOverrides map[string]int
}

// NewKafkaProducer creates a KafkaProducer connected to the given brokers.
// It explicitly resolves per-market partitions from environment config (BTC:0, ETH:1, SOL:2).
func NewKafkaProducer(brokers []string, logger *zap.Logger) (*KafkaProducer, error) {
	btcPart, err := config.GetEnvAsInt("BTC_PARTITION", 0)
	if err != nil {
		return nil, fmt.Errorf("BTC_PARTITION: %w", err)
	}
	ethPart, err := config.GetEnvAsInt("ETH_PARTITION", 1)
	if err != nil {
		return nil, fmt.Errorf("ETH_PARTITION: %w", err)
	}
	solPart, err := config.GetEnvAsInt("SOL_PARTITION", 2)
	if err != nil {
		return nil, fmt.Errorf("SOL_PARTITION: %w", err)
	}

	w := &kafkago.Writer{
		Addr:         kafkago.TCP(brokers...),
		RequiredAcks: kafkago.RequireOne,
		// Explicit partition balancer: kafka-go defaults to RoundRobin if Balancer is nil,
		// which ignores msg.Partition. By providing a BalancerFunc that returns msg.Partition,
		// we guarantee the message is delivered to its exact assigned market partition.
		Balancer: kafkago.BalancerFunc(func(msg kafkago.Message, partitions ...int) int {
			return msg.Partition
		}),
	}

	return &KafkaProducer{
		writer: w,
		logger: logger,
		partitionOverrides: map[string]int{
			"BTC-USDT": btcPart,
			"ETH-USDT": ethPart,
			"SOL-USDT": solPart,
		},
	}, nil
}

// ResolvePartition maps a marketID to its explicit designated Kafka partition.
func (p *KafkaProducer) ResolvePartition(marketID string) int {
	if p.partitionOverrides != nil {
		if part, ok := p.partitionOverrides[marketID]; ok {
			return part
		}
	}
	return 0
}

// Publish sends a message to the given Kafka topic with explicit partition routing.
func (p *KafkaProducer) Publish(ctx context.Context, topic, partitionKey string, payload []byte) error {
	partition := p.ResolvePartition(partitionKey)

	msg := kafkago.Message{
		Topic:     topic,
		Partition: partition,
		Key:       []byte(partitionKey),
		Value:     payload,
	}

	if err := p.writer.WriteMessages(ctx, msg); err != nil {
		return fmt.Errorf("kafka write to topic %q partition %d: %w", topic, partition, err)
	}

	p.logger.Info("Kafka Producer ACK: Event delivered to broker",
		zap.String("topic", topic),
		zap.String("partition_key", partitionKey),
		zap.Int("partition", partition),
		zap.Int("bytes", len(payload)),
	)
	return nil
}

// Close shuts down the Kafka writer cleanly, flushing pending messages.
func (p *KafkaProducer) Close() error {
	return p.writer.Close()
}
