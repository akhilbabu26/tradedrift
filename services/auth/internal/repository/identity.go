package repository

import (
	"context"
	"time"
)

// UserAuthIdentity represents an external authentication identity linked to a TradeDrift user.
type UserAuthIdentity struct {
	ID             string
	UserID         string
	Provider       string // e.g. "google"
	ProviderUserID string // Google sub claim (immutable subject ID)
	Email          string // Verified email address from provider
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// IdentityRepository defines database operations for user authentication identities.
type IdentityRepository interface {
	// Create inserts a new auth identity row.
	Create(ctx context.Context, identity *UserAuthIdentity) error

	// CreateUserWithIdentity atomically inserts a new user, their identity, and an outbox event in a single transaction.
	CreateUserWithIdentity(ctx context.Context, u *User, identity *UserAuthIdentity, outboxEventID string, outboxPayload []byte) error

	// GetByProviderAndUID retrieves an identity by provider name and provider user ID.
	GetByProviderAndUID(ctx context.Context, provider, providerUserID string) (*UserAuthIdentity, error)

	// GetByUserAndProvider retrieves an identity for a specific user and provider.
	GetByUserAndProvider(ctx context.Context, userID, provider string) (*UserAuthIdentity, error)
}
