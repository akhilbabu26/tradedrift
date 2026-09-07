package service_test

import (
	"context"
	"sync"
	"testing"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet/internal/repository"
	"tradedrift/services/wallet/internal/service"
)

func TestReleaseFunds_IdempotentOnAlreadyReleased(t *testing.T) {
	pool, cleanup := getWalletServiceTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	svc := service.NewService(pool, zap.NewNop())

	userID, _ := platformuuid.New()
	orderID, _ := platformuuid.New()
	walletID, _ := platformuuid.New()
	resID, _ := platformuuid.New()

	// Setup wallet: 800 available, 200 reserved, 1000 total
	_, err := pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
		VALUES ($1, $2, 'USDT', 800, 200, 1000)
	`, walletID, userID)
	if err != nil {
		t.Fatalf("failed to insert initial wallet: %v", err)
	}

	// Setup active reservation with remaining=200
	_, err = pool.Exec(ctx, `
		INSERT INTO wallet_reservations (id, order_id, user_id, asset, reserved_amount, consumed_amount, remaining_amount, status)
		VALUES ($1, $2, $3, 'USDT', 200, 0, 200, 'ACTIVE')
	`, resID, orderID, userID)
	if err != nil {
		t.Fatalf("failed to insert initial reservation: %v", err)
	}

	// First release call
	if err := svc.ReleaseFunds(ctx, orderID); err != nil {
		t.Fatalf("first ReleaseFunds failed: %v", err)
	}

	// Second release call with same orderID (already RELEASED)
	if err := svc.ReleaseFunds(ctx, orderID); err != nil {
		t.Fatalf("second ReleaseFunds should succeed idempotently, got: %v", err)
	}

	// Verify wallet balance: available = 1000, reserved = 0
	var avail, res decimal.Decimal
	err = pool.QueryRow(ctx, `SELECT available_balance, reserved_balance FROM wallets WHERE id = $1`, walletID).Scan(&avail, &res)
	if err != nil {
		t.Fatalf("failed to query wallet: %v", err)
	}

	expectedAvail := decimal.RequireFromString("1000.0000000000")
	expectedRes := decimal.RequireFromString("0.0000000000")
	if !avail.Equal(expectedAvail) {
		t.Fatalf("expected available %s, got %s", expectedAvail, avail)
	}
	if !res.Equal(expectedRes) {
		t.Fatalf("expected reserved %s, got %s", expectedRes, res)
	}

	// Verify reservation status is RELEASED
	var status string
	err = pool.QueryRow(ctx, `SELECT status FROM wallet_reservations WHERE id = $1`, resID).Scan(&status)
	if err != nil {
		t.Fatalf("failed to query reservation status: %v", err)
	}
	if status != repository.ReservationReleased {
		t.Fatalf("expected status %s, got %s", repository.ReservationReleased, status)
	}

	// Verify exactly 1 ledger entry
	var txCount int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM wallet_transactions WHERE reference_id = $1`, orderID).Scan(&txCount)
	if err != nil {
		t.Fatalf("failed to query tx count: %v", err)
	}
	if txCount != 1 {
		t.Fatalf("expected exactly 1 ledger entry, got %d", txCount)
	}
}

func TestReleaseFunds_Concurrent(t *testing.T) {
	pool, cleanup := getWalletServiceTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	svc := service.NewService(pool, zap.NewNop())

	userID, _ := platformuuid.New()
	orderID, _ := platformuuid.New()
	walletID, _ := platformuuid.New()
	resID, _ := platformuuid.New()

	// Setup wallet: 500 available, 500 reserved, 1000 total
	_, err := pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
		VALUES ($1, $2, 'USDT', 500, 500, 1000)
	`, walletID, userID)
	if err != nil {
		t.Fatalf("failed to insert initial wallet: %v", err)
	}

	// Setup active reservation with remaining=500
	_, err = pool.Exec(ctx, `
		INSERT INTO wallet_reservations (id, order_id, user_id, asset, reserved_amount, consumed_amount, remaining_amount, status)
		VALUES ($1, $2, $3, 'USDT', 500, 0, 500, 'ACTIVE')
	`, resID, orderID, userID)
	if err != nil {
		t.Fatalf("failed to insert initial reservation: %v", err)
	}

	concurrency := 6
	var wg sync.WaitGroup
	errors := make([]error, concurrency)

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		idx := i
		go func() {
			defer wg.Done()
			errors[idx] = svc.ReleaseFunds(ctx, orderID)
		}()
	}
	wg.Wait()

	// All concurrent callers should succeed idempotently
	for i, err := range errors {
		if err != nil {
			t.Fatalf("concurrent release caller %d failed unexpectedly: %v", i, err)
		}
	}

	// Invariant 1: Reservation status is RELEASED
	var status string
	err = pool.QueryRow(ctx, `SELECT status FROM wallet_reservations WHERE id = $1`, resID).Scan(&status)
	if err != nil {
		t.Fatalf("failed to query reservation status: %v", err)
	}
	if status != repository.ReservationReleased {
		t.Fatalf("expected status %s, got %s", repository.ReservationReleased, status)
	}

	// Invariant 2: Balances restored exactly once (available=1000, reserved=0)
	var avail, res decimal.Decimal
	err = pool.QueryRow(ctx, `SELECT available_balance, reserved_balance FROM wallets WHERE id = $1`, walletID).Scan(&avail, &res)
	if err != nil {
		t.Fatalf("failed to query wallet balances: %v", err)
	}
	expectedAvail := decimal.RequireFromString("1000.0000000000")
	expectedRes := decimal.RequireFromString("0.0000000000")
	if !avail.Equal(expectedAvail) {
		t.Fatalf("double-credit detected! Expected available %s, got %s", expectedAvail, avail)
	}
	if !res.Equal(expectedRes) {
		t.Fatalf("negative-reserve detected! Expected reserved %s, got %s", expectedRes, res)
	}

	// Invariant 3: Exactly 1 ledger entry
	var txCount int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM wallet_transactions WHERE reference_id = $1`, orderID).Scan(&txCount)
	if err != nil {
		t.Fatalf("failed to query ledger count: %v", err)
	}
	if txCount != 1 {
		t.Fatalf("expected exactly 1 ledger entry, got %d", txCount)
	}

	t.Logf("Verified: %d concurrent ReleaseFunds calls produced exactly 1 release, 1 ledger entry, and exact balances", concurrency)
}
