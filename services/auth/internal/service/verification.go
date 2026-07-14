package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	platformerrors "tradedrift/platform/errors"
	"tradedrift/services/auth/internal/otp"
)

// VerifyEmail validates the OTP, triggers Wallet creation, and issues the first authentication session.
func (s *Service) VerifyEmail(ctx context.Context, email, code string) (*UserDTO, *TokenPairDTO, error) {
	// 1. Verify OTP in Redis
	valid, err := s.otpMgr.Verify(ctx, "register:"+email, code)
	if err != nil {
		if errors.Is(err, otp.ErrMaxAttemptsExceeded) {
			return nil, nil, platformerrors.New(platformerrors.CodeInvalidArgument, "max verification attempts exceeded. Please request a new code.")
		}
		if errors.Is(err, otp.ErrOTPNotFound) {
			return nil, nil, platformerrors.New(platformerrors.CodeInvalidArgument, "invalid or expired verification code")
		}
		return nil, nil, err
	}
	if !valid {
		return nil, nil, platformerrors.New(platformerrors.CodeInvalidArgument, "invalid verification code")
	}

	// 2. Query user record (we need the UserID for the wallet call)
	u, err := s.userRepo.GetByIdentifier(ctx, email)
	if err != nil {
		return nil, nil, err
	}
	if u == nil {
		return nil, nil, platformerrors.New(platformerrors.CodeNotFound, "user not found")
	}

	// 3. Synchronous gRPC call to Wallet Service to initialize user wallets (with timeout)
	walletCtx, walletCancel := context.WithTimeout(ctx, 5*time.Second)
	defer walletCancel()

	err = s.walletCl.InitializeWallet(walletCtx, u.ID)
	if err != nil {
		s.log.Error("Error initializing wallet for user during verification", zap.String("userID", u.ID), zap.Error(err))
		return nil, nil, platformerrors.Wrap(err, platformerrors.CodeInternal, "failed to initialize wallet, please retry verification")
	}

	// 4. Update status to VERIFIED and publish event via Transactional Outbox
	eventID := uuid.NewString()
	eventPayload, err := json.Marshal(map[string]string{
		"user_id":  u.ID,
		"email":    u.Email,
		"username": u.Username,
	})
	if err != nil {
		return nil, nil, err
	}

	err = s.userRepo.VerifyEmail(ctx, email, time.Now().UTC(), eventID, eventPayload)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to complete email verification transaction: %w", err)
	}

	// 5. Issue access + refresh token session pair directly
	tp, err := s.issueTokenPair(ctx, u.ID, u.Email, u.TokenVersion)
	if err != nil {
		return nil, nil, err
	}

	return &UserDTO{ID: u.ID, Email: u.Email, Username: u.Username}, tp, nil
}

// ResendVerificationCode generates and emails a new OTP code if the account is still pending.
func (s *Service) ResendVerificationCode(ctx context.Context, email string) error {
	u, err := s.userRepo.GetByIdentifier(ctx, email)
	if err != nil {
		return err
	}
	if u == nil {
		// Silent safety: do not reveal that the email doesn't exist
		return nil
	}

	if u.Status != "PENDING_VERIFICATION" {
		return platformerrors.New(platformerrors.CodeInvalidArgument, "email is already verified")
	}

	code, err := s.otpMgr.Generate(ctx, "register:"+email)
	if err != nil {
		return err
	}

	_ = s.mailer.SendVerificationCode(ctx, email, code)
	return nil
}
