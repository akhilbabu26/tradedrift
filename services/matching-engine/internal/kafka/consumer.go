package kafka

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	kafkago "github.com/segmentio/kafka-go"

	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/orderbook"
)

// Topics the ME consumes from — published by Order Service outbox.
const (
	TopicOrderCommands = "orders.commands"
)

type dbQueryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type offsetTracker interface {
	Track(pos orderbook.KafkaPosition)
	MarkDone(ctx context.Context, pos orderbook.KafkaPosition) error
}

type kafkaCommitter interface {
	CommitMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// Consumer reads from the orders.commands Kafka topic and routes InputEvents.
type Consumer struct {
	commandReader          *kafkago.Reader
	manager                *market.MarketManager
	tracker                offsetTracker
	cancelCtx              context.CancelFunc // context cancel func to fail closed gracefully (Issue #10)
	FatalCallback          func()             // fail-stop callback on unrecoverable command corruption
	brokers                []string
	groupID                string
	db                     dbQueryer
	discoverPartitionsFunc func(topic string) ([]int, error)
	commitMessagesFunc     func(ctx context.Context, brokers []string, topic string, groupID string, partition int, offset int64) error
	queryWatermarksFunc    func(ctx context.Context, topic string, partition int) (lwm, hwm int64, err error)
}

type Config struct {
	Brokers []string
	GroupID string
	DB      dbQueryer
}

func NewConsumer(cfg Config, manager *market.MarketManager, tracker offsetTracker) *Consumer {
	c := &Consumer{
		manager: manager,
		tracker: tracker,
		brokers: cfg.Brokers,
		groupID: cfg.GroupID,
		db:      cfg.DB,
		commandReader: kafkago.NewReader(kafkago.ReaderConfig{
			Brokers:        cfg.Brokers,
			Topic:          TopicOrderCommands,
			GroupID:        cfg.GroupID,
			MinBytes:       1,
			MaxBytes:       10e6,
			MaxWait:        1 * time.Second,
			CommitInterval: 0,
		}),
	}
	c.discoverPartitionsFunc = func(topic string) ([]int, error) {
		conn, err := kafkago.Dial("tcp", c.brokers[0])
		if err != nil {
			return nil, err
		}
		parts, err := conn.ReadPartitions(topic)
		conn.Close()
		if err != nil {
			return nil, err
		}
		partitionIDs := make([]int, len(parts))
		for i, p := range parts {
			partitionIDs[i] = p.ID
		}
		return partitionIDs, nil
	}
	c.commitMessagesFunc = func(ctx context.Context, brokers []string, topic string, groupID string, partition int, offset int64) error {
		tempReader := kafkago.NewReader(kafkago.ReaderConfig{
			Brokers: brokers,
			Topic:   topic,
			GroupID: groupID,
		})
		defer tempReader.Close()
		commitCtx, cancelCommit := context.WithTimeout(ctx, 5*time.Second)
		defer cancelCommit()
		return tempReader.CommitMessages(commitCtx, kafkago.Message{
			Topic:     topic,
			Partition: partition,
			Offset:    offset,
		})
	}
	c.queryWatermarksFunc = func(ctx context.Context, topic string, partition int) (int64, int64, error) {
		conn, err := kafkago.DialLeader(ctx, "tcp", c.brokers[0], topic, partition)
		if err != nil {
			return 0, 0, err
		}
		defer conn.Close()
		lwm, err := conn.ReadFirstOffset()
		if err != nil {
			return 0, 0, err
		}
		hwm, err := conn.ReadLastOffset()
		if err != nil {
			return 0, 0, err
		}
		return lwm, hwm, nil
	}

	if committerReg, ok := tracker.(interface {
		RegisterCommitter(topic string, committer kafkaCommitter)
	}); ok {
		committerReg.RegisterCommitter(TopicOrderCommands, c.commandReader)
	}

	return c
}

// Start launches the consumer read loop.
func (c *Consumer) Start(ctx context.Context, cancel context.CancelFunc) error {
	c.cancelCtx = cancel
	if err := c.seekToPostgresCheckpoints(ctx); err != nil {
		log.Printf("[kafka] FATAL positioning error: %v", err)
		cancel()
		return fmt.Errorf("position consumer to postgres checkpoint: %w", err)
	}
	go c.consume(ctx, c.commandReader, c.handleOrderCommand)
	return nil
}

func (c *Consumer) seekToPostgresCheckpoints(ctx context.Context) error {
	if c.db == nil {
		return nil
	}

	// 1. Discover partitions dynamically
	partitions, err := c.discoverPartitionsFunc(TopicOrderCommands)
	if err != nil {
		return fmt.Errorf("discover partitions: %w", err)
	}

	// 2. Commit postgres checkpoint for each partition to seek the group (Issue 1)
	for _, pID := range partitions {
		var savedOffset int64
		err := c.db.QueryRow(ctx,
			`SELECT "offset" FROM kafka_checkpoints WHERE topic = $1 AND partition = $2`,
			TopicOrderCommands, pID,
		).Scan(&savedOffset)
		if err != nil {
			// No PostgreSQL checkpoint for this partition.
			// Clean state bootstrap: establish authoritative baseline on Kafka broker.
			if c.queryWatermarksFunc != nil {
				lwm, hwm, wErr := c.queryWatermarksFunc(ctx, TopicOrderCommands, pID)
				if wErr != nil {
					log.Printf("[kafka] warning: failed to query watermarks for partition %d: %v — relying on consumer group default", pID, wErr)
					continue
				}
				if hwm == 0 {
					// Empty partition — no messages exist.
					log.Printf("[kafka] partition %d is empty (HWM=0), skipping offset commit", pID)
					continue
				}
				// Partition contains messages, but Postgres has no checkpoint.
				// Explicitly position the Kafka group to start consuming from the earliest offset (LWM).
				targetOffset := lwm - 1
				if targetOffset < -1 {
					targetOffset = -1
				}
				if err := c.commitMessagesFunc(ctx, c.brokers, TopicOrderCommands, c.groupID, pID, targetOffset); err != nil {
					return fmt.Errorf("reset Kafka group offset for partition %d to earliest (%d): %w", pID, targetOffset, err)
				}
				log.Printf("[kafka] partition %d has no PostgreSQL checkpoint — positioned broker group offset to %d (LIVE will consume from %d)", pID, targetOffset, targetOffset+1)
			}
			continue
		}

		err = c.commitMessagesFunc(ctx, c.brokers, TopicOrderCommands, c.groupID, pID, savedOffset)
		if err != nil {
			return fmt.Errorf("align Kafka group offset for partition %d to PostgreSQL checkpoint %d: %w", pID, savedOffset, err)
		}
		log.Printf("[kafka] positioned partition %d offset on broker to %d (LIVE will consume from %d)", pID, savedOffset, savedOffset+1)
	}
	return nil
}

func (c *Consumer) Close() error {
	return c.commandReader.Close()
}

// OverrideDiscoveryAndCommit overrides partition discovery and offset committing for unit tests (Issue I).
func (c *Consumer) OverrideDiscoveryAndCommit(
	discover func(topic string) ([]int, error),
	commit func(ctx context.Context, brokers []string, topic string, groupID string, partition int, offset int64) error,
) {
	c.discoverPartitionsFunc = discover
	c.commitMessagesFunc = commit
}

// OverrideQueryWatermarks overrides watermark queries for unit tests.
func (c *Consumer) OverrideQueryWatermarks(fn func(ctx context.Context, topic string, partition int) (int64, int64, error)) {
	c.queryWatermarksFunc = fn
}

func (c *Consumer) consume(
	ctx context.Context,
	reader *kafkago.Reader,
	handler func(msg kafkago.Message) (bool, error),
) {
	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return // context cancelled — graceful shutdown
			}
			log.Printf("[kafka] fetch error: %v — retrying in 500ms", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if _, err := c.processMessage(msg, handler); err != nil {
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
		// INVARIANT (Issue #10): Fail-Closed policy on live malformed command.
		// Trigger application-wide fail-stop shutdown immediately to prevent divergence or CPU spin loops.
		log.Printf("[kafka] FATAL: malformed command (topic=%s partition=%d offset=%d): %v — initiating fail-closed shutdown",
			msg.Topic, msg.Partition, msg.Offset, err)
		if c.cancelCtx != nil {
			c.cancelCtx()
		}
		if c.FatalCallback != nil {
			c.FatalCallback()
		}
		return false, err
	}
	return routed, nil
}

// ProcessMessageForTest exposes single-message processing logic for unit testing fatal shutdown wiring.
func (c *Consumer) ProcessMessageForTest(msg kafkago.Message, handler func(msg kafkago.Message) (bool, error)) (bool, error) {
	return c.processMessage(msg, handler)
}

func (c *Consumer) handleOrderCommand(msg kafkago.Message) (bool, error) {
	return HandleOrderCommand(msg, func(marketID string) chan market.InputEvent {
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
