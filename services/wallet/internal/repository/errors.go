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

	// ErrInsufficientReservation is returned when remaining_amount < requested settlement amount.
	ErrInsufficientReservation = errors.New("insufficient reservation remaining amount")

	// ErrReservationNotFound is returned when an order reservation does not exist.
	ErrReservationNotFound = errors.New("reservation not found")

	// ErrInvalidSettlement is returned when settlement inputs violate domain invariants.
	ErrInvalidSettlement = errors.New("invalid settlement parameters")

	// ErrSettlementConflict is returned when a TradeID is already settled but the incoming
	// request carries different market_id or sequence values. This indicates upstream replay
	// corruption — the same TradeID must always represent the same immutable trade.
	ErrSettlementConflict = errors.New("settlement conflict: trade_id already settled with different market_id or sequence")

	// ErrInvalidReservation is returned when ReserveFunds inputs fail domain validation
	// (e.g. non-positive amount, unparseable decimal, disabled asset, excess precision).
	ErrInvalidReservation = errors.New("invalid reservation parameters")

	// ErrReservationConflict is returned when a reservation already exists for the given
	// orderID but belongs to a different (userID, asset, amount) identity, or when the
	// existing reservation has reached a terminal status (RELEASED or CONSUMED) that
	// cannot be reused — orderID represents one immutable reservation lifecycle.
	ErrReservationConflict = errors.New("reservation conflict: order_id already has a reservation with different identity or terminal status")

	// ErrInvalidDeposit is returned when DepositFunds inputs fail domain validation.
	ErrInvalidDeposit = errors.New("invalid deposit parameters")

	// ErrWalletNotFound is returned when a wallet for the requested user and asset does not exist.
	ErrWalletNotFound = errors.New("wallet not found")
)


