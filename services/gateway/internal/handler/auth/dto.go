package auth

import (
	authv1 "tradedrift/platform/api/gen/auth/v1"
	"tradedrift/services/gateway/internal/handler/common"
)

type RegisterRequestDTO struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type RegisterResponseDTO struct {
	UserID               string `json:"user_id"`
	VerificationRequired bool   `json:"verification_required"`
}

type VerifyEmailRequestDTO struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

type ResendVerificationRequestDTO struct {
	Email string `json:"email"`
}

type LoginRequestDTO struct {
	Identifier string `json:"identifier"` // Email or Username
	Password   string `json:"password"`
}

type RefreshTokenRequestDTO struct {
	RefreshToken string `json:"refreshToken"`
}

type ForgotPasswordRequestDTO struct {
	Email string `json:"email"`
}

type ResetPasswordRequestDTO struct {
	Email       string `json:"email"`
	Code        string `json:"code"`
	NewPassword string `json:"newPassword"`
}

type ChangePasswordRequestDTO struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

type LogoutRequestDTO struct {
	RefreshToken string `json:"refreshToken"`
}

type UserDTO struct {
	UserID   string `json:"userId"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

type AuthResponseDTO struct {
	User                  UserDTO `json:"user"`
	AccessToken           string  `json:"accessToken"`
	RefreshToken          string  `json:"refreshToken"`
	AccessTokenExpiresAt  string  `json:"accessTokenExpiresAt"`
	RefreshTokenExpiresAt string  `json:"refreshTokenExpiresAt"`
}

func userDTO(u *authv1.User) UserDTO {
	if u == nil {
		return UserDTO{}
	}
	return UserDTO{
		UserID:   u.UserId,
		Email:    u.Email,
		Username: u.Username,
	}
}

func loginResponseDTO(res *authv1.LoginResponse) AuthResponseDTO {
	if res == nil {
		return AuthResponseDTO{}
	}
	return AuthResponseDTO{
		User:                  userDTO(res.User),
		AccessToken:           res.AccessToken,
		RefreshToken:          res.RefreshToken,
		AccessTokenExpiresAt:  common.FormatTimestamp(res.AccessTokenExpiresAt),
		RefreshTokenExpiresAt: common.FormatTimestamp(res.RefreshTokenExpiresAt),
	}
}

func verifyResponseDTO(res *authv1.VerifyEmailResponse) AuthResponseDTO {
	if res == nil {
		return AuthResponseDTO{}
	}
	return AuthResponseDTO{
		User:                  userDTO(res.User),
		AccessToken:           res.AccessToken,
		RefreshToken:          res.RefreshToken,
		AccessTokenExpiresAt:  common.FormatTimestamp(res.AccessTokenExpiresAt),
		RefreshTokenExpiresAt: common.FormatTimestamp(res.RefreshTokenExpiresAt),
	}
}

func refreshResponseDTO(res *authv1.RefreshTokenResponse) AuthResponseDTO {
	if res == nil {
		return AuthResponseDTO{}
	}
	return AuthResponseDTO{
		AccessToken:           res.AccessToken,
		RefreshToken:          res.RefreshToken,
		AccessTokenExpiresAt:  common.FormatTimestamp(res.AccessTokenExpiresAt),
		RefreshTokenExpiresAt: common.FormatTimestamp(res.RefreshTokenExpiresAt),
	}
}
