package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"go.uber.org/zap"

	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/service"
)

// AdminHandler handles administrative REST endpoints for user, wallet, and market mutations.
type AdminHandler struct {
	adminSvc *service.AdminService
	log      *zap.Logger
}

// NewAdminHandler constructs the admin handler.
func NewAdminHandler(adminSvc *service.AdminService, log *zap.Logger) *AdminHandler {
	return &AdminHandler{adminSvc: adminSvc, log: log}
}

// HandleSuspendUser handles POST /api/v1/admin/users/{user_id}/suspend
func (h *AdminHandler) HandleSuspendUser(w http.ResponseWriter, r *http.Request) {
	adminID := AdminIDFromContext(r.Context())
	requestID := RequestIDFromContext(r.Context())
	idempotencyKey := IdempotencyKeyFromContext(r.Context())

	userID := r.PathValue("user_id")
	if err := ValidateUserID(userID); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
		return
	}

	var body SuspendUserRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body"))
		return
	}
	reason, err := ValidateReason(body.Reason)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
		return
	}

	op, err := h.adminSvc.SuspendUser(r.Context(), service.SuspendUserRequest{
		AdminID:        adminID,
		IdempotencyKey: idempotencyKey,
		RequestID:      requestID,
		TargetUserID:   userID,
		Reason:         reason,
		IPAddress:      r.RemoteAddr,
		UserAgent:      r.UserAgent(),
	})
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, ToOperationDTO(op))
}

// HandleUnsuspendUser handles POST /api/v1/admin/users/{user_id}/unsuspend
func (h *AdminHandler) HandleUnsuspendUser(w http.ResponseWriter, r *http.Request) {
	adminID := AdminIDFromContext(r.Context())
	requestID := RequestIDFromContext(r.Context())
	idempotencyKey := IdempotencyKeyFromContext(r.Context())

	userID := r.PathValue("user_id")
	if err := ValidateUserID(userID); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
		return
	}

	var body UnsuspendUserRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body"))
		return
	}
	reason, err := ValidateReason(body.Reason)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
		return
	}

	op, err := h.adminSvc.UnsuspendUser(r.Context(), service.UnsuspendUserRequest{
		AdminID:        adminID,
		IdempotencyKey: idempotencyKey,
		RequestID:      requestID,
		TargetUserID:   userID,
		Reason:         reason,
		IPAddress:      r.RemoteAddr,
		UserAgent:      r.UserAgent(),
	})
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, ToOperationDTO(op))
}

// HandleFreezeWallet handles POST /api/v1/admin/users/{user_id}/wallets/{asset}/freeze
func (h *AdminHandler) HandleFreezeWallet(w http.ResponseWriter, r *http.Request) {
	adminID := AdminIDFromContext(r.Context())
	requestID := RequestIDFromContext(r.Context())
	idempotencyKey := IdempotencyKeyFromContext(r.Context())

	userID := r.PathValue("user_id")
	if err := ValidateUserID(userID); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
		return
	}
	asset := r.PathValue("asset")
	if err := ValidateAsset(asset); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
		return
	}

	var body FreezeWalletRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body"))
		return
	}
	reason, err := ValidateReason(body.Reason)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
		return
	}

	op, err := h.adminSvc.FreezeWallet(r.Context(), service.FreezeWalletRequest{
		AdminID:        adminID,
		IdempotencyKey: idempotencyKey,
		RequestID:      requestID,
		TargetUserID:   userID,
		Asset:          asset,
		Reason:         reason,
		IPAddress:      r.RemoteAddr,
		UserAgent:      r.UserAgent(),
	})
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, ToOperationDTO(op))
}

// HandleUnfreezeWallet handles POST /api/v1/admin/users/{user_id}/wallets/{asset}/unfreeze
func (h *AdminHandler) HandleUnfreezeWallet(w http.ResponseWriter, r *http.Request) {
	adminID := AdminIDFromContext(r.Context())
	requestID := RequestIDFromContext(r.Context())
	idempotencyKey := IdempotencyKeyFromContext(r.Context())

	userID := r.PathValue("user_id")
	if err := ValidateUserID(userID); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
		return
	}
	asset := r.PathValue("asset")
	if err := ValidateAsset(asset); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
		return
	}

	var body UnfreezeWalletRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body"))
		return
	}
	reason, err := ValidateReason(body.Reason)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
		return
	}

	op, err := h.adminSvc.UnfreezeWallet(r.Context(), service.UnfreezeWalletRequest{
		AdminID:        adminID,
		IdempotencyKey: idempotencyKey,
		RequestID:      requestID,
		TargetUserID:   userID,
		Asset:          asset,
		Reason:         reason,
		IPAddress:      r.RemoteAddr,
		UserAgent:      r.UserAgent(),
	})
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, ToOperationDTO(op))
}

// HandleHaltMarket handles POST /api/v1/admin/markets/{market_id}/halt
func (h *AdminHandler) HandleHaltMarket(w http.ResponseWriter, r *http.Request) {
	adminID := AdminIDFromContext(r.Context())
	requestID := RequestIDFromContext(r.Context())
	idempotencyKey := IdempotencyKeyFromContext(r.Context())

	marketID := r.PathValue("market_id")
	if err := ValidateMarketID(marketID); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
		return
	}

	var body HaltMarketRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body"))
		return
	}
	reason, err := ValidateReason(body.Reason)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
		return
	}

	op, err := h.adminSvc.HaltMarket(r.Context(), service.HaltMarketRequest{
		AdminID:        adminID,
		IdempotencyKey: idempotencyKey,
		RequestID:      requestID,
		MarketID:       marketID,
		Reason:         reason,
		IPAddress:      r.RemoteAddr,
		UserAgent:      r.UserAgent(),
	})
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, ToOperationDTO(op))
}

// HandleResumeMarket handles POST /api/v1/admin/markets/{market_id}/resume
func (h *AdminHandler) HandleResumeMarket(w http.ResponseWriter, r *http.Request) {
	adminID := AdminIDFromContext(r.Context())
	requestID := RequestIDFromContext(r.Context())
	idempotencyKey := IdempotencyKeyFromContext(r.Context())

	marketID := r.PathValue("market_id")
	if err := ValidateMarketID(marketID); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
		return
	}

	var body ResumeMarketRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body"))
		return
	}
	reason, err := ValidateReason(body.Reason)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
		return
	}

	op, err := h.adminSvc.ResumeMarket(r.Context(), service.ResumeMarketRequest{
		AdminID:        adminID,
		IdempotencyKey: idempotencyKey,
		RequestID:      requestID,
		MarketID:       marketID,
		Reason:         reason,
		IPAddress:      r.RemoteAddr,
		UserAgent:      r.UserAgent(),
	})
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, ToOperationDTO(op))
}

// handleServiceError maps domain errors to appropriate HTTP status codes.
func (h *AdminHandler) handleServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrOperationInProgress):
		writeJSON(w, http.StatusConflict, errorResponse(err.Error()))
	case errors.Is(err, domain.ErrInvalidTarget), errors.Is(err, domain.ErrInvalidReason):
		writeJSON(w, http.StatusBadRequest, errorResponse(err.Error()))
	case errors.Is(err, domain.ErrAuthUnavailable), errors.Is(err, domain.ErrWalletUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, errorResponse("downstream service unavailable"))
	default:
		h.log.Error("admin handler: error executing request", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, errorResponse(err.Error()))
	}
}
