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
	"github.com/shopspring/decimal"

	intkafka "tradedrift/services/matching-engine/internal/kafka"
	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/publisher"
	"tradedrift/services/matching-engine/internal/recovery"
)

// ─── Entrypoint ───────────────────────────────────────────────────────────────

// main delegates to run() so all startup errors funnel to a single exit point.
// This avoids scattered log.Fatal() calls and makes the shutdown path explicit.
func main() {
	if err := run(); err != nil {
		log.Printf("[server] fatal: %v", err)
		os.Exit(1)
	}
}

// ─── Config ───────────────────────────────────────────────────────────────────

type config struct {
	KafkaBrokers []string // e.g. ["localhost:9092"]
	KafkaGroupID string   // e.g. "matching-engine"
	PostgresDSN  string   // e.g. "postgres://user:pass@localhost/tradedrift_matching"
	RedisAddr    string   // e.g. "localhost:6379"
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

// ─── Market Configs (V1 hardcoded) ───────────────────────────────────────────
//
// V1: market configs are hardcoded here.
// V2: fetch from Market Service API on startup so configs are data-driven.

func marketConfigs() []market.MarketConfig {
	return []market.MarketConfig{
		{
			MarketID: "BTC-USDT",
			TickSize: decimal.RequireFromString("0.01"),    // minimum price increment: $0.01
			LotSize:  decimal.RequireFromString("0.00001"), // minimum qty increment: 0.00001 BTC
		},
		{
			MarketID: "ETH-USDT",
			TickSize: decimal.RequireFromString("0.01"),
			LotSize:  decimal.RequireFromString("0.0001"),
		},
		{
			MarketID: "SOL-USDT",
			TickSize: decimal.RequireFromString("0.001"),
			LotSize:  decimal.RequireFromString("0.01"),
		},
	}
}

// validateMarketConfigs ensures no market has a zero TickSize or LotSize.
// An engine started with zero TickSize or LotSize skips all validation,
// which silently allows any price or quantity through the matcher.
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

// ─── run ──────────────────────────────────────────────────────────────────────

func run() error {
	// ── 1. Operational context — cancelled on SIGTERM or SIGINT ───────────────
	//
	// This context drives all live goroutines (engines, publishers, consumer).
	// When this cancels, goroutines stop accepting new work.
	opCtx, opCancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer opCancel()

	log.Println("[server] starting TradeDrift Matching Engine")

	// ── 2. Load and validate config ────────────────────────────────────────────
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	configs := marketConfigs()
	if err := validateMarketConfigs(configs); err != nil {
		return fmt.Errorf("validate market configs: %w", err)
	}

	// ── 3. Connect to PostgreSQL ───────────────────────────────────────────────
	db, err := pgxpool.New(opCtx, cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("postgres connect: %w", err)
	}
	defer db.Close()

	if err := db.Ping(opCtx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}
	log.Println("[server] postgres connected")

	// ── 4. Connect to Redis ────────────────────────────────────────────────────
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

	// ── 5. Create MarketManager and register all engines ──────────────────────
	//
	// All engines start in ModeRecovery by default (set in NewMarketEngine).
	// They will transition to ModeLive after ReplayAll() completes.
	manager := market.NewMarketManager()
	for _, mc := range configs {
		manager.Add(mc)
		log.Printf("[server] registered market: %s", mc.MarketID)
	}

	// ── 6. RECOVERY PHASE ─────────────────────────────────────────────────────
	//
	// ReplayAll:
	//   - Reads kafka_checkpoints from Postgres for each topic/partition
	//   - Creates dedicated Kafka readers from savedOffset+1 to HWM
	//   - Starts engine.Run(opCtx) goroutines (they persist into LIVE mode)
	//   - Feeds events through each engine's Event Loop in ModeRecovery
	//   - Drains OutputQueue (publisher not running — results discarded)
	//   - Pushes fresh depth snapshot to Redis
	//   - Calls engine.SetLive() for each engine
	//
	// Consumer.Start() MUST NOT be called until ReplayAll returns.
	log.Println("[server] starting recovery phase...")

	replayer := recovery.NewReplayer(cfg.KafkaBrokers, db, rdb, manager)
	if err := replayer.ReplayAll(opCtx); err != nil {
		return fmt.Errorf("recovery failed: %w", err)
	}

	log.Printf("[server] recovery complete — %d markets in LIVE mode", len(manager.All()))

	// ── 7. Start Publisher goroutines (one per MarketEngine) ──────────────────
	//
	// Publisher reads MatchResults from each engine's OutputQueue and:
	//   1. Writes TradeExecuted events to Kafka (trades.executed)
	//   2. Pushes depth snapshots to Redis (depth:{market_id})
	//   3. UPSERTs Kafka checkpoint to Postgres (monotonic, only advances)
	//
	// Starts AFTER ReplayAll so the Publisher doesn't consume OutputQueue
	// results that belong to the recovery drain phase.
	pub := publisher.NewPublisher(cfg.KafkaBrokers, rdb, db)
	defer pub.Close()

	var wg sync.WaitGroup

	for _, engine := range manager.All() {
		wg.Add(1)
		e := engine // capture loop variable
		go func() {
			defer wg.Done()
			pub.Run(opCtx, e)
		}()
		log.Printf("[server] publisher started for market: %s", e.MarketID)
	}

	// ── 8. Start Kafka Consumer (live event ingestion) ─────────────────────────
	//
	// Consumer routes live Kafka events to MarketEngine InputQueues.
	// Starts AFTER all engines are in ModeLive — ensures no live events
	// are routed to engines still replaying historical data.
	consumer := intkafka.NewConsumer(intkafka.Config{
		Brokers: cfg.KafkaBrokers,
		GroupID: cfg.KafkaGroupID,
	}, manager)
	defer consumer.Close()

	consumer.Start(opCtx)
	log.Println("[server] kafka consumer started")
	log.Println("[server] ✓ all systems live — matching engine ready")

	// ── 9. Block until shutdown signal ────────────────────────────────────────
	<-opCtx.Done()

	log.Println("[server] shutdown signal received — draining in-flight events...")

	// ── 10. GRACEFUL SHUTDOWN ─────────────────────────────────────────────────
	//
	// Two-context shutdown pattern:
	//
	//   opCtx (cancelled)         — goroutines stop accepting NEW work
	//        ↓
	//   consumer.Close()          — Kafka readers closed; no new events enter InputQueues
	//        ↓
	//   wg.Wait() with 15s limit  — wait for in-flight Publisher results to complete
	//        ↓
	//   deferred db/redis Close() — infrastructure torn down last
	//
	// We use a fresh context for the drain phase because opCtx is already cancelled.
	// Publisher.Run() already has:
	//   select { case result := <-engine.OutputQueue: ... case <-ctx.Done(): return }
	// so after opCtx cancels, publishers will exit on their next select iteration.
	// The WaitGroup ensures we don't tear down Postgres/Redis until they finish.

	// Stop accepting new events. Kafka reader goroutines exit on opCtx cancellation.
	consumer.Close()

	// Wait for publisher goroutines and engine event loops to finish.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("[server] graceful shutdown complete")
	case <-time.After(15 * time.Second):
		log.Println("[server] drain timeout (15s) — forcing shutdown")
	}

	return nil
}
