package repository

import (
	"context"
	"time"
)

//User represent the users database record
type User struct{
	ID 						string
	Email					string
	Username 				string
	PasswordHash			string
	TokenVersion			int
	Status					string //// PENDING_VERIFICATION, VERIFIED, SUSPENDED, BANNED
	FailedLoginAttempts		int
	LockedUntil				*time.Time
	LastLoginAt				*time.Time
	EmailVerifiedAt			*time.Time
	CreatedAt				time.Time
	UpdatedAt				time.Time
}

// UserRepository defines the persistence contract for users
type UserRepository interface{
	// create inserts a user with PENDING_VERIFICATION status.
	Create(ctx context.Context, u *User) error

	// GetByID retrieves a user by their UUID.
	GetByID(ctx context.Context, id string) (*User, error)
	
	// GetByIdentifier retrieves a user by either username OR email (used during Login).
	GetByIdentifier(ctx context.Context, identifier string) (*User, error)
	
	// VerifyEmail marks a user as VERIFIED and commits a UserVerified event payload
	// into the outbox table in a single atomic SQL transaction.
	VerifyEmail(ctx context.Context, email string, verifiedAt time.Time, outboxEventID string, outboxPayload []byte) error
	
	// IncrementTokenVersion increments token_version to invalidate all currently active access tokens.
	IncrementTokenVersion(ctx context.Context, id string) error
	
	// TrackFailedLogin updates failed attempts and lock time when password check fails.
	TrackFailedLogin(ctx context.Context, id string, attempts int, lockUntil *time.Time) error
	
	// ResetFailedLogin resets attempts to 0 and updates the last_login_at timestamp.
	ResetFailedLogin(ctx context.Context, id string, loginTime time.Time) error

	// UpdatePassword updates the password hash and increments token_version in an atomic operation.
	UpdatePassword(ctx context.Context, id string, passwordHash string) error
}