package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	kafkago "github.com/segmentio/kafka-go"

	"tradedrift/platform/postgres"
	"tradedrift/services/matching-engine/internal/checkpoint"
	intkafka "tradedrift/services/matching-engine/internal/kafka"
	"tradedrift/services/matching-engine/internal/lease"
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

func run() error {
	opCtx, opCancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer opCancel()

	log.Println("[server] starting TradeDrift Matching Engine")

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	configs, err := marketConfigs()
	if err != nil {
		return fmt.Errorf("load market configs: %w", err)
	}
	for _, mc := range configs {
		log.Printf("[server] using static assignment for market %s on partition %d", mc.MarketID, mc.Partition)
	}

	if err := validateMarketConfigs(configs); err != nil {
		return fmt.Errorf("validate market configs: %w", err)
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("parse postgres config: %w", err)
	}
	// Sizing: Each partition lease checks out 1 dedicated connection for the process lifetime.
	// We ensure MaxConns provides enough headroom for leases (1 per market) + concurrent transactions.
	minRequired := int32(len(configs) + 25)
	if minRequired < 30 {
		minRequired = 30
	}
	poolCfg.MaxConns = minRequired
	poolCfg.MinConns = 10

	db, err := pgxpool.NewWithConfig(opCtx, poolCfg)
	if err != nil {
		return fmt.Errorf("postgres connect: %w", err)
	}
	defer db.Close()

	if err := db.Ping(opCtx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}
	log.Println("[server] postgres connected")

	// STAGE 3: Acquire PostgreSQL session advisory lock lease for each partition BEFORE recovery.
	// Guarantees single-engine ownership: only one Matching Engine instance can own and mutate a partition.
	leaseMgr := lease.NewPGXPoolLeaseManager(db)
	partitionLeases := make(map[int]lease.Lease)
	for _, mc := range configs {
		l, err := leaseMgr.AcquireLease(opCtx, mc.Partition)
		if err != nil {
			for _, held := range partitionLeases {
				_ = held.Release(context.Background())
			}
			return fmt.Errorf("fail-closed: failed to acquire exclusive partition lease for %s (partition %d): %w",
				mc.MarketID, mc.Partition, err)
		}
		partitionLeases[mc.Partition] = l
		log.Printf("[server] acquired exclusive partition lease for %s (partition %d, lock_id=0x%X)",
			mc.MarketID, mc.Partition, lease.PartitionLockID(mc.Partition))
	}

	defer func() {
		relCtx, relCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer relCancel()
		for p, l := range partitionLeases {
			if err := l.Release(relCtx); err != nil {
				log.Printf("[server] error releasing lease for partition %d: %v", p, err)
			} else {
				log.Printf("[server] cleanly released lease for partition %d", p)
			}
		}
	}()



	// Run database migrations on startup (Issue E & v9.9 Tables setup)
	migrationDir := os.Getenv("ME_MIGRATIONS_DIR")
	if migrationDir == "" {
		migrationDir = "services/matching-engine/migration"
	}
	if _, err := os.Stat(migrationDir); os.IsNotExist(err) {
		if _, err := os.Stat("migration"); err == nil {
			migrationDir = "migration"
		}
	}
	log.Printf("[server] running database migrations from dir: %s...", migrationDir)
	if err := postgres.RunMigrations(cfg.PostgresDSN, migrationDir); err != nil {
		return fmt.Errorf("postgres migrations: %w", err)
	}
	log.Println("[server] database migrations applied successfully")

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
		e := engine
		// Set fail-stop HaltCallback (Issue #1 & #10)
		e.HaltCallback = func() {
			log.Printf("[server] market engine %s triggered fail-stop halt", e.MarketID)
			for _, eng := range manager.All() {
				eng.TriggerFatalHalt()
			}
			opCancel()
		}
		log.Printf("[server] registered market: %s", mc.MarketID)
	}

	// Monitor partition lease liveness in background.
	// If any lease is lost (e.g. database disconnect), fail-stop immediately and halt all engines.
	for p, l := range partitionLeases {
		go func(part int, le lease.Lease) {
			select {
			case <-le.Done():
				log.Printf("[server] FATAL: partition %d lease lost — triggering fail-stop shutdown and halting all engines", part)
				for _, eng := range manager.All() {
					eng.TriggerFatalHalt()
				}
				opCancel()
			case <-opCtx.Done():
			}
		}(p, l)
	}

	var engineWg sync.WaitGroup
	log.Println("[server] starting recovery phase...")

	leaseMap := make(map[int]recovery.Lease, len(partitionLeases))
	for p, l := range partitionLeases {
		leaseMap[p] = l
	}
	leaseChecker := recovery.NewMapLeaseChecker(leaseMap)

	replayer := recovery.NewReplayer(cfg.KafkaBrokers, cfg.KafkaGroupID, db, rdb, manager)
	replayer.SetLeaseChecker(leaseChecker)
	if err := replayer.ReplayAll(opCtx, &engineWg); err != nil {
		return fmt.Errorf("recovery failed: %w", err)
	}
	log.Printf("[server] recovery complete — %d markets in LIVE mode", len(manager.All()))

	coord := checkpoint.NewCoordinator(checkpoint.WrapPGXPool(db))

	// Initialize baseline checkpoints for coordinator from DB (Issue E)
	{
		kConn, err := kafkago.Dial("tcp", cfg.KafkaBrokers[0])
		if err != nil {
			return fmt.Errorf("dial kafka for checkpoint baseline: %w", err)
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
		for _, eng := range manager.All() {
			eng.TriggerFatalHalt()
		}
		opCancel()
	}
	defer pub.Close()

	pubCtx, cancelPub := context.WithCancel(opCtx)
	defer cancelPub()

	var isReady atomic.Bool
	var isDegraded atomic.Bool

	// Seed initial depth snapshots from authoritative in-memory books to Redis before live intake.
	// User Requirement #3: If seed fails, ME can continue matching but readiness remains degraded (503).
	for _, eng := range manager.All() {
		if eng.Mode() == market.ModeBlocked {
			log.Printf("[server] market %s is BLOCKED — skipping initial Redis depth seed", eng.MarketID)
			continue
		}
		snap := eng.GetDepth(20)
		snap.SnapshotAt = time.Now().UTC()
		if err := pub.SeedDepth(pubCtx, snap); err != nil {
			log.Printf("[server] error: failed to seed initial Redis depth for market=%s: %v", eng.MarketID, err)
			isDegraded.Store(true)
		} else {
			log.Printf("[server] initial Redis depth seeded successfully for market=%s (seq=%d)", eng.MarketID, snap.Sequence)
		}
	}

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

	orderVerifier := recovery.NewPostgresOrderVerifier(db)
	settlementVerifier := recovery.NewPostgresSettlementVerifier(db)
	reconService := recovery.NewReconciliationService(
		db,
		manager,
		leaseChecker,
		orderVerifier,
		settlementVerifier,
		cfg.AdminSecret,
	)

	appEnv := strings.ToLower(os.Getenv("ENV"))
	if appEnv == "" {
		appEnv = strings.ToLower(os.Getenv("APP_ENV"))
	}
	if appEnv == "production" || os.Getenv("REQUIRE_ADMIN_SECRET") == "true" {
		if err := reconService.ValidateDependencies(); err != nil {
			return fmt.Errorf("reconciliation service dependency validation failed: %w", err)
		}
	}

	httpServer := newHTTPServer(cfg.HTTPPort, manager, &isReady, &isDegraded, reconService)

	go func() {
		log.Printf("[server] health HTTP server listening on %s", cfg.HTTPPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[server] health HTTP server error: %v", err)
		}
	}()
	defer func() {
		sCtx, sCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer sCancel()
		_ = httpServer.Shutdown(sCtx)
	}()

	consumer := intkafka.NewConsumer(intkafka.Config{
		Brokers:      cfg.KafkaBrokers,
		GroupID:      cfg.KafkaGroupID,
		DB:           db,
		LockAcquirer: leaseMgr,
		Leases:       partitionLeases,
	}, manager, coord)
	consumer.FatalCallback = func() {
		log.Println("[server] consumer initiated fail-stop halt due to fatal malformed command")
		for _, eng := range manager.All() {
			eng.TriggerFatalHalt()
		}
		opCancel()
	}
	defer consumer.Close()

	consumerCtx, cancelConsumer := context.WithCancel(opCtx)
	defer cancelConsumer()

	// Consumer Start takes cancel function for fail-closed graceful shutdown (Issue #10)
	if err := consumer.Start(consumerCtx, cancelConsumer); err != nil {
		return fmt.Errorf("start consumer: %w", err)
	}
	log.Println("[server] kafka consumer started")
	isReady.Store(true)
	var blockedCount int
	for _, eng := range manager.All() {
		if eng.Mode() == market.ModeBlocked {
			blockedCount++
		}
	}
	if blockedCount > 0 {
		log.Printf("[server] ⚠ matching engine ready in DEGRADED state (%d market(s) blocked)", blockedCount)
	} else if isDegraded.Load() {
		log.Println("[server] ⚠ matching engine ready in DEGRADED state (initial depth seed failed)")
	} else {
		log.Println("[server] ✓ all systems live — matching engine ready")
	}

	<-opCtx.Done()
	isReady.Store(false)

	// DETERMINISTIC STAGED GRACEFUL SHUTDOWN (Issue #6)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	log.Println("[server] [shutdown phase 1/4] stopping consumer intake...")
	cancelConsumer()
	if err := consumer.Stop(10 * time.Second); err != nil {
		log.Printf("[server] FATAL: consumer did not stop cleanly: %v", err)
		os.Exit(1)
	}
	log.Println("[server] ✓ consumer stopped cleanly")

	log.Println("[server] [shutdown phase 2/4] closing engine input queues...")
	manager.CloseInputQueues()

	log.Println("[server] [shutdown phase 3/4] waiting for engines to exit...")
	enginesDone := make(chan struct{})
	go func() {
		engineWg.Wait()
		close(enginesDone)
	}()

	select {
	case <-enginesDone:
		log.Println("[server] ✓ engines stopped cleanly")
	case <-shutdownCtx.Done():
		log.Println("[server] [shutdown timeout] forcing exit waiting for engines")
		os.Exit(1)
	}

	log.Println("[server] [shutdown phase 4/4] waiting for publisher drain...")
	cancelPub() // signal publisher goroutines to exit and drain (Issue 11/12)

	pubDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(pubDone)
	}()

	select {
	case <-pubDone:
		if pub.HasDrainFailed() {
			log.Println("[server] [shutdown error] publisher drain failed — forcing exit with status 1")
			os.Exit(1)
		}
		log.Println("[server] [shutdown complete] all systems stopped cleanly")
	case <-shutdownCtx.Done():
		log.Println("[server] [shutdown timeout] forcing exit waiting for publisher drain")
		os.Exit(1)
	}

	return nil
}
