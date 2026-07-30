package repository

import "errors"

// Sentinel errors for the wallet domain.
// Callers use errors.Is() to check for these, especially in gRPC handlers
// to map them to the correct gRPC status codes.

var (
	// ErrDuplicate is returned when a UNIQUE constraint is violated.
	// Treat as idempotent success — the operation already happened.
	ErrDuplicate = errors.New("duplicate: already processed")

	// ErrNotFound is returned when a requested entity does not exist.
	ErrNotFound = errors.New("not found")

	// ErrInsufficientBalance is returned when available_balance < requested amount.
	ErrInsufficientBalance = errors.New("insufficient balance")

	// ErrWalletFrozen is returned when attempting to transact on a frozen wallet.
	ErrWalletFrozen = errors.New("wallet is frozen")
)
