package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/shopspring/decimal"

	"tradedrift/services/matching-engine/internal/checkpoint"
	intkafka "tradedrift/services/matching-engine/internal/kafka"
	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/publisher"
	"tradedrift/services/matching-engine/internal/recovery"
)

func main() {
	if err := run(); err != nil {
		log.Printf("[server] fatal: %v", err)
		os.Exit(1)
	}
}

type config struct {
	KafkaBrokers []string
	KafkaGroupID string
	PostgresDSN  string
	RedisAddr    string
}

func loadConfig() (config, error) {
	postgresDSN := os.Getenv("POSTGRES_DSN")
	if postgresDSN == "" {
		return config{}, fmt.Errorf("POSTGRES_DSN env var is required")
	}

	return config{
		KafkaBrokers: strings.Split(getEnv("KAFKA_BROKERS", "localhost:9092"), ","),
		KafkaGroupID: getEnv("KAFKA_GROUP_ID", "matching-engine"),
		PostgresDSN:  postgresDSN,
		RedisAddr:    getEnv("REDIS_ADDR", "localhost:6379"),
	}, nil
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func marketConfigs() []market.MarketConfig {
	return []market.MarketConfig{
		{
			MarketID:         "BTC-USDT",
			TickSize:         decimal.RequireFromString("0.01"),
			LotSize:          decimal.RequireFromString("0.00001"),
			Partition:        0,
			SnapshotInterval: 10000,
			SnapshotDuration: 60 * time.Second,
		},
		{
			MarketID:         "ETH-USDT",
			TickSize:         decimal.RequireFromString("0.01"),
			LotSize:          decimal.RequireFromString("0.0001"),
			Partition:        0,
			SnapshotInterval: 10000,
			SnapshotDuration: 60 * time.Second,
		},
		{
			MarketID:         "SOL-USDT",
			TickSize:         decimal.RequireFromString("0.001"),
			LotSize:          decimal.RequireFromString("0.01"),
			Partition:        0,
			SnapshotInterval: 10000,
			SnapshotDuration: 60 * time.Second,
		},
	}
}

func validateMarketConfigs(configs []market.MarketConfig) error {
	for _, mc := range configs {
		if mc.TickSize.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("market %s: TickSize must be > 0 (got %s)", mc.MarketID, mc.TickSize)
		}
		if mc.LotSize.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("market %s: LotSize must be > 0 (got %s)", mc.MarketID, mc.LotSize)
		}
	}
	return nil
}

