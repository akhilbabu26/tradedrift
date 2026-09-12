package webhook

import (
	"math"
	"strings"
	"time"

	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/payment"
)

type Verifier struct {
	providers map[string]payment.PaymentProvider
}

func NewVerifier(providers ...payment.PaymentProvider) *Verifier {
	pMap := make(map[string]payment.PaymentProvider)
	for _, p := range providers {
		pMap[p.ProviderName()] = p
	}
	return &Verifier{providers: pMap}
}

// Verify checks HMAC signature and replay window (|T_now - T_event| <= 300s).
func (v *Verifier) Verify(providerName string, rawPayload []byte, signature string, timestamp int64) error {
	p, ok := v.providers[strings.ToUpper(providerName)]
	if !ok {
		return domain.ErrInvalidSignature
	}

	// 1. Check replay window (300 seconds)
	now := time.Now().Unix()
	if math.Abs(float64(now-timestamp)) > 300 {
		return domain.ErrWebhookReplay
	}

	// 2. Cryptographic signature check
	if !p.VerifySignature(rawPayload, signature, timestamp) {
		return domain.ErrInvalidSignature
	}

	return nil
}
