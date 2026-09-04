package postgres_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"tradedrift/services/portfolio/internal/repository"
	"tradedrift/services/portfolio/internal/repository/postgres"
)

func getTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	dsn := os.Getenv("PORTFOLIO_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:123@localhost:5432/tradedrift_portfolio?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("Skipping postgres integration tests: cannot connect to %s: %v", dsn, err)
		return nil, nil
	}

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("Skipping postgres integration tests: ping failed to %s: %v", dsn, err)
		return nil, nil
	}

	// Apply schema for test run
	setupDDL := `
		CREATE TABLE IF NOT EXISTS holdings (
			user_id             UUID NOT NULL,
			asset_code          VARCHAR(10) NOT NULL,
			quantity            DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (quantity >= 0),
			total_cost          DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (total_cost >= 0),
			realized_pnl        DECIMAL(30,10) NOT NULL DEFAULT 0,
			version             BIGINT NOT NULL DEFAULT 0,
			updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (user_id, asset_code)
		);
		ALTER TABLE holdings ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 0;

		CREATE TABLE IF NOT EXISTS processed_user_trades (
			trade_id            UUID NOT NULL,
			user_id             UUID NOT NULL,
			market_id           VARCHAR(20) NOT NULL DEFAULT '',
			sequence            BIGINT NOT NULL DEFAULT 0,
			processed_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (trade_id, user_id)
		);

		CREATE TABLE IF NOT EXISTS processed_market_sequences (
			market_id           VARCHAR(20) NOT NULL,
			sequence            BIGINT NOT NULL,
			trade_id            UUID NOT NULL,
			recorded_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (market_id, sequence)
		);

		CREATE TABLE IF NOT EXISTS portfolio_outbox (
			id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			aggregate_id        UUID NOT NULL,
			event_type          VARCHAR(50) NOT NULL,
			payload             JSONB NOT NULL,
			partition_key       VARCHAR(50) NOT NULL,
			status              VARCHAR(20) NOT NULL DEFAULT 'PENDING',
			claimed_at          TIMESTAMPTZ,
			created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			published_at        TIMESTAMPTZ
		);
	`
	if _, err := pool.Exec(ctx, setupDDL); err != nil {
		t.Fatalf("Failed to execute test setup DDL: %v", err)
	}

	cleanup := func() {
		pool.Close()
	}

	return pool, cleanup
}

