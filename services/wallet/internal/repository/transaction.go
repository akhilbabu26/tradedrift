package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// WalletTransaction is an immutable ledger entry for a balance change.
type WalletTransaction struct {
	ID              string
	WalletID        string
	ReferenceID     string
	ReferenceType   string // INITIAL_ALLOCATION | RESERVATION | RELEASE | SETTLEMENT | DEPOSIT | WITHDRAWAL
	TransactionType string // CREDIT | DEBIT
	Asset           string
	Amount          string
	CreatedAt       time.Time
}

// TransactionRepository defines the persistence contract for the immutable ledger.
type TransactionRepository interface {
	// WithTx binds the repository to an active PostgreSQL transaction.
	WithTx(tx pgx.Tx) TransactionRepository

	// Create inserts a new transaction row.
	// Returns an error wrapping ErrDuplicate if UNIQUE(wallet_id, reference_id, reference_type) is violated.
	Create(ctx context.Context, t *WalletTransaction) error


	// GetByWalletAndReference retrieves a transaction row for a specific wallet and reference.
	// Matches DB constraint UNIQUE(wallet_id, reference_id, reference_type).
	GetByWalletAndReference(ctx context.Context, walletID, referenceID, referenceType string) (*WalletTransaction, error)

	// ExistsByWalletAndReference checks if a transaction row already exists for a specific wallet.
	// This matches the DB constraint UNIQUE(wallet_id, reference_id, reference_type).
	ExistsByWalletAndReference(ctx context.Context, walletID, referenceID, referenceType string) (bool, error)

	// CreateBatch inserts multiple transaction rows in a single statement.
	// Used by SettleTrade to insert buyer + seller rows atomically.
	CreateBatch(ctx context.Context, txns []*WalletTransaction) error
}