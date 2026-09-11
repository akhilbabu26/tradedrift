package handler

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"go.uber.org/zap"

	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/service"
)

type WebhookHandler struct {
	webhookService *service.WebhookService
	log            *zap.Logger
}

func NewWebhookHandler(webhookService *service.WebhookService, log *zap.Logger) *WebhookHandler {
	return &WebhookHandler{
		webhookService: webhookService,
		log:            log,
	}
}

// HandleProviderWebhook handles POST /api/v1/webhooks/payment/{provider}
func (h *WebhookHandler) HandleProviderWebhook(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	if provider == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider path parameter required"})
		return
	}

	sig := r.Header.Get("X-Webhook-Signature")
	if sig == "" {
		sig = r.Header.Get("X-Razorpay-Signature")
	}
	if sig == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing signature header"})
		return
	}

	var timestamp int64
	tsStr := r.Header.Get("X-Webhook-Timestamp")
	if tsStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing X-Webhook-Timestamp header"})
		return
	}
	val, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil || val <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid X-Webhook-Timestamp header"})
		return
	}
	timestamp = val

	// Enforce 64 KB maximum request body size to prevent memory exhaustion
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body or body exceeds 64KB limit"})
		return
	}

	err = h.webhookService.ProcessWebhook(r.Context(), provider, body, sig, timestamp)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidSignature):
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid signature"})
		case errors.Is(err, domain.ErrWebhookReplay):
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "webhook timestamp outside allowed replay window"})
		case errors.Is(err, domain.ErrInvalidEventType),
			errors.Is(err, domain.ErrProviderMismatch):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			h.log.Warn("HandleProviderWebhook: processing error",
				zap.String("provider", provider),
				zap.Error(err),
			)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return
	}


	// Fast 200 OK
	writeJSON(w, http.StatusOK, map[string]string{"status": "SUCCESS"})
}
