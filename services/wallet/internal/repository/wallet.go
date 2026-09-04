package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Wallet represents a single (user, asset) balance record.
type Wallet struct{
	ID               string
	UserID           string
	Asset            string
	AvailableBalance string // Decimal string e.g. "10000.0000000000"
	ReservedBalance  string
	IsFrozen         bool
	FrozenAt         *time.Time
	FrozenBy         *string
	FreezeReason     *string
	InitialBalance   string
	TotalBalance     string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// WalletRepository defines the persistence contract for wallet balances.
type WalletRepository interface {
	// WithTx binds the repository to an active PostgreSQL transaction.
	WithTx(tx pgx.Tx) WalletRepository
	// GetByUserAndAsset retrieves a single wallet for a (user, asset) pair.
	// Returns nil, nil if not found.
	GetByUserAndAsset(ctx context.Context, userID, asset string) (*Wallet, error)
	// GetAllByUser retrieves all wallets owned by a user.
	GetAllByUser(ctx context.Context, userID string) ([]*Wallet, error)
	// FreezeWallet marks a wallet as frozen, blocking all balance changes.
	FreezeWallet(ctx context.Context, walletID, frozenBy, reason string) error
	// UnfreezeWallet removes the freeze from a wallet.
	UnfreezeWallet(ctx context.Context, walletID string) error
	// Create inserts a new wallet row.
	Create(ctx context.Context, w *Wallet) error
	// CreditAvailable adds amount to available_balance and updates total_balance.
	CreditAvailable(ctx context.Context, walletID, amount string) error
	// DebitAvailable subtracts amount from available_balance and updates total_balance.
	DebitAvailable(ctx context.Context, walletID, amount string) error
	// MoveToReserved moves amount from available_balance to reserved_balance atomically.
	MoveToReserved(ctx context.Context, walletID, amount string) error
	// MoveFromReserved moves amount from reserved_balance back to available_balance atomically.
	MoveFromReserved(ctx context.Context, walletID, amount string) error
	// DebitReserved subtracts amount from reserved_balance (used during settlement).
	DebitReserved(ctx context.Context, walletID, amount string) error
}