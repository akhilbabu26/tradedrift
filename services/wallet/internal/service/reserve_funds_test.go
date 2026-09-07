package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet/internal/repository"
	"tradedrift/services/wallet/internal/service"
)

func TestReserveFunds_IdempotentReturn(t *testing.T) {
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

	// Setup wallet with 1000 USDT available
	_, err := pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
		VALUES ($1, $2, 'USDT', 1000, 0, 1000)
	`, walletID, userID)
	if err != nil {
		t.Fatalf("failed to insert initial wallet: %v", err)
	}

	// First reservation call — USDT has decimals=2, so use ≤ 2 d.p.
	res1, err := svc.ReserveFunds(ctx, userID, orderID, "USDT", "150.00")
	if err != nil {
		t.Fatalf("first ReserveFunds failed: %v", err)
	}
	if res1 == nil || res1.ID == "" {
		t.Fatalf("expected valid reservation, got nil or empty ID")
	}

	// Second reservation call with same orderID
	res2, err := svc.ReserveFunds(ctx, userID, orderID, "USDT", "150.00")
	if err != nil {
		t.Fatalf("second ReserveFunds failed: %v", err)
	}
	if res2 == nil {
		t.Fatalf("expected valid reservation on second call, got nil")
	}
	if res1.ID != res2.ID {
		t.Fatalf("expected same reservation ID %s, got %s", res1.ID, res2.ID)
	}

	// Verify wallet balance in DB: available = 850, reserved = 150
	var avail, res decimal.Decimal
	err = pool.QueryRow(ctx, `SELECT available_balance, reserved_balance FROM wallets WHERE id = $1`, walletID).Scan(&avail, &res)
	if err != nil {
		t.Fatalf("failed to query wallet: %v", err)
	}

	expectedAvail := decimal.RequireFromString("850.00")
	expectedRes := decimal.RequireFromString("150.00")
	if !avail.Equal(expectedAvail) {
		t.Fatalf("expected available %s, got %s", expectedAvail, avail)
	}
	if !res.Equal(expectedRes) {
		t.Fatalf("expected reserved %s, got %s", expectedRes, res)
	}

	// Verify exactly 1 ledger transaction
	var txCount int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM wallet_transactions WHERE reference_id = $1`, orderID).Scan(&txCount)
	if err != nil {
		t.Fatalf("failed to query tx count: %v", err)
	}
	if txCount != 1 {
		t.Fatalf("expected exactly 1 ledger transaction, got %d", txCount)
	}
}

