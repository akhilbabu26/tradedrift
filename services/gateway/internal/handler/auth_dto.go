package handler

// ─────────────────────────────────────────────
// Auth Request DTOs
// ─────────────────────────────────────────────

type RegisterRequest struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type VerifyEmailRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

type ResendVerificationRequest struct {
	Email string `json:"email"`
}

type LoginRequest struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

type RefreshTokenRequest struct {
	RefreshToken string `json:"refreshToken"`
}

type ForgotPasswordRequest struct {
	Email string `json:"email"`
}

type ResetPasswordRequest struct {
	Email       string `json:"email"`
	Code        string `json:"code"`
	NewPassword string `json:"newPassword"`
}

type LogoutRequest struct {
	RefreshToken string `json:"refreshToken"`
}

type ChangePasswordRequest struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

// ─────────────────────────────────────────────
// Auth Response DTOs
// ─────────────────────────────────────────────

type RegisterResponse struct {
	UserID               string `json:"userId"`
	VerificationRequired bool   `json:"verificationRequired"`
}

type VerifyEmailResponse struct {
	User UserDTO `json:"user"`
	TokenPairDTO
}

type LoginResponse struct {
	User UserDTO `json:"user"`
	TokenPairDTO
}

type RefreshTokenResponse struct {
	TokenPairDTO
}
