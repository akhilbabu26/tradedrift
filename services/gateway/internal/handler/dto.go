package handler

import (
	"time"

	authv1 "tradedrift/platform/api/gen/auth/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ─────────────────────────────────────────────
// Shared Response DTOs
// Used across multiple handler domains.
// ─────────────────────────────────────────────

type UserDTO struct {
	UserID   string `json:"userId"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

type TokenPairDTO struct {
	AccessToken           string `json:"accessToken"`
	RefreshToken          string `json:"refreshToken"`
	AccessTokenExpiresAt  string `json:"accessTokenExpiresAt"`
	RefreshTokenExpiresAt string `json:"refreshTokenExpiresAt"`
}

type SuccessResponse struct {
	Success bool `json:"success"`
}

// ─────────────────────────────────────────────
// Shared helper constructors
// ─────────────────────────────────────────────

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

func tokenDTO(access, refresh string, accessExp, refreshExp *timestamppb.Timestamp) TokenPairDTO {
	return TokenPairDTO{
		AccessToken:           access,
		RefreshToken:          refresh,
		AccessTokenExpiresAt:  formatTimestamp(accessExp),
		RefreshTokenExpiresAt: formatTimestamp(refreshExp),
	}
}

// formatTimestamp safely converts a protobuf Timestamp to RFC3339 string.
// Returns empty string if nil — guards against unexpected nil responses from services.
func formatTimestamp(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	return ts.AsTime().Format(time.RFC3339)
}