func TestReserveFunds_ConcurrentSameOrderID(t *testing.T) {
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

	// Setup wallet with 1000 USDT available
	_, err := pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
		VALUES ($1, $2, 'USDT', 1000, 0, 1000)
	`, walletID, userID)
	if err != nil {
		t.Fatalf("failed to insert initial wallet: %v", err)
	}

	concurrency := 6
	var wg sync.WaitGroup
	results := make([]*repository.Reservation, concurrency)
	errors := make([]error, concurrency)

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		idx := i
		go func() {
			defer wg.Done()
			r, err := svc.ReserveFunds(ctx, userID, orderID, "USDT", "100.00")
			results[idx] = r
			errors[idx] = err
		}()
	}
	wg.Wait()

	// All concurrent callers should succeed idempotently and return the reservation
	for i, err := range errors {
		if err != nil {
			t.Fatalf("concurrent caller %d failed unexpectedly: %v", i, err)
		}
		if results[i] == nil {
			t.Fatalf("concurrent caller %d returned nil reservation", i)
		}
	}

	// Check reservation IDs are all identical
	firstID := results[0].ID
	for i := 1; i < concurrency; i++ {
		if results[i].ID != firstID {
			t.Fatalf("caller %d got different reservation ID %s vs %s", i, results[i].ID, firstID)
		}
	}

	// Invariant 1: Exactly 1 reservation row in DB
	var resCount int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM wallet_reservations WHERE order_id = $1`, orderID).Scan(&resCount)
	if err != nil {
		t.Fatalf("failed to count reservations: %v", err)
	}
	if resCount != 1 {
		t.Fatalf("expected exactly 1 reservation row, got %d", resCount)
	}

	// Invariant 2: Wallet balance changed by exactly 100 (available=900, reserved=100)
	var avail, res decimal.Decimal
	err = pool.QueryRow(ctx, `SELECT available_balance, reserved_balance FROM wallets WHERE id = $1`, walletID).Scan(&avail, &res)
	if err != nil {
		t.Fatalf("failed to query wallet balances: %v", err)
	}
	expectedAvail := decimal.RequireFromString("900.00")
	expectedRes := decimal.RequireFromString("100.00")
	if !avail.Equal(expectedAvail) {
		t.Fatalf("double-debit detected! Expected available %s, got %s", expectedAvail, avail)
	}
	if !res.Equal(expectedRes) {
		t.Fatalf("double-reserve detected! Expected reserved %s, got %s", expectedRes, res)
	}

	// Invariant 3: Exactly 1 ledger debit row
	var txCount int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM wallet_transactions WHERE reference_id = $1`, orderID).Scan(&txCount)
	if err != nil {
		t.Fatalf("failed to count ledger rows: %v", err)
	}
	if txCount != 1 {
		t.Fatalf("expected exactly 1 ledger row, got %d", txCount)
	}

	t.Logf("Verified: %d concurrent ReserveFunds calls produced exactly 1 reservation, 1 ledger entry, and exact balances", concurrency)
}

// ─────────────────────────────────────────────────────────────────────────────
// Identity + status conflict tests
// ─────────────────────────────────────────────────────────────────────────────

func TestReserveFunds_RejectsConflictingIdentity(t *testing.T) {
	pool, cleanup := getWalletServiceTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	svc := service.NewService(pool, zap.NewNop())

	user1, _ := platformuuid.New()
	user2, _ := platformuuid.New()
	orderID, _ := platformuuid.New()
	wallet1, _ := platformuuid.New()
	wallet2, _ := platformuuid.New()

	// Setup USDT wallets for both users
	for _, row := range []struct{ id, uid string }{
		{wallet1, user1},
		{wallet2, user2},
	} {
		_, err := pool.Exec(ctx, `
			INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
			VALUES ($1, $2, 'USDT', 1000, 0, 1000)
		`, row.id, row.uid)
		if err != nil {
			t.Fatalf("failed to insert wallet: %v", err)
		}
	}

	// First reservation: user1, USDT, 100.00
	if _, err := svc.ReserveFunds(ctx, user1, orderID, "USDT", "100.00"); err != nil {
		t.Fatalf("first reservation failed: %v", err)
	}

	// Replay with different userID → ErrReservationConflict
	_, err := svc.ReserveFunds(ctx, user2, orderID, "USDT", "100.00")
	if !errors.Is(err, repository.ErrReservationConflict) {
		t.Fatalf("expected ErrReservationConflict for wrong userID, got: %v", err)
	}

	// Replay with different amount → ErrReservationConflict
	_, err = svc.ReserveFunds(ctx, user1, orderID, "USDT", "99.00")
	if !errors.Is(err, repository.ErrReservationConflict) {
		t.Fatalf("expected ErrReservationConflict for wrong amount, got: %v", err)
	}

	t.Log("Verified: mismatched userID and amount correctly rejected with ErrReservationConflict")
}

func TestReserveFunds_RejectsReleasedReuse(t *testing.T) {
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

	// Wallet with 800 available, 0 reserved (reservation already released)
	_, err := pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
		VALUES ($1, $2, 'USDT', 800, 0, 800)
	`, walletID, userID)
	if err != nil {
		t.Fatalf("failed to insert wallet: %v", err)
	}

	// Pre-existing RELEASED reservation
	_, err = pool.Exec(ctx, `
		INSERT INTO wallet_reservations (id, order_id, user_id, asset, reserved_amount, consumed_amount, remaining_amount, status)
		VALUES ($1, $2, $3, 'USDT', 200, 0, 0, 'RELEASED')
	`, resID, orderID, userID)
	if err != nil {
		t.Fatalf("failed to insert reservation: %v", err)
	}

	_, err = svc.ReserveFunds(ctx, userID, orderID, "USDT", "200.0000000000")
	if !errors.Is(err, repository.ErrReservationConflict) {
		t.Fatalf("expected ErrReservationConflict for RELEASED reuse, got: %v", err)
	}

	// Wallet balance must be unchanged
	var avail decimal.Decimal
	pool.QueryRow(ctx, `SELECT available_balance FROM wallets WHERE id = $1`, walletID).Scan(&avail)
	if !avail.Equal(decimal.RequireFromString("800.0000000000")) {
		t.Fatalf("balance must not change for RELEASED reuse, got available=%s", avail)
	}
	t.Log("Verified: RELEASED reservation reuse rejected with ErrReservationConflict, balance unchanged")
}

