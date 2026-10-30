package kafka

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"tradedrift/services/matching-engine/internal/lease"
	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/orderbook"
)

// PartitionReader abstracts a pinned, single-partition Kafka reader.
type PartitionReader interface {
	FetchMessage(ctx context.Context) (kafkago.Message, error)
	SetOffset(offset int64) error
	Close() error
}

// PartitionReaderFactory produces a PartitionReader for a given topic and partition.
type PartitionReaderFactory func(brokers []string, topic string, partition int) (PartitionReader, error)

func defaultPartitionReaderFactory(brokers []string, topic string, partition int) (PartitionReader, error) {
	return kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:        brokers,
		Topic:          topic,
		Partition:      partition,
		MinBytes:       1,
		MaxBytes:       10e6,
		MaxWait:        1 * time.Second,
		CommitInterval: 0,
	}), nil
}

// PartitionConsumer encapsulates the isolated intake loop, reader, and lease for one partition.
type PartitionConsumer struct {
	Partition int
	Reader    PartitionReader
	Lease     lease.Lease
}

func (c *Consumer) consumePartition(ctx context.Context, pc *PartitionConsumer) {
	defer func() {
		if pc.Reader != nil {
			_ = pc.Reader.Close()
		}
	}()

	partCtx, partCancel := context.WithCancel(ctx)
	defer partCancel()

	// If lease is configured, monitor it for loss or revocation
	if pc.Lease != nil {
		go func() {
			select {
			case <-pc.Lease.Done():
				log.Printf("[kafka] FATAL: partition %d lease lost — cancelling intake", pc.Partition)
				partCancel()
				c.triggerFatalHalt()
			case <-partCtx.Done():
			}
		}()
	}

	for {
		if pc.Lease != nil && !pc.Lease.IsValid() {
			log.Printf("[kafka] FATAL: partition %d lease invalid — stopping intake", pc.Partition)
			c.triggerFatalHalt()
			return
		}

		msg, err := pc.Reader.FetchMessage(partCtx)
		if err != nil {
			if partCtx.Err() != nil || ctx.Err() != nil {
				return // shutdown
			}
			if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "closed") {
				return // reader closed
			}
			log.Printf("[kafka] partition %d fetch error: %v — retrying in 500ms", pc.Partition, err)
			select {
			case <-time.After(500 * time.Millisecond):
			case <-partCtx.Done():
				return
			}
			continue
		}

		if pc.Lease != nil && !pc.Lease.IsValid() {
			log.Printf("[kafka] FATAL: partition %d lease revoked during fetch — halting", pc.Partition)
			c.triggerFatalHalt()
			return
		}

		if _, err := c.processMessage(msg, c.handleOrderCommand); err != nil {
			return
		}
	}
}

func (c *Consumer) processMessage(msg kafkago.Message, handler func(msg kafkago.Message) (bool, error)) (bool, error) {
	pos := orderbook.KafkaPosition{
		Topic:     msg.Topic,
		Partition: msg.Partition,
		Offset:    msg.Offset,
	}

	if c.tracker != nil {
		c.tracker.Track(pos)
	}

	routed, err := handler(msg)
	if err != nil {
		if c.consumeCtx != nil && c.consumeCtx.Err() != nil {
			return false, err // context cancelled during graceful shutdown
		}
		if errors.Is(err, context.Canceled) {
			return false, err
		}
		// INVARIANT (Issue #10): Fail-Closed policy on live malformed command.
		// Trigger application-wide fail-stop shutdown immediately to prevent divergence or CPU spin loops.
		log.Printf("[kafka] FATAL: malformed command (topic=%s partition=%d offset=%d): %v — initiating fail-closed shutdown",
			msg.Topic, msg.Partition, msg.Offset, err)
		c.triggerFatalHalt()
		return false, err
	}
	return routed, nil
}

// ProcessMessageForTest exposes single-message processing logic for unit testing fatal shutdown wiring.
func (c *Consumer) ProcessMessageForTest(msg kafkago.Message, handler func(msg kafkago.Message) (bool, error)) (bool, error) {
	return c.processMessage(msg, handler)
}

func (c *Consumer) handleOrderCommand(msg kafkago.Message) (bool, error) {
	ctx := c.consumeCtx
	if ctx == nil {
		ctx = context.Background()
	}
	return HandleOrderCommandWithContext(ctx, msg, func(marketID string) chan market.InputEvent {
		engine := c.manager.Get(marketID)
		if engine == nil {
			return nil
		}
		return engine.InputQueue
	})
}

// HandleOrderCommand routes and validates an incoming order command message.
func (c *Consumer) HandleOrderCommand(msg kafkago.Message) (bool, error) {
	return c.handleOrderCommand(msg)
}
