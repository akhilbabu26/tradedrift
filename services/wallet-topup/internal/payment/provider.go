package payment

import "context"

// ProviderOrderResult contains gateway identifiers created for a top-up.
type ProviderOrderResult struct {
	ProviderOrderID string
	AmountINR       int64
	Currency        string
}

// PaymentDetails models verified server-side payment state returned by provider APIs.
type PaymentDetails struct {
	PaymentID       string
	ProviderOrderID string
	AmountINR       int64
	Currency        string
	Status          string
	Captured        bool
	CreatedAt       int64
}

// PaymentProvider defines the gateway abstraction for creating orders, verifying signatures, and looking up payments.
type PaymentProvider interface {
	ProviderName() string
	CreateOrder(ctx context.Context, orderID string, amountINR int64, currency string) (*ProviderOrderResult, error)
	VerifySignature(payload []byte, signature string, timestamp int64) bool
	VerifyCheckoutSignature(orderID, paymentID, signature string) bool
	FetchPayment(ctx context.Context, paymentID string) (*PaymentDetails, error)
	FetchOrderPayments(ctx context.Context, providerOrderID string) ([]*PaymentDetails, error)
}
