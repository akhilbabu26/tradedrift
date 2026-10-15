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

func setSessionCookies(w http.ResponseWriter, accessToken, refreshToken string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    accessToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    refreshToken,
		Path:     "/api/v1/auth/refresh",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "access_token",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Path:     "/api/v1/auth/refresh",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
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

	setSessionCookies(w, res.AccessToken, res.RefreshToken)
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

	setSessionCookies(w, res.AccessToken, res.RefreshToken)
	response.WriteJSON(w, http.StatusOK, loginResponseDTO(res))
}

func (h *Handler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req RefreshTokenRequestDTO
	_ = json.NewDecoder(r.Body).Decode(&req)

	refreshToken := req.RefreshToken
	if refreshToken == "" {
		if c, err := r.Cookie("refresh_token"); err == nil && c.Value != "" {
			refreshToken = c.Value
		}
	}

	if refreshToken == "" {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "missing refresh token in body or cookie")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	res, err := h.client.RefreshToken(ctx, &authv1.RefreshTokenRequest{RefreshToken: refreshToken})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	setSessionCookies(w, res.AccessToken, res.RefreshToken)
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

	refreshToken := req.RefreshToken
	if refreshToken == "" {
		if c, err := r.Cookie("refresh_token"); err == nil && c.Value != "" {
			refreshToken = c.Value
		}
	}

	ctx, cancel := common.OutgoingCtx(r, 5*time.Second)
	defer cancel()

	_, err := h.client.Logout(ctx, &authv1.LogoutRequest{RefreshToken: refreshToken})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	clearSessionCookies(w)
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

	clearSessionCookies(w)
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

func (h *Handler) GoogleLogin(w http.ResponseWriter, r *http.Request) {
	var req GoogleLoginRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IDToken == "" {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "missing or malformed idToken")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 8*time.Second)
	defer cancel()

	res, err := h.client.GoogleLogin(ctx, &authv1.GoogleLoginRequest{IdToken: req.IDToken})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	if res.Status == authv1.GoogleLoginStatus_GOOGLE_LOGIN_STATUS_LINK_REQUIRED {
		response.WriteJSON(w, http.StatusOK, GoogleLoginResponseDTO{
			Status:    "LINK_REQUIRED",
			LinkToken: res.LinkToken,
			Email:     res.Email,
			Message:   res.Message,
		})
		return
	}

	setSessionCookies(w, res.AccessToken, res.RefreshToken)
	u := userDTO(res.User)
	response.WriteJSON(w, http.StatusOK, GoogleLoginResponseDTO{
		Status:                "SUCCESS",
		User:                  &u,
		AccessToken:           res.AccessToken,
		RefreshToken:          res.RefreshToken,
		AccessTokenExpiresAt:  common.FormatTimestamp(res.AccessTokenExpiresAt),
		RefreshTokenExpiresAt: common.FormatTimestamp(res.RefreshTokenExpiresAt),
	})
}

func (h *Handler) LinkGoogleAccount(w http.ResponseWriter, r *http.Request) {
	var req LinkGoogleRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" || req.Password == "" || req.LinkToken == "" {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "email, password, and linkToken are required")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 8*time.Second)
	defer cancel()

	res, err := h.client.LinkGoogleAccount(ctx, &authv1.LinkGoogleAccountRequest{
		Email:     req.Email,
		Password:  req.Password,
		LinkToken: req.LinkToken,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	setSessionCookies(w, res.AccessToken, res.RefreshToken)
	u := userDTO(res.User)
	response.WriteJSON(w, http.StatusOK, AuthResponseDTO{
		User:                  u,
		AccessToken:           res.AccessToken,
		RefreshToken:          res.RefreshToken,
		AccessTokenExpiresAt:  common.FormatTimestamp(res.AccessTokenExpiresAt),
		RefreshTokenExpiresAt: common.FormatTimestamp(res.RefreshTokenExpiresAt),
	})
}

func (h *Handler) ConnectGoogleAccount(w http.ResponseWriter, r *http.Request) {
	var req ConnectGoogleRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IDToken == "" {
		response.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "missing or malformed idToken")
		return
	}

	ctx, cancel := common.OutgoingCtx(r, 8*time.Second)
	defer cancel()

	res, err := h.client.ConnectGoogleAccount(ctx, &authv1.ConnectGoogleAccountRequest{
		IdToken: req.IDToken,
	})
	if err != nil {
		common.WriteGRPCError(w, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, ConnectGoogleResponseDTO{
		Success:  res.Success,
		Provider: res.Provider,
		Email:    res.Email,
	})
}
