package domain

import "errors"

var (
	ErrDailyLimitExceeded   = errors.New("daily top-up limit exceeded")
	ErrIdempotencyConflict  = errors.New("idempotency key reused with different amount")
	ErrInvalidAmount        = errors.New("top-up amount must be a whole rupee between ₹1 and ₹10")
	ErrOrderNotFound        = errors.New("top-up order not found")
	ErrOrderExpired         = errors.New("top-up order has expired")
	ErrInvalidSignature     = errors.New("invalid webhook signature")
	ErrWebhookReplay        = errors.New("webhook timestamp outside allowed replay window")
	ErrStaleLease           = errors.New("reconciler worker lease expired or token mismatch")
	ErrOrderTerminalStatus  = errors.New("order is in a terminal status and cannot be modified")
	ErrDuplicateWebhook     = errors.New("webhook event already processed")
	ErrMissingIdempotency   = errors.New("X-Idempotency-Key header is required")
	ErrUnauthorized         = errors.New("unauthorized")
	ErrProviderMismatch     = errors.New("webhook provider does not match order provider")
	ErrInvalidEventType     = errors.New("unsupported or non-capturing webhook event type")
	ErrInvalidIdempotencyKey = errors.New("idempotency key must be between 1 and 100 characters")
)