func run() error {
	opCtx, opCancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer opCancel()

	log.Println("[server] starting TradeDrift Matching Engine")

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Dynamic partition assignment based on Kafka balancer (Issue #4)
	var partitionList []kafkago.Partition
	{
		kConn, err := kafkago.Dial("tcp", cfg.KafkaBrokers[0])
		if err != nil {
			return fmt.Errorf("dial kafka for partition discovery: %w", err)
		}
		parts, err := kConn.ReadPartitions(intkafka.TopicOrderCommands)
		kConn.Close()
		if err != nil {
			return fmt.Errorf("read partitions for TopicOrderCommands: %w", err)
		}
		partitionList = parts
	}

	configs := marketConfigs()
	balancer := &kafkago.Hash{}
	partitionIDs := make([]int, len(partitionList))
	for i, p := range partitionList {
		partitionIDs[i] = p.ID
	}
	for i := range configs {
		msg := kafkago.Message{Key: []byte(configs[i].MarketID)}
		pID := balancer.Balance(msg, partitionIDs...)
		configs[i].Partition = pID
		log.Printf("[server] dynamically mapped market %s to partition %d", configs[i].MarketID, pID)
	}

	if err := validateMarketConfigs(configs); err != nil {
		return fmt.Errorf("validate market configs: %w", err)
	}

	db, err := pgxpool.New(opCtx, cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("postgres connect: %w", err)
	}
	defer db.Close()

	if err := db.Ping(opCtx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}
	log.Println("[server] postgres connected")

	// Create tables
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS kafka_checkpoints (
		topic      VARCHAR(255) NOT NULL,
		partition  INTEGER      NOT NULL,
		"offset"     BIGINT       NOT NULL,
		updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
		PRIMARY KEY (topic, partition)
	);`
	if _, err := db.Exec(opCtx, createTableSQL); err != nil {
		return fmt.Errorf("init kafka_checkpoints table: %w", err)
	}

	createSeqTableSQL := `
	CREATE TABLE IF NOT EXISTS market_sequences (
		market_id  VARCHAR(64)  NOT NULL,
		sequence   BIGINT       NOT NULL DEFAULT 0,
		updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
		PRIMARY KEY (market_id)
	);`
	if _, err := db.Exec(opCtx, createSeqTableSQL); err != nil {
		return fmt.Errorf("init market_sequences table: %w", err)
	}

	// Schema creation for market_snapshots (Issue #9)
	createSnapTableSQL := `
	CREATE TABLE IF NOT EXISTS market_snapshots (
		market_id      VARCHAR(64)  NOT NULL,
		sequence       BIGINT       NOT NULL,
		partition      INTEGER      NOT NULL,
		"offset"       BIGINT       NOT NULL,
		schema_version INTEGER      NOT NULL,
		snapshot       JSONB        NOT NULL,
		checksum       BYTEA        NOT NULL,
		created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
		PRIMARY KEY (market_id, sequence)
	);`
	if _, err := db.Exec(opCtx, createSnapTableSQL); err != nil {
		return fmt.Errorf("init market_snapshots table: %w", err)
	}

	createSnapIndexSQL := `
	CREATE INDEX IF NOT EXISTS idx_market_snapshots_market_sequence ON market_snapshots (market_id, sequence DESC);`
	if _, err := db.Exec(opCtx, createSnapIndexSQL); err != nil {
		return fmt.Errorf("init market_snapshots index: %w", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddr,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})
	defer rdb.Close()

	if err := rdb.Ping(opCtx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}
	log.Println("[server] redis connected")

	manager := market.NewMarketManager()
	for _, mc := range configs {
		engine := manager.Add(mc)
		// Set fail-stop HaltCallback (Issue #1 & #10)
		engine.HaltCallback = func() {
			log.Printf("[server] market engine %s triggered fail-stop halt", mc.MarketID)
			opCancel()
		}
		log.Printf("[server] registered market: %s", mc.MarketID)
	}

	var engineWg sync.WaitGroup
	log.Println("[server] starting recovery phase...")
	replayer := recovery.NewReplayer(cfg.KafkaBrokers, cfg.KafkaGroupID, db, rdb, manager)
	if err := replayer.ReplayAll(opCtx, &engineWg); err != nil {
		return fmt.Errorf("recovery failed: %w", err)
	}
	log.Printf("[server] recovery complete — %d markets in LIVE mode", len(manager.All()))

	coord := checkpoint.NewCoordinator(checkpoint.WrapPGXPool(db))

	// Discover and initialize partition baselines
	{
		kConn, err := kafkago.Dial("tcp", cfg.KafkaBrokers[0])
		if err != nil {
			return fmt.Errorf("dial kafka for partition discovery: %w", err)
		}
		parts, err := kConn.ReadPartitions(intkafka.TopicOrderCommands)
		kConn.Close()
		if err != nil {
			return fmt.Errorf("read partitions for checkpoint baseline: %w", err)
		}
		for _, p := range parts {
			var savedOffset int64
			err := db.QueryRow(opCtx,
				`SELECT "offset" FROM kafka_checkpoints WHERE topic = $1 AND partition = $2`,
				intkafka.TopicOrderCommands, p.ID,
			).Scan(&savedOffset)
			if err == nil {
				coord.InitBaseline(intkafka.TopicOrderCommands, p.ID, savedOffset)
				log.Printf("[server] checkpoint baseline: %s/partition=%d @ offset=%d",
					intkafka.TopicOrderCommands, p.ID, savedOffset)
			}
		}
	}

	pub := publisher.NewPublisher(cfg.KafkaBrokers, rdb, coord, db)
	pub.HaltCallback = func() {
		log.Println("[server] publisher initiated fail-stop halt due to fatal side-effect failure")
		opCancel()
	}
	defer pub.Close()

	pubCtx, cancelPub := context.WithCancel(context.Background())
	defer cancelPub()

	var wg sync.WaitGroup
	for _, engine := range manager.All() {
		wg.Add(1)
		e := engine
		go func() {
			defer wg.Done()
			pub.Run(pubCtx, e)
		}()
		log.Printf("[server] publisher started for market: %s", e.MarketID)
	}

	consumer := intkafka.NewConsumer(intkafka.Config{
		Brokers: cfg.KafkaBrokers,
		GroupID: cfg.KafkaGroupID,
	}, manager, coord)
	defer consumer.Close()

	consumerCtx, cancelConsumer := context.WithCancel(context.Background())
	defer cancelConsumer()

	// Consumer Start takes cancel function for fail-closed graceful shutdown (Issue #10)
	consumer.Start(consumerCtx, cancelConsumer)
	log.Println("[server] kafka consumer started")
	log.Println("[server] ✓ all systems live — matching engine ready")

	<-opCtx.Done()

	// DETERMINISTIC STAGED GRACEFUL SHUTDOWN (Issue #6)
	log.Println("[server] [shutdown phase 1/4] stopping consumer intake...")
	cancelConsumer()
	consumer.Close()

	log.Println("[server] [shutdown phase 2/4] closing engine input queues...")
	manager.CloseInputQueues()

	log.Println("[server] [shutdown phase 3/4] waiting for engines to exit...")
	engineWg.Wait()

	log.Println("[server] [shutdown phase 4/4] waiting for publisher drain...")
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("[server] graceful shutdown complete")
	case <-time.After(30 * time.Second): // Graceful shutdown timeout (Issue #8)
		log.Println("[server] shutdown timeout expired (30s) — forcing process termination")
		os.Exit(1)
	}

	return nil
}
