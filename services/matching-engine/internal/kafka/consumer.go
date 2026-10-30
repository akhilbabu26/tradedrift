package kafka

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	kafkago "github.com/segmentio/kafka-go"

	"tradedrift/services/matching-engine/internal/checkpoint"
	"tradedrift/services/matching-engine/internal/lease"
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
// Consumer reads from the orders.commands Kafka topic using isolated per-partition readers.
type Consumer struct {
	commandReader          *kafkago.Reader // used for group commit registration / compatibility
	manager                *market.MarketManager
	tracker                offsetTracker
	cancelCtx              context.CancelFunc // context cancel func to fail closed gracefully (Issue #10)
	consumeCtx             context.Context
	consumeWg              sync.WaitGroup
	FatalCallback          func() // fail-stop callback on unrecoverable command corruption or lease loss
	brokers                []string
	groupID                string
	db                     dbQueryer
	lockAcquirer           lease.LockAcquirer
	leases                 map[int]lease.Lease
	leasesMu               sync.Mutex
	partitions             map[int]*PartitionConsumer
	partitionMu            sync.RWMutex
	newReaderFunc          PartitionReaderFactory
	discoverPartitionsFunc func(topic string) ([]int, error)
	commitMessagesFunc     func(ctx context.Context, brokers []string, topic string, groupID string, partition int, offset int64) error
	queryWatermarksFunc    func(ctx context.Context, topic string, partition int) (lwm, hwm int64, err error)
}

type Config struct {
	Brokers      []string
	GroupID      string
	DB           dbQueryer
	LockAcquirer lease.LockAcquirer
	Leases       map[int]lease.Lease
}

