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

type TokenRepository struct {
	db *pgxpool.Pool
}

// NewTokenRepository creates a new PostgreSQL TokenRepository.
func NewTokenRepository(db *pgxpool.Pool) *TokenRepository {
	return &TokenRepository{db: db}
}

func (r *TokenRepository) Create(ctx context.Context, t *repository.RefreshToken) error {
	query := `
		INSERT INTO refresh_tokens (
			id, user_id, token_hash, status, ip_address, user_agent,
			device_name, last_used_at, rotated_at, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	_, err := r.db.Exec(ctx, query,
		t.ID, t.UserID, t.TokenHash, t.Status, t.IPAddress, t.UserAgent,
		t.DeviceName, t.LastUsedAt, t.RotatedAt, t.ExpiresAt, t.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert refresh token: %w", err)
	}
	return nil
}

func (r *TokenRepository) GetByHash(ctx context.Context, hash string) (*repository.RefreshToken, error) {
	query := `
		SELECT id, user_id, token_hash, status, ip_address, user_agent,
		       device_name, last_used_at, rotated_at, expires_at, created_at
		FROM refresh_tokens
		WHERE token_hash = $1
	`
	var t repository.RefreshToken
	err := r.db.QueryRow(ctx, query, hash).Scan(
		&t.ID, &t.UserID, &t.TokenHash, &t.Status, &t.IPAddress, &t.UserAgent,
		&t.DeviceName, &t.LastUsedAt, &t.RotatedAt, &t.ExpiresAt, &t.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query refresh token: %w", err)
	}
	return &t, nil
}

func (r *TokenRepository) Rotate(ctx context.Context, oldID string, newToken *repository.RefreshToken) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Mark old token as rotated
	rotateQuery := `
		UPDATE refresh_tokens
		SET status = 'ROTATED', rotated_at = $1
		WHERE id = $2 AND status = 'ACTIVE'
	`
	res, err := tx.Exec(ctx, rotateQuery, newToken.CreatedAt, oldID)
	if err != nil {
		return fmt.Errorf("failed to mark token as rotated: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("active token not found for rotation (potential token reuse hijack attempt)")
	}

	// 2. Insert new token
	insertQuery := `
		INSERT INTO refresh_tokens (
			id, user_id, token_hash, status, ip_address, user_agent,
			device_name, last_used_at, rotated_at, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	_, err = tx.Exec(ctx, insertQuery,
		newToken.ID, newToken.UserID, newToken.TokenHash, newToken.Status, newToken.IPAddress, newToken.UserAgent,
		newToken.DeviceName, newToken.LastUsedAt, newToken.RotatedAt, newToken.ExpiresAt, newToken.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert new refresh token: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit token rotation transaction: %w", err)
	}

	return nil
}

func (r *TokenRepository) Revoke(ctx context.Context, id string) error {
	query := `
		UPDATE refresh_tokens
		SET status = 'REVOKED'
		WHERE id = $1 AND status = 'ACTIVE'
	`
	_, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to revoke refresh token: %w", err)
	}
	return nil
}

func (r *TokenRepository) RevokeAll(ctx context.Context, userID string) error {
	query := `
		UPDATE refresh_tokens
		SET status = 'REVOKED'
		WHERE user_id = $1 AND status = 'ACTIVE'
	`
	_, err := r.db.Exec(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("failed to revoke all refresh tokens: %w", err)
	}
	return nil
}

// BlacklistToken inserts a JTI and user_id into PG durably (for single logout recovery).
func (r *TokenRepository) BlacklistToken(ctx context.Context, jti string, userID string, expiresAt time.Time) error {
	query := `
		INSERT INTO blacklisted_tokens (jti, user_id, expires_at, created_at)
		VALUES ($1, $2, $3, NOW())
	`
	_, err := r.db.Exec(ctx, query, jti, userID, expiresAt)
	if err != nil {
		return fmt.Errorf("failed to insert blacklisted token: %w", err)
	}
	return nil
}

// IsTokenBlacklisted checks if the JTI exists in the PG backing store.
func (r *TokenRepository) IsTokenBlacklisted(ctx context.Context, jti string) (bool, error) {
	query := `
		SELECT EXISTS (
			SELECT 1 FROM blacklisted_tokens
			WHERE jti = $1 AND expires_at > NOW()
		)
	`
	var exists bool
	err := r.db.QueryRow(ctx, query, jti).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check blacklisted token: %w", err)
	}
	return exists, nil
}
