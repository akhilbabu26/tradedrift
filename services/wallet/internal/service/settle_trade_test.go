package service_test

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet/internal/service"
)

func getWalletServiceTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	dsn := os.Getenv("WALLET_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:123@localhost:5432/tradedrift_wallet?sslmode=disable"
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

	cleanup := func() {
		pool.Close()
	}

	return pool, cleanup
}

// setupTestWalletsAndReservation creates:
//   - Buyer BTC wallet (available=0, reserved=0)
//   - Buyer USDT wallet (available=0, reserved=buyerReservedUSDT) & BuyOrder reservation
//   - Seller BTC wallet (available=0, reserved=sellerReservedBTC) & SellOrder reservation
//   - Seller USDT wallet (available=0, reserved=0)
func setupTestWalletsAndReservation(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	buyerID, sellerID, buyOrderID, sellerOrderID string,
	sellerReservedBTC, buyerReservedUSDT string,
) {
	// 1. Buyer BTC wallet
	buyerBTCWalletID, _ := platformuuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
		VALUES ($1, $2, 'BTC', 0, 0, 0)
		ON CONFLICT (user_id, asset) DO UPDATE SET available_balance = 0, reserved_balance = 0, total_balance = 0
	`, buyerBTCWalletID, buyerID)
	if err != nil {
		t.Fatalf("failed to setup buyer BTC wallet: %v", err)
	}

	// 2. Buyer USDT wallet
	buyerUSDTWalletID, _ := platformuuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
		VALUES ($1, $2, 'USDT', 0, $3::DECIMAL, $3::DECIMAL)
		ON CONFLICT (user_id, asset) DO UPDATE SET available_balance = 0, reserved_balance = $3::DECIMAL, total_balance = $3::DECIMAL
	`, buyerUSDTWalletID, buyerID, buyerReservedUSDT)
	if err != nil {
		t.Fatalf("failed to setup buyer USDT wallet: %v", err)
	}

	// 3. Buyer USDT reservation (BuyOrder)
	buyerResID, _ := platformuuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO wallet_reservations (id, order_id, user_id, asset, reserved_amount, consumed_amount, remaining_amount, status)
		VALUES ($1, $2, $3, 'USDT', $4::DECIMAL, 0, $4::DECIMAL, 'ACTIVE')
		ON CONFLICT (order_id) DO UPDATE SET consumed_amount = 0, remaining_amount = $4::DECIMAL, status = 'ACTIVE'
	`, buyerResID, buyOrderID, buyerID, buyerReservedUSDT)
	if err != nil {
		t.Fatalf("failed to setup buyer USDT reservation: %v", err)
	}

	// 4. Seller BTC wallet
	sellerBTCWalletID, _ := platformuuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
		VALUES ($1, $2, 'BTC', 0, $3::DECIMAL, $3::DECIMAL)
		ON CONFLICT (user_id, asset) DO UPDATE SET available_balance = 0, reserved_balance = $3::DECIMAL, total_balance = $3::DECIMAL
	`, sellerBTCWalletID, sellerID, sellerReservedBTC)
	if err != nil {
		t.Fatalf("failed to setup seller BTC wallet: %v", err)
	}

	// 5. Seller USDT wallet
	sellerUSDTWalletID, _ := platformuuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
		VALUES ($1, $2, 'USDT', 0, 0, 0)
		ON CONFLICT (user_id, asset) DO UPDATE SET available_balance = 0, reserved_balance = 0, total_balance = 0
	`, sellerUSDTWalletID, sellerID)
	if err != nil {
		t.Fatalf("failed to setup seller USDT wallet: %v", err)
	}

	// 6. Seller BTC reservation (SellOrder)
	sellerResID, _ := platformuuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO wallet_reservations (id, order_id, user_id, asset, reserved_amount, consumed_amount, remaining_amount, status)
		VALUES ($1, $2, $3, 'BTC', $4::DECIMAL, 0, $4::DECIMAL, 'ACTIVE')
		ON CONFLICT (order_id) DO UPDATE SET consumed_amount = 0, remaining_amount = $4::DECIMAL, status = 'ACTIVE'
	`, sellerResID, sellerOrderID, sellerID, sellerReservedBTC)
	if err != nil {
		t.Fatalf("failed to setup seller BTC reservation: %v", err)
	}
}

