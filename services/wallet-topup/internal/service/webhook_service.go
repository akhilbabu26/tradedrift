package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/repository"
	"tradedrift/services/wallet-topup/internal/webhook"
)

// PaymentWebhookPayload models the normalized incoming gateway webhook payload.
type PaymentWebhookPayload struct {
	EventID         string `json:"event_id"`
	EventType       string `json:"event_type"` // strictly "payment.captured"
	PaymentID       string `json:"payment_id"`
	ProviderOrderID string `json:"provider_order_id"`
	OrderID         string `json:"order_id"` // internal topup ID fallback
	INRAmount       int64  `json:"inr_amount"`
	Currency        string `json:"currency"`
	PaidAt          int64  `json:"paid_at"` // provider payment capture epoch seconds
	Timestamp       int64  `json:"timestamp"`
}

type WebhookService struct {
	verifier      *webhook.Verifier
	webhookRepo   repository.WebhookEventRepository
	orderRepo     repository.TopUpOrderRepository
	txManager     repository.TransactionManager
	dailyLimitINR int64
	log           *zap.Logger
	locIST        *time.Location
}

func NewWebhookService(
	verifier *webhook.Verifier,
	webhookRepo repository.WebhookEventRepository,
	orderRepo repository.TopUpOrderRepository,
	txManager repository.TransactionManager,
	dailyLimitINR int64,
	log *zap.Logger,
) *WebhookService {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.FixedZone("IST", 5*3600+30*60)
	}
	if dailyLimitINR <= 0 {
		dailyLimitINR = 10
	}
	return &WebhookService{
		verifier:      verifier,
		webhookRepo:   webhookRepo,
		orderRepo:     orderRepo,
		txManager:     txManager,
		dailyLimitINR: dailyLimitINR,
		log:           log,
		locIST:        loc,
	}
}

