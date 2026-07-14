package repository

import (
	"context"
	"time"
)

// RefreshToken represents the database model for active sessions.
type RefreshToken struct {
	ID          string
	UserID      string
	TokenHash   string
	Status      string // ACTIVE, ROTATED, REVOKED
	IPAddress   *string
	UserAgent   *string
	DeviceName  *string
	LastUsedAt  *time.Time
	RotatedAt   *time.Time
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

// RefreshTokenRepository defines database contract for session management.
type RefreshTokenRepository interface {
	// Create inserts a new refresh token.
	Create(ctx context.Context, t *RefreshToken) error
	
	// GetByHash retrieves a refresh token record by its SHA-256 hash.
	GetByHash(ctx context.Context, hash string) (*RefreshToken, error)
	
	// Rotate marks the old token as ROTATED and inserts a new token in a single SQL transaction.
	Rotate(ctx context.Context, oldID string, newToken *RefreshToken) error
	
	// Revoke marks a specific refresh token as REVOKED (Logout).
	Revoke(ctx context.Context, id string) error
	
	// RevokeAll marks all active refresh tokens of a user as REVOKED (LogoutAll / ChangePassword).
	RevokeAll(ctx context.Context, userID string) error

	// BlacklistToken inserts a JTI and user_id into the blacklisted_tokens table for single logout.
	BlacklistToken(ctx context.Context, jti string, userID string, expiresAt time.Time) error

	// IsTokenBlacklisted checks if the JTI exists in the blacklisted_tokens table.
	IsTokenBlacklisted(ctx context.Context, jti string) (bool, error)
}
