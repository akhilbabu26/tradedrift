package payment

import "context"

// ProviderOrderResult contains gateway identifiers created for a top-up.
type ProviderOrderResult struct {
	ProviderOrderID string
	AmountINR       int64
	Currency        string
}

// PaymentProvider defines the gateway abstraction for creating orders and verifying signatures.
type PaymentProvider interface {
	ProviderName() string
	CreateOrder(ctx context.Context, orderID string, amountINR int64, currency string) (*ProviderOrderResult, error)
	VerifySignature(payload []byte, signature string, timestamp int64) bool
}
