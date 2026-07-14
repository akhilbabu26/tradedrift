package service

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	platformerrors "tradedrift/platform/errors"
)

// Login verifies password and user status, then returns a signed session.
func (s *Service) Login(ctx context.Context, identifier, password string) (*UserDTO, *TokenPairDTO, error) {
	u, err := s.userRepo.GetByIdentifier(ctx, identifier)
	if err != nil {
		return nil, nil, err
	}

	// Identical invalid credential response for user non-existence (anti-enumeration)
	if u == nil {
		return nil, nil, platformerrors.New(platformerrors.CodeInvalidCredentials, "invalid email/username or password")
	}

	// Check if account is locked due to brute force protection
	if u.LockedUntil != nil && u.LockedUntil.After(time.Now().UTC()) {
		return nil, nil, platformerrors.New(platformerrors.CodeFailedPrecondition, fmt.Sprintf("account is temporarily locked. Please try again after %s", u.LockedUntil.Format(time.RFC3339)))
	}

	// Verify Password Hash
	err = bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password))
	if err != nil {
		// Update failed login counter
		failedCount := u.FailedLoginAttempts + 1
		var lockUntil *time.Time
		if failedCount >= 5 {
			lockTime := time.Now().UTC().Add(15 * time.Minute)
			lockUntil = &lockTime
		}
		_ = s.userRepo.TrackFailedLogin(ctx, u.ID, failedCount, lockUntil)

		return nil, nil, platformerrors.New(platformerrors.CodeInvalidCredentials, "invalid email/username or password")
	}

	// Verify user is activated
	if u.Status == "PENDING_VERIFICATION" {
		return nil, nil, platformerrors.New(platformerrors.CodeAccountNotActive, "account is not verified. Please verify your email first.")
	}
	if u.Status == "SUSPENDED" || u.Status == "BANNED" {
		return nil, nil, platformerrors.New(platformerrors.CodePermissionDenied, fmt.Sprintf("your account has been %s", u.Status))
	}

	// Reset failed attempts and update last_login_at
	_ = s.userRepo.ResetFailedLogin(ctx, u.ID, time.Now().UTC())

	// Issue token pair
	tp, err := s.issueTokenPair(ctx, u.ID, u.Email, u.TokenVersion)
	if err != nil {
		return nil, nil, err
	}

	return &UserDTO{ID: u.ID, Email: u.Email, Username: u.Username}, tp, nil
}
