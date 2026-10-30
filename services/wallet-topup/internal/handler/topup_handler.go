package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"go.uber.org/zap"

	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/service"
)

type TopUpHandler struct {
	topupService *service.TopUpService
	log          *zap.Logger
}

func NewTopUpHandler(topupService *service.TopUpService, log *zap.Logger) *TopUpHandler {
	return &TopUpHandler{
		topupService: topupService,
		log:          log,
	}
}

// HandleCreateTopUp handles POST /api/v1/topups
func (h *TopUpHandler) HandleCreateTopUp(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	idempotencyKey := r.Header.Get("X-Idempotency-Key")
	if idempotencyKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "X-Idempotency-Key header is required"})
		return
	}

	var req struct {
		INRAmount  int64 `json:"inrAmount"`
		INRAmount2 int64 `json:"inr_amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}
	amt := req.INRAmount
	if amt == 0 {
		amt = req.INRAmount2
	}

	order, err := h.topupService.CreateTopUp(r.Context(), userID, idempotencyKey, amt)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidAmount), errors.Is(err, domain.ErrInvalidIdempotencyKey):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, domain.ErrIdempotencyConflict):
			writeJSON(w, http.StatusConflict, map[string]string{
				"code":  "IDEMPOTENCY_KEY_REUSED",
				"error": "Idempotency key has already been used with a different INR amount",
			})
		case errors.Is(err, domain.ErrDailyLimitExceeded):
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"code":  "DAILY_LIMIT_EXCEEDED",
				"error": "Daily top-up limit exceeded",
			})
		default:
			h.log.Error("HandleCreateTopUp: unexpected internal error", zap.Error(err))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create top-up order"})
		}
		return
	}

	writeJSON(w, http.StatusCreated, ToTopUpDTO(order))
}

// HandleVerifyCheckout handles POST /api/v1/topups/verify
func (h *TopUpHandler) HandleVerifyCheckout(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var reqDTO VerifyCheckoutRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&reqDTO); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	topupID, orderID, paymentID, signature := reqDTO.Normalized()

	order, err := h.topupService.VerifyCheckoutPayment(r.Context(), userID, service.VerifyCheckoutRequest{
		TopUpID:           topupID,
		RazorpayOrderID:   orderID,
		RazorpayPaymentID: paymentID,
		RazorpaySignature: signature,
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrUnauthorized):
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		case errors.Is(err, domain.ErrOrderNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "top-up order not found"})
		case errors.Is(err, domain.ErrInvalidSignature):
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "INVALID_SIGNATURE", "error": "invalid checkout signature"})
		case errors.Is(err, domain.ErrMismatchedProviderOrder):
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "MISMATCHED_ORDER_ID", "error": err.Error()})
		case errors.Is(err, domain.ErrPaymentNotCaptured),
			errors.Is(err, domain.ErrPaymentAmountMismatch),
			errors.Is(err, domain.ErrPaymentCurrencyMismatch):
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "PAYMENT_VERIFICATION_FAILED", "error": err.Error()})
		case errors.Is(err, domain.ErrOrderTerminalStatus):
			writeJSON(w, http.StatusConflict, map[string]string{"code": "ORDER_TERMINAL_STATUS", "error": err.Error()})
		default:
			h.log.Error("HandleVerifyCheckout: verification failed", zap.Error(err), zap.String("topupID", topupID))
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return
	}

	writeJSON(w, http.StatusOK, ToTopUpDTO(order))
}

// HandleGetDailyUsage handles GET /api/v1/topups/daily-usage
func (h *TopUpHandler) HandleGetDailyUsage(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	usage, err := h.topupService.GetDailyUsage(r.Context(), userID)
	if err != nil {
		h.log.Error("HandleGetDailyUsage error", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve daily usage"})
		return
	}

	writeJSON(w, http.StatusOK, ToDailyUsageDTO(usage))
}

// HandleGetTopUpByID handles GET /api/v1/topups/{id}
func (h *TopUpHandler) HandleGetTopUpByID(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	orderID := r.PathValue("id")
	if orderID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "order id is required"})
		return
	}

	order, err := h.topupService.GetTopUpByID(r.Context(), userID, orderID)
	if err != nil {
		if errors.Is(err, domain.ErrOrderNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "top-up order not found"})
			return
		}
		h.log.Error("HandleGetTopUpByID error", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve top-up order"})
		return
	}

	writeJSON(w, http.StatusOK, ToTopUpDTO(order))
}

// HandleListTopUps handles GET /api/v1/topups
func (h *TopUpHandler) HandleListTopUps(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	orders, err := h.topupService.ListTopUps(r.Context(), userID, 50)
	if err != nil {
		h.log.Error("HandleListTopUps error", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list top-up orders"})
		return
	}

	dtos := make([]*CreateTopUpResponseDTO, 0, len(orders))
	for _, o := range orders {
		dtos = append(dtos, ToTopUpDTO(o))
	}

	writeJSON(w, http.StatusOK, map[string]any{"topups": dtos})
}


