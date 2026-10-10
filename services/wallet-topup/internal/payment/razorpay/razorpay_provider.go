package razorpay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"tradedrift/services/wallet-topup/internal/payment"
)

const razorpayOrdersURL = "https://api.razorpay.com/v1/orders"

type RazorpayPaymentProvider struct {
	keyID         string
	keySecret     string
	webhookSecret string
	httpClient    *http.Client
}

var _ payment.PaymentProvider = (*RazorpayPaymentProvider)(nil)

func NewRazorpayPaymentProvider(keyID, keySecret, webhookSecret string) *RazorpayPaymentProvider {
	return &RazorpayPaymentProvider{
		keyID:         keyID,
		keySecret:     keySecret,
		webhookSecret: webhookSecret,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (p *RazorpayPaymentProvider) ProviderName() string {
	return "RAZORPAY"
}

type rzpCreateOrderReq struct {
	Amount   int64             `json:"amount"` // in paise (₹1 = 100 paise)
	Currency string            `json:"currency"`
	Receipt  string            `json:"receipt"`
	Notes    map[string]string `json:"notes,omitempty"`
}

type rzpCreateOrderRes struct {
	ID       string `json:"id"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Status   string `json:"status"`
}

func (p *RazorpayPaymentProvider) CreateOrder(ctx context.Context, orderID string, amountINR int64, currency string) (*payment.ProviderOrderResult, error) {
	if p.keyID == "" || p.keySecret == "" {
		return nil, fmt.Errorf("razorpay key_id or key_secret is not configured")
	}

	reqBody := rzpCreateOrderReq{
		Amount:   amountINR * 100, // INR to paise
		Currency: currency,
		Receipt:  orderID,
		Notes: map[string]string{
			"topup_order_id": orderID,
		},
	}

	rawJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal razorpay order request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, razorpayOrdersURL, bytes.NewReader(rawJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create razorpay http request: %w", err)
	}

	httpReq.SetBasicAuth(p.keyID, p.keySecret)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute razorpay create order call: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read razorpay response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("razorpay order creation failed (status %d): %s", resp.StatusCode, string(respBytes))
	}

	var res rzpCreateOrderRes
	if err := json.Unmarshal(respBytes, &res); err != nil {
		return nil, fmt.Errorf("failed to unmarshal razorpay order response: %w", err)
	}

	if res.ID == "" {
		return nil, fmt.Errorf("razorpay returned empty order id")
	}

	return &payment.ProviderOrderResult{
		ProviderOrderID: res.ID,
		AmountINR:       amountINR,
		Currency:        currency,
	}, nil
}

// VerifySignature verifies the HMAC-SHA256 signature sent by Razorpay webhook (X-Razorpay-Signature).
func (p *RazorpayPaymentProvider) VerifySignature(payload []byte, signature string, timestamp int64) bool {
	secret := p.webhookSecret
	if secret == "" {
		return false
	}

	// 1. Direct raw payload HMAC-SHA256 (Standard Razorpay Webhook specification)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if hmac.Equal([]byte(expectedSig), []byte(signature)) {
		return true
	}

	// 2. Timestamp prefixed format (timestamp.payload) fallback for mock/custom verification
	if timestamp > 0 {
		macTimestamped := hmac.New(sha256.New, []byte(secret))
		data := fmt.Sprintf("%d.%s", timestamp, string(payload))
		macTimestamped.Write([]byte(data))
		expectedSigTimestamped := hex.EncodeToString(macTimestamped.Sum(nil))
		if hmac.Equal([]byte(expectedSigTimestamped), []byte(signature)) {
			return true
		}
	}

	return false
}
