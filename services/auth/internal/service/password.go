package service

import (
	"context"
	"errors"

	platformerrors "tradedrift/platform/errors"
	"tradedrift/services/auth/internal/otp"
)

// ForgotPassword generates password reset OTP (Silent anti-enumeration).
func (s *Service) ForgotPassword(ctx context.Context, email string) error {
	u, err := s.userRepo.GetByIdentifier(ctx, email)
	if err != nil {
		return err
	}

	// Silent safety: do not reveal that the email doesn't exist
	if u == nil || u.Status != "VERIFIED" {
		return nil
	}

	code, err := s.otpMgr.Generate(ctx, "reset:"+email)
	if err != nil {
		return err
	}

	_ = s.mailer.SendPasswordResetCode(ctx, email, code)
	return nil
}

// ResetPassword checks OTP and updates password, terminating all current sessions.
func (s *Service) ResetPassword(ctx context.Context, email, code, newPassword string) error {
	if len(newPassword) < 8 {
		return platformerrors.New(platformerrors.CodeInvalidArgument, "password must be at least 8 characters long")
	}

	// 1. Verify OTP in Redis
	valid, err := s.otpMgr.Verify(ctx, "reset:"+email, code)
	if err != nil {
		if errors.Is(err, otp.ErrMaxAttemptsExceeded) {
			return platformerrors.New(platformerrors.CodeInvalidArgument, "max verification attempts exceeded. Please request a new code.")
		}
		if errors.Is(err, otp.ErrOTPNotFound) {
			return platformerrors.New(platformerrors.CodeInvalidArgument, "invalid or expired reset code")
		}
		return err
	}
	if !valid {
		return platformerrors.New(platformerrors.CodeInvalidArgument, "invalid verification code")
	}

	u, err := s.userRepo.GetByIdentifier(ctx, email)
	if err != nil {
		return err
	}
	if u == nil {
		return platformerrors.New(platformerrors.CodeNotFound, "user not found")
	}

	// 2. Hash new password
	hashBytes, err := s.hashBcrypt(newPassword)
	if err != nil {
		return err
	}

	// 3. Update password, increment version, and revoke all active refresh tokens in Postgres
	err = s.userRepo.UpdatePassword(ctx, u.ID, string(hashBytes))
	if err != nil {
		return err
	}

	// 4. Revoke sessions, bump token version, and evict cache
	_ = s.tokenRepo.RevokeAll(ctx, u.ID)
	_ = s.userRepo.IncrementTokenVersion(ctx, u.ID)
	cacheKey := "auth:token_version:" + u.ID
	_ = s.rdb.Del(ctx, cacheKey).Err()

	return nil
}

// ChangePassword updates password for authenticated user, terminating all other sessions.
func (s *Service) ChangePassword(ctx context.Context, userID, oldPassword, newPassword string) error {
	if len(newPassword) < 8 {
		return platformerrors.New(platformerrors.CodeInvalidArgument, "password must be at least 8 characters long")
	}

	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u == nil {
		return platformerrors.New(platformerrors.CodeNotFound, "user not found")
	}

	// Verify old password
	err = s.compareBcrypt(u.PasswordHash, oldPassword)
	if err != nil {
		return platformerrors.New(platformerrors.CodeInvalidCredentials, "incorrect current password")
	}

	// Hash new password
	hashBytes, err := s.hashBcrypt(newPassword)
	if err != nil {
		return err
	}

	// Postgres updates: update password hash, increment version, and revoke active sessions
	err = s.userRepo.UpdatePassword(ctx, userID, string(hashBytes))
	if err != nil {
		return err
	}
	_ = s.tokenRepo.RevokeAll(ctx, userID)

	// Evict Redis
	cacheKey := "auth:token_version:" + userID
	_ = s.rdb.Del(ctx, cacheKey).Err()

	return nil
}
