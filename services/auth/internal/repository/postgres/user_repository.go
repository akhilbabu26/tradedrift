package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/auth/internal/repository"
)

type UserRepository struct {
	db *pgxpool.Pool
}

// NewUserRepository creates a new PostgreSQL UserRepository.
func NewUserRepository(db *pgxpool.Pool) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) Create(ctx context.Context, u *repository.User) error {
	query := `
		INSERT INTO users (
			id, email, username, password_hash, token_version, status,
			failed_login_attempts, locked_until, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := r.db.Exec(ctx, query,
		u.ID, u.Email, u.Username, u.PasswordHash, u.TokenVersion, u.Status,
		u.FailedLoginAttempts, u.LockedUntil, u.CreatedAt, u.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert user: %w", err)
	}
	return nil
}

func (r *UserRepository) GetByID(ctx context.Context, id string) (*repository.User, error) {
	query := `
		SELECT id, email, username, password_hash, token_version, status,
		       failed_login_attempts, locked_until, last_login_at, email_verified_at,
		       created_at, updated_at
		FROM users
		WHERE id = $1
	`
	var u repository.User
	err := r.db.QueryRow(ctx, query, id).Scan(
		&u.ID, &u.Email, &u.Username, &u.PasswordHash, &u.TokenVersion, &u.Status,
		&u.FailedLoginAttempts, &u.LockedUntil, &u.LastLoginAt, &u.EmailVerifiedAt,
		&u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // Return nil, nil if user is not found
		}
		return nil, fmt.Errorf("failed to query user by id: %w", err)
	}
	return &u, nil
}

func (r *UserRepository) GetByIdentifier(ctx context.Context, identifier string) (*repository.User, error) {
	query := `
		SELECT id, email, username, password_hash, token_version, status,
		       failed_login_attempts, locked_until, last_login_at, email_verified_at,
		       created_at, updated_at
		FROM users
		WHERE email = $1 OR username = $1
	`
	var u repository.User
	err := r.db.QueryRow(ctx, query, identifier).Scan(
		&u.ID, &u.Email, &u.Username, &u.PasswordHash, &u.TokenVersion, &u.Status,
		&u.FailedLoginAttempts, &u.LockedUntil, &u.LastLoginAt, &u.EmailVerifiedAt,
		&u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query user by identifier: %w", err)
	}
	return &u, nil
}

func (r *UserRepository) VerifyEmail(ctx context.Context, email string, verifiedAt time.Time, outboxEventID string, outboxPayload []byte) error {
	// Execute status update AND outbox write in a single PostgreSQL transaction
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Update status
	updateQuery := `
		UPDATE users
		SET status = 'VERIFIED', email_verified_at = $1, updated_at = $1
		WHERE email = $2 AND status = 'PENDING_VERIFICATION'
		RETURNING id
	`
	var userID string
	err = tx.QueryRow(ctx, updateQuery, verifiedAt, email).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("user not found or already verified")
		}
		return fmt.Errorf("failed to update user status: %w", err)
	}

	// 2. Insert into transactional outbox
	outboxQuery := `
		INSERT INTO outbox (
			id, aggregate_type, aggregate_id, event_type, payload, status, created_at
		) VALUES ($1, 'User', $2, 'UserVerified', $3, 'PENDING', $4)
	`
	_, err = tx.Exec(ctx, outboxQuery, outboxEventID, userID, outboxPayload, verifiedAt)
	if err != nil {
		return fmt.Errorf("failed to insert outbox event: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (r *UserRepository) IncrementTokenVersion(ctx context.Context, id string) error {
	query := `
		UPDATE users
		SET token_version = token_version + 1, updated_at = NOW()
		WHERE id = $1
	`
	res, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to increment token version: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func (r *UserRepository) TrackFailedLogin(ctx context.Context, id string, attempts int, lockUntil *time.Time) error {
	query := `
		UPDATE users
		SET failed_login_attempts = $1, locked_until = $2, updated_at = NOW()
		WHERE id = $3
	`
	_, err := r.db.Exec(ctx, query, attempts, lockUntil, id)
	if err != nil {
		return fmt.Errorf("failed to update failed login attempts: %w", err)
	}
	return nil
}

func (r *UserRepository) ResetFailedLogin(ctx context.Context, id string, loginTime time.Time) error {
	query := `
		UPDATE users
		SET failed_login_attempts = 0, locked_until = NULL, last_login_at = $1, updated_at = NOW()
		WHERE id = $2
	`
	_, err := r.db.Exec(ctx, query, loginTime, id)
	if err != nil {
		return fmt.Errorf("failed to reset failed login attempts: %w", err)
	}
	return nil
}

func (r *UserRepository) UpdatePassword(ctx context.Context, id string, passwordHash string) error {
	query := `
		UPDATE users
		SET password_hash = $1, token_version = token_version + 1, updated_at = NOW()
		WHERE id = $2
	`
	res, err := r.db.Exec(ctx, query, passwordHash, id)
	if err != nil {
		return fmt.Errorf("failed to update user password and increment token version: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}
