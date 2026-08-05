package service

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	platformerrors "tradedrift/platform/errors"
)

// Login verifies password and user status, then returns a signed session.
func (s *Service) Login(ctx context.Context, identifier, password string) (*UserDTO, *TokenPairDTO, error) {
	s.log.Info("Login attempt", zap.String("identifier", identifier))

	u, err := s.userRepo.GetByIdentifier(ctx, identifier)
	if err != nil {
		s.log.Error("Login: failed to look up user", zap.String("identifier", identifier), zap.Error(err))
		return nil, nil, err
	}

	// Identical invalid credential response for user non-existence (anti-enumeration)
	if u == nil {
		s.log.Warn("Login: user not found", zap.String("identifier", identifier))
		return nil, nil, platformerrors.New(platformerrors.CodeInvalidCredentials, "invalid email/username or password")
	}

	// Check if account is locked due to brute force protection
	if u.LockedUntil != nil && u.LockedUntil.After(time.Now().UTC()) {
		s.log.Warn("Login: account locked", zap.String("userID", u.ID), zap.Time("lockedUntil", *u.LockedUntil))
		return nil, nil, platformerrors.New(platformerrors.CodeFailedPrecondition, fmt.Sprintf("account is temporarily locked. Please try again after %s", u.LockedUntil.Format(time.RFC3339)))
	}

	// Verify Password Hash
	err = s.compareBcrypt(u.PasswordHash, password)
	if err != nil {
		// Update failed login counter
		failedCount := u.FailedLoginAttempts + 1
		var lockUntil *time.Time
		if failedCount >= 5 {
			lockTime := time.Now().UTC().Add(15 * time.Minute)
			lockUntil = &lockTime
			s.log.Warn("Login: account locked after too many failed attempts", zap.String("userID", u.ID))
		} else {
			s.log.Warn("Login: invalid password", zap.String("userID", u.ID), zap.Int("failedAttempts", failedCount))
		}
		if trackErr := s.userRepo.TrackFailedLogin(ctx, u.ID, failedCount, lockUntil); trackErr != nil {
			s.log.Warn("failed to track failed login attempt", zap.String("userID", u.ID), zap.Error(trackErr))
		}

		return nil, nil, platformerrors.New(platformerrors.CodeInvalidCredentials, "invalid email/username or password")
	}

	// Verify user is activated
	if u.Status == "PENDING_VERIFICATION" {
		s.log.Warn("Login: account not verified", zap.String("userID", u.ID))
		return nil, nil, platformerrors.New(platformerrors.CodeAccountNotActive, "account is not verified. Please verify your email first.")
	}
	if u.Status == "SUSPENDED" || u.Status == "BANNED" {
		s.log.Warn("Login: account suspended or banned", zap.String("userID", u.ID), zap.String("status", u.Status))
		return nil, nil, platformerrors.New(platformerrors.CodePermissionDenied, fmt.Sprintf("your account has been %s", u.Status))
	}

	// Reset failed attempts and update last_login_at
	if resetErr := s.userRepo.ResetFailedLogin(ctx, u.ID, time.Now().UTC()); resetErr != nil {
		s.log.Warn("failed to reset login attempt counter", zap.String("userID", u.ID), zap.Error(resetErr))
	}

	// Issue token pair
	tp, err := s.issueTokenPair(ctx, u.ID, u.Email, u.TokenVersion)
	if err != nil {
		s.log.Error("Login: failed to issue token pair", zap.String("userID", u.ID), zap.Error(err))
		return nil, nil, err
	}

	s.log.Info("Login successful", zap.String("userID", u.ID), zap.String("email", u.Email))
	return &UserDTO{ID: u.ID, Email: u.Email, Username: u.Username}, tp, nil
}
