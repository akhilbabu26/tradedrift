package mock

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"tradedrift/services/wallet-topup/internal/payment"
)

type MockPaymentProvider struct {
	name   string
	secret string
	mu     sync.Mutex
	// FailNextCreateOrder allows tests to simulate provider failures/timeouts
	FailNextCreateOrder  bool
	FailNextFetchPayment bool
	CustomPayments       map[string]*payment.PaymentDetails
	CustomOrderPayments  map[string][]*payment.PaymentDetails
}

var _ payment.PaymentProvider = (*MockPaymentProvider)(nil)

func NewMockPaymentProvider(secret string) *MockPaymentProvider {
	return &MockPaymentProvider{
		name:                "MOCK",
		secret:              secret,
		CustomPayments:      make(map[string]*payment.PaymentDetails),
		CustomOrderPayments: make(map[string][]*payment.PaymentDetails),
	}
}

func NewNamedMockPaymentProvider(name, secret string) *MockPaymentProvider {
	return &MockPaymentProvider{
		name:                name,
		secret:              secret,
		CustomPayments:      make(map[string]*payment.PaymentDetails),
		CustomOrderPayments: make(map[string][]*payment.PaymentDetails),
	}
}

func (p *MockPaymentProvider) ProviderName() string {
	if p.name != "" {
		return p.name
	}
	return "MOCK"
}

func (p *MockPaymentProvider) SetFailNextCreateOrder(fail bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.FailNextCreateOrder = fail
}

func (p *MockPaymentProvider) SetFailNextFetchPayment(fail bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.FailNextFetchPayment = fail
}

func (p *MockPaymentProvider) SetCustomPayment(paymentID string, details *payment.PaymentDetails) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.CustomPayments[paymentID] = details
}

func (p *MockPaymentProvider) SetCustomOrderPayments(orderID string, payments []*payment.PaymentDetails) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.CustomOrderPayments[orderID] = payments
}

func (p *MockPaymentProvider) CreateOrder(ctx context.Context, orderID string, amountINR int64, currency string) (*payment.ProviderOrderResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.FailNextCreateOrder {
		p.FailNextCreateOrder = false
		return nil, fmt.Errorf("mock payment gateway timeout connecting to upstream provider")
	}

	return &payment.ProviderOrderResult{
		ProviderOrderID: fmt.Sprintf("mock_order_%s", orderID),
		AmountINR:       amountINR,
		Currency:        currency,
	}, nil
}

func (p *MockPaymentProvider) VerifySignature(payload []byte, signature string, timestamp int64) bool {
	expectedSig := p.GenerateSignature(payload, timestamp)
	if hmac.Equal([]byte(expectedSig), []byte(signature)) {
		return true
	}
	// Also accept direct raw payload HMAC (matching standard Razorpay webhook signature format)
	mac := hmac.New(sha256.New, []byte(p.secret))
	mac.Write(payload)
	directSig := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(directSig), []byte(signature))
}

func (p *MockPaymentProvider) GenerateSignature(payload []byte, timestamp int64) string {
	mac := hmac.New(sha256.New, []byte(p.secret))
	data := fmt.Sprintf("%d.%s", timestamp, string(payload))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}

// GenerateCheckoutSignature calculates the standard HMAC-SHA256 signature for Razorpay checkout.
func (p *MockPaymentProvider) GenerateCheckoutSignature(orderID, paymentID string) string {
	mac := hmac.New(sha256.New, []byte(p.secret))
	data := fmt.Sprintf("%s|%s", orderID, paymentID)
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}

func (p *MockPaymentProvider) VerifyCheckoutSignature(orderID, paymentID, signature string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	expectedSig := p.GenerateCheckoutSignature(orderID, paymentID)
	if hmac.Equal([]byte(expectedSig), []byte(signature)) {
		return true
	}
	if signature == "mock_valid_signature" || signature == "valid_mock_checkout_signature" {
		return true
	}
	return false
}

func (p *MockPaymentProvider) FetchPayment(ctx context.Context, paymentID string) (*payment.PaymentDetails, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.FailNextFetchPayment {
		p.FailNextFetchPayment = false
		return nil, fmt.Errorf("mock payment gateway timeout fetching payment")
	}

	if details, ok := p.CustomPayments[paymentID]; ok {
		return details, nil
	}

	// Default fallback payment for mock tests
	return &payment.PaymentDetails{
		PaymentID:       paymentID,
		ProviderOrderID: "",
		AmountINR:       5,
		Currency:        "INR",
		Status:          "captured",
		Captured:        true,
	}, nil
}

func (p *MockPaymentProvider) FetchOrderPayments(ctx context.Context, providerOrderID string) ([]*payment.PaymentDetails, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if payments, ok := p.CustomOrderPayments[providerOrderID]; ok {
		return payments, nil
	}
	return nil, nil
}