func TestSettleTrade_TransfersBothAssets(t *testing.T) {
	pool, cleanup := getWalletServiceTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	svc := service.NewService(pool, zap.NewNop())

	buyerID, _ := platformuuid.New()
	sellerID, _ := platformuuid.New()
	buyOrderID, _ := platformuuid.New()
	sellerOrderID, _ := platformuuid.New()
	tradeID, _ := platformuuid.New()

	setupTestWalletsAndReservation(t, ctx, pool, buyerID, sellerID, buyOrderID, sellerOrderID, "1.0000000000", "50000.0000000000")

	req := service.TradeSettlementRequest{
		TradeID:       tradeID,
		BuyerUserID:   buyerID,
		SellerUserID:  sellerID,
		BuyOrderID:    buyOrderID,
		SellerOrderID: sellerOrderID,
		MarketID:      "BTC-USDT",
		BaseAsset:     "BTC",
		QuoteAsset:    "USDT",
		BaseAmount:    "1.0000000000",
		QuoteAmount:   "50000.0000000000",
		Price:         "50000.0000000000",
		Quantity:      "1.0000000000",
		Sequence:      1,
		ExecutedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	err := svc.SettleTrade(ctx, req)
	if err != nil {
		t.Fatalf("SettleTrade failed: %v", err)
	}

	// 1. Leg 1: Seller BTC reserved debited to 0
	var sellerReservedBTC string
	err = pool.QueryRow(ctx, "SELECT reserved_balance FROM wallets WHERE user_id = $1 AND asset = 'BTC'", sellerID).Scan(&sellerReservedBTC)
	if err != nil || !decimal.RequireFromString(sellerReservedBTC).IsZero() {
		t.Fatalf("expected seller reserved BTC 0, got %s (err: %v)", sellerReservedBTC, err)
	}

	// 2. Leg 1: Seller reservation consumed
	var sellerResRemaining, sellerResStatus string
	err = pool.QueryRow(ctx, "SELECT remaining_amount, status FROM wallet_reservations WHERE order_id = $1", sellerOrderID).Scan(&sellerResRemaining, &sellerResStatus)
	if err != nil || !decimal.RequireFromString(sellerResRemaining).IsZero() || sellerResStatus != "CONSUMED" {
		t.Fatalf("expected seller reservation consumed, got %s / %s", sellerResRemaining, sellerResStatus)
	}

	// 3. Leg 1: Buyer BTC available credited with 1.0
	var buyerAvailableBTC string
	err = pool.QueryRow(ctx, "SELECT available_balance FROM wallets WHERE user_id = $1 AND asset = 'BTC'", buyerID).Scan(&buyerAvailableBTC)
	if err != nil || !decimal.RequireFromString(buyerAvailableBTC).Equal(decimal.NewFromInt(1)) {
		t.Fatalf("expected buyer available BTC 1, got %s (err: %v)", buyerAvailableBTC, err)
	}

	// 4. Leg 2: Buyer USDT reserved debited to 0
	var buyerReservedUSDT string
	err = pool.QueryRow(ctx, "SELECT reserved_balance FROM wallets WHERE user_id = $1 AND asset = 'USDT'", buyerID).Scan(&buyerReservedUSDT)
	if err != nil || !decimal.RequireFromString(buyerReservedUSDT).IsZero() {
		t.Fatalf("expected buyer reserved USDT 0, got %s (err: %v)", buyerReservedUSDT, err)
	}

	// 5. Leg 2: Buyer reservation consumed
	var buyerResRemaining, buyerResStatus string
	err = pool.QueryRow(ctx, "SELECT remaining_amount, status FROM wallet_reservations WHERE order_id = $1", buyOrderID).Scan(&buyerResRemaining, &buyerResStatus)
	if err != nil || !decimal.RequireFromString(buyerResRemaining).IsZero() || buyerResStatus != "CONSUMED" {
		t.Fatalf("expected buyer reservation consumed, got %s / %s", buyerResRemaining, buyerResStatus)
	}

	// 6. Leg 2: Seller USDT available credited with 50,000
	var sellerAvailableUSDT string
	err = pool.QueryRow(ctx, "SELECT available_balance FROM wallets WHERE user_id = $1 AND asset = 'USDT'", sellerID).Scan(&sellerAvailableUSDT)
	if err != nil || !decimal.RequireFromString(sellerAvailableUSDT).Equal(decimal.RequireFromString("50000")) {
		t.Fatalf("expected seller available USDT 50000, got %s (err: %v)", sellerAvailableUSDT, err)
	}

	// 7. Exactly 4 ledger transactions (Seller BTC DEBIT, Buyer BTC CREDIT, Buyer USDT DEBIT, Seller USDT CREDIT)
	var txnCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM wallet_transactions WHERE reference_id = $1", tradeID).Scan(&txnCount)
	if err != nil || txnCount != 4 {
		t.Fatalf("expected 4 wallet transactions, got %d (err: %v)", txnCount, err)
	}

	// 8. Exactly 1 row in settled_trades
	var settledCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM settled_trades WHERE trade_id = $1", tradeID).Scan(&settledCount)
	if err != nil || settledCount != 1 {
		t.Fatalf("expected 1 settled_trades entry, got %d (err: %v)", settledCount, err)
	}

	// 9. Exactly 3 outbox events
	var outboxCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE aggregate_id = $1", tradeID).Scan(&outboxCount)
	if err != nil || outboxCount != 3 {
		t.Fatalf("expected 3 outbox events, got %d", outboxCount)
	}
}

func TestSettleTrade_CreatesThreeOutboxEvents(t *testing.T) {
	pool, cleanup := getWalletServiceTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	svc := service.NewService(pool, zap.NewNop())

	buyerID, _ := platformuuid.New()
	sellerID, _ := platformuuid.New()
	buyOrderID, _ := platformuuid.New()
	sellerOrderID, _ := platformuuid.New()
	tradeID, _ := platformuuid.New()

	setupTestWalletsAndReservation(t, ctx, pool, buyerID, sellerID, buyOrderID, sellerOrderID, "1.0000000000", "50000.0000000000")

	req := service.TradeSettlementRequest{
		TradeID:       tradeID,
		BuyerUserID:   buyerID,
		SellerUserID:  sellerID,
		BuyOrderID:    buyOrderID,
		SellerOrderID: sellerOrderID,
		MarketID:      "BTC-USDT",
		BaseAsset:     "BTC",
		QuoteAsset:    "USDT",
		BaseAmount:    "1.0000000000",
		QuoteAmount:   "50000.0000000000",
		Price:         "50000.0000000000",
		Quantity:      "1.0000000000",
		Sequence:      1,
		ExecutedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	err := svc.SettleTrade(ctx, req)
	if err != nil {
		t.Fatalf("SettleTrade failed: %v", err)
	}

	// Verify exactly 4 ledger transactions
	var txnCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM wallet_transactions WHERE reference_id = $1", tradeID).Scan(&txnCount)
	if err != nil || txnCount != 4 {
		t.Fatalf("expected 4 wallet transactions, got %d (err: %v)", txnCount, err)
	}

	// Verify exactly 3 outbox events
	rows, err := pool.Query(ctx, "SELECT event_type, partition_key, payload FROM outbox WHERE aggregate_id = $1", tradeID)
	if err != nil {
		t.Fatalf("failed to query outbox: %v", err)
	}
	defer rows.Close()

	events := make(map[string]string)
	for rows.Next() {
		var eventType, partitionKey string
		var payload []byte
		if err := rows.Scan(&eventType, &partitionKey, &payload); err != nil {
			t.Fatalf("failed to scan outbox row: %v", err)
		}

		if eventType == "TradeSettled" {
			events["TradeSettled"] = partitionKey
		} else if eventType == "PortfolioUserTrade" {
			var p map[string]any
			if err := json.Unmarshal(payload, &p); err != nil {
				t.Fatalf("failed to unmarshal portfolio payload: %v", err)
			}
			role := p["role"].(string)
			events["PortfolioUserTrade_"+role] = partitionKey
		}
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 outbox events, got %d: %+v", len(events), events)
	}

	// Check partition keys: TradeSettled -> buyer, Portfolio BUY -> buyer, Portfolio SELL -> seller
	if events["TradeSettled"] != buyerID {
		t.Errorf("TradeSettled partition key mismatch: expected %s, got %s", buyerID, events["TradeSettled"])
	}
	if events["PortfolioUserTrade_BUY"] != buyerID {
		t.Errorf("Portfolio BUY partition key mismatch: expected %s, got %s", buyerID, events["PortfolioUserTrade_BUY"])
	}
	if events["PortfolioUserTrade_SELL"] != sellerID {
		t.Errorf("Portfolio SELL partition key mismatch: expected %s, got %s", sellerID, events["PortfolioUserTrade_SELL"])
	}
}

func TestSettleTrade_AtomicRollbackOnBuyerCreditFailure(t *testing.T) {
	pool, cleanup := getWalletServiceTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	svc := service.NewService(pool, zap.NewNop())

	buyerID, _ := platformuuid.New()
	sellerID, _ := platformuuid.New()
	buyOrderID, _ := platformuuid.New()
	sellerOrderID, _ := platformuuid.New()
	tradeID, _ := platformuuid.New()

	// Setup seller and buyer wallets and reservations
	setupTestWalletsAndReservation(t, ctx, pool, buyerID, sellerID, buyOrderID, sellerOrderID, "2.0000000000", "100000.0000000000")
	// Delete buyer BTC wallet to force Step 5c to fail
	_, err := pool.Exec(ctx, "DELETE FROM wallets WHERE user_id = $1 AND asset = 'BTC'", buyerID)
	if err != nil {
		t.Fatalf("failed to delete buyer BTC wallet: %v", err)
	}

	req := service.TradeSettlementRequest{
		TradeID:       tradeID,
		BuyerUserID:   buyerID,
		SellerUserID:  sellerID,
		BuyOrderID:    buyOrderID,
		SellerOrderID: sellerOrderID,
		MarketID:      "BTC-USDT",
		BaseAsset:     "BTC",
		QuoteAsset:    "USDT",
		BaseAmount:    "1.0000000000",
		QuoteAmount:   "50000.0000000000",
		Price:         "50000.0000000000",
		Quantity:      "1.0000000000",
		Sequence:      1,
		ExecutedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	// SettleTrade MUST return an error
	err = svc.SettleTrade(ctx, req)
	if err == nil {
		t.Fatal("expected SettleTrade to fail due to missing buyer BTC wallet, but succeeded")
	}

	// Assert: seller reserved BTC balance is STILL 2.0000000000 (atomic rollback)
	var sellerReserved string
	err = pool.QueryRow(ctx, "SELECT reserved_balance FROM wallets WHERE user_id = $1 AND asset = 'BTC'", sellerID).Scan(&sellerReserved)
	if err != nil || !decimal.RequireFromString(sellerReserved).Equal(decimal.NewFromInt(2)) {
		t.Fatalf("expected seller reserved balance untouched at 2, got %s (err: %v)", sellerReserved, err)
	}

	// Assert: seller reservation remaining amount is STILL 2.0000000000 and status ACTIVE
	var remainingAmount, status string
	err = pool.QueryRow(ctx, "SELECT remaining_amount, status FROM wallet_reservations WHERE order_id = $1", sellerOrderID).Scan(&remainingAmount, &status)
	if err != nil || !decimal.RequireFromString(remainingAmount).Equal(decimal.NewFromInt(2)) || status != "ACTIVE" {
		t.Fatalf("expected reservation untouched at 2 / ACTIVE, got %s / %s (err: %v)", remainingAmount, status, err)
	}

	// Assert: buyer reserved USDT balance is STILL 100,000 (atomic rollback)
	var buyerReservedUSDT string
	err = pool.QueryRow(ctx, "SELECT reserved_balance FROM wallets WHERE user_id = $1 AND asset = 'USDT'", buyerID).Scan(&buyerReservedUSDT)
	if err != nil || !decimal.RequireFromString(buyerReservedUSDT).Equal(decimal.RequireFromString("100000")) {
		t.Fatalf("expected buyer reserved USDT untouched at 100000, got %s (err: %v)", buyerReservedUSDT, err)
	}

	// Assert: zero ledger transactions written
	var txnCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM wallet_transactions WHERE reference_id = $1", tradeID).Scan(&txnCount)
	if err != nil || txnCount != 0 {
		t.Fatalf("expected 0 wallet transactions, got %d (err: %v)", txnCount, err)
	}

	// Assert: zero outbox events written
	var outboxCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE aggregate_id = $1", tradeID).Scan(&outboxCount)
	if err != nil || outboxCount != 0 {
		t.Fatalf("expected 0 outbox events, got %d (err: %v)", outboxCount, err)
	}

	// Assert: zero settled_trades rows written
	var settledCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM settled_trades WHERE trade_id = $1", tradeID).Scan(&settledCount)
	if err != nil || settledCount != 0 {
		t.Fatalf("expected 0 settled_trades rows after rollback, got %d", settledCount)
	}
}

func TestSettleTrade_ConcurrentPartialFills(t *testing.T) {
	pool, cleanup := getWalletServiceTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	svc := service.NewService(pool, zap.NewNop())

	buyer1ID, _ := platformuuid.New()
	buyer2ID, _ := platformuuid.New()
	sellerID, _ := platformuuid.New()
	buy1OrderID, _ := platformuuid.New()
	buy2OrderID, _ := platformuuid.New()
	sellerOrderID, _ := platformuuid.New()

	// Seller has 1.0 BTC reserved; buyer 1 has 30,000 USDT reserved
	setupTestWalletsAndReservation(t, ctx, pool, buyer1ID, sellerID, buy1OrderID, sellerOrderID, "1.0000000000", "30000.0000000000")

	// Setup buyer 2 BTC wallet, USDT wallet and reservation
	b2BTCWalletID, _ := platformuuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
		VALUES ($1, $2, 'BTC', 0, 0, 0)
		ON CONFLICT (user_id, asset) DO UPDATE SET available_balance = 0, reserved_balance = 0, total_balance = 0
	`, b2BTCWalletID, buyer2ID)
	if err != nil {
		t.Fatalf("failed to setup buyer 2 BTC wallet: %v", err)
	}

	b2USDTWalletID, _ := platformuuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
		VALUES ($1, $2, 'USDT', 0, 30000, 30000)
		ON CONFLICT (user_id, asset) DO UPDATE SET available_balance = 0, reserved_balance = 30000, total_balance = 30000
	`, b2USDTWalletID, buyer2ID)
	if err != nil {
		t.Fatalf("failed to setup buyer 2 USDT wallet: %v", err)
	}

	b2ResID, _ := platformuuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO wallet_reservations (id, order_id, user_id, asset, reserved_amount, consumed_amount, remaining_amount, status)
		VALUES ($1, $2, $3, 'USDT', 30000, 0, 30000, 'ACTIVE')
		ON CONFLICT (order_id) DO UPDATE SET consumed_amount = 0, remaining_amount = 30000, status = 'ACTIVE'
	`, b2ResID, buy2OrderID, buyer2ID)
	if err != nil {
		t.Fatalf("failed to setup buyer 2 reservation: %v", err)
	}

	trade1ID, _ := platformuuid.New()
	trade2ID, _ := platformuuid.New()

	// Both concurrent trades attempt to settle 0.6 BTC against the 1.0 BTC seller reservation
	// Exactly one must succeed and the second must fail with ErrInsufficientReservation
	req1 := service.TradeSettlementRequest{
		TradeID:       trade1ID,
		BuyerUserID:   buyer1ID,
		SellerUserID:  sellerID,
		BuyOrderID:    buy1OrderID,
		SellerOrderID: sellerOrderID,
		MarketID:      "BTC-USDT",
		BaseAsset:     "BTC",
		QuoteAsset:    "USDT",
		BaseAmount:    "0.6000000000",
		QuoteAmount:   "30000.0000000000",
		Price:         "50000.0000000000",
		Quantity:      "0.6000000000",
		Sequence:      1,
		ExecutedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	req2 := service.TradeSettlementRequest{
		TradeID:       trade2ID,
		BuyerUserID:   buyer2ID,
		SellerUserID:  sellerID,
		BuyOrderID:    buy2OrderID,
		SellerOrderID: sellerOrderID,
		MarketID:      "BTC-USDT",
		BaseAsset:     "BTC",
		QuoteAsset:    "USDT",
		BaseAmount:    "0.6000000000",
		QuoteAmount:   "30000.0000000000",
		Price:         "50000.0000000000",
		Quantity:      "0.6000000000",
		Sequence:      2,
		ExecutedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	var wg sync.WaitGroup
	var err1, err2 error
	wg.Add(2)

	go func() {
		defer wg.Done()
		err1 = svc.SettleTrade(ctx, req1)
	}()
	go func() {
		defer wg.Done()
		err2 = svc.SettleTrade(ctx, req2)
	}()
	wg.Wait()

	// Exactly one should succeed and one should fail
	successCount := 0
	if err1 == nil {
		successCount++
	}
	if err2 == nil {
		successCount++
	}

	if successCount != 1 {
		t.Fatalf("expected exactly 1 settlement to succeed, got %d (err1: %v, err2: %v)", successCount, err1, err2)
	}

	// Verify remaining amount is 0.4000000000 and status is PARTIALLY_CONSUMED
	var remainingAmount, status string
	err = pool.QueryRow(ctx, "SELECT remaining_amount, status FROM wallet_reservations WHERE order_id = $1", sellerOrderID).Scan(&remainingAmount, &status)
	if err != nil || !decimal.RequireFromString(remainingAmount).Equal(decimal.RequireFromString("0.4")) || status != "PARTIALLY_CONSUMED" {
		t.Fatalf("expected remaining 0.4 / PARTIALLY_CONSUMED, got %s / %s", remainingAmount, status)
	}

	// Verify seller reserved balance is 0.4000000000
	var sellerReserved string
	err = pool.QueryRow(ctx, "SELECT reserved_balance FROM wallets WHERE user_id = $1 AND asset = 'BTC'", sellerID).Scan(&sellerReserved)
	if err != nil || !decimal.RequireFromString(sellerReserved).Equal(decimal.RequireFromString("0.4")) {
		t.Fatalf("expected seller reserved balance 0.4, got %s", sellerReserved)
	}
}

func TestSettleTrade_ConcurrentSameTradeID(t *testing.T) {
	pool, cleanup := getWalletServiceTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	svc := service.NewService(pool, zap.NewNop())

	buyerID, _ := platformuuid.New()
	sellerID, _ := platformuuid.New()
	buyOrderID, _ := platformuuid.New()
	sellerOrderID, _ := platformuuid.New()
	sharedTradeID, _ := platformuuid.New()

	setupTestWalletsAndReservation(t, ctx, pool, buyerID, sellerID, buyOrderID, sellerOrderID, "1.0000000000", "50000.0000000000")

	req := service.TradeSettlementRequest{
		TradeID:       sharedTradeID,
		BuyerUserID:   buyerID,
		SellerUserID:  sellerID,
		BuyOrderID:    buyOrderID,
		SellerOrderID: sellerOrderID,
		MarketID:      "BTC-USDT",
		BaseAsset:     "BTC",
		QuoteAsset:    "USDT",
		BaseAmount:    "1.0000000000",
		QuoteAmount:   "50000.0000000000",
		Price:         "50000.0000000000",
		Quantity:      "1.0000000000",
		Sequence:      1,
		ExecutedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	// 5 concurrent workers attempting the exact same TradeID
	const workers = 5
	var wg sync.WaitGroup
	var successCount atomic.Int64
	var errCount atomic.Int64

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			err := svc.SettleTrade(ctx, req)
			if err == nil {
				successCount.Add(1)
			} else {
				errCount.Add(1)
			}
		}()
	}
	wg.Wait()

	// All workers must return without fatal error (either winner commits, or duplicates safely return nil)
	if errCount.Load() > 0 {
		t.Fatalf("expected 0 unexpected errors, got %d", errCount.Load())
	}

	// Invariant 1: Seller BTC balance debited exactly once (to 0)
	var sellerReservedBTC string
	err := pool.QueryRow(ctx, "SELECT reserved_balance FROM wallets WHERE user_id = $1 AND asset = 'BTC'", sellerID).Scan(&sellerReservedBTC)
	if err != nil || !decimal.RequireFromString(sellerReservedBTC).IsZero() {
		t.Fatalf("expected seller reserved BTC 0, got %s", sellerReservedBTC)
	}

	// Invariant 2: Buyer BTC balance credited exactly once (to 1.0)
	var buyerAvailableBTC string
	err = pool.QueryRow(ctx, "SELECT available_balance FROM wallets WHERE user_id = $1 AND asset = 'BTC'", buyerID).Scan(&buyerAvailableBTC)
	if err != nil || !decimal.RequireFromString(buyerAvailableBTC).Equal(decimal.NewFromInt(1)) {
		t.Fatalf("expected buyer available BTC 1, got %s", buyerAvailableBTC)
	}

	// Invariant 3: Buyer USDT balance debited exactly once (to 0)
	var buyerReservedUSDT string
	err = pool.QueryRow(ctx, "SELECT reserved_balance FROM wallets WHERE user_id = $1 AND asset = 'USDT'", buyerID).Scan(&buyerReservedUSDT)
	if err != nil || !decimal.RequireFromString(buyerReservedUSDT).IsZero() {
		t.Fatalf("expected buyer reserved USDT 0, got %s", buyerReservedUSDT)
	}

	// Invariant 4: Seller USDT balance credited exactly once (to 50000)
	var sellerAvailableUSDT string
	err = pool.QueryRow(ctx, "SELECT available_balance FROM wallets WHERE user_id = $1 AND asset = 'USDT'", sellerID).Scan(&sellerAvailableUSDT)
	if err != nil || !decimal.RequireFromString(sellerAvailableUSDT).Equal(decimal.RequireFromString("50000")) {
		t.Fatalf("expected seller available USDT 50000, got %s", sellerAvailableUSDT)
	}

	// Invariant 5: Exactly 4 ledger records exist for this trade
	var txnCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM wallet_transactions WHERE reference_id = $1", sharedTradeID).Scan(&txnCount)
	if err != nil || txnCount != 4 {
		t.Fatalf("expected exactly 4 ledger records, got %d", txnCount)
	}

	// Invariant 6: Exactly 1 settled_trades entry exists
	var settledCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM settled_trades WHERE trade_id = $1", sharedTradeID).Scan(&settledCount)
	if err != nil || settledCount != 1 {
		t.Fatalf("expected exactly 1 settled_trades record, got %d", settledCount)
	}

	// Invariant 7: Exactly 3 outbox events exist for this trade
	var outboxCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE aggregate_id = $1", sharedTradeID).Scan(&outboxCount)
	if err != nil || outboxCount != 3 {
		t.Fatalf("expected exactly 3 outbox events, got %d", outboxCount)
	}
}

func TestSettleTrade_AtomicRollbackOnOutboxFailure(t *testing.T) {
	pool, cleanup := getWalletServiceTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	svc := service.NewService(pool, zap.NewNop())

	buyerID, _ := platformuuid.New()
	sellerID, _ := platformuuid.New()
	buyOrderID, _ := platformuuid.New()
	sellerOrderID, _ := platformuuid.New()
	tradeID, _ := platformuuid.New()

	setupTestWalletsAndReservation(t, ctx, pool, buyerID, sellerID, buyOrderID, sellerOrderID, "1.0000000000", "50000.0000000000")

	// Create a temporary PostgreSQL trigger that fails ONLY on the 3rd outbox event (PortfolioUserTrade with role=SELL)
	// This simulates a failure occurring after balances debited/credited, ledger written, TradeSettled inserted, and BUY leg inserted.
	_, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION test_fail_outbox_seller_leg()
		RETURNS TRIGGER AS $$
		BEGIN
			IF NEW.event_type = 'PortfolioUserTrade' AND (NEW.payload->>'role') = 'SELL' THEN
				RAISE EXCEPTION 'simulated outbox insertion failure on seller leg';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;

		DROP TRIGGER IF EXISTS trg_test_fail_outbox ON outbox;
		CREATE TRIGGER trg_test_fail_outbox
		BEFORE INSERT ON outbox
		FOR EACH ROW EXECUTE FUNCTION test_fail_outbox_seller_leg();
	`)
	if err != nil {
		t.Fatalf("failed to create test failure trigger: %v", err)
	}

	defer func() {
		pool.Exec(context.Background(), `
			DROP TRIGGER IF EXISTS trg_test_fail_outbox ON outbox;
			DROP FUNCTION IF EXISTS test_fail_outbox_seller_leg();
		`)
	}()

	req := service.TradeSettlementRequest{
		TradeID:       tradeID,
		BuyerUserID:   buyerID,
		SellerUserID:  sellerID,
		BuyOrderID:    buyOrderID,
		SellerOrderID: sellerOrderID,
		MarketID:      "BTC-USDT",
		BaseAsset:     "BTC",
		QuoteAsset:    "USDT",
		BaseAmount:    "1.0000000000",
		QuoteAmount:   "50000.0000000000",
		Price:         "50000.0000000000",
		Quantity:      "1.0000000000",
		Sequence:      1,
		ExecutedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	// SettleTrade must fail when inserting the 3rd event
	err = svc.SettleTrade(ctx, req)
	if err == nil {
		t.Fatal("expected SettleTrade to fail on outbox insertion, but succeeded")
	}

	// Invariant 1: Seller reserved BTC balance must be restored to 1.0000000000 (atomic rollback)
	var sellerReservedBTC string
	err = pool.QueryRow(ctx, "SELECT reserved_balance FROM wallets WHERE user_id = $1 AND asset = 'BTC'", sellerID).Scan(&sellerReservedBTC)
	if err != nil || !decimal.RequireFromString(sellerReservedBTC).Equal(decimal.NewFromInt(1)) {
		t.Fatalf("expected seller reserved balance to be rolled back to 1, got %s (err: %v)", sellerReservedBTC, err)
	}

	// Invariant 2: Buyer available BTC balance must be 0 (atomic rollback)
	var buyerAvailableBTC string
	err = pool.QueryRow(ctx, "SELECT available_balance FROM wallets WHERE user_id = $1 AND asset = 'BTC'", buyerID).Scan(&buyerAvailableBTC)
	if err != nil || !decimal.RequireFromString(buyerAvailableBTC).IsZero() {
		t.Fatalf("expected buyer available balance to be rolled back to 0, got %s (err: %v)", buyerAvailableBTC, err)
	}

	// Invariant 3: Buyer reserved USDT balance must be restored to 50000 (atomic rollback)
	var buyerReservedUSDT string
	err = pool.QueryRow(ctx, "SELECT reserved_balance FROM wallets WHERE user_id = $1 AND asset = 'USDT'", buyerID).Scan(&buyerReservedUSDT)
	if err != nil || !decimal.RequireFromString(buyerReservedUSDT).Equal(decimal.RequireFromString("50000")) {
		t.Fatalf("expected buyer reserved USDT to be rolled back to 50000, got %s (err: %v)", buyerReservedUSDT, err)
	}

	// Invariant 4: Seller available USDT balance must be 0 (atomic rollback)
	var sellerAvailableUSDT string
	err = pool.QueryRow(ctx, "SELECT available_balance FROM wallets WHERE user_id = $1 AND asset = 'USDT'", sellerID).Scan(&sellerAvailableUSDT)
	if err != nil || !decimal.RequireFromString(sellerAvailableUSDT).IsZero() {
		t.Fatalf("expected seller available USDT to be rolled back to 0, got %s (err: %v)", sellerAvailableUSDT, err)
	}

	// Invariant 5: Reservation remaining amount must remain 1.0000000000 and status ACTIVE
	var remainingAmount, status string
	err = pool.QueryRow(ctx, "SELECT remaining_amount, status FROM wallet_reservations WHERE order_id = $1", sellerOrderID).Scan(&remainingAmount, &status)
	if err != nil || !decimal.RequireFromString(remainingAmount).Equal(decimal.NewFromInt(1)) || status != "ACTIVE" {
		t.Fatalf("expected reservation remaining 1 / ACTIVE, got %s / %s (err: %v)", remainingAmount, status, err)
	}

	// Invariant 6: Zero ledger transactions must exist
	var txnCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM wallet_transactions WHERE reference_id = $1", tradeID).Scan(&txnCount)
	if err != nil || txnCount != 0 {
		t.Fatalf("expected 0 ledger entries after rollback, got %d", txnCount)
	}

	// Invariant 7: Zero outbox events must exist
	var outboxCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE aggregate_id = $1", tradeID).Scan(&outboxCount)
	if err != nil || outboxCount != 0 {
		t.Fatalf("expected 0 outbox events after rollback, got %d", outboxCount)
	}

	// Invariant 8: Zero settled_trades rows must exist
	var settledCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM settled_trades WHERE trade_id = $1", tradeID).Scan(&settledCount)
	if err != nil || settledCount != 0 {
		t.Fatalf("expected 0 settled_trades rows after rollback, got %d", settledCount)
	}
}
