package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	platformerrors "tradedrift/platform/errors"
	platformjwt "tradedrift/platform/jwt"
	"tradedrift/services/auth/internal/repository"
)

// RefreshToken rotates the old refresh token and issues a new signed session pair.
func (s *Service) RefreshToken(ctx context.Context, rawRefreshToken string) (*TokenPairDTO, error) {
	tokenHash := hashToken(rawRefreshToken)

	// Fetch token from DB
	t, err := s.tokenRepo.GetByHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, platformerrors.New(platformerrors.CodeInvalidCredentials, "invalid or expired refresh token")
	}

	// Reuse Hijack Detection
	if t.Status == "ROTATED" {
		// Log potential hijack and immediately revoke all sessions for this user!
		s.log.Warn("Potentially hijacked token detected! Revoking all sessions for user",
			zap.String("tokenID", t.ID),
			zap.String("userID", t.UserID),
		)
		_ = s.tokenRepo.RevokeAll(ctx, t.UserID)
		_ = s.userRepo.IncrementTokenVersion(ctx, t.UserID)
		_ = s.rdb.Del(ctx, "auth:token_version:"+t.UserID).Err()

		return nil, platformerrors.New(platformerrors.CodePermissionDenied, "session security breach detected. Please log in again.")
	}

	if t.Status == "REVOKED" {
		return nil, platformerrors.New(platformerrors.CodeInvalidCredentials, "session has been revoked")
	}

	if t.ExpiresAt.Before(time.Now().UTC()) {
		return nil, platformerrors.New(platformerrors.CodeInvalidCredentials, "refresh token has expired")
	}

	u, err := s.userRepo.GetByID(ctx, t.UserID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, platformerrors.New(platformerrors.CodeNotFound, "user not found")
	}

	// Rotate token: invalidate old and create new
	newRaw, newHash, err := platformjwt.IssueRefreshToken()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	newToken := &repository.RefreshToken{
		ID:         uuid.NewString(),
		UserID:     t.UserID,
		TokenHash:  newHash,
		Status:     "ACTIVE",
		IPAddress:  t.IPAddress,
		UserAgent:  t.UserAgent,
		DeviceName: t.DeviceName,
		ExpiresAt:  now.Add(s.refreshTTL),
		CreatedAt:  now,
	}

	err = s.tokenRepo.Rotate(ctx, t.ID, newToken)
	if err != nil {
		return nil, fmt.Errorf("failed to execute token rotation: %w", err)
	}

	// Issue new access token
	newAccessToken, _, err := platformjwt.IssueAccessToken(u.ID, u.Email, u.TokenVersion, s.jwtSecret, s.accessTTL)
	if err != nil {
		return nil, err
	}

	return &TokenPairDTO{
		AccessToken:           newAccessToken,
		RefreshToken:          newRaw,
		AccessTokenExpiresAt:  now.Add(s.accessTTL),
		RefreshTokenExpiresAt: now.Add(s.refreshTTL),
	}, nil
}
