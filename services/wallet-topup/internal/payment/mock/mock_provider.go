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
	FailNextCreateOrder bool
}

var _ payment.PaymentProvider = (*MockPaymentProvider)(nil)

func NewMockPaymentProvider(secret string) *MockPaymentProvider {
	return &MockPaymentProvider{name: "MOCK", secret: secret}
}

func NewNamedMockPaymentProvider(name, secret string) *MockPaymentProvider {
	return &MockPaymentProvider{name: name, secret: secret}
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
