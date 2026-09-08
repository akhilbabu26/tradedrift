package postgres_test

import (
	"context"
	"errors"
	"fmt"
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
			order_id            UUID NOT NULL,
			role                VARCHAR(10) NOT NULL,
			price               DECIMAL(30,10) NOT NULL,
			quantity            DECIMAL(30,10) NOT NULL,
			processed_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (trade_id, user_id)
		);
		ALTER TABLE processed_user_trades ADD COLUMN IF NOT EXISTS order_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000';
		ALTER TABLE processed_user_trades ADD COLUMN IF NOT EXISTS role VARCHAR(10) NOT NULL DEFAULT 'BUY';
		ALTER TABLE processed_user_trades ADD COLUMN IF NOT EXISTS price DECIMAL(30,10) NOT NULL DEFAULT 0;
		ALTER TABLE processed_user_trades ADD COLUMN IF NOT EXISTS quantity DECIMAL(30,10) NOT NULL DEFAULT 0;

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

	// Clean outbox table for clean isolation
	if _, err := pool.Exec(ctx, "DELETE FROM portfolio_outbox;"); err != nil {
		t.Fatalf("clean outbox: %v", err)
	}

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

func TestProcessUserTrade_WeightedAverageMultipleBuys(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	repo := postgres.New(pool)
	ctx := context.Background()
	userID := uuid.New().String()
	baseSeq := uint64(time.Now().UnixNano())

	// Buy 1: 1.0 BTC @ 50,000 USDT -> cost 50,000
	buy1 := repository.UserTradeInput{
		TradeID:    uuid.New().String(),
		UserID:     userID,
		OrderID:    uuid.New().String(),
		Role:       "BUY",
		MarketID:   "BTC-USDT",
		BaseAsset:  "BTC",
		QuoteAsset: "USDT",
		Price:      decimal.RequireFromString("50000.00"),
		Quantity:   decimal.RequireFromString("1.00"),
		Sequence:   baseSeq + 1,
		ExecutedAt: time.Now().UTC(),
		SettledAt:  time.Now().UTC(),
	}
	out1, err := repo.ProcessUserTrade(ctx, buy1)
	if err != nil {
		t.Fatalf("buy1 failed: %v", err)
	}
	if out1 == nil {
		t.Fatal("expected outbox message for buy1")
	}

	// Buy 2: 2.0 BTC @ 80,000 USDT -> cost 160,000. Total cost = 210,000. Total qty = 3.0. Avg entry = 70,000
	buy2 := repository.UserTradeInput{
		TradeID:    uuid.New().String(),
		UserID:     userID,
		OrderID:    uuid.New().String(),
		Role:       "BUY",
		MarketID:   "BTC-USDT",
		BaseAsset:  "BTC",
		QuoteAsset: "USDT",
		Price:      decimal.RequireFromString("80000.00"),
		Quantity:   decimal.RequireFromString("2.00"),
		Sequence:   baseSeq + 2,
		ExecutedAt: time.Now().UTC(),
		SettledAt:  time.Now().UTC(),
	}
	out2, err := repo.ProcessUserTrade(ctx, buy2)
	if err != nil {
		t.Fatalf("buy2 failed: %v", err)
	}
	if out2 == nil {
		t.Fatal("expected outbox message for buy2")
	}

	holdings, err := repo.GetHoldingsByUser(ctx, userID)
	if err != nil {
		t.Fatalf("get holdings failed: %v", err)
	}
	if len(holdings) != 1 {
		t.Fatalf("expected 1 holding, got %d", len(holdings))
	}

	h := holdings[0]
	if !h.Quantity.Equal(decimal.RequireFromString("3.00")) {
		t.Errorf("Quantity = %s, want 3.00", h.Quantity)
	}
	if !h.TotalCost.Equal(decimal.RequireFromString("210000.00")) {
		t.Errorf("TotalCost = %s, want 210000.00", h.TotalCost)
	}
	expectedAvg := decimal.RequireFromString("70000.00")
	if !h.AverageEntryPrice().Equal(expectedAvg) {
		t.Errorf("AverageEntryPrice = %s, want %s", h.AverageEntryPrice(), expectedAvg)
	}
	if h.Version != 2 {
		t.Errorf("Version = %d, want 2", h.Version)
	}
}

