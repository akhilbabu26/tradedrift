package auth

import (
	"encoding/json"
	"net/http"
	"time"

	authv1 "tradedrift/platform/api/gen/auth/v1"
	"tradedrift/services/gateway/internal/handler/common"
	"tradedrift/services/gateway/internal/response"
)

type Handler struct {
	client authv1.AuthServiceClient
}

func NewHandler(client authv1.AuthServiceClient) *Handler {
	return &Handler{client: client}
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed request payload")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.Register(ctx, &authv1.RegisterRequest{
		Email:    req.Email,
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusCreated, RegisterResponseDTO{
		UserID:               res.UserId,
		VerificationRequired: res.VerificationRequired,
	})
}

func (h *Handler) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req VerifyEmailRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed request payload")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.VerifyEmail(ctx, &authv1.VerifyEmailRequest{
		Email: req.Email,
		Code:  req.Code,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, verifyResponseDTO(res))
}

func (h *Handler) ResendVerification(w http.ResponseWriter, r *http.Request) {
	var req ResendVerificationRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed request payload")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	_, err := h.client.ResendVerificationCode(ctx, &authv1.ResendVerificationCodeRequest{Email: req.Email})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, map[string]string{"message": "verification code resent"})
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed request payload")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.Login(ctx, &authv1.LoginRequest{
		Identifier: req.Identifier,
		Password:   req.Password,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, loginResponseDTO(res))
}

func (h *Handler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req RefreshTokenRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed request payload")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.RefreshToken(ctx, &authv1.RefreshTokenRequest{RefreshToken: req.RefreshToken})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, refreshResponseDTO(res))
}

func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req ForgotPasswordRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed request payload")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	_, err := h.client.ForgotPassword(ctx, &authv1.ForgotPasswordRequest{Email: req.Email})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, map[string]string{"message": "password reset email sent"})
}

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req ResetPasswordRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed request payload")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	_, err := h.client.ResetPassword(ctx, &authv1.ResetPasswordRequest{
		Email:       req.Email,
		Code:        req.Code,
		NewPassword: req.NewPassword,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, map[string]string{"message": "password reset successfully"})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	var req LogoutRequestDTO
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	_, err := h.client.Logout(ctx, &authv1.LogoutRequest{RefreshToken: req.RefreshToken})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, map[string]string{"message": "logged out successfully"})
}

func (h *Handler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	_, err := h.client.LogoutAll(ctx, &authv1.LogoutAllRequest{})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, map[string]string{"message": "logged out from all devices"})
}

func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var req ChangePasswordRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed request payload")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	_, err := h.client.ChangePassword(ctx, &authv1.ChangePasswordRequest{
		OldPassword: req.OldPassword,
		NewPassword: req.NewPassword,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, map[string]string{"message": "password changed successfully"})
}
