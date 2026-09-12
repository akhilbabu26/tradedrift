package domain

import "errors"

var (
	// Authentication / Authorization
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden: admin role required")
	ErrMissingToken = errors.New("missing Authorization header")

	// Idempotency
	ErrOperationInProgress   = errors.New("operation in progress: same idempotency key is currently being processed")
	ErrMissingIdempotencyKey = errors.New("Idempotency-Key header is required")
	ErrIdempotencyKeyTooLong = errors.New("Idempotency-Key must be 1-128 characters")

	// Business validation
	ErrInvalidTarget     = errors.New("target_id is required")
	ErrInvalidReason     = errors.New("reason is required and must be 5-500 characters")
	ErrOperationNotFound = errors.New("operation not found")

	// Saga
	ErrSagaExhausted = errors.New("saga task exhausted all retry attempts")

	// Downstream
	ErrAuthUnavailable   = errors.New("auth service temporarily unavailable")
	ErrWalletUnavailable = errors.New("wallet service temporarily unavailable")

	// Worker Leasing
	ErrWorkerLeaseLost = errors.New("worker lease lost or expired; operation aborted")
)
