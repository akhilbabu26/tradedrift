package service

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Logout terminates a single active session durably.
func (s *Service) Logout(ctx context.Context, rawRefreshToken, accessJTI, userID string, accessExpiresAt time.Time) error {
	tokenHash := hashToken(rawRefreshToken)

	// 1. Revoke the refresh token in Postgres
	t, err := s.tokenRepo.GetByHash(ctx, tokenHash)
	if err != nil {
		return err
	}
	if t != nil && t.UserID == userID {
		if revokeErr := s.tokenRepo.Revoke(ctx, t.ID); revokeErr != nil {
			s.log.Warn("failed to revoke refresh token during logout", zap.String("tokenID", t.ID), zap.Error(revokeErr))
		}
	}

	// 2. Blacklist access token JTI durably in Postgres and cache in Redis
	err = s.tokenRepo.BlacklistToken(ctx, accessJTI, userID, accessExpiresAt)
	if err != nil {
		return fmt.Errorf("failed to blacklist token JTI in database: %w", err)
	}

	ttl := time.Until(accessExpiresAt)
	if ttl > time.Second {
		cacheKey := "jwt:blacklist:" + accessJTI
		_ = s.rdb.Set(ctx, cacheKey, "revoked", ttl).Err()
	}

	return nil
}

// LogoutAll terminates all active sessions globally by bumping version.
func (s *Service) LogoutAll(ctx context.Context, userID string) error {
	// 1. PostgreSQL updates: increment user version and revoke all refresh tokens
	err := s.userRepo.IncrementTokenVersion(ctx, userID)
	if err != nil {
		return err
	}
	_ = s.tokenRepo.RevokeAll(ctx, userID)

	// 2. Evict from Redis cache
	cacheKey := "auth:token_version:" + userID
	_ = s.rdb.Del(ctx, cacheKey).Err()

	return nil
}