func TestProcessUserTrade_OrderedBuyThenSell(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	repo := postgres.New(pool)
	ctx := context.Background()

	userID := uuid.New().String()
	trade1ID := uuid.New().String()
	trade2ID := uuid.New().String()

	now := time.Now().UTC()

	// 1. Bob Buys 1 BTC @ 90,000
	buyIn := repository.UserTradeInput{
		TradeID:    trade1ID,
		UserID:     userID,
		OrderID:    uuid.New().String(),
		Role:       "BUY",
		MarketID:   "BTC-USDT",
		BaseAsset:  "BTC",
		QuoteAsset: "USDT",
		Price:      decimal.RequireFromString("90000.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Sequence:   uint64(time.Now().UnixNano()),
		ExecutedAt: now,
		SettledAt:  now,
	}

	outbox1, err := repo.ProcessUserTrade(ctx, buyIn)
	if err != nil {
		t.Fatalf("unexpected error on buy: %v", err)
	}
	if outbox1 == nil {
		t.Fatalf("expected outbox message from buy trade")
	}

	holdings, err := repo.GetHoldingsByUser(ctx, userID)
	if err != nil || len(holdings) != 1 {
		t.Fatalf("expected 1 holding, got %d: %v", len(holdings), err)
	}
	if !holdings[0].Quantity.Equal(decimal.RequireFromString("1.0")) {
		t.Errorf("expected 1.0 BTC, got %s", holdings[0].Quantity)
	}
	if holdings[0].Version != 1 {
		t.Errorf("expected version 1, got %d", holdings[0].Version)
	}

	// 2. Bob Sells 1 BTC @ 95,000
	sellIn := repository.UserTradeInput{
		TradeID:    trade2ID,
		UserID:     userID,
		OrderID:    uuid.New().String(),
		Role:       "SELL",
		MarketID:   "BTC-USDT",
		BaseAsset:  "BTC",
		QuoteAsset: "USDT",
		Price:      decimal.RequireFromString("95000.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Sequence:   uint64(time.Now().UnixNano()) + 1,
		ExecutedAt: now,
		SettledAt:  now,
	}

	outbox2, err := repo.ProcessUserTrade(ctx, sellIn)
	if err != nil {
		t.Fatalf("unexpected error on sell: %v", err)
	}
	if outbox2 == nil {
		t.Fatalf("expected outbox message from sell trade")
	}

	// Holdings with 0 quantity are filtered out by GetHoldingsByUser
	activeHoldings, err := repo.GetHoldingsByUser(ctx, userID)
	if err != nil {
		t.Fatalf("unexpected error fetching active holdings: %v", err)
	}
	if len(activeHoldings) != 0 {
		t.Errorf("expected 0 active holdings after complete liquidation, got %d", len(activeHoldings))
	}
}

func TestProcessUserTrade_OutOfOrderSellRejection(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	repo := postgres.New(pool)
	ctx := context.Background()

	userID := uuid.New().String()
	tradeID := uuid.New().String()
	now := time.Now().UTC()

	// Bob has 0 BTC. A sell trade arrives first (simulating out-of-order delivery).
	sellIn := repository.UserTradeInput{
		TradeID:    tradeID,
		UserID:     userID,
		OrderID:    uuid.New().String(),
		Role:       "SELL",
		MarketID:   "BTC-USDT",
		BaseAsset:  "BTC",
		QuoteAsset: "USDT",
		Price:      decimal.RequireFromString("95000.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Sequence:   uint64(time.Now().UnixNano()),
		ExecutedAt: now,
		SettledAt:  now,
	}

	_, err := repo.ProcessUserTrade(ctx, sellIn)
	if err == nil {
		t.Fatalf("expected ErrInsufficientHoldings for premature sell trade, got nil")
	}
	if !errors.Is(err, repository.ErrInsufficientHoldings) {
		t.Errorf("expected ErrInsufficientHoldings, got: %v", err)
	}
}

func TestProcessUserTrade_SequenceCollisionRejection(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	repo := postgres.New(pool)
	ctx := context.Background()

	seq := uint64(time.Now().UnixNano())
	now := time.Now().UTC()

	trade1ID := uuid.New().String()
	trade2ID := uuid.New().String()

	// Trade 1 registered with Sequence seq
	in1 := repository.UserTradeInput{
		TradeID:    trade1ID,
		UserID:     uuid.New().String(),
		OrderID:    uuid.New().String(),
		Role:       "BUY",
		MarketID:   "BTC-USDT",
		BaseAsset:  "BTC",
		QuoteAsset: "USDT",
		Price:      decimal.RequireFromString("90000.00"),
		Quantity:   decimal.RequireFromString("0.1"),
		Sequence:   seq,
		ExecutedAt: now,
		SettledAt:  now,
	}
	if _, err := repo.ProcessUserTrade(ctx, in1); err != nil {
		t.Fatalf("failed to process in1: %v", err)
	}

	// Trade 2 attempts to reuse the same Sequence seq on BTC-USDT
	in2 := repository.UserTradeInput{
		TradeID:    trade2ID,
		UserID:     uuid.New().String(),
		OrderID:    uuid.New().String(),
		Role:       "BUY",
		MarketID:   "BTC-USDT",
		BaseAsset:  "BTC",
		QuoteAsset: "USDT",
		Price:      decimal.RequireFromString("90000.00"),
		Quantity:   decimal.RequireFromString("0.1"),
		Sequence:   seq, // Collision!
		ExecutedAt: now,
		SettledAt:  now,
	}
	_, err := repo.ProcessUserTrade(ctx, in2)
	if err == nil {
		t.Fatalf("expected sequence collision error on duplicate sequence, got nil")
	}
	t.Logf("Verified sequence collision caught: %v", err)
}

func TestProcessUserTrade_DuplicateTradeSkipping(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	repo := postgres.New(pool)
	ctx := context.Background()

	tradeID := uuid.New().String()
	userID := uuid.New().String()
	seq := uint64(time.Now().UnixNano())
	now := time.Now().UTC()

	in := repository.UserTradeInput{
		TradeID:    tradeID,
		UserID:     userID,
		OrderID:    uuid.New().String(),
		Role:       "BUY",
		MarketID:   "BTC-USDT",
		BaseAsset:  "BTC",
		QuoteAsset: "USDT",
		Price:      decimal.RequireFromString("90000.00"),
		Quantity:   decimal.RequireFromString("0.1"),
		Sequence:   seq,
		ExecutedAt: now,
		SettledAt:  now,
	}

	if _, err := repo.ProcessUserTrade(ctx, in); err != nil {
		t.Fatalf("failed first trade: %v", err)
	}

	// Second execution with identical (trade_id, user_id)
	_, err := repo.ProcessUserTrade(ctx, in)
	if !errors.Is(err, repository.ErrTradeAlreadyProcessed) {
		t.Fatalf("expected ErrTradeAlreadyProcessed on second call, got: %v", err)
	}

	// Verify holdings: quantity MUST remain exactly 0.1, not 0.2, and version MUST remain 1
	holdings, err := repo.GetHoldingsByUser(ctx, userID)
	if err != nil {
		t.Fatalf("failed to query holdings: %v", err)
	}
	if len(holdings) != 1 {
		t.Fatalf("expected 1 holding, got %d", len(holdings))
	}
	if !holdings[0].Quantity.Equal(decimal.RequireFromString("0.1")) {
		t.Fatalf("expected quantity 0.1, got %s (duplicate was double-counted!)", holdings[0].Quantity)
	}
	if holdings[0].Version != 1 {
		t.Fatalf("expected holding version 1, got %d", holdings[0].Version)
	}
}

func TestProcessUserTrade_ConsumerCrashAndRedelivery(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	repo := postgres.New(pool)
	ctx := context.Background()

	tradeID := uuid.New().String()
	userID := uuid.New().String()
	seq := uint64(time.Now().UnixNano())
	now := time.Now().UTC()

	eventPayload := repository.UserTradeInput{
		TradeID:    tradeID,
		UserID:     userID,
		OrderID:    uuid.New().String(),
		Role:       "BUY",
		MarketID:   "BTC-USDT",
		BaseAsset:  "BTC",
		QuoteAsset: "USDT",
		Price:      decimal.RequireFromString("65000.00"),
		Quantity:   decimal.RequireFromString("2.5"),
		Sequence:   seq,
		ExecutedAt: now,
		SettledAt:  now,
	}

	// 1. First delivery processed and committed to PostgreSQL
	outboxMsg, err := repo.ProcessUserTrade(ctx, eventPayload)
	if err != nil {
		t.Fatalf("first delivery failed: %v", err)
	}
	if outboxMsg == nil {
		t.Fatal("expected outbox message returned")
	}

	// 2. Simulate consumer crash before committing Kafka offset:
	// Kafka re-delivers the exact same message to another consumer instance
	redeliveredOutboxMsg, redeliverErr := repo.ProcessUserTrade(ctx, eventPayload)
	if !errors.Is(redeliverErr, repository.ErrTradeAlreadyProcessed) {
		t.Fatalf("expected ErrTradeAlreadyProcessed on Kafka redelivery, got: %v", redeliverErr)
	}
	if redeliveredOutboxMsg != nil {
		t.Fatal("expected nil outbox message on duplicate redelivery")
	}

	// 3. Assert idempotency: state is identical to after first delivery
	holdings, err := repo.GetHoldingsByUser(ctx, userID)
	if err != nil {
		t.Fatalf("failed to query holdings: %v", err)
	}
	if len(holdings) != 1 {
		t.Fatalf("expected 1 holding, got %d", len(holdings))
	}
	if !holdings[0].Quantity.Equal(decimal.RequireFromString("2.5")) {
		t.Fatalf("expected quantity 2.5, got %s", holdings[0].Quantity)
	}
	if holdings[0].Version != 1 {
		t.Fatalf("expected version 1, got %d", holdings[0].Version)
	}
}

func TestProcessUserTrade_FirstTimeConcurrentBuys(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	repo := postgres.New(pool)
	ctx := context.Background()

	userID := uuid.New().String()
	now := time.Now().UTC()

	const concurrency = 5
	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)

	// Launch 5 concurrent buys for a brand new user row
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			in := repository.UserTradeInput{
				TradeID:    uuid.New().String(),
				UserID:     userID,
				OrderID:    uuid.New().String(),
				Role:       "BUY",
				MarketID:   "BTC-USDT",
				BaseAsset:  "BTC",
				QuoteAsset: "USDT",
				Price:      decimal.RequireFromString("100000.00"),
				Quantity:   decimal.RequireFromString("1.0"),
				Sequence:   uint64(time.Now().UnixNano()) + uint64(idx),
				ExecutedAt: now,
				SettledAt:  now,
			}
			_, err := repo.ProcessUserTrade(ctx, in)
			if err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent buy failed: %v", err)
	}

	holdings, err := repo.GetHoldingsByUser(ctx, userID)
	if err != nil || len(holdings) != 1 {
		t.Fatalf("expected 1 holding, got %d: %v", len(holdings), err)
	}

	expectedQty := decimal.RequireFromString("5.0")
	if !holdings[0].Quantity.Equal(expectedQty) {
		t.Fatalf("expected exact 5.0 BTC after concurrent buys, got %s", holdings[0].Quantity)
	}
	if holdings[0].Version != 5 {
		t.Fatalf("expected version 5, got %d", holdings[0].Version)
	}

	t.Logf("Verified: 5 concurrent buys initialized row and accumulated to exactly %s BTC (version=%d)",
		holdings[0].Quantity, holdings[0].Version)
}

func TestProcessTradeSettled_CrossedConcurrentTrades(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	repo := postgres.New(pool)
	ctx := context.Background()

	aliceID := uuid.New().String()
	bobID := uuid.New().String()
	now := time.Now().UTC()

	// Initialize Alice and Bob with 50 BTC each
	_, err := pool.Exec(ctx, `
		INSERT INTO holdings (user_id, asset_code, quantity, total_cost, realized_pnl, version, updated_at)
		VALUES ($1, 'BTC', 50, 4500000, 0, 1, NOW()), ($2, 'BTC', 50, 4500000, 0, 1, NOW())
		ON CONFLICT (user_id, asset_code) DO UPDATE SET quantity = 50, total_cost = 4500000;
	`, aliceID, bobID)
	if err != nil {
		t.Fatalf("failed to setup Alice and Bob holdings: %v", err)
	}

	const iterations = 8
	var wg sync.WaitGroup
	deadlockErrors := make(chan error, iterations*2)

	// Simultaneously execute Alice buys from Bob, and Bob buys from Alice
	for i := 0; i < iterations; i++ {
		wg.Add(2)

		// Tx A: Alice buys from Bob
		go func(idx int) {
			defer wg.Done()
			in := repository.TradeSettledInput{
				TradeID:    uuid.New().String(),
				BuyerID:    aliceID,
				SellerID:   bobID,
				MarketID:   "BTC-USDT",
				BaseAsset:  "BTC",
				QuoteAsset: "USDT",
				Price:      decimal.RequireFromString("90000.00"),
				Quantity:   decimal.RequireFromString("0.1"),
				Sequence:   uint64(time.Now().UnixNano()) + uint64(idx*2),
				ExecutedAt: now,
				SettledAt:  now,
			}
			_, err := repo.ProcessTradeSettled(ctx, in)
			if err != nil {
				deadlockErrors <- err
			}
		}(i)

		// Tx B: Bob buys from Alice
		go func(idx int) {
			defer wg.Done()
			in := repository.TradeSettledInput{
				TradeID:    uuid.New().String(),
				BuyerID:    bobID,
				SellerID:   aliceID,
				MarketID:   "BTC-USDT",
				BaseAsset:  "BTC",
				QuoteAsset: "USDT",
				Price:      decimal.RequireFromString("90000.00"),
				Quantity:   decimal.RequireFromString("0.1"),
				Sequence:   uint64(time.Now().UnixNano()) + uint64(idx*2+1),
				ExecutedAt: now,
				SettledAt:  now,
			}
			_, err := repo.ProcessTradeSettled(ctx, in)
			if err != nil {
				deadlockErrors <- err
			}
		}(i)
	}

	wg.Wait()
	close(deadlockErrors)

	for err := range deadlockErrors {
		t.Fatalf("unexpected deadlock or transaction error in crossed trades: %v", err)
	}

	t.Logf("Verified: %d crossed concurrent trades executed with zero PostgreSQL deadlocks (40P01 == 0)", iterations*2)
}

func TestOutbox_ClaimAndLeaseExpiryRecovery(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	repo := postgres.New(pool)
	ctx := context.Background()

	eventID := uuid.New().String()
	userID := uuid.New().String()

	// Insert an outbox event in PENDING state
	_, err := pool.Exec(ctx, `
		INSERT INTO portfolio_outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
		VALUES ($1::uuid, $2::uuid, 'PortfolioUpdated', '{"test": true}', $3, 'PENDING', NOW() - INTERVAL '10 hours');
	`, eventID, userID, userID)
	if err != nil {
		t.Fatalf("insert outbox: %v", err)
	}

	// 1. First claim: Transitions from PENDING -> PROCESSING
	msgs, err := repo.FetchPendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("fetch pending: %v", err)
	}
	found := false
	for _, m := range msgs {
		if m.ID == eventID {
			found = true
			if m.Status != "PROCESSING" {
				t.Errorf("expected status PROCESSING, got %s", m.Status)
			}
		}
	}
	if !found {
		t.Fatalf("expected to claim event %s", eventID)
	}

	// 2. Simulate publisher crash & 2 minutes passing (lease expiration)
	_, err = pool.Exec(ctx, `
		UPDATE portfolio_outbox SET claimed_at = NOW() - INTERVAL '2 minutes'
		WHERE id = $1;
	`, eventID)
	if err != nil {
		t.Fatalf("simulate expired lease: %v", err)
	}

	// 3. New publisher reclaims the expired event
	reclaimedMsgs, err := repo.FetchPendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("reclaim expired lease: %v", err)
	}
	reclaimed := false
	for _, m := range reclaimedMsgs {
		if m.ID == eventID {
			reclaimed = true
		}
	}
	if !reclaimed {
		t.Fatalf("expected new publisher to reclaim expired event %s", eventID)
	}

	// 4. Mark published
	if err := repo.MarkOutboxPublished(ctx, []string{eventID}); err != nil {
		t.Fatalf("mark published: %v", err)
	}

	var finalStatus string
	err = pool.QueryRow(ctx, `SELECT status FROM portfolio_outbox WHERE id = $1`, eventID).Scan(&finalStatus)
	if err != nil || finalStatus != "PUBLISHED" {
		t.Fatalf("expected status PUBLISHED, got %s (err=%v)", finalStatus, err)
	}

	t.Logf("Verified outbox lease expiration and reclamation lifecycle for event %s", eventID)
}
