package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/auth/internal/repository"
)

type IdentityRepository struct {
	db *pgxpool.Pool
}

func NewIdentityRepository(db *pgxpool.Pool) *IdentityRepository {
	return &IdentityRepository{db: db}
}

func (r *IdentityRepository) Create(ctx context.Context, id *repository.UserAuthIdentity) error {
	query := `
		INSERT INTO user_auth_identities (
			id, user_id, provider, provider_user_id, email, created_at, updated_at
		) VALUES (
			COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()),
			$2, $3, $4, $5, $6, $7
		)
		RETURNING id
	`
	err := r.db.QueryRow(ctx, query,
		id.ID, id.UserID, id.Provider, id.ProviderUserID, id.Email, id.CreatedAt, id.UpdatedAt,
	).Scan(&id.ID)
	if err != nil {
		return fmt.Errorf("failed to insert user auth identity: %w", err)
	}
	return nil
}

func (r *IdentityRepository) CreateUserWithIdentity(
	ctx context.Context,
	u *repository.User,
	id *repository.UserAuthIdentity,
	outboxEventID string,
	outboxPayload []byte,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if u.Role == "" {
		u.Role = "user"
	}

	// 1. Insert user
	userQuery := `
		INSERT INTO users (
			id, email, username, password_hash, token_version, role, status,
			failed_login_attempts, locked_until, email_verified_at, created_at, updated_at
		) VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, $10, $11, $12)
	`
	_, err = tx.Exec(ctx, userQuery,
		u.ID, u.Email, u.Username, u.PasswordHash, u.TokenVersion, u.Role, u.Status,
		u.FailedLoginAttempts, u.LockedUntil, u.EmailVerifiedAt, u.CreatedAt, u.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert user: %w", err)
	}

	// 2. Insert identity
	identityQuery := `
		INSERT INTO user_auth_identities (
			id, user_id, provider, provider_user_id, email, created_at, updated_at
		) VALUES (
			COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()),
			$2, $3, $4, $5, $6, $7
		)
		RETURNING id
	`
	err = tx.QueryRow(ctx, identityQuery,
		id.ID, u.ID, id.Provider, id.ProviderUserID, id.Email, id.CreatedAt, id.UpdatedAt,
	).Scan(&id.ID)
	if err != nil {
		return fmt.Errorf("failed to insert identity: %w", err)
	}

	// 3. Insert transactional outbox event if provided
	if outboxEventID != "" && len(outboxPayload) > 0 {
		outboxQuery := `
			INSERT INTO outbox (
				id, aggregate_type, aggregate_id, event_type, payload, status, created_at
			) VALUES ($1, 'User', $2, 'WalletInitializePending', $3, 'PENDING', $4)
		`
		_, err = tx.Exec(ctx, outboxQuery, outboxEventID, u.ID, outboxPayload, u.CreatedAt)
		if err != nil {
			return fmt.Errorf("failed to insert outbox event: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit user and identity transaction: %w", err)
	}

	return nil
}

func (r *IdentityRepository) GetByProviderAndUID(ctx context.Context, provider, providerUserID string) (*repository.UserAuthIdentity, error) {
	query := `
		SELECT id, user_id, provider, provider_user_id, email, created_at, updated_at
		FROM user_auth_identities
		WHERE provider = $1 AND provider_user_id = $2
	`
	var identity repository.UserAuthIdentity
	err := r.db.QueryRow(ctx, query, provider, providerUserID).Scan(
		&identity.ID, &identity.UserID, &identity.Provider, &identity.ProviderUserID,
		&identity.Email, &identity.CreatedAt, &identity.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query identity by provider and uid: %w", err)
	}
	return &identity, nil
}

func (r *IdentityRepository) GetByUserAndProvider(ctx context.Context, userID, provider string) (*repository.UserAuthIdentity, error) {
	query := `
		SELECT id, user_id, provider, provider_user_id, email, created_at, updated_at
		FROM user_auth_identities
		WHERE user_id = $1 AND provider = $2
	`
	var identity repository.UserAuthIdentity
	err := r.db.QueryRow(ctx, query, userID, provider).Scan(
		&identity.ID, &identity.UserID, &identity.Provider, &identity.ProviderUserID,
		&identity.Email, &identity.CreatedAt, &identity.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query identity by user and provider: %w", err)
	}
	return &identity, nil
}