func TestProcessUserTrade_FullLiquidationZeroReset(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	repo := postgres.New(pool)
	ctx := context.Background()
	userID := uuid.New().String()
	baseSeq := uint64(time.Now().UnixNano())

	// Buy 2.0 BTC @ 60,000
	buy := repository.UserTradeInput{
		TradeID:    uuid.New().String(),
		UserID:     userID,
		OrderID:    uuid.New().String(),
		Role:       "BUY",
		MarketID:   "BTC-USDT",
		BaseAsset:  "BTC",
		QuoteAsset: "USDT",
		Price:      decimal.RequireFromString("60000.00"),
		Quantity:   decimal.RequireFromString("2.00"),
		Sequence:   baseSeq + 1,
		ExecutedAt: time.Now().UTC(),
		SettledAt:  time.Now().UTC(),
	}
	if _, err := repo.ProcessUserTrade(ctx, buy); err != nil {
		t.Fatalf("buy failed: %v", err)
	}

	// Full liquidation: Sell 2.0 BTC @ 70,000 -> PnL = (70000 - 60000) * 2 = 20,000
	sell := repository.UserTradeInput{
		TradeID:    uuid.New().String(),
		UserID:     userID,
		OrderID:    uuid.New().String(),
		Role:       "SELL",
		MarketID:   "BTC-USDT",
		BaseAsset:  "BTC",
		QuoteAsset: "USDT",
		Price:      decimal.RequireFromString("70000.00"),
		Quantity:   decimal.RequireFromString("2.00"),
		Sequence:   baseSeq + 2,
		ExecutedAt: time.Now().UTC(),
		SettledAt:  time.Now().UTC(),
	}
	if _, err := repo.ProcessUserTrade(ctx, sell); err != nil {
		t.Fatalf("sell failed: %v", err)
	}

	// Verify directly from DB table (GetHoldingsByUser filters quantity > 0)
	var qty, totalCost, realizedPnL decimal.Decimal
	var version int64
	err := pool.QueryRow(ctx, `
		SELECT quantity, total_cost, realized_pnl, version
		FROM holdings
		WHERE user_id = $1 AND asset_code = 'BTC';
	`, userID).Scan(&qty, &totalCost, &realizedPnL, &version)
	if err != nil {
		t.Fatalf("scan holding failed: %v", err)
	}

	if !qty.IsZero() {
		t.Errorf("expected quantity to be 0, got %s", qty)
	}
	if !totalCost.IsZero() {
		t.Errorf("expected totalCost to be 0 on full liquidation, got %s", totalCost)
	}
	if !realizedPnL.Equal(decimal.RequireFromString("20000.00")) {
		t.Errorf("realizedPnL = %s, want 20000.00", realizedPnL)
	}
	if version != 2 {
		t.Errorf("version = %d, want 2", version)
	}
}

func TestProcessUserTrade_DualLegIndependentAccounting(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	repo := postgres.New(pool)
	ctx := context.Background()
	buyerID := uuid.New().String()
	sellerID := uuid.New().String()
	baseSeq := uint64(time.Now().UnixNano())

	// Pre-seed seller holding: 5 BTC
	seedSell := repository.UserTradeInput{
		TradeID:    uuid.New().String(),
		UserID:     sellerID,
		OrderID:    uuid.New().String(),
		Role:       "BUY",
		MarketID:   "BTC-USDT",
		BaseAsset:  "BTC",
		QuoteAsset: "USDT",
		Price:      decimal.RequireFromString("40000.00"),
		Quantity:   decimal.RequireFromString("5.00"),
		Sequence:   baseSeq + 1,
		ExecutedAt: time.Now().UTC(),
		SettledAt:  time.Now().UTC(),
	}
	if _, err := repo.ProcessUserTrade(ctx, seedSell); err != nil {
		t.Fatalf("seed seller failed: %v", err)
	}

	// Matched trade T1 between buyer and seller:
	tradeID := uuid.New().String()
	tradeSeq := baseSeq + 2

	// Buyer leg
	buyerLeg := repository.UserTradeInput{
		TradeID:    tradeID,
		UserID:     buyerID,
		OrderID:    uuid.New().String(),
		Role:       "BUY",
		MarketID:   "BTC-USDT",
		BaseAsset:  "BTC",
		QuoteAsset: "USDT",
		Price:      decimal.RequireFromString("50000.00"),
		Quantity:   decimal.RequireFromString("1.50"),
		Sequence:   tradeSeq,
		ExecutedAt: time.Now().UTC(),
		SettledAt:  time.Now().UTC(),
	}
	outBuyer, err := repo.ProcessUserTrade(ctx, buyerLeg)
	if err != nil {
		t.Fatalf("buyer leg failed: %v", err)
	}
	if outBuyer == nil || outBuyer.AggregateID != buyerID {
		t.Fatalf("expected buyer outbox event for %s", buyerID)
	}

	// Seller leg for same trade_id
	sellerLeg := repository.UserTradeInput{
		TradeID:    tradeID,
		UserID:     sellerID,
		OrderID:    uuid.New().String(),
		Role:       "SELL",
		MarketID:   "BTC-USDT",
		BaseAsset:  "BTC",
		QuoteAsset: "USDT",
		Price:      decimal.RequireFromString("50000.00"),
		Quantity:   decimal.RequireFromString("1.50"),
		Sequence:   tradeSeq,
		ExecutedAt: time.Now().UTC(),
		SettledAt:  time.Now().UTC(),
	}
	outSeller, err := repo.ProcessUserTrade(ctx, sellerLeg)
	if err != nil {
		t.Fatalf("seller leg failed: %v", err)
	}
	if outSeller == nil || outSeller.AggregateID != sellerID {
		t.Fatalf("expected seller outbox event for %s", sellerID)
	}

	// Verify buyer holdings
	buyerHoldings, err := repo.GetHoldingsByUser(ctx, buyerID)
	if err != nil || len(buyerHoldings) != 1 || !buyerHoldings[0].Quantity.Equal(decimal.RequireFromString("1.50")) {
		t.Fatalf("buyer holdings verification failed: %+v", buyerHoldings)
	}

	// Verify seller holdings (5 - 1.5 = 3.5)
	sellerHoldings, err := repo.GetHoldingsByUser(ctx, sellerID)
	if err != nil || len(sellerHoldings) != 1 || !sellerHoldings[0].Quantity.Equal(decimal.RequireFromString("3.50")) {
		t.Fatalf("seller holdings verification failed: %+v", sellerHoldings)
	}
}

