package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet/internal/repository"
	"tradedrift/services/wallet/internal/repository/postgres"
)

func TestWalletRepository_DebitReserved_BalanceGuard(t *testing.T) {
	pool, cleanup := getWalletTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewWalletRepository(pool)

	walletID, _ := platformuuid.New()
	userID, _ := platformuuid.New()

	// Seed wallet with 5.0 BTC reserved
	_, err := pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
		VALUES ($1, $2, 'BTC', 0, 5.0, 5.0)
	`, walletID, userID)
	if err != nil {
		t.Fatalf("failed to insert test wallet: %v", err)
	}

	// 1. Debit 3.0 BTC (valid, 5.0 >= 3.0) -> must succeed
	if err := repo.DebitReserved(ctx, walletID, "3.0000000000"); err != nil {
		t.Fatalf("expected DebitReserved to succeed, got: %v", err)
	}

	var reserved string
	err = pool.QueryRow(ctx, "SELECT reserved_balance FROM wallets WHERE id = $1", walletID).Scan(&reserved)
	if err != nil || !decimal.RequireFromString(reserved).Equal(decimal.NewFromInt(2)) {
		t.Fatalf("expected reserved balance 2, got %s (err: %v)", reserved, err)
	}

	// 2. Debit 3.0 BTC again (invalid, 2.0 < 3.0) -> must return ErrInsufficientBalance via SQL balance guard
	err = repo.DebitReserved(ctx, walletID, "3.0000000000")
	if !errors.Is(err, repository.ErrInsufficientBalance) {
		t.Fatalf("expected ErrInsufficientBalance, got %v", err)
	}

	// 3. Verify reserved balance was NOT modified (remains 2.0)
	err = pool.QueryRow(ctx, "SELECT reserved_balance FROM wallets WHERE id = $1", walletID).Scan(&reserved)
	if err != nil || !decimal.RequireFromString(reserved).Equal(decimal.NewFromInt(2)) {
		t.Fatalf("expected reserved balance untouched at 2, got %s", reserved)
	}
}

func TestWalletRepository_MoveFromReserved_BalanceGuard(t *testing.T) {
	pool, cleanup := getWalletTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewWalletRepository(pool)

	walletID, _ := platformuuid.New()
	userID, _ := platformuuid.New()

	// Seed wallet with 2.0 USDT reserved, 0 available
	_, err := pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, total_balance)
		VALUES ($1, $2, 'USDT', 0, 2.0, 2.0)
	`, walletID, userID)
	if err != nil {
		t.Fatalf("failed to insert test wallet: %v", err)
	}

	// 1. Move 1.0 USDT back to available (valid) -> must succeed
	if err := repo.MoveFromReserved(ctx, walletID, "1.0000000000"); err != nil {
		t.Fatalf("expected MoveFromReserved to succeed, got: %v", err)
	}

	var reserved, available string
	err = pool.QueryRow(ctx, "SELECT reserved_balance, available_balance FROM wallets WHERE id = $1", walletID).Scan(&reserved, &available)
	if err != nil || !decimal.RequireFromString(reserved).Equal(decimal.NewFromInt(1)) || !decimal.RequireFromString(available).Equal(decimal.NewFromInt(1)) {
		t.Fatalf("expected reserved 1, available 1, got %s, %s", reserved, available)
	}

	// 2. Move 2.0 USDT (invalid, 1.0 < 2.0) -> must return ErrInsufficientBalance
	err = repo.MoveFromReserved(ctx, walletID, "2.0000000000")
	if !errors.Is(err, repository.ErrInsufficientBalance) {
		t.Fatalf("expected ErrInsufficientBalance, got %v", err)
	}

	// 3. Verify balances remained untouched
	err = pool.QueryRow(ctx, "SELECT reserved_balance, available_balance FROM wallets WHERE id = $1", walletID).Scan(&reserved, &available)
	if err != nil || !decimal.RequireFromString(reserved).Equal(decimal.NewFromInt(1)) || !decimal.RequireFromString(available).Equal(decimal.NewFromInt(1)) {
		t.Fatalf("expected balances untouched at 1, 1, got %s, %s", reserved, available)
	}
}