func NewConsumer(cfg Config, manager *market.MarketManager, tracker offsetTracker) *Consumer {
	c := &Consumer{
		manager:       manager,
		tracker:       tracker,
		brokers:       cfg.Brokers,
		groupID:       cfg.GroupID,
		db:            cfg.DB,
		lockAcquirer:  cfg.LockAcquirer,
		leases:        make(map[int]lease.Lease),
		partitions:    make(map[int]*PartitionConsumer),
		newReaderFunc: defaultPartitionReaderFactory,
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
	if cfg.Leases != nil {
		for p, l := range cfg.Leases {
			c.leases[p] = l
		}
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

	if committerReg, ok := tracker.(checkpoint.CommitterRegistrar); ok {
		committerReg.RegisterCommitter(TopicOrderCommands, c.commandReader)
	}

	return c
}

// SetPartitionLease attaches an existing lease for a partition.
func (c *Consumer) SetPartitionLease(partition int, l lease.Lease) {
	c.leasesMu.Lock()
	defer c.leasesMu.Unlock()
	c.leases[partition] = l
}

// OverridePartitionReaderFactory allows unit tests to inject mock PartitionReaders.
func (c *Consumer) OverridePartitionReaderFactory(fn PartitionReaderFactory) {
	c.newReaderFunc = fn
}

func (c *Consumer) getTargetPartitions() ([]int, error) {
	if c.manager != nil {
		engines := c.manager.All()
		if len(engines) > 0 {
			partMap := make(map[int]bool)
			for _, eng := range engines {
				partMap[eng.Partition()] = true
			}
			parts := make([]int, 0, len(partMap))
			for p := range partMap {
				parts = append(parts, p)
			}
			sort.Ints(parts)
			return parts, nil
		}
	}
	if c.discoverPartitionsFunc != nil {
		parts, err := c.discoverPartitionsFunc(TopicOrderCommands)
		if err == nil {
			return parts, nil
		}
		if c.db == nil {
			// In offline test environments without a database or Kafka broker, default to partition 0.
			return []int{0}, nil
		}
		return nil, err
	}
	return []int{0}, nil
}

// Start launches dedicated intake loops for all owned partitions.
func (c *Consumer) Start(ctx context.Context, cancel context.CancelFunc) error {
	c.cancelCtx = cancel
	c.consumeCtx = ctx

	// 1. Identify target partitions to consume
	partitions, err := c.getTargetPartitions()
	if err != nil {
		log.Printf("[kafka] FATAL partition discovery error: %v", err)
		cancel()
		return fmt.Errorf("discover partitions: %w", err)
	}

	c.partitionMu.Lock()
	defer c.partitionMu.Unlock()

	// 2. Initialize each partition consumer: verify/acquire lease and position standalone reader
	for _, pID := range partitions {
		c.leasesMu.Lock()
		l, hasLease := c.leases[pID]
		if !hasLease && c.lockAcquirer != nil {
			acquiredLease, err := c.lockAcquirer.AcquireLease(ctx, pID)
			if err != nil {
				c.leasesMu.Unlock()
				log.Printf("[kafka] FATAL: failed to acquire lease for partition %d: %v", pID, err)
				cancel()
				return fmt.Errorf("acquire lease for partition %d: %w", pID, err)
			}
			l = acquiredLease
			c.leases[pID] = l
		}
		c.leasesMu.Unlock()

		if l != nil && !l.IsValid() {
			log.Printf("[kafka] FATAL: invalid lease for partition %d", pID)
			cancel()
			return fmt.Errorf("invalid lease for partition %d", pID)
		}

		startOffset, err := c.resolvePartitionStartOffset(ctx, pID)
		if err != nil {
			log.Printf("[kafka] FATAL: failed to resolve start offset for partition %d: %v", pID, err)
			cancel()
			return fmt.Errorf("resolve start offset for partition %d: %w", pID, err)
		}

		reader, err := c.newReaderFunc(c.brokers, TopicOrderCommands, pID)
		if err != nil {
			log.Printf("[kafka] FATAL: failed to create partition reader for partition %d: %v", pID, err)
			cancel()
			return fmt.Errorf("create partition %d reader: %w", pID, err)
		}

		if err := reader.SetOffset(startOffset); err != nil {
			log.Printf("[kafka] FATAL: failed to set offset %d for partition %d: %v", startOffset, pID, err)
			_ = reader.Close()
			cancel()
			return fmt.Errorf("set partition %d offset to %d: %w", pID, startOffset, err)
		}

		pc := &PartitionConsumer{
			Partition: pID,
			Reader:    reader,
			Lease:     l,
		}
		c.partitions[pID] = pc
		log.Printf("[kafka] partition %d reader initialized at offset %d (lease active: %v)",
			pID, startOffset, l != nil && l.IsValid())
	}

	// 3. Launch isolated intake goroutine per partition
	for _, pc := range c.partitions {
		c.consumeWg.Add(1)
		go func(consumer *PartitionConsumer) {
			defer c.consumeWg.Done()
			c.consumePartition(ctx, consumer)
		}(pc)
	}

	return nil
}

func (c *Consumer) resolvePartitionStartOffset(ctx context.Context, pID int) (int64, error) {
	if c.db == nil {
		return kafkago.FirstOffset, nil
	}

	var savedOffset int64
	err := c.db.QueryRow(ctx,
		`SELECT "offset" FROM kafka_checkpoints WHERE topic = $1 AND partition = $2`,
		TopicOrderCommands, pID,
	).Scan(&savedOffset)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("query checkpoint for partition %d: %w", pID, err)
		}
		// Truly no PostgreSQL checkpoint for this partition.
		if c.queryWatermarksFunc != nil {
			lwm, hwm, wErr := c.queryWatermarksFunc(ctx, TopicOrderCommands, pID)
			if wErr != nil {
				return 0, fmt.Errorf("query watermarks for uncheckpointed partition %d: %w", pID, wErr)
			}
			if hwm == 0 {
				log.Printf("[kafka] partition %d is empty (HWM=0), skipping offset commit", pID)
				return kafkago.FirstOffset, nil
			}
			targetOffset := lwm - 1
			if targetOffset < -1 {
				targetOffset = -1
			}
			if c.commitMessagesFunc != nil {
				if err := c.commitMessagesFunc(ctx, c.brokers, TopicOrderCommands, c.groupID, pID, targetOffset); err != nil {
					return 0, fmt.Errorf("reset Kafka group offset for partition %d to earliest (%d): %w", pID, targetOffset, err)
				}
			}
			log.Printf("[kafka] partition %d has no PostgreSQL checkpoint — positioned to earliest %d (HWM=%d)", pID, lwm, hwm)
			return lwm, nil
		}
		return kafkago.FirstOffset, nil
	}

	if c.commitMessagesFunc != nil {
		if err := c.commitMessagesFunc(ctx, c.brokers, TopicOrderCommands, c.groupID, pID, savedOffset); err != nil {
			return 0, fmt.Errorf("align Kafka group offset for partition %d to PostgreSQL checkpoint %d: %w", pID, savedOffset, err)
		}
	}
	log.Printf("[kafka] aligned partition %d offset on broker to %d (LIVE will consume from %d)", pID, savedOffset, savedOffset+1)
	return savedOffset + 1, nil
}

// Stop gracefully stops consumer intake across all partition readers:
// cancels intake context, closes readers, and synchronously waits for all goroutines to exit.
func (c *Consumer) Stop(timeout time.Duration) error {
	if c.cancelCtx != nil {
		c.cancelCtx()
	}

	c.partitionMu.Lock()
	var closeErrs []string
	if c.commandReader != nil {
		if err := c.commandReader.Close(); err != nil {
			closeErrs = append(closeErrs, err.Error())
		}
	}
	for _, pc := range c.partitions {
		if pc.Reader != nil {
			if err := pc.Reader.Close(); err != nil {
				closeErrs = append(closeErrs, err.Error())
			}
		}
	}
	c.partitionMu.Unlock()

	done := make(chan struct{})
	go func() {
		c.consumeWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		c.partitionMu.Lock()
		c.partitions = make(map[int]*PartitionConsumer)
		c.partitionMu.Unlock()
		if len(closeErrs) > 0 {
			return errors.New(strings.Join(closeErrs, "; "))
		}
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("consumer stop timed out after %v: intake goroutine did not exit", timeout)
	}
}

// Close closes the consumer with a default 5-second graceful exit timeout.
func (c *Consumer) Close() error {
	return c.Stop(5 * time.Second)
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

func (c *Consumer) triggerFatalHalt() {
	if c.cancelCtx != nil {
		c.cancelCtx()
	}
	if c.FatalCallback != nil {
		c.FatalCallback()
	}
}