// ProcessWebhook handles the incoming webhook with cryptographic verification, replay check,
// verified deduplication, strict payload & event validation, and single-transaction atomic confirmation.
func (s *WebhookService) ProcessWebhook(ctx context.Context, provider string, rawPayload []byte, signature string, timestamp int64) error {
	webhookEventID, err := platformuuid.New()
	if err != nil {
		return fmt.Errorf("failed to generate webhook event ID: %w", err)
	}

	// ── 1.Verify HMAC SHA256 signature/Cryptographic Verification & Replay Protection ─────────────────────
	if err := s.verifier.Verify(provider, rawPayload, signature, timestamp); err != nil {
		s.log.Warn("ProcessWebhook: signature verification or replay check failed",
			zap.String("provider", provider),
			zap.Error(err),
		)
		errMsg := err.Error()
		_ = s.webhookRepo.RecordFailedSignatureEvent(ctx, &domain.WebhookEvent{
			ID:             webhookEventID,
			Provider:       provider,
			EventID:        fmt.Sprintf("failed_sig_%s", webhookEventID),
			SignatureValid: false,
			Payload:        json.RawMessage(rawPayload),
			Status:         domain.WebhookStatusFailed,
			ErrorMessage:   &errMsg,
			ReceivedAt:     time.Now().UTC(),
		})
		return err
	}

	// ── 2. Parse Normalized Payload ───────────────────────────────────────────
	var payload PaymentWebhookPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		errMsg := fmt.Sprintf("malformed webhook payload: %v", err)
		s.recordFailedValidationEvent(ctx, webhookEventID, provider, fmt.Sprintf("invalid_json_%s", webhookEventID), nil, rawPayload, errors.New(errMsg))
		return fmt.Errorf("malformed webhook payload: %w", err)
	}

	var payIDPtr *string
	if strings.TrimSpace(payload.PaymentID) != "" {
		payIDPtr = &payload.PaymentID
	}
	evIDStr := payload.EventID
	if strings.TrimSpace(evIDStr) == "" {
		evIDStr = fmt.Sprintf("missing_evid_%s", webhookEventID)
	}

	// ── 3. Validate Mandatory Fields ──────────────────────────────────────────
	if strings.TrimSpace(payload.EventID) == "" {
		err := fmt.Errorf("missing mandatory event_id in webhook payload")
		s.recordFailedValidationEvent(ctx, webhookEventID, provider, evIDStr, payIDPtr, rawPayload, err)
		return err
	}
	if strings.TrimSpace(payload.PaymentID) == "" {
		err := fmt.Errorf("missing mandatory payment_id in webhook payload")
		s.recordFailedValidationEvent(ctx, webhookEventID, provider, evIDStr, payIDPtr, rawPayload, err)
		return err
	}
	if strings.TrimSpace(payload.ProviderOrderID) == "" && strings.TrimSpace(payload.OrderID) == "" {
		err := fmt.Errorf("missing mandatory order reference in webhook payload")
		s.recordFailedValidationEvent(ctx, webhookEventID, provider, evIDStr, payIDPtr, rawPayload, err)
		return err
	}

	// ── 4. Validate Header Timestamp vs Payload Timestamp Coherence ───────────
	if payload.Timestamp > 0 {
		if math.Abs(float64(timestamp-payload.Timestamp)) > 5 {
			s.log.Warn("ProcessWebhook: header timestamp and payload timestamp skew exceeded",
				zap.Int64("headerTimestamp", timestamp),
				zap.Int64("payloadTimestamp", payload.Timestamp),
			)
			s.recordFailedValidationEvent(ctx, webhookEventID, provider, evIDStr, payIDPtr, rawPayload, domain.ErrWebhookReplay)
			return domain.ErrWebhookReplay
		}
	}

	// ── 5. Validate Event Type (Only 'payment.captured' triggers credit) ──────
	if payload.EventType != "payment.captured" {
		s.log.Warn("ProcessWebhook: non-capturing event received, rejecting credit transition",
			zap.String("eventType", payload.EventType),
			zap.String("orderID", payload.OrderID),
			zap.String("providerOrderID", payload.ProviderOrderID),
		)
		s.recordFailedValidationEvent(ctx, webhookEventID, provider, evIDStr, payIDPtr, rawPayload, domain.ErrInvalidEventType)
		return domain.ErrInvalidEventType
	}

	// ── 6. Validate Mandatory Payment Capture Timestamp ───────────────────────
	if payload.PaidAt <= 0 {
		err := fmt.Errorf("missing mandatory paid_at timestamp for payment.captured")
		s.recordFailedValidationEvent(ctx, webhookEventID, provider, evIDStr, payIDPtr, rawPayload, err)
		return err
	}

	// ── 7. Payload Currency & Strict Order Identity Validation ────────────────
	if payload.Currency != "INR" {
		err := fmt.Errorf("invalid currency %q, expected INR", payload.Currency)
		s.recordFailedValidationEvent(ctx, webhookEventID, provider, evIDStr, payIDPtr, rawPayload, err)
		return err
	}

	var order *domain.TopUpOrder
	if strings.TrimSpace(payload.ProviderOrderID) != "" {
		order, err = s.orderRepo.GetByProviderOrderID(ctx, provider, payload.ProviderOrderID)
	} else {
		order, err = s.orderRepo.GetByID(ctx, payload.OrderID)
	}
	if err != nil || order == nil {
		lookupErr := fmt.Errorf("order not found for provider %s (provider_order_id=%q, order_id=%q)", provider, payload.ProviderOrderID, payload.OrderID)
		s.recordFailedValidationEvent(ctx, webhookEventID, provider, evIDStr, payIDPtr, rawPayload, lookupErr)
		return lookupErr
	}

	// Validate provider matches order (case-insensitive)
	if !strings.EqualFold(order.Provider, provider) {
		s.log.Warn("ProcessWebhook: provider mismatch",
			zap.String("expectedProvider", order.Provider),
			zap.String("receivedProvider", provider),
		)
		s.recordFailedValidationEvent(ctx, webhookEventID, provider, evIDStr, payIDPtr, rawPayload, domain.ErrProviderMismatch)
		return domain.ErrProviderMismatch
	}

	// Validate provider order ID if both are present
	if payload.ProviderOrderID != "" && order.ProviderOrderID != nil && *order.ProviderOrderID != payload.ProviderOrderID {
		misErr := fmt.Errorf("provider order ID mismatch: expected %q, got %q", *order.ProviderOrderID, payload.ProviderOrderID)
		s.recordFailedValidationEvent(ctx, webhookEventID, provider, evIDStr, payIDPtr, rawPayload, misErr)
		return misErr
	}

	// Validate order amount
	if order.INRAmount != payload.INRAmount {
		amtErr := fmt.Errorf("amount mismatch: expected %d, got %d", order.INRAmount, payload.INRAmount)
		s.recordFailedValidationEvent(ctx, webhookEventID, provider, evIDStr, payIDPtr, rawPayload, amtErr)
		return amtErr
	}

	// ── 8. Unified Atomic Database Transaction (Attributed by Capture Time) ──
	captureTimeIST := time.Unix(payload.PaidAt, 0).In(s.locIST)
	paidDate := captureTimeIST.Format("2006-01-02")

	result, err := s.txManager.ProcessPaymentConfirmationTx(
		ctx,
		order.ID,
		provider,
		payload.PaymentID,
		paidDate,
		rawPayload,
		payload.EventID,
		s.dailyLimitINR,
	)
	if err != nil {
		s.log.Error("ProcessWebhook: atomic confirmation transaction failed",
			zap.String("orderID", order.ID),
			zap.String("eventID", payload.EventID),
			zap.Error(err),
		)
		return err
	}

	if result.AlreadyProcessed {
		s.log.Info("ProcessWebhook: event already processed, returning fast 200 OK",
			zap.String("provider", provider),
			zap.String("eventID", payload.EventID),
		)
		return nil
	}

	s.log.Info("ProcessWebhook: payment successfully confirmed atomically",
		zap.String("orderID", order.ID),
		zap.String("paymentID", payload.PaymentID),
		zap.String("provider", provider),
	)

	return nil
}

func (s *WebhookService) recordFailedValidationEvent(
	ctx context.Context,
	id, provider, eventID string,
	paymentID *string,
	rawPayload []byte,
	valErr error,
) {
	errMsg := valErr.Error()
	now := time.Now().UTC()
	ev := &domain.WebhookEvent{
		ID:             id,
		Provider:       provider,
		EventID:        eventID,
		PaymentID:      paymentID,
		SignatureValid: true,
		Status:         domain.WebhookStatusFailed,
		Payload:        json.RawMessage(rawPayload),
		ErrorMessage:   &errMsg,
		ReceivedAt:     now,
		ProcessedAt:    &now,
	}
	if err := s.webhookRepo.RecordEvent(ctx, ev); err != nil {
		s.log.Warn("recordFailedValidationEvent: failed to record event audit",
			zap.String("eventID", eventID),
			zap.Error(err),
		)
	}
}