func TestOutbox_ProcessingNullClaimedAtRecovery(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	repo := postgres.New(pool)
	ctx := context.Background()

	// Clean outbox table for test isolation
	if _, err := pool.Exec(ctx, "DELETE FROM portfolio_outbox;"); err != nil {
		t.Fatalf("clean outbox: %v", err)
	}

	eventID := uuid.New().String()
	userID := uuid.New().String()

	// Insert an outbox event in PROCESSING state with claimed_at = NULL (edge case)
	_, err := pool.Exec(ctx, `
		INSERT INTO portfolio_outbox (id, aggregate_id, event_type, payload, partition_key, status, claimed_at, created_at)
		VALUES ($1::uuid, $2::uuid, 'PortfolioUpdated', '{"test": true}', $3, 'PROCESSING', NULL, NOW() - INTERVAL '10 minutes');
	`, eventID, userID, userID)
	if err != nil {
		t.Fatalf("insert outbox: %v", err)
	}

	// FetchPendingOutbox must reclaim this row despite claimed_at being NULL
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
			if m.ClaimedAt == nil {
				t.Errorf("expected claimed_at to be populated upon claim")
			}
		}
	}
	if !found {
		t.Fatalf("expected FetchPendingOutbox to recover PROCESSING row with NULL claimed_at: %s", eventID)
	}
}

func TestOutbox_MarkPublishedGuard(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	repo := postgres.New(pool)
	ctx := context.Background()

	// Clean outbox table for test isolation
	if _, err := pool.Exec(ctx, "DELETE FROM portfolio_outbox;"); err != nil {
		t.Fatalf("clean outbox: %v", err)
	}

	eventID := uuid.New().String()
	userID := uuid.New().String()

	// Insert event in PENDING state
	_, err := pool.Exec(ctx, `
		INSERT INTO portfolio_outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
		VALUES ($1::uuid, $2::uuid, 'PortfolioUpdated', '{"test": true}', $3, 'PENDING', NOW());
	`, eventID, userID, userID)
	if err != nil {
		t.Fatalf("insert outbox: %v", err)
	}

	// Attempting to MarkOutboxPublished directly while in PENDING must fail (lease guard)
	err = repo.MarkOutboxPublished(ctx, []string{eventID})
	if err == nil {
		t.Fatal("expected MarkOutboxPublished to fail for PENDING row, got nil")
	}
	if !errors.Is(err, repository.ErrOutboxLeaseExpired) {
		t.Errorf("expected ErrOutboxLeaseExpired, got: %v", err)
	}

	// Claim row so status -> PROCESSING
	msgs, err := repo.FetchPendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("fetch pending: %v", err)
	}
	claimed := false
	for _, m := range msgs {
		if m.ID == eventID {
			claimed = true
		}
	}
	if !claimed {
		t.Fatalf("failed to claim event %s", eventID)
	}

	// Now MarkOutboxPublished must succeed
	if err := repo.MarkOutboxPublished(ctx, []string{eventID}); err != nil {
		t.Fatalf("expected MarkOutboxPublished to succeed for PROCESSING row, got: %v", err)
	}

	// Calling MarkOutboxPublished a second time must fail because status is now PUBLISHED
	err = repo.MarkOutboxPublished(ctx, []string{eventID})
	if err == nil {
		t.Fatal("expected second MarkOutboxPublished to fail, got nil")
	}
	if !errors.Is(err, repository.ErrOutboxLeaseExpired) {
		t.Errorf("expected ErrOutboxLeaseExpired on republish, got: %v", err)
	}
}

