package service

import "errors"

var (
	ErrOrderNotFound           = errors.New("order not found")
	ErrInvalidSide             = errors.New("order side must be BUY or SELL")
	ErrInvalidType             = errors.New("order type must be LIMIT or MARKET")
	ErrInvalidMarket           = errors.New("invalid market ID format (expected BASE-QUOTE, e.g. BTC-USDT)")
	ErrInvalidPrice            = errors.New("limit order requires a valid positive decimal price")
	ErrInvalidQuantity         = errors.New("quantity must be a valid positive decimal number")
	ErrDuplicateIdempotencyKey = errors.New("idempotency key reused with different request parameters")
	ErrInsufficientFunds       = errors.New("insufficient funds for order placement")
	ErrOrderNotCancellable     = errors.New("order cannot be cancelled in its current status")
	ErrInvalidPaginationCursor = errors.New("invalid pagination cursor")
)