func TestReserveFunds_RejectsConsumedReuse(t *testing.T) {
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

	_, err := pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
		VALUES ($1, $2, 'USDT', 800, 0, 800)
	`, walletID, userID)
	if err != nil {
		t.Fatalf("failed to insert wallet: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO wallet_reservations (id, order_id, user_id, asset, reserved_amount, consumed_amount, remaining_amount, status)
		VALUES ($1, $2, $3, 'USDT', 100, 100, 0, 'CONSUMED')
	`, resID, orderID, userID)
	if err != nil {
		t.Fatalf("failed to insert reservation: %v", err)
	}

	_, err = svc.ReserveFunds(ctx, userID, orderID, "USDT", "100.0000000000")
	if !errors.Is(err, repository.ErrReservationConflict) {
		t.Fatalf("expected ErrReservationConflict for CONSUMED reuse, got: %v", err)
	}
	t.Log("Verified: CONSUMED reservation reuse rejected with ErrReservationConflict")
}

// ─────────────────────────────────────────────────────────────────────────────
// Input validation tests (run before any DB I/O)
// ─────────────────────────────────────────────────────────────────────────────

func TestReserveFunds_RejectsNegativeAmount(t *testing.T) {
	pool, cleanup := getWalletServiceTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	svc := service.NewService(pool, zap.NewNop())
	ctx := context.Background()

	userID, _ := platformuuid.New()
	orderID, _ := platformuuid.New()

	_, err := svc.ReserveFunds(ctx, userID, orderID, "USDT", "-10.00")
	if !errors.Is(err, repository.ErrInvalidReservation) {
		t.Fatalf("expected ErrInvalidReservation for negative amount, got: %v", err)
	}
	t.Log("Verified: negative amount rejected with ErrInvalidReservation")
}

func TestReserveFunds_RejectsZeroAmount(t *testing.T) {
	pool, cleanup := getWalletServiceTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	svc := service.NewService(pool, zap.NewNop())
	ctx := context.Background()

	userID, _ := platformuuid.New()
	orderID, _ := platformuuid.New()

	_, err := svc.ReserveFunds(ctx, userID, orderID, "USDT", "0")
	if !errors.Is(err, repository.ErrInvalidReservation) {
		t.Fatalf("expected ErrInvalidReservation for zero amount, got: %v", err)
	}
	t.Log("Verified: zero amount rejected with ErrInvalidReservation")
}

func TestReserveFunds_RejectsExcessAssetPrecision(t *testing.T) {
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

	// USDT wallet with enough balance
	_, err := pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
		VALUES ($1, $2, 'USDT', 1000, 0, 1000)
	`, walletID, userID)
	if err != nil {
		t.Fatalf("failed to insert wallet: %v", err)
	}

	// USDT has decimals=2 in supported_assets; "10.123" has 3 decimal places → rejected
	_, err = svc.ReserveFunds(ctx, userID, orderID, "USDT", "10.123")
	if !errors.Is(err, repository.ErrInvalidReservation) {
		t.Fatalf("expected ErrInvalidReservation for USDT amount with 3 d.p., got: %v", err)
	}
	t.Log("Verified: amount with excess decimal places for USDT (decimals=2) rejected with ErrInvalidReservation")
}
