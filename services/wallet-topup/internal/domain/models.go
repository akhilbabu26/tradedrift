package domain

import (
	"encoding/json"
	"time"
)

// Order Status Constants
const (
	StatusInitiated        = "INITIATED"
	StatusPaymentPending   = "PAYMENT_PENDING"
	StatusCreditPending    = "CREDIT_PENDING"
	StatusCreditProcessing = "CREDIT_PROCESSING"
	StatusCompleted        = "COMPLETED"
	StatusFailed           = "FAILED"
	StatusExpired          = "EXPIRED"
	StatusRefundRequired   = "REFUND_REQUIRED"
)

// Webhook Status Constants
const (
	WebhookStatusReceived  = "RECEIVED"
	WebhookStatusProcessed = "PROCESSED"
	WebhookStatusIgnored   = "IGNORED"
	WebhookStatusFailed    = "FAILED"
)

// DailyTopUpLimit represents the per-user daily INR top-up limit tracker.
type DailyTopUpLimit struct {
	UserID      string    `json:"user_id"`
	UsageDate   string    `json:"usage_date"` // YYYY-MM-DD
	LimitINR    int64     `json:"limit_inr"`
	ReservedINR int64     `json:"reserved_inr"`
	ConsumedINR int64     `json:"consumed_inr"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// RemainingINR returns the remaining INR available for top-up today.
func (d *DailyTopUpLimit) RemainingINR() int64 {
	remaining := d.LimitINR - (d.ReservedINR + d.ConsumedINR)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// DailyUsageResponse represents the response payload for daily usage inquiry.
type DailyUsageResponse struct {
	UserID       string `json:"user_id"`
	UsageDate    string `json:"usage_date"`
	LimitINR     int64  `json:"limit_inr"`
	ReservedINR  int64  `json:"reserved_inr"`
	ConsumedINR  int64  `json:"consumed_inr"`
	RemainingINR int64  `json:"remaining_inr"`
	ResetsAt     string `json:"resets_at"`
}

// TopUpOrder represents a single fiat-to-simulated-USDT top-up transaction.
type TopUpOrder struct {
	ID              string     `json:"id"`
	UserID          string     `json:"user_id"`
	IdempotencyKey  string     `json:"idempotency_key"`
	INRAmount       int64      `json:"inr_amount"`
	USDTAmount      string     `json:"usdt_amount"`
	ReservationDate string     `json:"reservation_date"` // YYYY-MM-DD
	Provider        string     `json:"provider"`
	ProviderOrderID *string    `json:"provider_order_id,omitempty"`
	PaymentID       *string    `json:"payment_id,omitempty"`
	Status          string     `json:"status"`
	ClaimToken      *string    `json:"claim_token,omitempty"`
	ClaimUntil      *time.Time `json:"claim_until,omitempty"`
	ClaimedAt       *time.Time `json:"claimed_at,omitempty"`
	AttemptCount    int        `json:"attempt_count"`
	LastError       *string    `json:"last_error,omitempty"`
	ExpiresAt       time.Time  `json:"expires_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// WebhookEvent represents an incoming payment gateway webhook notification.
type WebhookEvent struct {
	ID             string          `json:"id"`
	Provider       string          `json:"provider"`
	EventID        string          `json:"event_id"`
	PaymentID      *string         `json:"payment_id,omitempty"`
	SignatureValid bool            `json:"signature_valid"`
	Payload        json.RawMessage `json:"payload"`
	Status         string          `json:"status"`
	ErrorMessage   *string         `json:"error_message,omitempty"`
	ReceivedAt     time.Time       `json:"received_at"`
	ProcessedAt    *time.Time      `json:"processed_at,omitempty"`
}