func TestProcessUserTrade_ConflictingMetadataRejection(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	repo := postgres.New(pool)
	ctx := context.Background()

	tradeID := uuid.New().String()
	userID := uuid.New().String()
	orderID := uuid.New().String()
	baseSeq := uint64(time.Now().UnixNano())

	// 1. Initial valid trade
	trade := repository.UserTradeInput{
		TradeID:    tradeID,
		UserID:     userID,
		OrderID:    orderID,
		Role:       "BUY",
		MarketID:   "BTC-USDT",
		BaseAsset:  "BTC",
		QuoteAsset: "USDT",
		Price:      decimal.RequireFromString("50000.00"),
		Quantity:   decimal.RequireFromString("1.00"),
		Sequence:   baseSeq + 1,
		ExecutedAt: time.Now().UTC(),
		SettledAt:  time.Now().UTC(),
	}

	out, err := repo.ProcessUserTrade(ctx, trade)
	if err != nil || out == nil {
		t.Fatalf("first trade processing failed: %v", err)
	}

	// 2. Exact duplicate -> harmless ErrTradeAlreadyProcessed
	_, err = repo.ProcessUserTrade(ctx, trade)
	if !errors.Is(err, repository.ErrTradeAlreadyProcessed) {
		t.Fatalf("expected ErrTradeAlreadyProcessed for exact replay, got: %v", err)
	}

	// 3. Conflicting price -> ErrTradeConflict
	conflictingPriceTrade := trade
	conflictingPriceTrade.Price = decimal.RequireFromString("99000.00")
	_, err = repo.ProcessUserTrade(ctx, conflictingPriceTrade)
	if !errors.Is(err, repository.ErrTradeConflict) {
		t.Fatalf("expected ErrTradeConflict for differing price, got: %v", err)
	}

	// 4. Conflicting quantity -> ErrTradeConflict
	conflictingQtyTrade := trade
	conflictingQtyTrade.Quantity = decimal.RequireFromString("5.00")
	_, err = repo.ProcessUserTrade(ctx, conflictingQtyTrade)
	if !errors.Is(err, repository.ErrTradeConflict) {
		t.Fatalf("expected ErrTradeConflict for differing quantity, got: %v", err)
	}

	// 5. Conflicting role -> ErrTradeConflict
	conflictingRoleTrade := trade
	conflictingRoleTrade.Role = "SELL"
	_, err = repo.ProcessUserTrade(ctx, conflictingRoleTrade)
	if !errors.Is(err, repository.ErrTradeConflict) {
		t.Fatalf("expected ErrTradeConflict for differing role, got: %v", err)
	}
}

func TestOutbox_FetchPendingOutboxDeterministicOrder(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	repo := postgres.New(pool)
	ctx := context.Background()

	// Clean outbox table for clean isolation
	if _, err := pool.Exec(ctx, "DELETE FROM portfolio_outbox;"); err != nil {
		t.Fatalf("clean outbox: %v", err)
	}

	userID := uuid.New().String()
	eventIDs := make([]string, 5)
	for i := 0; i < 5; i++ {
		eventIDs[i] = uuid.New().String()
		// Insert with staggered created_at: i=0 is oldest (50 mins ago), i=4 is newest (10 mins ago)
		interval := fmt.Sprintf("%d minutes", (5-i)*10)
		_, err := pool.Exec(ctx, fmt.Sprintf(`
			INSERT INTO portfolio_outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
			VALUES ($1::uuid, $2::uuid, 'PortfolioUpdated', '{"test": true}', $3, 'PENDING', NOW() - INTERVAL '%s');
		`, interval), eventIDs[i], userID, userID)
		if err != nil {
			t.Fatalf("insert outbox %d: %v", i, err)
		}
	}

	// Claim all 5 events
	claimed, err := repo.FetchPendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("fetch pending outbox failed: %v", err)
	}
	if len(claimed) != 5 {
		t.Fatalf("expected 5 claimed events, got %d", len(claimed))
	}

	// Verify strict ascending order of returned messages
	for i := 0; i < 5; i++ {
		if claimed[i].ID != eventIDs[i] {
			t.Errorf("claimed[%d] ID = %s, want %s (out of order)", i, claimed[i].ID, eventIDs[i])
		}
	}
}


