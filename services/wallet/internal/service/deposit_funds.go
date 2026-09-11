package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet/internal/repository"
)

// DepositResult represents the result of a successful or idempotent deposit.
type DepositResult struct {
	TransactionID string
	NewBalance    string
}

// DepositFunds credits available balance for a user and writes an immutable ledger entry.
//
// Guaranteed Idempotency:
//  1. Fast Path: Checks ExistsByWalletAndReference(wallet_id, reference_id, reference_type).
//     Scoped strictly to the user's wallet, matching DB constraint UNIQUE(wallet_id, reference_id, reference_type).
//  2. Database-Enforced Path: If a race occurs where two requests pass check 1 simultaneously,
//     the database constraint uq_wallet_transactions_key (wallet_id, reference_id, reference_type)
//     rejects the second insert with ErrDuplicate (PostgreSQL 23505). DepositFunds intercepts
//     this error, rolls back the transaction, and returns the current balance idempotently.
//
// Policy Decisions:
//   - Wallets must be pre-initialized via InitializeWallet (returns ErrWalletNotFound if missing).
//   - Frozen wallets permit incoming credits/deposits/refunds (Policy B). Outgoing debits are blocked by ReserveFunds.
func (s *Service) DepositFunds(ctx context.Context, userID, asset, amount, referenceID, referenceType string) (*DepositResult, error) {
	// ── 1. Input Validation ──────────────────────────────────────────────────────
	if userID == "" {
		return nil, fmt.Errorf("%w: user_id is required", repository.ErrInvalidDeposit)
	}
	if asset == "" {
		return nil, fmt.Errorf("%w: asset is required", repository.ErrInvalidDeposit)
	}
	if referenceID == "" {
		return nil, fmt.Errorf("%w: reference_id is required", repository.ErrInvalidDeposit)
	}
	if referenceType == "" {
		return nil, fmt.Errorf("%w: reference_type is required", repository.ErrInvalidDeposit)
	}

	depositAmount, err := decimal.NewFromString(amount)
	if err != nil || !depositAmount.IsPositive() {
		return nil, fmt.Errorf("%w: invalid amount %s", repository.ErrInvalidDeposit, amount)
	}

	// Reject scientific notation (e.g. "5e4") — all amounts must be plain decimal.
	if depositAmount.Exponent() > 0 {
		return nil, fmt.Errorf("%w: amount %q must be plain decimal notation (scientific notation not accepted)", repository.ErrInvalidDeposit, amount)
	}

	// ── 2. Supported Asset Validation & Decimal Precision Check ─────────────────
	assetInfo, err := s.assetRepo.GetByCode(ctx, asset)
	if err != nil {
		return nil, fmt.Errorf("failed to look up asset %s: %w", asset, err)
	}
	if assetInfo == nil {
		return nil, fmt.Errorf("%w: asset %q is not a supported asset", repository.ErrInvalidDeposit, asset)
	}
	if !assetInfo.IsEnabled {
		return nil, fmt.Errorf("%w: asset %q is currently disabled", repository.ErrInvalidDeposit, asset)
	}
	if scale := decimalScale(depositAmount); scale > assetInfo.Decimals {
		return nil, fmt.Errorf("%w: amount %s has %d decimal places, maximum for %s is %d",
			repository.ErrInvalidDeposit, amount, scale, asset, assetInfo.Decimals)
	}

	// ── 3. Fetch User's Pre-Initialized Wallet ──────────────────────────────────
	wallet, err := s.walletRepo.GetByUserAndAsset(ctx, userID, asset)
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet: %w", err)
	}
	if wallet == nil {
		return nil, fmt.Errorf("%w: wallet not found for user %s asset %s (wallets must be initialized)",
			repository.ErrWalletNotFound, userID, asset)
	}

	// Note: Frozen wallets permit incoming credits (deposits/refunds). Outgoing debits are blocked by ReserveFunds.

	// ── 4. Layer 1: Fast Upfront Wallet-Scoped Idempotency Check ────────────────
	existingTxn, err := s.txnRepo.GetByWalletAndReference(ctx, wallet.ID, referenceID, referenceType)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing transaction: %w", err)
	}
	if existingTxn != nil {
		s.log.Info("DepositFunds: transaction already processed (fast-path)",
			zap.String("walletID", wallet.ID),
			zap.String("referenceID", referenceID),
			zap.String("referenceType", referenceType),
			zap.String("userID", userID),
			zap.String("asset", asset),
			zap.String("transactionID", existingTxn.ID),
		)
		return &DepositResult{
			TransactionID: existingTxn.ID,
			NewBalance:    wallet.AvailableBalance,
		}, nil
	}

	// ── 5. Layer 2: Atomic Transaction with DB-Enforced Uniqueness ──────────────
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin deposit transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	walletRepo := s.walletRepo.WithTx(tx)
	txnRepo := s.txnRepo.WithTx(tx)

	// Acquire row lock on wallet
	if _, err := walletRepo.LockByIDs(ctx, []string{wallet.ID}); err != nil {
		return nil, fmt.Errorf("failed to lock wallet row: %w", err)
	}

	// Re-check idempotency under lock
	existingUnderLock, err := txnRepo.GetByWalletAndReference(ctx, wallet.ID, referenceID, referenceType)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing transaction under lock: %w", err)
	}
	if existingUnderLock != nil {
		freshWallet, err := walletRepo.GetByUserAndAsset(ctx, userID, asset)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch wallet after idempotency check: %w", err)
		}
		if freshWallet == nil {
			return nil, repository.ErrWalletNotFound
		}
		s.log.Info("DepositFunds: transaction already processed under lock",
			zap.String("walletID", wallet.ID),
			zap.String("referenceID", referenceID),
			zap.String("transactionID", existingUnderLock.ID),
		)
		return &DepositResult{
			TransactionID: existingUnderLock.ID,
			NewBalance:    freshWallet.AvailableBalance,
		}, nil
	}

	// Generate transaction ID
	txnID, err := platformuuid.New()
	if err != nil {
		return nil, fmt.Errorf("failed to generate transaction ID: %w", err)
	}

	// Credit wallet available balance
	formattedAmount := depositAmount.StringFixed(10)
	if err := walletRepo.CreditAvailable(ctx, wallet.ID, formattedAmount); err != nil {
		return nil, fmt.Errorf("failed to credit available balance: %w", err)
	}

	now := time.Now().UTC()

	// Insert immutable ledger transaction
	txn := &repository.WalletTransaction{
		ID:              txnID,
		WalletID:        wallet.ID,
		ReferenceID:     referenceID,
		ReferenceType:   referenceType,
		TransactionType: repository.TxnTypeCredit,
		Asset:           asset,
		Amount:          formattedAmount,
		CreatedAt:       now,
	}

	if err := txnRepo.Create(ctx, txn); err != nil {
		// If duplicate key violation, another concurrent worker won the race
		if errors.Is(err, repository.ErrDuplicate) {
			_ = tx.Rollback(ctx)
			s.log.Warn("DepositFunds: concurrent duplicate detected via DB constraint, returning current balance",
				zap.String("walletID", wallet.ID),
				zap.String("referenceID", referenceID),
				zap.String("referenceType", referenceType),
			)
			freshWallet, getErr := s.walletRepo.GetByUserAndAsset(ctx, userID, asset)
			if getErr == nil && freshWallet != nil {
				return &DepositResult{
					TransactionID: referenceID,
					NewBalance:    freshWallet.AvailableBalance,
				}, nil
			}
			return nil, repository.ErrDuplicate
		}
		return nil, fmt.Errorf("failed to insert deposit transaction record: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit deposit transaction: %w", err)
	}

	// Calculate new balance
	curBal, _ := decimal.NewFromString(wallet.AvailableBalance)
	newBal := curBal.Add(depositAmount).StringFixed(10)

	s.log.Info("DepositFunds: successfully credited deposit",
		zap.String("userID", userID),
		zap.String("walletID", wallet.ID),
		zap.String("asset", asset),
		zap.String("amount", formattedAmount),
		zap.String("newBalance", newBal),
		zap.String("referenceID", referenceID),
		zap.String("txnID", txnID),
	)

	return &DepositResult{
		TransactionID: txnID,
		NewBalance:    newBal,
	}, nil
}
