package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet/internal/repository"
	"tradedrift/services/wallet/internal/service"
)

func TestDepositFunds_InputValidation(t *testing.T) {
	ctx := context.Background()
	// svc with nil pool to verify domain validation fails BEFORE any DB access
	svc := service.NewService(nil, zap.NewNop())

	tests := []struct {
		name          string
		userID        string
		asset         string
		amount        string
		refID         string
		refType       string
		expectedErr   error
	}{
		{
			name:        "empty user_id",
			userID:      "",
			asset:       "USDT",
			amount:      "1000",
			refID:       "ref-1",
			refType:     "TOPUP",
			expectedErr: repository.ErrInvalidDeposit,
		},
		{
			name:        "empty asset",
			userID:      "user-1",
			asset:       "",
			amount:      "1000",
			refID:       "ref-1",
			refType:     "TOPUP",
			expectedErr: repository.ErrInvalidDeposit,
		},
		{
			name:        "empty reference_id",
			userID:      "user-1",
			asset:       "USDT",
			amount:      "1000",
			refID:       "",
			refType:     "TOPUP",
			expectedErr: repository.ErrInvalidDeposit,
		},
		{
			name:        "empty reference_type",
			userID:      "user-1",
			asset:       "USDT",
			amount:      "1000",
			refID:       "ref-1",
			refType:     "",
			expectedErr: repository.ErrInvalidDeposit,
		},
		{
			name:        "zero amount",
			userID:      "user-1",
			asset:       "USDT",
			amount:      "0",
			refID:       "ref-1",
			refType:     "TOPUP",
			expectedErr: repository.ErrInvalidDeposit,
		},
		{
			name:        "negative amount",
			userID:      "user-1",
			asset:       "USDT",
			amount:      "-50",
			refID:       "ref-1",
			refType:     "TOPUP",
			expectedErr: repository.ErrInvalidDeposit,
		},
		{
			name:        "invalid decimal format",
			userID:      "user-1",
			asset:       "USDT",
			amount:      "not-a-number",
			refID:       "ref-1",
			refType:     "TOPUP",
			expectedErr: repository.ErrInvalidDeposit,
		},
		{
			name:        "scientific notation rejected",
			userID:      "user-1",
			asset:       "USDT",
			amount:      "1e5",
			refID:       "ref-1",
			refType:     "TOPUP",
			expectedErr: repository.ErrInvalidDeposit,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.DepositFunds(ctx, tc.userID, tc.asset, tc.amount, tc.refID, tc.refType)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, tc.expectedErr) {
				t.Fatalf("expected error wrapping %v, got %v", tc.expectedErr, err)
			}
		})
	}
}

func TestDepositFunds_RejectsUninitializedWallet(t *testing.T) {
	pool, cleanup := getWalletServiceTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	svc := service.NewService(pool, zap.NewNop())

	randomUserID, _ := platformuuid.New()
	_, err := svc.DepositFunds(ctx, randomUserID, "USDT", "50.00", "ref-uninit-1", repository.RefTopUp)
	if err == nil {
		t.Fatalf("expected error for uninitialized wallet, got nil")
	}
	if !errors.Is(err, repository.ErrWalletNotFound) {
		t.Fatalf("expected ErrWalletNotFound, got %v", err)
	}
}

func TestDepositFunds_AssetValidation(t *testing.T) {
	pool, cleanup := getWalletServiceTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	svc := service.NewService(pool, zap.NewNop())
	userID, _ := platformuuid.New()

	// Initialize wallet for user
	if err := svc.InitializeWallet(ctx, userID); err != nil {
		t.Fatalf("InitializeWallet failed: %v", err)
	}

	// 1. Unsupported asset DOGE
	_, err := svc.DepositFunds(ctx, userID, "DOGE", "50.00", "ref-doge-1", repository.RefTopUp)
	if err == nil || !errors.Is(err, repository.ErrInvalidDeposit) {
		t.Fatalf("expected ErrInvalidDeposit for DOGE, got %v", err)
	}

	// 2. Excess decimal places for USDT (decimals = 2)
	_, err = svc.DepositFunds(ctx, userID, "USDT", "50.005", "ref-prec-1", repository.RefTopUp)
	if err == nil || !errors.Is(err, repository.ErrInvalidDeposit) {
		t.Fatalf("expected ErrInvalidDeposit for excess precision, got %v", err)
	}
}

func TestDepositFunds_WalletScopedIdempotency(t *testing.T) {
	pool, cleanup := getWalletServiceTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	svc := service.NewService(pool, zap.NewNop())

	userA, _ := platformuuid.New()
	userB, _ := platformuuid.New()

	if err := svc.InitializeWallet(ctx, userA); err != nil {
		t.Fatalf("InitializeWallet for userA failed: %v", err)
	}
	if err := svc.InitializeWallet(ctx, userB); err != nil {
		t.Fatalf("InitializeWallet for userB failed: %v", err)
	}

	sharedRefID, _ := platformuuid.New()

	// 1. User A deposits with sharedRefID
	resA, err := svc.DepositFunds(ctx, userA, "USDT", "100.00", sharedRefID, repository.RefTopUp)
	if err != nil {
		t.Fatalf("User A DepositFunds failed: %v", err)
	}
	balA := decimal.RequireFromString(resA.NewBalance)

	// 2. User B deposits with EXACT SAME sharedRefID
	// Must NOT be short-circuited by User A's transaction!
	resB, err := svc.DepositFunds(ctx, userB, "USDT", "100.00", sharedRefID, repository.RefTopUp)
	if err != nil {
		t.Fatalf("User B DepositFunds failed: %v", err)
	}
	balB := decimal.RequireFromString(resB.NewBalance)

	// User B must have been credited!
	if !balB.Equal(balA) {
		t.Errorf("expected both users to have equal balance after deposit, got A=%s, B=%s", balA, balB)
	}

	// 3. User A retries with same sharedRefID
	// Must be idempotent and return existing balance without double-crediting
	retryA, err := svc.DepositFunds(ctx, userA, "USDT", "100.00", sharedRefID, repository.RefTopUp)
	if err != nil {
		t.Fatalf("User A retry failed: %v", err)
	}
	if !decimal.RequireFromString(retryA.NewBalance).Equal(balA) {
		t.Errorf("expected balance %s on retry, got %s (double credited!)", balA, retryA.NewBalance)
	}
	if retryA.TransactionID != resA.TransactionID {
		t.Errorf("expected idempotent retry to return identical TransactionID %s, got %s", resA.TransactionID, retryA.TransactionID)
	}
}
