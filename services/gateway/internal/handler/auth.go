package handler

import (
	"net/http"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "tradedrift/platform/api/gen/auth/v1"
	"tradedrift/services/gateway/internal/response"
)


type AuthHandler struct {
	client authv1.AuthServiceClient
}

func NewAuthHandler(client authv1.AuthServiceClient) *AuthHandler {
	return &AuthHandler{client: client}
}


// Register — POST /api/v1/auth/register
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := response.DecodeJSON(r, &req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	ctx, cancel := outgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.Register(ctx, &authv1.RegisterRequest{
		Email:    req.Email,
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusCreated, RegisterResponse{
		UserID:               res.UserId,
		VerificationRequired: res.VerificationRequired,
	})
}

// VerifyEmail — POST /api/v1/auth/verify
func (h *AuthHandler) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req VerifyEmailRequest
	if err := response.DecodeJSON(r, &req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	ctx, cancel := outgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.VerifyEmail(ctx, &authv1.VerifyEmailRequest{
		Email: req.Email,
		Code:  req.Code,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, VerifyEmailResponse{
		User:         userDTO(res.User),
		TokenPairDTO: tokenDTO(res.AccessToken, res.RefreshToken, res.AccessTokenExpiresAt, res.RefreshTokenExpiresAt),
	})
}

// ResendVerification — POST /api/v1/auth/resend
func (h *AuthHandler) ResendVerification(w http.ResponseWriter, r *http.Request) {
	var req ResendVerificationRequest
	if err := response.DecodeJSON(r, &req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	ctx, cancel := outgoingCtx(r, 3*time.Second)
	defer cancel()

	_, err := h.client.ResendVerificationCode(ctx, &authv1.ResendVerificationCodeRequest{
		Email: req.Email,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, SuccessResponse{Success: true})
}

// Login — POST /api/v1/auth/login
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := response.DecodeJSON(r, &req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	ctx, cancel := outgoingCtx(r, 3*time.Second)
	defer cancel()

	res, err := h.client.Login(ctx, &authv1.LoginRequest{
		Identifier: req.Identifier,
		Password:   req.Password,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, LoginResponse{
		User:         userDTO(res.User),
		TokenPairDTO: tokenDTO(res.AccessToken, res.RefreshToken, res.AccessTokenExpiresAt, res.RefreshTokenExpiresAt),
	})
}

// RefreshToken — POST /api/v1/auth/refresh
func (h *AuthHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req RefreshTokenRequest
	if err := response.DecodeJSON(r, &req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	ctx, cancel := outgoingCtx(r, 3*time.Second)
	defer cancel()

	res, err := h.client.RefreshToken(ctx, &authv1.RefreshTokenRequest{
		RefreshToken: req.RefreshToken,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, RefreshTokenResponse{
		TokenPairDTO: tokenDTO(res.AccessToken, res.RefreshToken, res.AccessTokenExpiresAt, res.RefreshTokenExpiresAt),
	})
}

// ForgotPassword — POST /api/v1/auth/forgot-password
func (h *AuthHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req ForgotPasswordRequest
	if err := response.DecodeJSON(r, &req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	ctx, cancel := outgoingCtx(r, 3*time.Second)
	defer cancel()

	_, err := h.client.ForgotPassword(ctx, &authv1.ForgotPasswordRequest{Email: req.Email})
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	// Always success — never reveal if email exists (prevents user enumeration)
	response.WriteJSON(w, http.StatusOK, SuccessResponse{Success: true})
}

// ResetPassword — POST /api/v1/auth/reset-password
func (h *AuthHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req ResetPasswordRequest
	if err := response.DecodeJSON(r, &req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	ctx, cancel := outgoingCtx(r, 5*time.Second)
	defer cancel()

	_, err := h.client.ResetPassword(ctx, &authv1.ResetPasswordRequest{
		Email:       req.Email,
		Code:        req.Code,
		NewPassword: req.NewPassword,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, SuccessResponse{Success: true})
}

// Logout — POST /api/v1/auth/logout (protected)
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var req LogoutRequest
	if err := response.DecodeJSON(r, &req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	ctx, cancel := outgoingCtx(r, 3*time.Second)
	defer cancel()

	_, err := h.client.Logout(ctx, &authv1.LogoutRequest{RefreshToken: req.RefreshToken})
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, SuccessResponse{Success: true})
}

// LogoutAll — POST /api/v1/auth/logout-all (protected)
func (h *AuthHandler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := outgoingCtx(r, 3*time.Second)
	defer cancel()

	_, err := h.client.LogoutAll(ctx, &authv1.LogoutAllRequest{})
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, SuccessResponse{Success: true})
}

// ChangePassword — POST /api/v1/auth/change-password (protected)
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var req ChangePasswordRequest
	if err := response.DecodeJSON(r, &req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	ctx, cancel := outgoingCtx(r, 5*time.Second)
	defer cancel()

	_, err := h.client.ChangePassword(ctx, &authv1.ChangePasswordRequest{
		OldPassword: req.OldPassword,
		NewPassword: req.NewPassword,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, SuccessResponse{Success: true})
}

// writeGRPCError maps gRPC status codes to HTTP status + error code JSON.
func writeGRPCError(w http.ResponseWriter, err error) {
	st, _ := status.FromError(err)
	switch st.Code() {
	case codes.Unauthenticated:
		response.WriteError(w, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS", st.Message())
	case codes.FailedPrecondition:
		response.WriteError(w, http.StatusForbidden, "AUTH_ACCOUNT_LOCKED", st.Message())
	case codes.AlreadyExists:
		response.WriteError(w, http.StatusConflict, "ALREADY_EXISTS", st.Message())
	case codes.NotFound:
		response.WriteError(w, http.StatusNotFound, "NOT_FOUND", st.Message())
	case codes.InvalidArgument:
		response.WriteError(w, http.StatusBadRequest, "INVALID_ARGUMENT", st.Message())
	case codes.PermissionDenied:
		response.WriteError(w, http.StatusForbidden, "PERMISSION_DENIED", st.Message())
	case codes.ResourceExhausted:
		response.WriteError(w, http.StatusTooManyRequests, "API_RATE_LIMIT_EXCEEDED", st.Message())
	case codes.DeadlineExceeded:
		response.WriteError(w, http.StatusGatewayTimeout, "TIMEOUT", st.Message())
	case codes.Unavailable:
		response.WriteError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service is temporarily unavailable")
	default:
		response.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an internal error occurred")
	}
}
