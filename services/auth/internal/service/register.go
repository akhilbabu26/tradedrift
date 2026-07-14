package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	platformerrors "tradedrift/platform/errors"
	"tradedrift/services/auth/internal/repository"
)

// Register creates a new user account with PENDING_VERIFICATION status and sends an OTP.
func (s *Service) Register(ctx context.Context, email, username, password string) (string, error) {
	// 1. Validation
	if !emailRegex.MatchString(email) {
		return "", platformerrors.New(platformerrors.CodeInvalidArgument, "invalid email format")
	}
	if len(password) < 8 {
		return "", platformerrors.New(platformerrors.CodeInvalidArgument, "password must be at least 8 characters long")
	}
	if len(username) < 3 || len(username) > 30 {
		return "", platformerrors.New(platformerrors.CodeInvalidArgument, "username must be between 3 and 30 characters")
	}

	// 2. Hash password
	hashBytes, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}

	// 3. Create user record in PENDING_VERIFICATION state
	userID := uuid.NewString()
	user := &repository.User{
		ID:           userID,
		Email:        email,
		Username:     username,
		PasswordHash: string(hashBytes),
		TokenVersion: 1,
		Status:       "PENDING_VERIFICATION",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}

	err = s.userRepo.Create(ctx, user)
	if err != nil {
		return "", platformerrors.Wrap(err, platformerrors.CodeAlreadyExists, "email or username already exists")
	}

	// 4. Generate & store OTP
	code, err := s.otpMgr.Generate(ctx, "register:"+email)
	if err != nil {
		return "", fmt.Errorf("failed to generate verification OTP: %w", err)
	}

	// 5. Send verification email (log mock)
	_ = s.mailer.SendVerificationCode(ctx, email, code)

	return userID, nil
}
