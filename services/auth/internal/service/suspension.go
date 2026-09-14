package service

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// SuspendUser updates the user's status to SUSPENDED and revokes all active sessions.
func (s *Service) SuspendUser(ctx context.Context, userID, reason string) error {
	if err := s.userRepo.UpdateStatus(ctx, userID, "SUSPENDED"); err != nil {
		return fmt.Errorf("failed to update user status to SUSPENDED: %w", err)
	}

	// Invalidate active sessions immediately
	if err := s.LogoutAll(ctx, userID); err != nil {
		s.log.Warn("SuspendUser: LogoutAll failed", zap.String("user_id", userID), zap.Error(err))
	}

	s.log.Info("user suspended successfully", zap.String("user_id", userID), zap.String("reason", reason))
	return nil
}

// UnsuspendUser updates the user's status back to VERIFIED.
func (s *Service) UnsuspendUser(ctx context.Context, userID, reason string) error {
	if err := s.userRepo.UpdateStatus(ctx, userID, "VERIFIED"); err != nil {
		return fmt.Errorf("failed to update user status to VERIFIED: %w", err)
	}

	s.log.Info("user unsuspended successfully", zap.String("user_id", userID), zap.String("reason", reason))
	return nil
}

// ListSuspendedUsers retrieves paginated suspended user IDs.
func (s *Service) ListSuspendedUsers(ctx context.Context, limit, offset int) ([]string, int, error) {
	return s.userRepo.ListSuspendedUserIDs(ctx, limit, offset)
}
